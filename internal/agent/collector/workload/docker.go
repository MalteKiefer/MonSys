// Package workload contains workload (container/VM/pod) collectors.
//
// docker.go talks to the Docker Engine API directly over its UNIX socket.
// We deliberately do not depend on the upstream docker client SDK to keep the
// agent binary small and our supply-chain narrow; the Engine API surface we
// need is tiny (list + stats).
//
// All calls are GET-only — the agent never mutates container state.
package workload

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pr0ph37/mon/internal/shared/apitypes"
)

// DefaultDockerEndpoint is the conventional Docker socket on Linux. The agent
// is expected to be a member of the docker group OR connected through a
// read-only docker socket proxy (operator's choice — see deploy/ docs).
const DefaultDockerEndpoint = "unix:///var/run/docker.sock"

// Docker is a Source + InventoryProvider for the Docker workload.
type Docker struct {
	endpoint string
	hc       *http.Client
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
		endpoint: endpoint,
		hc:       newHTTPClient(endpoint),
	}
}

func (d *Docker) Name() string { return "docker" }

// Available reports whether the configured socket can be reached. Used by the
// agent orchestrator to decide whether to register this collector at all.
func (d *Docker) Available(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
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

func (d *Docker) Inventory(ctx context.Context, snap *apitypes.InventorySnap) error {
	containers, err := d.listContainers(ctx)
	if err != nil {
		return err
	}
	for _, c := range containers {
		snap.Workloads = append(snap.Workloads, apitypes.WorkloadInfo{
			Kind:       "docker",
			ExternalID: c.ID,
			Name:       firstName(c.Names),
			Image:      c.Image,
			State:      c.State,
			Labels:     c.Labels,
		})
	}
	return nil
}

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
		batch.Workloads = append(batch.Workloads, apitypes.WorkloadSample{
			Time:            now,
			Kind:            "docker",
			ExternalID:      c.ID,
			CPUUsagePct:     cpuPercent(s),
			MemUsedBytes:    int64(s.MemoryStats.Usage), //nolint:gosec // uint64 from gopsutil/docker; bytes/packets fit in int64
			MemLimitBytes:   int64(s.MemoryStats.Limit), //nolint:gosec // uint64 from gopsutil/docker; bytes/packets fit in int64
			NetRxBytes:      sumRx(s.Networks),
			NetTxBytes:      sumTx(s.Networks),
			BlockReadBytes:  blockIO(s.BlkioStats.IOServiceBytesRecursive, "Read"),
			BlockWriteBytes: blockIO(s.BlkioStats.IOServiceBytesRecursive, "Write"),
			State:           c.State,
		})
	}
	return nil
}

// --- internal HTTP plumbing ------------------------------------------------

func newHTTPClient(endpoint string) *http.Client {
	tr := &http.Transport{
		// Don't share connections across hosts; one socket only.
		MaxIdleConns:        4,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  true,
		ResponseHeaderTimeout: 10 * time.Second,
	}
	if strings.HasPrefix(endpoint, "unix://") {
		path := strings.TrimPrefix(endpoint, "unix://")
		tr.DialContext = func(ctx context.Context, _ string, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		}
	}
	return &http.Client{Transport: tr, Timeout: 15 * time.Second}
}

func (d *Docker) req(ctx context.Context, method, path string, q url.Values) (*http.Request, error) {
	// For unix sockets the host is irrelevant; "docker" is a stable placeholder.
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

type dockerContainer struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	State  string            `json:"State"`
	Labels map[string]string `json:"Labels"`
}

func (d *Docker) listContainers(ctx context.Context) ([]dockerContainer, error) {
	q := url.Values{}
	q.Set("all", "true") // include stopped, so we can report them
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
		return nil, fmt.Errorf("docker /containers/json: status %d", resp.StatusCode)
	}
	var out []dockerContainer
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode containers: %w", err)
	}
	return out, nil
}

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
		return nil, fmt.Errorf("docker /containers/%s/stats: status %d", id, resp.StatusCode)
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

func sumRx(m map[string]netStats) int64 {
	var total uint64
	for _, n := range m {
		total += n.RxBytes
	}
	return int64(total) //nolint:gosec // uint64 from gopsutil/docker; bytes/packets fit in int64
}

func sumTx(m map[string]netStats) int64 {
	var total uint64
	for _, n := range m {
		total += n.TxBytes
	}
	return int64(total) //nolint:gosec // uint64 from gopsutil/docker; bytes/packets fit in int64
}

func blockIO(entries []blkioEntry, op string) int64 {
	var total uint64
	for _, e := range entries {
		if strings.EqualFold(e.Op, op) {
			total += e.Value
		}
	}
	return int64(total) //nolint:gosec // uint64 from gopsutil/docker; bytes/packets fit in int64
}

func firstName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	// Docker prefixes names with "/"; strip it for display.
	return strings.TrimPrefix(names[0], "/")
}
