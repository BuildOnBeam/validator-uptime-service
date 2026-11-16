package subgraph

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ava-labs/avalanchego/ids"
)

// maxAliasesPerQuery bounds how many validations are looked up per request.
const maxAliasesPerQuery = 100

type Client struct {
	endpoint string
	http     *http.Client
}

func NewClient(endpoint string) *Client {
	return &Client{
		endpoint: endpoint,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

type graphQLRequest struct {
	Query string `json:"query"`
}

type graphQLError struct {
	Message string `json:"message"`
}

type uptimeUpdate struct {
	UptimeSeconds string `json:"uptimeSeconds"`
}

// LatestUptimes returns the most recent uptime recorded on-chain (via the
// staking manager's UptimeUpdated event) for each validation ID. Validations
// with no recorded update are omitted from the result.
func (c *Client) LatestUptimes(validationIDs []ids.ID) (map[ids.ID]uint64, error) {
	latest := make(map[ids.ID]uint64, len(validationIDs))

	for start := 0; start < len(validationIDs); start += maxAliasesPerQuery {
		end := min(start+maxAliasesPerQuery, len(validationIDs))
		batch := validationIDs[start:end]

		var query strings.Builder
		query.WriteString("query {")
		for i, id := range batch {
			fmt.Fprintf(&query,
				` v%d: uptimeUpdates(first: 1, orderBy: epoch, orderDirection: desc, where: {validationID: "0x%s"}) { uptimeSeconds }`,
				i, id.Hex())
		}
		query.WriteString(" }")

		data, err := c.do(query.String())
		if err != nil {
			return nil, err
		}

		for i, id := range batch {
			raw, ok := data[fmt.Sprintf("v%d", i)]
			if !ok {
				continue
			}
			var updates []uptimeUpdate
			if err := json.Unmarshal(raw, &updates); err != nil {
				return nil, fmt.Errorf("decode uptime updates for %s: %w", id, err)
			}
			if len(updates) == 0 {
				continue
			}
			uptime, err := strconv.ParseUint(updates[0].UptimeSeconds, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("parse uptime for %s: %w", id, err)
			}
			latest[id] = uptime
		}
	}

	return latest, nil
}

// ActiveValidations returns the IDs of all validations the subgraph reports as
// Active.
func (c *Client) ActiveValidations() ([]ids.ID, error) {
	const pageSize = 1000
	var (
		out    []ids.ID
		lastID = "0x"
	)

	for {
		data, err := c.do(fmt.Sprintf(
			`query { validations(first: %d, orderBy: id, where: {status: "Active", id_gt: "%s"}) { id } }`,
			pageSize, lastID))
		if err != nil {
			return nil, err
		}

		var page []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(data["validations"], &page); err != nil {
			return nil, fmt.Errorf("decode validations: %w", err)
		}

		for _, v := range page {
			raw, err := hex.DecodeString(strings.TrimPrefix(v.ID, "0x"))
			if err != nil {
				return nil, fmt.Errorf("decode validation ID %s: %w", v.ID, err)
			}
			id, err := ids.ToID(raw)
			if err != nil {
				return nil, fmt.Errorf("parse validation ID %s: %w", v.ID, err)
			}
			out = append(out, id)
		}

		if len(page) < pageSize {
			return out, nil
		}
		lastID = page[len(page)-1].ID
	}
}

func (c *Client) do(query string) (map[string]json.RawMessage, error) {
	body, err := json.Marshal(graphQLRequest{Query: query})
	if err != nil {
		return nil, fmt.Errorf("marshal GraphQL query: %w", err)
	}

	resp, err := c.http.Post(c.endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("execute GraphQL request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code from subgraph: %d", resp.StatusCode)
	}

	var out struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []graphQLError             `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode GraphQL response: %w", err)
	}
	if len(out.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL error: %s", out.Errors[0].Message)
	}

	return out.Data, nil
}
