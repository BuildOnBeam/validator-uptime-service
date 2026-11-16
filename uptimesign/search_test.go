package uptimesign

import (
	"errors"
	"testing"

	"github.com/ava-labs/avalanchego/vms/platformvm/warp"
)

// fakeQuorum signs any uptime up to limit, mirroring how validators only
// attest to uptime they have observed themselves.
type fakeQuorum struct {
	limit uint64
	calls int
}

func (q *fakeQuorum) sign(uptime uint64) (*warp.Message, error) {
	q.calls++
	if uptime > q.limit {
		return nil, errors.New("failed to aggregate signatures")
	}
	return &warp.Message{}, nil
}

func TestHighest(t *testing.T) {
	const tolerance = DefaultTolerance

	tests := []struct {
		name       string
		candidates []uint64
		floor      uint64
		limit      uint64
	}{
		{
			// Every node reading is slightly above what the quorum signs.
			name:       "all candidates rejected",
			candidates: []uint64{52_789_159, 52_362_329},
			floor:      49_787_285,
			limit:      52_343_873,
		},
		{
			name:       "lower candidate signs",
			candidates: []uint64{52_789_159, 52_200_000},
			floor:      49_787_285,
			limit:      52_343_873,
		},
		{
			name:       "highest candidate signs and quorum allows more",
			candidates: []uint64{52_000_000, 51_000_000},
			floor:      49_787_285,
			limit:      52_343_873,
		},
		{
			name:       "quorum only slightly above floor",
			candidates: []uint64{52_789_159},
			floor:      49_787_285,
			limit:      49_787_300,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := &fakeQuorum{limit: tt.limit}
			got, msg, err := Highest(tt.candidates, tt.floor, tolerance, q.sign)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if msg == nil {
				t.Fatal("expected a signed message")
			}
			if got <= tt.floor {
				t.Fatalf("result %d is not above floor %d", got, tt.floor)
			}
			if got > tt.limit {
				t.Fatalf("result %d exceeds quorum limit %d", got, tt.limit)
			}
			if tt.limit-got > tolerance {
				t.Fatalf("result %d is %d below quorum limit %d, want within %d", got, tt.limit-got, tt.limit, tolerance)
			}
			if q.calls > 40 {
				t.Fatalf("too many signing attempts: %d", q.calls)
			}
		})
	}
}

func TestHighestNothingAboveFloor(t *testing.T) {
	q := &fakeQuorum{limit: 47_787_212}
	_, _, err := Highest([]uint64{47_900_000}, 47_787_212, DefaultTolerance, q.sign)
	if !errors.Is(err, ErrNothingSignable) {
		t.Fatalf("expected ErrNothingSignable, got %v", err)
	}
}

func TestHighestNeverSignsAtOrBelowFloor(t *testing.T) {
	q := &fakeQuorum{limit: 100}
	var signed []uint64
	sign := func(uptime uint64) (*warp.Message, error) {
		signed = append(signed, uptime)
		return q.sign(uptime)
	}
	// A stale reading at the floor must never be attempted.
	if _, _, err := Highest([]uint64{500, 50}, 50, 1, sign); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, v := range signed {
		if v <= 50 {
			t.Fatalf("attempted to sign %d, at or below floor 50", v)
		}
	}
}
