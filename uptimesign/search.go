// Package uptimesign finds the highest uptime value the signing quorum is
// willing to attest to for a validator.
package uptimesign

import (
	"errors"
	"math"

	"github.com/ava-labs/avalanchego/vms/platformvm/warp"
)

const (
	// DefaultTolerance is how close (in seconds) the search gets to the
	// quorum's actual limit before it stops refining.
	DefaultTolerance uint64 = 1800

	// maxRampSteps bounds the upward probing when even the highest candidate
	// signs.
	maxRampSteps = 10
)

// ErrNothingSignable means the quorum would not sign any uptime above floor.
var ErrNothingSignable = errors.New("quorum did not sign any uptime above the on-chain uptime")

// SignFunc requests an aggregated signature for the given uptime.
type SignFunc func(uptime uint64) (*warp.Message, error)

// Highest returns the highest uptime above floor that sign succeeds for, along
// with its signed message.
//
// candidates are node-reported uptimes, sorted descending and all strictly
// above floor. They are tried first since they are usually close to what the
// quorum accepts. The result is then refined by binary search between the
// highest signed value and the lowest rejected one, to within tolerance
// seconds. If no candidate signs, the search runs between floor and the lowest
// candidate; if floor+1 itself is rejected, ErrNothingSignable is returned.
func Highest(candidates []uint64, floor, tolerance uint64, sign SignFunc) (uint64, *warp.Message, error) {
	if tolerance == 0 {
		tolerance = 1
	}

	var (
		lo, hi uint64 // highest signed value; lowest rejected value (0 = none yet)
		loMsg  *warp.Message
		last   error
	)

	for _, c := range candidates {
		if c <= floor {
			continue
		}
		msg, err := sign(c)
		if err == nil {
			lo, loMsg = c, msg
			break
		}
		hi, last = c, err
	}

	if loMsg == nil {
		// Nothing reported by the nodes signs. Check that anything above the
		// on-chain value does before searching below the lowest candidate.
		msg, err := sign(floor + 1)
		if err != nil {
			return 0, nil, errors.Join(ErrNothingSignable, err, last)
		}
		lo, loMsg = floor+1, msg
	}

	if hi == 0 {
		// The highest candidate signed: probe upward until the quorum refuses.
		for range maxRampSteps {
			next := uint64(math.Ceil(float64(lo) * 1.05))
			if next <= lo {
				next = lo + 1
			}
			msg, err := sign(next)
			if err != nil {
				hi = next
				break
			}
			lo, loMsg = next, msg
		}
		if hi == 0 {
			return lo, loMsg, nil
		}
	}

	for hi-lo > tolerance {
		mid := lo + (hi-lo)/2
		msg, err := sign(mid)
		if err != nil {
			hi = mid
			continue
		}
		lo, loMsg = mid, msg
	}

	return lo, loMsg, nil
}
