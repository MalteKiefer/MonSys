package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// HostFacts is the read-only projection used to enrich alert notifications with
// human-readable host context (AUDIT/feature 2026-07-16 HTML alert emails).
type HostFacts struct {
	Hostname     string
	Distro       string
	Kernel       string
	Arch         string
	CPUCores     int
	RAMBytes     int64
	AgentVersion string
	Status       string
	LastSeen     time.Time
	IP           string
}

// HostFactsFor returns display facts for a host. It is best-effort context for
// notifications: callers treat an error as "no host block" and still deliver.
func (s *Store) HostFactsFor(ctx context.Context, hostID uuid.UUID) (HostFacts, error) {
	var f HostFacts
	var ip string
	err := s.Pool.QueryRow(ctx, `
		SELECT h.hostname,
		       COALESCE(h.distro, ''),
		       COALESCE(h.kernel, ''),
		       COALESCE(h.arch, ''),
		       COALESCE(h.cpu_cores, 0),
		       COALESCE(h.ram_total_bytes, 0),
		       COALESCE(h.agent_version, ''),
		       h.last_seen_at,
		       COALESCE(hs.status, 'unknown'),
		       COALESCE((
		           SELECT a FROM nics n, unnest(n.addrs) AS a
		           WHERE n.host_id = h.id
		             AND a NOT LIKE '127.%'
		             AND a NOT LIKE '::1%'
		             AND a NOT LIKE 'fe80%'
		             AND position('.' in a) > 0
		           ORDER BY a
		           LIMIT 1), '')
		FROM hosts h
		LEFT JOIN host_status hs ON hs.host_id = h.id
		WHERE h.id = $1 AND h.revoked_at IS NULL`,
		hostID,
	).Scan(&f.Hostname, &f.Distro, &f.Kernel, &f.Arch, &f.CPUCores,
		&f.RAMBytes, &f.AgentVersion, &f.LastSeen, &f.Status, &ip)
	if errors.Is(err, pgx.ErrNoRows) {
		return HostFacts{}, ErrHostNotFound
	}
	if err != nil {
		return HostFacts{}, err
	}
	// Addresses may carry a /prefix suffix; show the bare address.
	if i := strings.IndexByte(ip, '/'); i >= 0 {
		ip = ip[:i]
	}
	f.IP = ip
	return f, nil
}
