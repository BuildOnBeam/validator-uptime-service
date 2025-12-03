package delegation

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"uptime-service/logging"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/libevm/accounts/abi"
	"github.com/ava-labs/libevm/common"
	"github.com/ava-labs/libevm/core/types"
	"github.com/ava-labs/libevm/crypto"
	"github.com/ava-labs/libevm/ethclient"
)

type GraphQLQuery struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables,omitempty"`
}

type GraphQLResponse struct {
	Data   GraphQLData `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors,omitempty"`
}

type GraphQLData struct {
	Delegations []Delegation `json:"delegations"`
}

type Delegation struct {
	ID           string `json:"id"`
	ValidationID string `json:"validationID"`
}

type Client struct {
	GraphQLEndpoint       string
	RPC                   string
	StakingManagerAddress string
	PrivateKey            *ecdsa.PrivateKey
	PublicAddress         common.Address
	EthClient             *ethclient.Client
}

func NewClient(graphqlEndpoint, rpcURL, stakingManagerAddr, privateKeyHex string) (*Client, error) {
	privKey, err := crypto.HexToECDSA(strings.TrimPrefix(privateKeyHex, "0x"))
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	pubAddr := crypto.PubkeyToAddress(privKey.PublicKey)

	ethClient, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("connect EVM client: %w", err)
	}

	return &Client{
		GraphQLEndpoint:       graphqlEndpoint,
		RPC:                   rpcURL,
		StakingManagerAddress: stakingManagerAddr,
		PrivateKey:            privKey,
		PublicAddress:         pubAddr,
		EthClient:             ethClient,
	}, nil
}

func (c *Client) GetDelegationsForValidator(validationID string) ([]Delegation, error) {
	validationIDBytes, err := ids.FromString(validationID)
	if err != nil {
		return nil, fmt.Errorf("parse validation ID %s: %w", validationID, err)
	}
	formattedID := ids.ID(validationIDBytes).Hex()

	query := `
	query GetDelegations($validationID: Bytes!) {
		delegations(
			first: 1000,
			where: {
				validationID: $validationID,
				lastRewardedEpoch: 0,
				startedAt_lte: "1746095346"
			}
		) {
			id
			validationID
		}
	}`

	variables := map[string]interface{}{
		"validationID": formattedID,
	}

	graphqlQuery := GraphQLQuery{
		Query:     query,
		Variables: variables,
	}

	jsonData, err := json.Marshal(graphqlQuery)
	if err != nil {
		return nil, fmt.Errorf("marshal GraphQL query: %w", err)
	}

	req, err := http.NewRequest("POST", c.GraphQLEndpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute GraphQL request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var graphqlResp GraphQLResponse
	if err := json.NewDecoder(resp.Body).Decode(&graphqlResp); err != nil {
		return nil, fmt.Errorf("decode GraphQL response: %w", err)
	}

	if len(graphqlResp.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL error: %s", graphqlResp.Errors[0].Message)
	}

	return graphqlResp.Data.Delegations, nil
}

func (c *Client) ResolveRewards(delegations []Delegation) error {
	if len(delegations) == 0 {
		logging.Info("no delegations to resolve")
		return nil
	}

	const abiJSON = `[{"inputs":[{"internalType":"bytes32[]","name":"delegationIDs","type":"bytes32[]"}],"name":"resolveRewards","outputs":[],"stateMutability":"nonpayable","type":"function"}]`

	parsedABI, err := abi.JSON(strings.NewReader(abiJSON))
	if err != nil {
		return fmt.Errorf("parse ABI: %w", err)
	}

	delegationIDs := make([][32]byte, 0, len(delegations))
	for _, delegation := range delegations {
		var id [32]byte

		hexID := strings.TrimPrefix(delegation.ID, "0x")
		idBytes, err := hex.DecodeString(hexID)
		if err != nil {
			logging.Errorf("decode delegation ID %s: %v", delegation.ID, err)
			continue
		}
		if len(idBytes) > 32 {
			logging.Errorf("delegation ID %s exceeds 32 bytes", delegation.ID)
			continue
		}
		copy(id[32-len(idBytes):], idBytes)
		delegationIDs = append(delegationIDs, id)
	}

	if len(delegationIDs) == 0 {
		return fmt.Errorf("no valid delegation IDs after parsing")
	}

	const batchSize = 20
	for i := 0; i < len(delegationIDs); i += batchSize {
		end := i + batchSize
		if end > len(delegationIDs) {
			end = len(delegationIDs)
		}
		batch := delegationIDs[i:end]

		nonce, err := c.EthClient.PendingNonceAt(context.Background(), c.PublicAddress)
		if err != nil {
			return fmt.Errorf("get nonce: %w", err)
		}

		gasPrice, err := c.EthClient.SuggestGasPrice(context.Background())
		if err != nil {
			return fmt.Errorf("get gas price: %w", err)
		}

		data, err := parsedABI.Pack("resolveRewards", batch)
		if err != nil {
			return fmt.Errorf("pack tx data: %w", err)
		}

		contractAddr := common.HexToAddress(c.StakingManagerAddress)
		tx := types.NewTransaction(
			nonce,
			contractAddr,
			big.NewInt(0),
			3000000,
			gasPrice,
			data,
		)

		chainID, err := c.EthClient.ChainID(context.Background())
		if err != nil {
			return fmt.Errorf("get chain ID: %w", err)
		}

		signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), c.PrivateKey)
		if err != nil {
			return fmt.Errorf("sign tx: %w", err)
		}

		if err := c.EthClient.SendTransaction(context.Background(), signedTx); err != nil {
			return fmt.Errorf("send tx: %w", err)
		}

		logging.Infof(
			"submitted resolveRewards tx (batch %d/%d) with %d delegations, tx hash: %s",
			(i/batchSize)+1,
			(len(delegationIDs)+batchSize-1)/batchSize,
			len(batch),
			signedTx.Hash().Hex(),
		)

		time.Sleep(4 * time.Second)
	}

	return nil
}
