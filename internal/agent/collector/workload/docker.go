//go:build linux

// Package workload contains workload (container/VM/pod) collectors.
//
// docker.go talks to the Docker Engine API directly over its UNIX socket
// (/var/run/docker.sock). The npipe transport on Windows is intentionally
// out of scope for this file; when needed it will land in a sibling
// docker_windows.go using github.com/Microsoft/go-winio.
//
// We deliberately do not depend on the upstream docker client SDK to keep
// the agent binary small and our supply-chain narrow; the Engine API
// surface we need is tiny (list + stats + inspect).
//
// All calls are GET-only — the agent never mutates container state.
package workload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/MalteKiefer/MonSys/internal/agent/registry"
	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// DefaultDockerEndpoint is the conventional Docker socket on Linux. The agent
// is expected to be a member of the docker group OR connected through a
// read-only docker socket proxy (operator's choice — see deploy/ docs).
const DefaultDockerEndpoint = "unix:///var/run/docker.sock"

// Timeouts and sizing constants for talking to the Docker Engine. Values are
// intentionally generous — the Engine API is local (UNIX socket), but stats
// streams and image inspects can briefly block on a busy daemon.
const (
	// dialTimeout caps the time spent opening the UNIX socket connection.
	dialTimeout = 2 * time.Second
	// pingTimeout bounds the /_ping availability probe at startup.
	pingTimeout = 2 * time.Second
	// responseHeaderTimeout bounds how long the daemon has to start replying
	// after we send the request line.
	responseHeaderTimeout = 10 * time.Second
	// idleConnTimeout closes idle keep-alive sockets after this period.
	idleConnTimeout = 30 * time.Second
	// httpClientTimeout is the overall per-request ceiling. It must be larger
	// than responseHeaderTimeout to let slow stats responses finish.
	httpClientTimeout = 15 * time.Second
	// maxIdleConns is small on purpose: we only ever talk to one socket.
	maxIdleConns = 4
	// imageProbeBudget is the per-tick deadline applied to the whole batch of
	// registry HEAD requests; one slow registry can't stall the inventory.
	imageProbeBudget = 30 * time.Second
	// updateCheckInterval is how often the agent will hit upstream registries
	// to re-check for newer image manifests. The collector ingests workloads
	// on every tick (typically 60s), but the registry probe is throttled to
	// this cadence to stay well under the Docker Hub anonymous rate-limit
	// (~100 manifest requests / 6h / IP) and to be a good citizen of GHCR /
	// Quay.
	//
	// Future: graduate to a per-host config knob (proposed field name
	// `agent.docker.update_check_interval`) so admins can tighten or loosen
	// the cadence; tracked alongside the disable-toggle in registry.go.
	updateCheckInterval = 6 * time.Hour
)

// errInspectStatus / errListStatus / errStatsStatus are sentinel wrappers so
// callers can match on the failure class without parsing the status code out
// of the message. They are kept package-private; the Engine API itself is
// not part of our public surface.
var (
	errInspectStatus = errors.New("docker inspect: unexpected status")
	errListStatus    = errors.New("docker /containers/json: unexpected status")
	errStatsStatus   = errors.New("docker /containers/stats: unexpected status")
)

// log is the package-scoped logger; every record carries component=workload
// so operators can grep agent logs cleanly.
var log = slog.With("component", "workload")

// Docker is a Source + InventoryProvider for the Docker workload. It is safe
// for concurrent use by the agent scheduler; the registry-probe state is
// guarded by updateMu.
type Docker struct {
	endpoint string
	hc       *http.Client

	// reg is the upstream-registry probe client. It carries its own response
	// cache so concurrent containers sharing an image only trigger one HTTP
	// HEAD per (registry, repo, tag, CacheTTL) window.
	reg *registry.Client

	// updateMu guards lastUpdateCheck and updateState. The check runs at most
	// once per updateCheckInterval per host; in between, we serve the
	// previously computed tuple from updateState so the inventory snapshot
	// still carries the badge.
	updateMu        sync.Mutex
	lastUpdateCheck time.Time
	updateState     map[string]updateProbe // key = container ID
}

// updateProbe is the cached output of a single registry comparison. We keep
// a copy in-process so the inventory snapshot (which is rebuilt on every
// tick) always carries the most recent verdict, even on ticks where the
// registry probe itself is suppressed by the throttle.
type updateProbe struct {
	currentDigest   string
	latestDigest    string
	updateAvailable bool
}

// NewDocker returns a Docker collector. endpoint may be:
//   - "" → defaults to DefaultDockerEndpoint
//   - "unix:///path/to/docker.sock"
//   - "tcp://host:port" (dev only; no TLS support yet)
func NewDocker(endpoint string) *Docker {
	if endpoint == "" {
		endpoint = DefaultDockerEndpoint
	}
	return &Docker{
		endpoint:    endpoint,
		hc:          newHTTPClient(endpoint),
		reg:         registry.New(),
		updateState: map[string]updateProbe{},
	}
}

// Name returns the collector identifier used in source-scope lists and logs.
func (d *Docker) Name() string { return "docker" }

// Available reports whether the configured socket can be reached. Used by the
// agent orchestrator to decide whether to register this collector at all.
func (d *Docker) Available(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	req, err := d.req(ctx, "GET", "/_ping", nil)
	if err != nil {
		return false
	}
	resp, err := d.hc.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Inventory enumerates every container (running or stopped) on the host and
// appends one WorkloadInfo per container to snap.Workloads. It also refreshes
// the throttled image-update probe; see maybeRefreshUpdates.
func (d *Docker) Inventory(ctx context.Context, snap *apitypes.InventorySnap) error {
	containers, err := d.listContainers(ctx)
	if err != nil {
		return err
	}

	// Refresh the registry-probe cache at most every updateCheckInterval.
	// Network failures are non-fatal: any container we can't probe is
	// reported with empty digest fields, and the server treats that as
	// "no upstream info available".
	d.maybeRefreshUpdates(ctx, containers)

	state := d.snapshotUpdateState()
	for _, c := range containers {
		snap.Workloads = append(snap.Workloads, containerToInventory(c, state))
	}
	return nil
}

// Collect emits one WorkloadSample per container. Stopped containers yield a
// state-only sample (no resource numbers); running containers are stats-probed
// individually. A failing stats call for one container does not abort the tick.
func (d *Docker) Collect(ctx context.Context, batch *apitypes.IngestRequest) error {
	containers, err := d.listContainers(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, c := range containers {
		// Stats only make sense for running containers; skip Exited/Created.
		if !strings.EqualFold(c.State, "running") {
			batch.Workloads = append(batch.Workloads, apitypes.WorkloadSample{
				Time:       now,
				Kind:       "docker",
				ExternalID: c.ID,
				State:      c.State,
			})
			continue
		}
		s, err := d.containerStats(ctx, c.ID)
		if err != nil {
			// Don't kill the whole tick if one container disappeared; just skip.
			continue
		}
		batch.Workloads = append(batch.Workloads, statsToSample(c, s, now))
	}
	return nil
}

// snapshotUpdateState returns a shallow copy of d.updateState while holding
// updateMu. Callers can iterate without locking.
func (d *Docker) snapshotUpdateState() map[string]updateProbe {
	d.updateMu.Lock()
	defer d.updateMu.Unlock()
	out := make(map[string]updateProbe, len(d.updateState))
	for k, v := range d.updateState {
		out[k] = v
	}
	return out
}

// containerToInventory projects a raw container record + cached probe into a
// WorkloadInfo. Pure function; no I/O.
func containerToInventory(c dockerContainer, state map[string]updateProbe) apitypes.WorkloadInfo {
	info := apitypes.WorkloadInfo{
		Kind:       "docker",
		ExternalID: c.ID,
		Name:       firstName(c.Names),
		Image:      c.Image,
		State:      c.State,
		Labels:     c.Labels,
	}
	if probe, ok := state[c.ID]; ok {
		info.CurrentDigest = probe.currentDigest
		info.LatestDigest = probe.latestDigest
		info.UpdateAvailable = probe.updateAvailable
	}
	return info
}

// statsToSample turns a parsed /stats response into a WorkloadSample. Pure
// function; no I/O.
func statsToSample(c dockerContainer, s *dockerStats, now time.Time) apitypes.WorkloadSample {
	return apitypes.WorkloadSample{
		Time:            now,
		Kind:            "docker",
		ExternalID:      c.ID,
		CPUUsagePct:     cpuPercent(s),
		MemUsedBytes:    int64(s.MemoryStats.Usage), //nolint:gosec // uint64 from docker; bytes fit in int64
		MemLimitBytes:   int64(s.MemoryStats.Limit), //nolint:gosec // uint64 from docker; bytes fit in int64
		NetRxBytes:      sumRx(s.Networks),
		NetTxBytes:      sumTx(s.Networks),
		BlockReadBytes:  blockIO(s.BlkioStats.IOServiceBytesRecursive, "Read"),
		BlockWriteBytes: blockIO(s.BlkioStats.IOServiceBytesRecursive, "Write"),
		State:           c.State,
	}
}

// --- image-update detection -------------------------------------------------
//
// maybeRefreshUpdates re-evaluates "is there a newer image upstream?" for
// every container, but at most once per updateCheckInterval. When the
// throttle is active, callers continue to read the previous verdict out of
// d.updateState — see Inventory() above.
//
// Error handling contract:
//   - 401 / 404 from the registry are returned by registry.Client as ordinary
//     errors; we swallow them at DEBUG. The most common cause is a private
//     image that requires auth we don't have, or a tag that was deleted
//     upstream. Either way the right behavior is "no badge, move on."
//   - registry.ErrPinnedDigest is silenced entirely: a pinned image cannot
//     have an "update available" by definition.
//   - A failing docker inspect leaves curDigest empty; we still record the
//     upstream digest if we got one, so the UI can render "current digest
//     unknown, upstream is sha256:…".
//
// Future: a config toggle (proposed `agent.docker.check_image_updates`) will
// short-circuit this method; the matching registry-level disable lives in
// registry.go.
func (d *Docker) maybeRefreshUpdates(ctx context.Context, containers []dockerContainer) {
	if !d.takeUpdateSlot() {
		return
	}

	// Use a per-tick deadline so a single slow registry can't stall the
	// whole inventory pass.
	probeCtx, cancel := context.WithTimeout(ctx, imageProbeBudget)
	defer cancel()

	fresh := make(map[string]updateProbe, len(containers))
	for _, c := range containers {
		// Skip containers we don't have an image for, or pinned digests
		// (image@sha256:…). The latter has no concept of "newer".
		if c.Image == "" || strings.Contains(c.Image, "@sha256:") {
			continue
		}
		fresh[c.ID] = d.probeOne(probeCtx, c)
	}

	d.updateMu.Lock()
	d.updateState = fresh
	d.updateMu.Unlock()
}

// takeUpdateSlot returns true if enough time has elapsed since the last
// registry refresh to start a new one. It atomically records the new
// timestamp; subsequent calls within updateCheckInterval will return false.
func (d *Docker) takeUpdateSlot() bool {
	d.updateMu.Lock()
	defer d.updateMu.Unlock()
	if !d.lastUpdateCheck.IsZero() && time.Since(d.lastUpdateCheck) < updateCheckInterval {
		return false
	}
	d.lastUpdateCheck = time.Now()
	return true
}

// probeOne resolves the current local digest and the latest upstream digest
// for one container. Network failures are absorbed (logged at DEBUG) and
// reflected as zero-valued fields in the returned probe.
func (d *Docker) probeOne(ctx context.Context, c dockerContainer) updateProbe {
	// Inspect the local engine first. A missing digest is non-fatal; we can
	// still return the upstream digest alone, which at least tells the UI
	// "current container's tag points to this upstream digest."
	curDigest, err := d.imageDigestForContainer(ctx, c.ID)
	if err != nil {
		log.Debug("docker inspect failed", "container", c.ID, "err", err)
	}

	latest, err := d.reg.LatestDigest(ctx, c.Image)
	if err != nil {
		// Pinned digests are an expected, frequent case — don't spam logs.
		if !errors.Is(err, registry.ErrPinnedDigest) {
			log.Debug("registry probe failed", "image", c.Image, "err", err)
		}
		return updateProbe{currentDigest: curDigest}
	}
	return updateProbe{
		currentDigest:   curDigest,
		latestDigest:    latest,
		updateAvailable: curDigest != "" && latest != "" && curDigest != latest,
	}
}

// imageDigestForContainer returns the runtime digest of a container's image
// as recorded by the local engine. Docker exposes this in two places under
// `docker inspect`: ImageID is the local content-addressable id (often the
// same as the upstream digest for images pulled by tag), and Image
// historically held the user-supplied reference. We prefer ImageID — that's
// the one that actually compares apples-to-apples with the registry's
// Docker-Content-Digest header.
func (d *Docker) imageDigestForContainer(ctx context.Context, containerID string) (string, error) {
	req, err := d.req(ctx, "GET", "/containers/"+containerID+"/json", nil)
	if err != nil {
		return "", err
	}
	resp, err := d.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: %d", errInspectStatus, resp.StatusCode)
	}
	var body struct {
		Image string `json:"Image"` // "sha256:…" since 1.10
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode inspect: %w", err)
	}
	return strings.TrimSpace(body.Image), nil
}

// --- internal HTTP plumbing ------------------------------------------------

// newHTTPClient builds the HTTP client used to talk to the Docker daemon.
// For unix:// endpoints, the dialer is rewritten to open the socket directly;
// for tcp:// endpoints the default net stack is used unchanged.
func newHTTPClient(endpoint string) *http.Client {
	tr := &http.Transport{
		// Don't share connections across hosts; one socket only.
		MaxIdleConns:          maxIdleConns,
		IdleConnTimeout:       idleConnTimeout,
		DisableCompression:    true,
		ResponseHeaderTimeout: responseHeaderTimeout,
	}
	if strings.HasPrefix(endpoint, "unix://") {
		path := strings.TrimPrefix(endpoint, "unix://")
		tr.DialContext = func(ctx context.Context, _ string, _ string) (net.Conn, error) {
			dl := net.Dialer{Timeout: dialTimeout}
			return dl.DialContext(ctx, "unix", path)
		}
	}
	return &http.Client{Transport: tr, Timeout: httpClientTimeout}
}

// req builds an HTTP request bound to the configured endpoint. For unix
// sockets the host is irrelevant; "docker" is a stable placeholder Go's
// URL parser will accept.
func (d *Docker) req(ctx context.Context, method, path string, q url.Values) (*http.Request, error) {
	host := "docker"
	if strings.HasPrefix(d.endpoint, "tcp://") {
		host = strings.TrimPrefix(d.endpoint, "tcp://")
	}
	u := &url.URL{Scheme: "http", Host: host, Path: path}
	if q != nil {
		u.RawQuery = q.Encode()
	}
	return http.NewRequestWithContext(ctx, method, u.String(), nil)
}

// dockerContainer is the subset of /containers/json we consume. Other fields
// (Ports, Mounts, SizeRw, …) are deliberately omitted to keep allocations
// small on hosts with hundreds of containers.
type dockerContainer struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	State  string            `json:"State"`
	Labels map[string]string `json:"Labels"`
}

// listContainers calls /containers/json?all=true so we also see stopped
// containers; the inventory snapshot is supposed to be exhaustive.
func (d *Docker) listContainers(ctx context.Context) ([]dockerContainer, error) {
	q := url.Values{}
	q.Set("all", "true")
	req, err := d.req(ctx, "GET", "/containers/json", q)
	if err != nil {
		return nil, err
	}
	resp, err := d.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %d", errListStatus, resp.StatusCode)
	}
	var out []dockerContainer
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode containers: %w", err)
	}
	return out, nil
}

// dockerStats mirrors the subset of /containers/{id}/stats?stream=false we
// need to compute CPU%, memory, network, and block-IO. Field tags must match
// the Docker Engine wire format exactly; do not rename casually.
type dockerStats struct {
	CPUStats    cpuStats        `json:"cpu_stats"`
	PreCPUStats cpuStats        `json:"precpu_stats"`
	MemoryStats memStats        `json:"memory_stats"`
	BlkioStats  blkioStatsBlock `json:"blkio_stats"`
	Networks    map[string]netStats
}

type cpuStats struct {
	CPUUsage struct {
		TotalUsage  uint64   `json:"total_usage"`
		PerCPUUsage []uint64 `json:"percpu_usage"`
	} `json:"cpu_usage"`
	SystemUsage uint64 `json:"system_cpu_usage"`
	OnlineCPUs  uint64 `json:"online_cpus"`
}

type memStats struct {
	Usage uint64 `json:"usage"`
	Limit uint64 `json:"limit"`
}

type netStats struct {
	RxBytes uint64 `json:"rx_bytes"`
	TxBytes uint64 `json:"tx_bytes"`
}

type blkioStatsBlock struct {
	IOServiceBytesRecursive []blkioEntry `json:"io_service_bytes_recursive"`
}

type blkioEntry struct {
	Op    string `json:"op"`
	Value uint64 `json:"value"`
}

// containerStats issues a one-shot (stream=false) stats call for the given
// container. Streaming mode is avoided so we don't hold a daemon goroutine
// open between ticks.
func (d *Docker) containerStats(ctx context.Context, id string) (*dockerStats, error) {
	q := url.Values{}
	q.Set("stream", "false")
	req, err := d.req(ctx, "GET", "/containers/"+id+"/stats", q)
	if err != nil {
		return nil, err
	}
	resp, err := d.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: id=%s status=%d", errStatsStatus, id, resp.StatusCode)
	}
	var s dockerStats
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return nil, fmt.Errorf("decode stats: %w", err)
	}
	return &s, nil
}

// --- math helpers ----------------------------------------------------------

// cpuPercent matches `docker stats`: ratio of (delta_container, delta_system) * onlineCPUs * 100.
// When the container has no prior sample (first call after start), delta is 0 → returns 0.
func cpuPercent(s *dockerStats) float64 {
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(s.CPUStats.SystemUsage) - float64(s.PreCPUStats.SystemUsage)
	if cpuDelta <= 0 || sysDelta <= 0 {
		return 0
	}
	online := float64(s.CPUStats.OnlineCPUs)
	if online == 0 {
		online = float64(len(s.CPUStats.CPUUsage.PerCPUUsage))
	}
	if online == 0 {
		online = 1
	}
	return (cpuDelta / sysDelta) * online * 100.0
}

// sumRx returns the total received bytes across all of a container's networks.
func sumRx(m map[string]netStats) int64 {
	var total uint64
	for _, n := range m {
		total += n.RxBytes
	}
	return int64(total) //nolint:gosec // uint64 from docker; bytes fit in int64
}

// sumTx returns the total transmitted bytes across all of a container's networks.
func sumTx(m map[string]netStats) int64 {
	var total uint64
	for _, n := range m {
		total += n.TxBytes
	}
	return int64(total) //nolint:gosec // uint64 from docker; bytes fit in int64
}

// blockIO sums blkio counters whose Op field matches (case-insensitively).
// op is one of "Read" or "Write" as emitted by the Docker daemon.
func blockIO(entries []blkioEntry, op string) int64 {
	var total uint64
	for _, e := range entries {
		if strings.EqualFold(e.Op, op) {
			total += e.Value
		}
	}
	return int64(total) //nolint:gosec // uint64 from docker; bytes fit in int64
}

// firstName returns the first name in a Docker name list with the leading
// "/" stripped for display.
func firstName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}
