package collector

import (
	"context"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// Source is the per-collector contract: read-only, returns whatever this
// collector observed in this tick. A collector MUST NOT mutate host state.
type Source interface {
	Name() string
	Collect(ctx context.Context, batch *apitypes.IngestRequest) error
}

// InventoryProvider is implemented by collectors that contribute to the
// inventory snapshot (system info, disk topology, NIC list, …) so the agent's
// detect package can build the full snapshot deterministically.
type InventoryProvider interface {
	Inventory(ctx context.Context, snap *apitypes.InventorySnap) error
}
