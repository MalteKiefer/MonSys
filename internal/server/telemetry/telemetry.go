// Package telemetry wires OpenTelemetry traces and metrics for mon-server.
//
// Design contract:
//
//   - Default-off. If Config.OTLPEndpoint is empty, Init still succeeds but
//     installs no-op providers so the server starts cleanly in dev/test and
//     never emits external traffic.
//   - Single entrypoint. Init returns a single shutdown closure the main
//     goroutine defers; that closure flushes both providers in order.
//   - Resource attributes (service.name, service.version, service.instance.id,
//     deployment.environment) are attached to every span and every metric
//     so a multi-instance fleet can be distinguished in the collector.
//   - OTLP exporters use gRPC against an insecure local endpoint by default.
//     Operators wanting TLS should put a sidecar collector in front; this
//     package keeps the wire protocol uniform.
//
// Domain metrics live in internal/server/api/metrics.go; they register
// themselves against a Prometheus registry rather than going through the
// OTel meter, because the audit gate also wants a /metrics scrape endpoint
// that exposes runtime + domain counters in classic Prometheus text format.
// We bridge by using the OTel Prometheus exporter as the MeterProvider's
// reader, which keeps domain metrics consistent across both surfaces if
// they're ever ported to the OTel Meter API.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// PromRegistry is the package-level Prometheus registry. Domain metrics in
// internal/server/api/metrics.go register themselves on this registry, and
// the /metrics endpoint serves it via PromHandler(). Initialised at package
// load so collector registration in metrics.go runs without an ordering
// dependency on telemetry.Init.
var PromRegistry = prometheus.NewRegistry()

func init() {
	// Standard runtime collectors. Operators want process_cpu, process_rss,
	// go_goroutines, go_gc_*, plus the build-info gauge that lets them
	// confirm which binary version is running. These are cheap and have no
	// configuration knobs.
	PromRegistry.MustRegister(collectors.NewBuildInfoCollector())
	PromRegistry.MustRegister(collectors.NewGoCollector(
		collectors.WithGoCollectorRuntimeMetrics(collectors.MetricsAll),
	))
	PromRegistry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
}

// PromHandler returns a handler serving every metric registered on
// PromRegistry in classic Prometheus text format. Mount it under /metrics
// behind requireAdmin.
func PromHandler() http.Handler {
	return promhttp.HandlerFor(PromRegistry, promhttp.HandlerOpts{
		// Disable client compression negotiation entirely; the response
		// payload is small enough that gzip CPU overhead dominates.
		DisableCompression: true,
		// Surface errors in HTTP status so a misbehaving collector is
		// visible in logs rather than silently dropping samples.
		ErrorHandling: promhttp.HTTPErrorOnError,
	})
}

// Config captures everything Init needs at startup. Read from env vars in
// cmd/mon-server/main.go.
type Config struct {
	// OTLPEndpoint is the host:port of the OTLP gRPC collector. Empty
	// disables export entirely; Init returns no-op providers in that case.
	OTLPEndpoint string

	// ServiceName / ServiceVersion populate the resource attributes the
	// collector indexes by. Defaults applied if empty.
	ServiceName    string
	ServiceVersion string

	// Environment populates deployment.environment; "production" by default
	// because that's the cautious assumption when the env var isn't set.
	Environment string
}

// Init constructs the resource, OTLP exporters, and providers, wires them
// as the global OTel TracerProvider + MeterProvider, and returns a single
// shutdown closure. The shutdown flushes traces first (so any in-flight
// spans referencing recent metric reads finish reporting), then metrics.
//
// Empty OTLPEndpoint -> no-op exporters but the global propagator is still
// installed (TraceContext + Baggage) so propagation works through the
// process even when nothing is exported. This keeps tests deterministic.
func Init(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	if cfg.ServiceName == "" {
		cfg.ServiceName = "mon-server"
	}
	if cfg.Environment == "" {
		cfg.Environment = "production"
	}

	// Always install propagators so trace context flows through outbound
	// HTTP calls (none currently, but it costs nothing).
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	res, err := buildResource(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("telemetry resource: %w", err)
	}

	if cfg.OTLPEndpoint == "" {
		// Noop path: still install SDK providers backed by zero exporters
		// so the OTel Meter API the caller might use has somewhere to write,
		// but no network traffic is generated.
		tp := sdktrace.NewTracerProvider(sdktrace.WithResource(res))
		mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res))
		otel.SetTracerProvider(tp)
		otel.SetMeterProvider(mp)
		return func(ctx context.Context) error {
			return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx))
		}, nil
	}

	// Trace exporter. Insecure: the collector is expected to be a sidecar on
	// the same internal network. Operators wanting TLS terminate at the
	// collector.
	traceExp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("otlp trace exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		// 5s batch timeout balances export latency against batching: keeps
		// the export thread quiet in steady-state while still flushing
		// regularly enough that operators see fresh spans in their UI.
		sdktrace.WithBatcher(traceExp, sdktrace.WithBatchTimeout(5*time.Second)),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)

	metricExp, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(cfg.OTLPEndpoint),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		// Roll back the trace provider so the caller doesn't end up with a
		// half-initialised global state.
		_ = tp.Shutdown(ctx)
		return nil, fmt.Errorf("otlp metric exporter: %w", err)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		// 30s push interval. Anything tighter spams the collector for
		// counter-style metrics that don't change between samples.
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp,
			sdkmetric.WithInterval(30*time.Second))),
	)
	otel.SetMeterProvider(mp)

	return func(ctx context.Context) error {
		// Trace first: the metric provider's periodic reader is the slow
		// path on shutdown.
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx))
	}, nil
}

// buildResource composes the standard resource attributes plus the
// service.instance.id derived from hostname (or a fresh UUID when hostname
// is unavailable). Operators running multiple replicas behind a load
// balancer rely on service.instance.id to disambiguate them.
func buildResource(ctx context.Context, cfg Config) (*resource.Resource, error) {
	instance := os.Getenv("HOSTNAME")
	if instance == "" {
		if hn, err := os.Hostname(); err == nil {
			instance = hn
		}
	}
	if instance == "" {
		instance = uuid.NewString()
	}

	attrs := []attribute.KeyValue{
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceInstanceID(instance),
		semconv.DeploymentEnvironment(cfg.Environment),
	}
	if cfg.ServiceVersion != "" {
		attrs = append(attrs, semconv.ServiceVersion(cfg.ServiceVersion))
	}

	return resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(attrs...),
	)
}
