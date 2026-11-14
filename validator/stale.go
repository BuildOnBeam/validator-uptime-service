package validator

// StaleNode describes an API node whose readings lag behind the uptime already
// recorded on-chain while the signing quorum attests to more.
type StaleNode struct {
	Endpoint string
	Behind   int // progressed validations where this node is at or below the on-chain uptime
	Compared int // progressed validations this node reported
}

const (
	staleMinBehind = 5
	staleMinRatio  = 0.10
)

// FindStaleNodes flags nodes that report an uptime at or below the on-chain
// value (floors) for a meaningful share of the progressed validations, i.e.
// those the signing quorum attested to a higher uptime for in this run. The
// quorum is used as the reference rather than other nodes, because individual
// nodes can over-credit validators that are actually offline.
func FindStaleNodes(results []NodeUptimes, floors map[string]uint64, progressed map[string]bool) []StaleNode {
	var stale []StaleNode
	for _, r := range results {
		if r.Err != nil {
			continue
		}
		node := StaleNode{Endpoint: r.Endpoint}
		for id := range progressed {
			uptime, ok := r.Uptimes[id]
			if !ok {
				continue
			}
			node.Compared++
			if uptime <= floors[id] {
				node.Behind++
			}
		}
		if node.Behind >= staleMinBehind && float64(node.Behind) >= staleMinRatio*float64(node.Compared) {
			stale = append(stale, node)
		}
	}

	return stale
}
