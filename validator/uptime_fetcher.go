package validator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
)

type UptimeSample struct {
	ValidationID  string
	UptimeSeconds uint64
	NodeID        string
	IsActive      bool
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

	resp, err := http.Post(url, "application/json", bytes.NewReader(reqBody))
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

// FetchAggregatedUptimes fetches uptimes from multiple endpoints and aggregates them
// into a map of validationID -> sorted slice of uptimeSeconds (descending).
func FetchAggregatedUptimes(endpoints []string) map[string][]uint64 {
	type safeMap struct {
		sync.Mutex
		data map[string][]uint64
	}

	result := safeMap{data: make(map[string][]uint64)}
	var wg sync.WaitGroup

	for _, endpoint := range endpoints {
		wg.Add(1)
		go func(api string) {
			defer wg.Done()
			uptimes, err := FetchUptimesFromNode(api)
			if err != nil {
				return // log if you want, but silently ignore one bad node
			}
			result.Lock()
			for _, u := range uptimes {
				result.data[u.ValidationID] = append(result.data[u.ValidationID], u.UptimeSeconds)
			}
			result.Unlock()
		}(endpoint)
	}

	wg.Wait()

	for id := range result.data {
		slice := result.data[id]
		sort.Slice(slice, func(i, j int) bool { return slice[i] > slice[j] })
		result.data[id] = slice
	}

	return result.data
}
