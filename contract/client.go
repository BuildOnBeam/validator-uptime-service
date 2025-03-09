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
		 "name":"submitUptimeProof","outputs":[],"stateMutability":"nonpayable","type":"function"},
		{"inputs":[{"internalType":"bytes32","name":"validationID","type":"bytes32"},{"internalType":"uint32","name":"messageIndex","type":"uint32"}],
		 "name":"validateUptime","outputs":[{"internalType":"uint64","name":"","type":"uint64"}],"stateMutability":"view","type":"function"}
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
		WarpMessengerAddress:  warpMessengerAddr,
		ethClient:             ethCl,
		privateKey:            privKey,
		publicAddress:         pubAddr,
		chainID:               chainID,
		contractABI:           contractABI,
	}, nil
}

// ValidateUptime calls the validateUptime view function and returns the result
func (c *ContractClient) ValidateUptime(validationID [32]byte, signedMessage []byte) error {
	data, err := c.contractABI.Pack("validateUptime", validationID, uint32(0))
	if err != nil {
		return err
	}

	warpMessengerAddr := common.HexToAddress(c.WarpMessengerAddress)

	predicateStorageSlots := BytesToHashSlice(PackPredicate(signedMessage))
	accessList := types.AccessList{
		{
			Address:     warpMessengerAddr,
			StorageKeys: predicateStorageSlots,
		},
	}

	toAddr := common.HexToAddress(c.StakingManagerAddress)
	fromAddr := common.HexToAddress(c.publicAddress)
	callMsg := ethereum.CallMsg{
		From:       fromAddr,
		To:         &toAddr,
		Data:       data,
		AccessList: accessList,
	}

	// Make the call
	ctx := context.Background()
	result, err := c.ethClient.CallContract(ctx, callMsg, nil) // nil for latest block
	if err != nil {
		return err
	}

	// Unpack the result
	var uptimeValue uint64
	err = c.contractABI.UnpackIntoInterface(&uptimeValue, "validateUptime", result)
	if err != nil {
		return err
	}

	log.Printf("Uptime validation result: %d", uptimeValue)
	return nil
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

	// Create the access list with predicate bytes
	predicateStorageSlots := BytesToHashSlice(PackPredicate(signedMessage))
	accessList := types.AccessList{
		{
			Address:     warpMessengerAddr,
			StorageKeys: predicateStorageSlots,
		},
	}

	// Create the transaction
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:    c.chainID,
		Nonce:      nonce,
		To:         &toAddr,
		Gas:        gasLimit,
		GasFeeCap:  gasPrice,
		GasTipCap:  gasPrice, // Using gasPrice as tip cap for simplicity
		Value:      big.NewInt(0),
		Data:       data,
		AccessList: accessList,
	})

	// Sign the transaction
	signedTx, err := types.SignTx(tx, types.NewLondonSigner(c.chainID), c.privateKey)
	if err != nil {
		return err
	}

	if err := c.ethClient.SendTransaction(ctx, signedTx); err != nil {
		return err
	}

	log.Printf("Submitted uptime proof transaction: %s", signedTx.Hash().Hex())
	return nil
}

// EndByte is the delimiter used for predicate bytes in subnet-evm
const EndByte = byte(0xff)

// PackPredicate packs the predicate bytes using the correct format expected by the subnet-evm
func PackPredicate(predicateBytes []byte) [][]byte {
	// First, append the EndByte delimiter (0xff)
	predicateBytes = append(predicateBytes, EndByte)

	// Right-pad with zeros to a multiple of 32 bytes
	paddedLength := (len(predicateBytes) + 31) / 32 * 32
	paddedBytes := make([]byte, paddedLength)
	copy(paddedBytes, predicateBytes)

	// Now chunk the padded bytes into 32-byte chunks for the storage slots
	numChunks := paddedLength / 32
	result := make([][]byte, numChunks)

	for i := 0; i < numChunks; i++ {
		chunk := make([]byte, 32)
		copy(chunk, paddedBytes[i*32:(i+1)*32])
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
