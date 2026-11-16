package validator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"
)

const nodeRequestTimeout = 30 * time.Second

var httpClient = &http.Client{Timeout: nodeRequestTimeout}

type UptimeSample struct {
	ValidationID  string
	UptimeSeconds uint64
	NodeID        string
	IsActive      bool
}

// NodeUptimes is a single node's view of current validator uptimes.
type NodeUptimes struct {
	Endpoint string
	Uptimes  map[string]uint64 // validationID -> uptimeSeconds
	Err      error
}

type rpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	Result  *struct {
		Validators []struct {
			ValidationID  string `json:"validationID"`
			UptimeSeconds uint64 `json:"uptimeSeconds"`
			NodeID        string `json:"nodeID"`
			IsActive      bool   `json:"isActive"`
		} `json:"validators"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func FetchUptimesFromNode(apiBaseURL string) ([]UptimeSample, error) {
	reqBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"validators.getCurrentValidators","params":{}}`)
	url := apiBaseURL + "/validators"

	resp, err := httpClient.Post(url, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("call avalanche validators API (%s): %w", apiBaseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d from %s", resp.StatusCode, apiBaseURL)
	}

	var rpcResp rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("decode response from %s: %w", apiBaseURL, err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("avalanche API error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	if rpcResp.Result == nil {
		return nil, fmt.Errorf("invalid response: missing result from %s", apiBaseURL)
	}

	uptimes := make([]UptimeSample, 0, len(rpcResp.Result.Validators))
	for _, v := range rpcResp.Result.Validators {
		uptimes = append(uptimes, UptimeSample{
			ValidationID:  v.ValidationID,
			NodeID:        v.NodeID,
			IsActive:      v.IsActive,
			UptimeSeconds: v.UptimeSeconds,
		})
	}

	return uptimes, nil
}

// FetchNodeUptimes queries every endpoint concurrently and returns one result
// per endpoint, in the same order as endpoints. A failed node is reported via
// its Err field rather than dropped, so callers can surface it.
func FetchNodeUptimes(endpoints []string) []NodeUptimes {
	results := make([]NodeUptimes, len(endpoints))
	var wg sync.WaitGroup

	for i, endpoint := range endpoints {
		wg.Add(1)
		go func(i int, api string) {
			defer wg.Done()
			result := NodeUptimes{Endpoint: api}
			samples, err := FetchUptimesFromNode(api)
			if err != nil {
				result.Err = err
			} else {
				result.Uptimes = make(map[string]uint64, len(samples))
				for _, s := range samples {
					result.Uptimes[s.ValidationID] = s.UptimeSeconds
				}
			}
			results[i] = result
		}(i, endpoint)
	}

	wg.Wait()
	return results
}

// AggregateUptimes merges the successful node results into a map of
// validationID -> uptimeSeconds samples, sorted descending.
func AggregateUptimes(results []NodeUptimes) map[string][]uint64 {
	aggregated := make(map[string][]uint64)
	for _, r := range results {
		if r.Err != nil {
			continue
		}
		for id, uptime := range r.Uptimes {
			aggregated[id] = append(aggregated[id], uptime)
		}
	}

	for id := range aggregated {
		samples := aggregated[id]
		sort.Slice(samples, func(i, j int) bool { return samples[i] > samples[j] })
	}

	return aggregated
}

// NodeLabel returns a short, human-readable label (host:port) for an endpoint.
func NodeLabel(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return endpoint
	}
	return u.Host
}
