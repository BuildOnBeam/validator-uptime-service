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
	ethClient             *ethclient.Client
	privateKey            *ecdsa.PrivateKey
	publicAddress         string // address of the uptime service wallet (from private key)
	chainID               *big.Int
	contractABI           abi.ABI
}

func NewContractClient(rpcURL, contractAddr, privateKeyHex string) (*ContractClient, error) {
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
		{"inputs":[{"internalType":"bytes","name":"signedMessage","type":"bytes"}],
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

func (c *ContractClient) SubmitUptimeProof(signedMessage []byte) error {
	data, err := c.contractABI.Pack("submitUptimeProof", signedMessage)
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

	tx := types.NewTransaction(nonce, toAddr, big.NewInt(0), gasLimit, gasPrice, data)
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
