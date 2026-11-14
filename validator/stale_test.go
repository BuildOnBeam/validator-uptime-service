package validator

import (
	"errors"
	"fmt"
	"testing"
)

func TestFindStaleNodes(t *testing.T) {
	const validations = 20
	floors := make(map[string]uint64, validations)
	progressed := make(map[string]bool, validations)
	healthy := NodeUptimes{Endpoint: "http://healthy:9650", Uptimes: map[string]uint64{}}
	frozen := NodeUptimes{Endpoint: "http://frozen:9650", Uptimes: map[string]uint64{}}
	lagging := NodeUptimes{Endpoint: "http://lagging:9650", Uptimes: map[string]uint64{}}
	failed := NodeUptimes{Endpoint: "http://failed:9650", Err: errors.New("timeout")}

	for i := range validations {
		id := fmt.Sprintf("validation-%d", i)
		floors[id] = 1_000_000
		progressed[id] = true
		healthy.Uptimes[id] = 1_050_000
		// Frozen node: stuck below what is already recorded on-chain.
		frozen.Uptimes[id] = 990_000
		// Lagging node: behind on a single validation only.
		lagging.Uptimes[id] = 1_040_000
		if i == 0 {
			lagging.Uptimes[id] = 999_000
		}
	}

	stale := FindStaleNodes([]NodeUptimes{healthy, frozen, lagging, failed}, floors, progressed)
	if len(stale) != 1 {
		t.Fatalf("expected 1 stale node, got %d: %+v", len(stale), stale)
	}
	if got := stale[0]; got.Endpoint != frozen.Endpoint || got.Behind != validations || got.Compared != validations {
		t.Fatalf("unexpected stale node: %+v", got)
	}
}

func TestFindStaleNodesIgnoresValidatorsThatAreDown(t *testing.T) {
	// An accurate node shows no progress for offline validators while an
	// over-crediting node does. Since the quorum did not progress them, the
	// accurate node must not be flagged.
	floors := map[string]uint64{}
	accurate := NodeUptimes{Endpoint: "accurate", Uptimes: map[string]uint64{}}
	generous := NodeUptimes{Endpoint: "generous", Uptimes: map[string]uint64{}}
	for i := range 10 {
		id := fmt.Sprintf("down-%d", i)
		floors[id] = 500
		accurate.Uptimes[id] = 500
		generous.Uptimes[id] = 900
	}
	if stale := FindStaleNodes([]NodeUptimes{accurate, generous}, floors, map[string]bool{}); len(stale) != 0 {
		t.Fatalf("expected no stale nodes, got %+v", stale)
	}
}
