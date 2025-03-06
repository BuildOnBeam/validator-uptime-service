package contract

import (
	"context"
	"crypto/ecdsa"
	"log"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

type ContractClient struct {
	StakingManagerAddress string
	WarpMessengerAddress  string
	ethClient             *ethclient.Client
	privateKey            *ecdsa.PrivateKey
	publicAddress         string // address of the uptime service wallet (from private key)
	chainID               *big.Int
	contractABI           abi.ABI
}

func NewContractClient(rpcURL, contractAddr, warpMessengerAddr, privateKeyHex string) (*ContractClient, error) {
	ethCl, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, err
	}

	privKey, err := crypto.HexToECDSA(strings.TrimPrefix(privateKeyHex, "0x"))
	if err != nil {
		return nil, err
	}

	pubAddr := crypto.PubkeyToAddress(privKey.PublicKey).Hex()

	contractABI, err := abi.JSON(strings.NewReader(`[
		{"inputs":[{"internalType":"bytes32","name":"validationID","type":"bytes32"},{"internalType":"uint32","name":"messageIndex","type":"uint32"}],
		 "name":"submitUptimeProof","outputs":[],"stateMutability":"nonpayable","type":"function"}
	]`))
	if err != nil {
		return nil, err
	}

	chainID, err := ethCl.NetworkID(context.Background())
	if err != nil {
		return nil, err
	}

	return &ContractClient{
		StakingManagerAddress: contractAddr,
		ethClient:             ethCl,
		privateKey:            privKey,
		publicAddress:         pubAddr,
		chainID:               chainID,
		contractABI:           contractABI,
	}, nil
}

func (c *ContractClient) SubmitUptimeProof(validationID [32]byte, signedMessage []byte) error {
	log.Printf("Submitting uptime proof for validation ID %x", validationID)
	log.Printf("Signed message length: %d bytes", len(signedMessage))

	// Pack the call data for submitUptimeProof(validationID, 0)
	data, err := c.contractABI.Pack("submitUptimeProof", validationID, uint32(0))
	if err != nil {
		return err
	}

	toAddr := common.HexToAddress(c.StakingManagerAddress)
	fromAddr := common.HexToAddress(c.publicAddress)

	ctx := context.Background()
	nonce, err := c.ethClient.PendingNonceAt(ctx, fromAddr)
	if err != nil {
		return err
	}

	gasLimit, err := c.ethClient.EstimateGas(ctx, ethereum.CallMsg{
		From: fromAddr, To: &toAddr, Gas: 0, GasPrice: nil, Value: nil, Data: data,
	})
	if err != nil {
		gasLimit = 300000
	}

	gasPrice, err := c.ethClient.SuggestGasPrice(ctx)
	if err != nil {
		return err
	}

	// Create the predicate transaction with the warp message attached in the access list
	warpMessengerAddr := common.HexToAddress(c.WarpMessengerAddress)

	accessList := types.AccessList{}

	tx := NewPredicateTx(
		c.chainID,
		nonce,
		&toAddr,
		gasLimit,
		gasPrice,
		gasPrice, // Using gasPrice as tip cap for simplicity
		big.NewInt(0),
		data,
		accessList,
		warpMessengerAddr,
		signedMessage,
	)

	// Sign the transaction
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(c.chainID), c.privateKey)
	if err != nil {
		return err
	}

	if err := c.ethClient.SendTransaction(ctx, signedTx); err != nil {
		return err
	}

	log.Printf("Submitted uptime proof transaction: %s", signedTx.Hash().Hex())
	return nil
}

// NewPredicateTx returns a transaction with the predicateAddress/predicateBytes tuple
// packed and added to the access list of the transaction.
func NewPredicateTx(
	chainID *big.Int,
	nonce uint64,
	to *common.Address,
	gas uint64,
	gasPrice *big.Int,
	gasTipCap *big.Int,
	value *big.Int,
	data []byte,
	accessList types.AccessList,
	predicateAddress common.Address,
	predicateBytes []byte,
) *types.Transaction {
	predicateStorageSlots := BytesToHashSlice(PackPredicate(predicateBytes))

	// Add the predicate to the access list
	accessList = append(accessList, types.AccessTuple{
		Address:     predicateAddress,
		StorageKeys: predicateStorageSlots,
	})

	return types.NewTx(&types.DynamicFeeTx{
		ChainID:    chainID,
		Nonce:      nonce,
		To:         to,
		Gas:        gas,
		GasFeeCap:  gasPrice,
		GasTipCap:  gasTipCap,
		Value:      value,
		Data:       data,
		AccessList: accessList,
	})
}

// PackPredicate packs predicateBytes to be included as StorageKeys in an AccessTuple
func PackPredicate(predicateBytes []byte) [][]byte {
	// Length of each chunk in bytes (32 - 1 for the prefix)
	const chunkSize = 31

	numChunks := (len(predicateBytes) + chunkSize - 1) / chunkSize
	result := make([][]byte, numChunks)

	for i := 0; i < numChunks; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(predicateBytes) {
			end = len(predicateBytes)
		}

		// Allocate a new chunk with a prefix byte and copy data
		chunk := make([]byte, 32)
		// First byte is the index
		chunk[0] = byte(i)
		// Copy the payload bytes
		copy(chunk[1:], predicateBytes[start:end])

		result[i] = chunk
	}

	return result
}

// BytesToHashSlice converts a slice of byte slices to a slice of common.Hash
func BytesToHashSlice(b [][]byte) []common.Hash {
	hashes := make([]common.Hash, len(b))
	for i, bytes := range b {
		hashes[i] = common.BytesToHash(bytes)
	}
	return hashes
}
