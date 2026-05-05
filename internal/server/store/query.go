package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

var ErrHostNotFound = errors.New("host not found")

func (s *Store) ListHosts(ctx context.Context) ([]apitypes.Host, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT h.id, h.hostname, COALESCE(h.distro,''), COALESCE(h.arch,''),
		       COALESCE(h.cpu_cores,0), COALESCE(h.ram_total_bytes,0),
		       COALESCE(h.agent_version,''), h.first_seen_at, h.last_seen_at, h.labels,
		       COALESCE(hs.status, 'unknown'),
		       hs.since,
		       COALESCE(t.tags, '{}') AS tags,
		       COALESCE(g.groups, '{}'::jsonb) AS groups,
		       ps.updates_count, ps.security_updates
		FROM hosts h
		LEFT JOIN host_status hs ON hs.host_id = h.id
		LEFT JOIN LATERAL (
			SELECT array_agg(tag ORDER BY tag) AS tags
			FROM host_tags WHERE host_id = h.id
		) t ON TRUE
		LEFT JOIN LATERAL (
			SELECT jsonb_agg(jsonb_build_object('id', hg.id, 'name', hg.name) ORDER BY hg.name) AS groups
			FROM host_group_members m JOIN host_groups hg ON hg.id = m.group_id
			WHERE m.host_id = h.id
		) g ON TRUE
		LEFT JOIN LATERAL (
			SELECT updates_count, security_updates
			FROM package_summary
			WHERE host_id = h.id
			ORDER BY time DESC
			LIMIT 1
		) ps ON TRUE
		WHERE h.revoked_at IS NULL
		ORDER BY h.hostname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []apitypes.Host{}
	hostIDs := []uuid.UUID{}
	for rows.Next() {
		var (
			h           apitypes.Host
			id          uuid.UUID
			labels      []byte
			statusSince *time.Time
			tags        []string
			groupsRaw   []byte
		)
		if err := rows.Scan(&id, &h.Hostname, &h.Distro, &h.Arch,
			&h.CPUCores, &h.RAMTotalBytes, &h.AgentVersion,
			&h.FirstSeenAt, &h.LastSeenAt, &labels,
			&h.Status, &statusSince, &tags, &groupsRaw,
			&h.PendingUpdates, &h.SecurityUpdates); err != nil {
			return nil, err
		}
		h.ID = id.String()
		if len(labels) > 0 {
			_ = json.Unmarshal(labels, &h.Labels)
		}
		if h.Labels == nil {
			h.Labels = map[string]string{}
		}
		if statusSince != nil {
			h.StatusSince = *statusSince
		}
		h.Tags = tags
		if h.Tags == nil {
			h.Tags = []string{}
		}
		h.Groups = []apitypes.HostGroupRef{}
		if len(groupsRaw) > 0 && string(groupsRaw) != "{}" {
			_ = json.Unmarshal(groupsRaw, &h.Groups)
		}
		h.DistroFamily = distroFamily(h.Distro)
		out = append(out, h)
		hostIDs = append(hostIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Service detection from workloads: one query for all hosts.
	if len(hostIDs) > 0 {
		services, err := s.detectServices(ctx, hostIDs)
		if err == nil {
			for i := range out {
				if v, ok := services[out[i].ID]; ok {
					out[i].Services = v
				}
			}
		}
	}
	return out, nil
}

// detectServices fingerprints well-known services per host by combining two
// inventory sources: container workloads (name + image) and OS packages
// (name only). Containerised stacks were the original signal, but most home
// installs run things like jellyfin or pihole as native dpkg/rpm packages,
// so a workloads-only scan misses them entirely.
//
// The returned map keys are host UUIDs as strings (matching apitypes.Host.ID).
func (s *Store) detectServices(ctx context.Context, hostIDs []uuid.UUID) (map[string][]string, error) {
	hits := map[string]map[string]struct{}{}
	add := func(hid uuid.UUID, hay string) {
		key := hid.String()
		set, ok := hits[key]
		if !ok {
			set = map[string]struct{}{}
			hits[key] = set
		}
		for _, m := range serviceMatchers {
			if m.match(hay) {
				set[m.name] = struct{}{}
			}
		}
	}

	// 1) Container workloads — image + name.
	wlRows, err := s.Pool.Query(ctx, `
		SELECT host_id, lower(COALESCE(name,'')), lower(COALESCE(image,''))
		FROM workloads WHERE host_id = ANY($1)`, hostIDs)
	if err != nil {
		return nil, err
	}
	for wlRows.Next() {
		var hid uuid.UUID
		var name, image string
		if err := wlRows.Scan(&hid, &name, &image); err != nil {
			wlRows.Close()
			return nil, err
		}
		add(hid, name+" "+image)
	}
	wlRows.Close()
	if err := wlRows.Err(); err != nil {
		return nil, err
	}

	// 2) OS packages — name only. Skip the obvious -dev/-doc/-data noise so
	// a build-dep doesn't trigger a false positive (e.g. libpostgresql-dev
	// must not paint a "postgres" badge on a plain dev workstation).
	pkgRows, err := s.Pool.Query(ctx, `
		SELECT host_id, lower(name)
		FROM packages
		WHERE host_id = ANY($1)
		  AND name NOT ILIKE '%-dev'
		  AND name NOT ILIKE '%-doc'
		  AND name NOT ILIKE '%-data'
		  AND name NOT ILIKE '%-common'
		  AND name NOT ILIKE 'lib%'`, hostIDs)
	if err != nil {
		return nil, err
	}
	for pkgRows.Next() {
		var hid uuid.UUID
		var name string
		if err := pkgRows.Scan(&hid, &name); err != nil {
			pkgRows.Close()
			return nil, err
		}
		add(hid, name)
	}
	pkgRows.Close()
	if err := pkgRows.Err(); err != nil {
		return nil, err
	}

	out := map[string][]string{}
	for k, v := range hits {
		list := make([]string, 0, len(v))
		for s := range v {
			list = append(list, s)
		}
		// Stable order so the UI doesn't shuffle on every refetch.
		for i := 0; i < len(list); i++ {
			for j := i + 1; j < len(list); j++ {
				if list[j] < list[i] {
					list[i], list[j] = list[j], list[i]
				}
			}
		}
		out[k] = list
	}
	return out, nil
}

type serviceMatcher struct {
	name     string
	keywords []string
}

func (m serviceMatcher) match(hay string) bool {
	for _, k := range m.keywords {
		if strings.Contains(hay, k) {
			return true
		}
	}
	return false
}

// Order matters: more specific matchers first. "timescaledb" before
// "postgres" so a TimescaleDB image is reported as both. Each matcher's
// keywords are searched in `lower(name) + " " + lower(image)` so a single
// keyword can hit either a container name or its image reference.
var serviceMatchers = []serviceMatcher{
	// --- databases / caches ----------------------------------------------
	{"postgres", []string{"postgres", "timescaledb", "pgvector", "/citus", "supabase/postgres"}},
	{"mariadb", []string{"mariadb", "mysql"}},
	{"mongodb", []string{"mongo"}},
	{"redis", []string{"redis"}},
	{"valkey", []string{"valkey"}},
	{"dragonfly", []string{"dragonfly"}},
	{"memcached", []string{"memcached"}},
	{"clickhouse", []string{"clickhouse"}},
	{"cassandra", []string{"cassandra"}},
	{"cockroachdb", []string{"cockroach"}},
	{"influxdb", []string{"influxdb", "/influx"}},
	{"neo4j", []string{"neo4j"}},
	{"couchdb", []string{"couchdb"}},
	{"surrealdb", []string{"surrealdb"}},
	{"qdrant", []string{"qdrant"}},
	{"weaviate", []string{"weaviate"}},
	{"milvus", []string{"milvus"}},
	{"meilisearch", []string{"meilisearch"}},
	{"typesense", []string{"typesense"}},
	{"etcd", []string{"/etcd", "quay.io/coreos/etcd"}},

	// --- web servers / reverse proxies / cdn -----------------------------
	{"nginx", []string{"nginx"}},
	{"caddy", []string{"caddy"}},
	{"traefik", []string{"traefik"}},
	{"haproxy", []string{"haproxy"}},
	{"apache", []string{"httpd:", "/httpd", "apache"}},
	{"envoy", []string{"envoyproxy", "/envoy"}},
	{"kong", []string{"/kong"}},
	{"openresty", []string{"openresty"}},
	{"varnish", []string{"varnish"}},
	{"cloudflared", []string{"cloudflare/cloudflared", "cloudflared"}},

	// --- observability ---------------------------------------------------
	{"grafana", []string{"grafana/grafana", "grafana"}},
	{"prometheus", []string{"prom/prometheus", "prometheus"}},
	{"alertmanager", []string{"alertmanager"}},
	{"loki", []string{"grafana/loki", "loki"}},
	{"tempo", []string{"grafana/tempo", "tempo"}},
	{"mimir", []string{"grafana/mimir", "mimir"}},
	{"jaeger", []string{"jaegertracing", "jaeger"}},
	{"uptime-kuma", []string{"uptime-kuma"}},
	{"telegraf", []string{"telegraf"}},
	{"vector", []string{"timberio/vector", "/vector:"}},
	{"victoriametrics", []string{"victoriametrics", "/vmagent", "/vmselect", "/vminsert"}},
	{"signoz", []string{"signoz"}},
	{"fluentbit", []string{"fluent-bit", "fluentbit"}},
	{"netdata", []string{"netdata"}},

	// --- logging / search frontends --------------------------------------
	{"elasticsearch", []string{"elasticsearch"}},
	{"opensearch", []string{"opensearch"}},
	{"kibana", []string{"kibana"}},
	{"graylog", []string{"graylog"}},
	{"dozzle", []string{"dozzle"}},

	// --- messaging / streaming ------------------------------------------
	{"rabbitmq", []string{"rabbitmq"}},
	{"nats", []string{"nats:"}},
	{"kafka", []string{"kafka"}},
	{"redpanda", []string{"redpanda"}},
	{"mosquitto", []string{"mosquitto"}},

	// --- identity / auth ------------------------------------------------
	{"vault", []string{"hashicorp/vault", "vault:"}},
	{"keycloak", []string{"keycloak"}},
	{"authelia", []string{"authelia"}},
	{"authentik", []string{"goauthentik", "authentik"}},
	{"pocketid", []string{"pocket-id", "pocketid"}},
	{"lldap", []string{"lldap"}},
	{"openldap", []string{"openldap"}},
	{"vaultwarden", []string{"vaultwarden", "bitwarden"}},

	// --- devops / code / ci / artifact -----------------------------------
	{"gitea", []string{"gitea"}},
	{"forgejo", []string{"forgejo"}},
	{"gitlab", []string{"gitlab"}},
	{"drone", []string{"/drone:", "drone/drone"}},
	{"woodpecker", []string{"woodpecker"}},
	{"jenkins", []string{"jenkins"}},
	{"harbor", []string{"goharbor", "/harbor-"}},
	{"registry", []string{"/registry:", "registry:2"}},
	{"nexus", []string{"sonatype/nexus"}},
	{"sonarqube", []string{"sonarqube"}},
	{"argocd", []string{"argoproj", "argocd"}},
	{"portainer", []string{"portainer"}},
	{"watchtower", []string{"containrrr/watchtower", "watchtower"}},

	// --- self-hosted apps -----------------------------------------------
	{"nextcloud", []string{"nextcloud"}},
	{"immich", []string{"immich"}},
	{"photoprism", []string{"photoprism"}},
	{"paperless", []string{"paperless-ngx", "/paperless"}},
	{"vikunja", []string{"vikunja"}},
	{"syncthing", []string{"syncthing"}},
	{"audiobookshelf", []string{"audiobookshelf"}},
	{"bookstack", []string{"bookstack"}},
	{"miniflux", []string{"miniflux"}},
	{"wallabag", []string{"wallabag"}},
	{"seafile", []string{"seafile"}},
	{"freshrss", []string{"freshrss"}},
	{"healthvault", []string{"healthvault"}},

	// --- media / *arr / download clients --------------------------------
	{"jellyfin", []string{"jellyfin"}},
	{"plex", []string{"/plex"}},
	{"emby", []string{"emby"}},
	{"radarr", []string{"radarr"}},
	{"sonarr", []string{"sonarr"}},
	{"lidarr", []string{"lidarr"}},
	{"bazarr", []string{"bazarr"}},
	{"prowlarr", []string{"prowlarr"}},
	{"jellyseerr", []string{"jellyseerr", "overseerr"}},
	{"sabnzbd", []string{"sabnzbd"}},
	{"qbittorrent", []string{"qbittorrent"}},
	{"transmission", []string{"transmission"}},
	{"navidrome", []string{"navidrome"}},

	// --- smart home -----------------------------------------------------
	{"homeassistant", []string{"homeassistant", "home-assistant", "ghcr.io/home-assistant"}},
	{"zigbee2mqtt", []string{"zigbee2mqtt"}},
	{"nodered", []string{"node-red", "nodered"}},
	{"frigate", []string{"frigate"}},
	{"esphome", []string{"esphome"}},

	// --- virtualization -------------------------------------------------
	{"proxmox", []string{"pve-manager", "proxmox-ve", "pve-cluster", "/proxmox", "qemu-server"}},

	// --- networking / dns / vpn -----------------------------------------
	{"pihole", []string{"pi-hole", "pihole"}},
	{"adguard", []string{"adguardhome", "/adguard"}},
	{"unbound", []string{"unbound"}},
	{"headscale", []string{"headscale"}},
	{"tailscale", []string{"tailscale"}},
	{"wireguard", []string{"wireguard", "wg-easy"}},
	{"netbird", []string{"netbird"}},

	// --- mail / messaging gateways --------------------------------------
	{"mailcow", []string{"mailcow"}},
	{"postfix", []string{"postfix"}},
	{"dovecot", []string{"dovecot"}},
	{"ntfy", []string{"binwiederhier/ntfy", "/ntfy"}},
	{"gotify", []string{"gotify"}},

	// --- security / waf -------------------------------------------------
	{"crowdsec", []string{"crowdsecurity", "crowdsec"}},
	{"anubis", []string{"anubis"}},

	// --- self ------------------------------------------------------------
	{"mon", []string{"mon-server", "monsys"}},
}

// distroFamily collapses a /etc/os-release "PRETTY_NAME" string into one of a
// short list of icon families the UI knows how to draw.
func distroFamily(distro string) string {
	d := strings.ToLower(distro)
	switch {
	case strings.Contains(d, "endeavour"), strings.Contains(d, "manjaro"), strings.Contains(d, "arch"):
		return "arch"
	case strings.Contains(d, "ubuntu"):
		return "ubuntu"
	case strings.Contains(d, "debian"):
		return "debian"
	case strings.Contains(d, "fedora"):
		return "fedora"
	case strings.Contains(d, "rocky"), strings.Contains(d, "alma"), strings.Contains(d, "centos"), strings.Contains(d, "rhel"), strings.Contains(d, "red hat"):
		return "rhel"
	case strings.Contains(d, "alpine"):
		return "alpine"
	case strings.Contains(d, "suse"), strings.Contains(d, "opensuse"):
		return "suse"
	case strings.Contains(d, "nixos"):
		return "nixos"
	case strings.Contains(d, "proxmox"):
		return "debian"
	}
	return ""
}

func (s *Store) QuerySystemMetrics(ctx context.Context, hostID uuid.UUID, from, to time.Time) ([]apitypes.SystemSample, error) {
	var exists bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hosts WHERE id = $1)`, hostID).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("host check: %w", err)
	}
	if !exists {
		return nil, ErrHostNotFound
	}

	rows, err := s.Pool.Query(ctx, `
		SELECT time, cpu_usage_pct, cpu_per_core,
		       load_1, load_5, load_15,
		       ram_used_bytes, ram_avail_bytes, swap_used_bytes, uptime_sec
		FROM metrics_system
		WHERE host_id = $1 AND time >= $2 AND time <= $3
		ORDER BY time ASC`,
		hostID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []apitypes.SystemSample{}
	for rows.Next() {
		var m apitypes.SystemSample
		var perCore []float64
		if err := rows.Scan(&m.Time, &m.CPUUsagePct, &perCore,
			&m.Load1, &m.Load5, &m.Load15,
			&m.RAMUsedBytes, &m.RAMAvailBytes, &m.SwapUsedBytes, &m.UptimeSec); err != nil {
			return nil, err
		}
		m.CPUPerCore = perCore
		out = append(out, m)
	}
	return out, rows.Err()
}

// HostSecurity bundles the security-relevant snapshot a UI typically wants
// in a single fetch.
type HostSecurity struct {
	Firewalls []apitypes.FirewallStatus   `json:"firewalls"`
	Fail2ban  []apitypes.Fail2banJailInfo `json:"fail2ban"`
	CrowdSec  []apitypes.CrowdsecDecision `json:"crowdsec"`
}

func (s *Store) HostSecurity(ctx context.Context, hostID uuid.UUID) (HostSecurity, error) {
	var hs HostSecurity

	rows, err := s.Pool.Query(ctx, `
		SELECT engine, active,
		       COALESCE(default_input,''), COALESCE(default_output,''), COALESCE(default_forward,''),
		       COALESCE(rule_count,0), COALESCE(snapshot_excerpt,'')
		FROM firewall_status WHERE host_id = $1`, hostID)
	if err != nil {
		return hs, fmt.Errorf("firewall_status: %w", err)
	}
	for rows.Next() {
		var f apitypes.FirewallStatus
		if err := rows.Scan(&f.Engine, &f.Active,
			&f.DefaultInput, &f.DefaultOutput, &f.DefaultForward,
			&f.RuleCount, &f.SnapshotExcerpt); err != nil {
			rows.Close()
			return hs, err
		}
		hs.Firewalls = append(hs.Firewalls, f)
	}
	rows.Close()

	rows, err = s.Pool.Query(ctx, `
		SELECT jail, COALESCE(currently_failed,0), COALESCE(total_failed,0),
		       COALESCE(currently_banned,0), COALESCE(total_banned,0), COALESCE(banned_ips,'{}')
		FROM fail2ban_jails WHERE host_id = $1`, hostID)
	if err != nil {
		return hs, fmt.Errorf("fail2ban_jails: %w", err)
	}
	for rows.Next() {
		var j apitypes.Fail2banJailInfo
		if err := rows.Scan(&j.Jail, &j.CurrentlyFailed, &j.TotalFailed,
			&j.CurrentlyBanned, &j.TotalBanned, &j.BannedIPs); err != nil {
			rows.Close()
			return hs, err
		}
		hs.Fail2ban = append(hs.Fail2ban, j)
	}
	rows.Close()

	rows, err = s.Pool.Query(ctx, `
		SELECT decision_id, COALESCE(origin,''), COALESCE(scope,''), COALESCE(target,''),
		       COALESCE(decision_type,''), COALESCE(reason,''), COALESCE(until, 'epoch'::timestamptz)
		FROM crowdsec_decisions WHERE host_id = $1`, hostID)
	if err != nil {
		return hs, fmt.Errorf("crowdsec_decisions: %w", err)
	}
	for rows.Next() {
		var d apitypes.CrowdsecDecision
		if err := rows.Scan(&d.DecisionID, &d.Origin, &d.Scope, &d.Target,
			&d.Type, &d.Reason, &d.Until); err != nil {
			rows.Close()
			return hs, err
		}
		hs.CrowdSec = append(hs.CrowdSec, d)
	}
	rows.Close()

	return hs, nil
}

func (s *Store) ListHostLogins(ctx context.Context, hostID uuid.UUID, since time.Time, limit int) ([]apitypes.LoginEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT time, COALESCE(username,''), COALESCE(source_ip,''),
		       COALESCE(method,''), success, COALESCE(detail,'')
		FROM login_events
		WHERE host_id = $1 AND time >= $2
		ORDER BY time DESC
		LIMIT $3`, hostID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []apitypes.LoginEvent{}
	for rows.Next() {
		var e apitypes.LoginEvent
		if err := rows.Scan(&e.Time, &e.Username, &e.SourceIP, &e.Method, &e.Success, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// QueryDiskMetrics returns disk samples in [from, to], optionally filtered to
// a specific disk_id. Results are flat — caller groups by disk_id for charting.
func (s *Store) QueryDiskMetrics(ctx context.Context, hostID uuid.UUID, from, to time.Time, diskID *uuid.UUID) ([]apitypes.DiskSample, []string, error) {
	if err := s.requireHost(ctx, hostID); err != nil {
		return nil, nil, err
	}
	args := []any{hostID, from, to}
	filter := ""
	if diskID != nil {
		filter = " AND m.disk_id = $4"
		args = append(args, *diskID)
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT m.time, d.device, d.mountpoint,
		       COALESCE(m.used_bytes,0), COALESCE(m.free_bytes,0),
		       COALESCE(m.inodes_used,0), COALESCE(m.inodes_free,0),
		       COALESCE(m.read_bytes,0), COALESCE(m.write_bytes,0),
		       COALESCE(m.read_ops,0), COALESCE(m.write_ops,0),
		       COALESCE(m.io_time_ms,0)
		FROM metrics_disk m JOIN disks d ON d.id = m.disk_id
		WHERE m.host_id = $1 AND m.time >= $2 AND m.time <= $3`+filter+`
		ORDER BY m.time ASC`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := []apitypes.DiskSample{}
	seen := map[string]struct{}{}
	devices := []string{}
	for rows.Next() {
		var s apitypes.DiskSample
		if err := rows.Scan(&s.Time, &s.Device, &s.Mountpoint,
			&s.UsedBytes, &s.FreeBytes, &s.InodesUsed, &s.InodesFree,
			&s.ReadBytes, &s.WriteBytes, &s.ReadOps, &s.WriteOps, &s.IOTimeMS); err != nil {
			return nil, nil, err
		}
		out = append(out, s)
		if _, ok := seen[s.Mountpoint]; !ok {
			seen[s.Mountpoint] = struct{}{}
			devices = append(devices, s.Mountpoint)
		}
	}
	return out, devices, rows.Err()
}

// QueryNetMetrics returns nic samples in [from, to].
func (s *Store) QueryNetMetrics(ctx context.Context, hostID uuid.UUID, from, to time.Time, nicID *uuid.UUID) ([]apitypes.NetSample, []string, error) {
	if err := s.requireHost(ctx, hostID); err != nil {
		return nil, nil, err
	}
	args := []any{hostID, from, to}
	filter := ""
	if nicID != nil {
		filter = " AND m.nic_id = $4"
		args = append(args, *nicID)
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT m.time, n.name,
		       COALESCE(m.rx_bytes,0), COALESCE(m.tx_bytes,0),
		       COALESCE(m.rx_pkts,0),  COALESCE(m.tx_pkts,0),
		       COALESCE(m.rx_errs,0),  COALESCE(m.tx_errs,0),
		       COALESCE(m.rx_drops,0), COALESCE(m.tx_drops,0)
		FROM metrics_net m JOIN nics n ON n.id = m.nic_id
		WHERE m.host_id = $1 AND m.time >= $2 AND m.time <= $3`+filter+`
		ORDER BY m.time ASC`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := []apitypes.NetSample{}
	seen := map[string]struct{}{}
	nics := []string{}
	for rows.Next() {
		var s apitypes.NetSample
		if err := rows.Scan(&s.Time, &s.NicName,
			&s.RxBytes, &s.TxBytes, &s.RxPkts, &s.TxPkts,
			&s.RxErrs, &s.TxErrs, &s.RxDrops, &s.TxDrops); err != nil {
			return nil, nil, err
		}
		out = append(out, s)
		if _, ok := seen[s.NicName]; !ok {
			seen[s.NicName] = struct{}{}
			nics = append(nics, s.NicName)
		}
	}
	return out, nics, rows.Err()
}

func (s *Store) requireHost(ctx context.Context, hostID uuid.UUID) error {
	var ok bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hosts WHERE id = $1)`, hostID).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return ErrHostNotFound
	}
	return nil
}

// SearchPackages searches installed packages across hosts (or one host) with
// optional manager filter and case-insensitive name/version matching.
type PackageSearchResult struct {
	HostID      string
	Hostname    string
	Manager     string
	Name        string
	Version     string
	Arch        string
	SourceRepo  string
	InstalledAt *time.Time
}

func (s *Store) SearchPackages(ctx context.Context, q, manager string, hostID *uuid.UUID, limit, offset int) ([]PackageSearchResult, int, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	args := []any{}
	where := []string{"1=1"}
	addArg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if q != "" {
		// ILIKE on name and version. Normalize the query so trailing/leading
		// whitespace doesn't matter.
		pattern := "%" + q + "%"
		where = append(where, fmt.Sprintf("(p.name ILIKE %s OR p.version ILIKE %s)", addArg(pattern), addArg(pattern)))
	}
	if manager != "" {
		where = append(where, fmt.Sprintf("p.manager = %s", addArg(manager)))
	}
	if hostID != nil {
		where = append(where, fmt.Sprintf("p.host_id = %s", addArg(*hostID)))
	}

	whereSQL := strings.Join(where, " AND ")

	var total int
	if err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM packages p WHERE `+whereSQL, args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	rows, err := s.Pool.Query(ctx, `
		SELECT p.host_id, h.hostname, p.manager, p.name, p.version,
		       COALESCE(p.arch,''), COALESCE(p.source_repo,''), p.installed_at
		FROM packages p JOIN hosts h ON h.id = p.host_id
		WHERE `+whereSQL+`
		ORDER BY p.name, h.hostname
		LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []PackageSearchResult{}
	for rows.Next() {
		var r PackageSearchResult
		var hid uuid.UUID
		if err := rows.Scan(&hid, &r.Hostname, &r.Manager, &r.Name, &r.Version,
			&r.Arch, &r.SourceRepo, &r.InstalledAt); err != nil {
			return nil, 0, err
		}
		r.HostID = hid.String()
		out = append(out, r)
	}
	return out, total, rows.Err()
}

var _ = pgx.ErrNoRows
