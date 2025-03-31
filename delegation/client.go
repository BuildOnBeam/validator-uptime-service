package delegation

import (
	"bytes"
	"context"
	"crypto/ecdsa" // Added hex package
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"uptime-service/logging"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// GraphQL query struct for the request
type GraphQLQuery struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables,omitempty"`
}

// GraphQL response structures
type GraphQLResponse struct {
	Data   GraphQLData `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors,omitempty"`
}

type GraphQLData struct {
	Delegations []Delegation `json:"delegations"`
}

// Delegation represents a delegation from the GraphQL response
type Delegation struct {
	ID           string `json:"id"`
	ValidationID string `json:"validationID"`
}

// DelegationClient handles fetching delegations and resolving rewards
type DelegationClient struct {
	GraphQLEndpoint       string
	RPC                   string
	StakingManagerAddress string
	PrivateKey            *ecdsa.PrivateKey
	PublicAddress         common.Address
	EthClient             *ethclient.Client
}

// NewDelegationClient creates a new client to handle delegation operations
func NewDelegationClient(graphqlEndpoint, rpcURL, stakingManagerAddr, privateKeyHex string) (*DelegationClient, error) {
	privKey, err := crypto.HexToECDSA(strings.TrimPrefix(privateKeyHex, "0x"))
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	pubAddr := crypto.PubkeyToAddress(privKey.PublicKey)

	ethClient, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Ethereum client: %w", err)
	}

	return &DelegationClient{
		GraphQLEndpoint:       graphqlEndpoint,
		RPC:                   rpcURL,
		StakingManagerAddress: stakingManagerAddr,
		PrivateKey:            privKey,
		PublicAddress:         pubAddr,
		EthClient:             ethClient,
	}, nil
}

// GetDelegationsForValidator fetches all delegations for a given validator
func (dc *DelegationClient) GetDelegationsForValidator(validationID string) ([]Delegation, error) {
	// Convert the validation ID to ids.ID format (consistent with other code)
	validationIDBytes, err := ids.FromString(validationID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse validation ID for %s: %w", validationID, err)
	}
	formattedID := ids.ID(validationIDBytes).Hex()

	query := `
	query GetDelegations($validationID: Bytes!) {
		delegations(where: {validationID: $validationID}) {
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
		return nil, fmt.Errorf("failed to marshal GraphQL query: %w", err)
	}

	req, err := http.NewRequest("POST", dc.GraphQLEndpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute GraphQL request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var graphqlResp GraphQLResponse
	if err := json.NewDecoder(resp.Body).Decode(&graphqlResp); err != nil {
		return nil, fmt.Errorf("failed to decode GraphQL response: %w", err)
	}

	if len(graphqlResp.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL error: %s", graphqlResp.Errors[0].Message)
	}

	return graphqlResp.Data.Delegations, nil
}

// ResolveRewards calls the resolveRewards method for a list of delegations
func (dc *DelegationClient) ResolveRewards(delegations []Delegation) error {
	if len(delegations) == 0 {
		logging.Info("No delegations to resolve")
		return nil
	}

	const abiJSON = `[{"inputs":[{"internalType":"bytes32[]","name":"delegationIDs","type":"bytes32[]"}],"name":"resolveRewards","outputs":[],"stateMutability":"nonpayable","type":"function"}]`

	parsedABI, err := abi.JSON(strings.NewReader(abiJSON))
	if err != nil {
		return fmt.Errorf("failed to parse ABI: %w", err)
	}

	// Prepare the delegationIDs parameter as []bytes32
	delegationIDs := make([][32]byte, 0, len(delegations))
	for _, delegation := range delegations {
		// Convert hex string ID to bytes32
		var id [32]byte
		idObj, err := ids.FromString(delegation.ID)
		if err != nil {
			logging.Errorf("Failed to parse delegation ID %s: %v", delegation.ID, err)
			continue
		}
		copy(id[:], idObj[:])
		delegationIDs = append(delegationIDs, id)
	}

	if len(delegationIDs) == 0 {
		return fmt.Errorf("no valid delegation IDs after parsing")
	}

	// Process delegations in batches of 20 to avoid gas limits
	const batchSize = 20
	for i := 0; i < len(delegationIDs); i += batchSize {
		end := i + batchSize
		if end > len(delegationIDs) {
			end = len(delegationIDs)
		}

		batch := delegationIDs[i:end]

		// Get the current nonce for the sender address
		nonce, err := dc.EthClient.PendingNonceAt(context.Background(), dc.PublicAddress)
		if err != nil {
			return fmt.Errorf("failed to get nonce: %w", err)
		}

		// Get gas price
		gasPrice, err := dc.EthClient.SuggestGasPrice(context.Background())
		if err != nil {
			return fmt.Errorf("failed to get gas price: %w", err)
		}

		// Create transaction data
		data, err := parsedABI.Pack("resolveRewards", batch)
		if err != nil {
			return fmt.Errorf("failed to pack transaction data: %w", err)
		}

		// Create transaction
		contractAddr := common.HexToAddress(dc.StakingManagerAddress)
		tx := types.NewTransaction(
			nonce,
			contractAddr,
			big.NewInt(0),
			3000000, // Gas limit
			gasPrice,
			data,
		)

		// Sign the transaction
		chainID, err := dc.EthClient.ChainID(context.Background())
		if err != nil {
			return fmt.Errorf("failed to get chain ID: %w", err)
		}

		signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), dc.PrivateKey)
		if err != nil {
			return fmt.Errorf("failed to sign transaction: %w", err)
		}

		// Send the transaction
		err = dc.EthClient.SendTransaction(context.Background(), signedTx)
		if err != nil {
			return fmt.Errorf("failed to send transaction: %w", err)
		}

		logging.Infof("Submitted resolveRewards transaction (batch %d/%d) with %d delegations, tx hash: %s",
			(i/batchSize)+1, (len(delegationIDs)+batchSize-1)/batchSize, len(batch), signedTx.Hash().Hex())

		// Wait a short time between transactions to avoid nonce issues
		time.Sleep(2 * time.Second)
	}

	return nil
}
