package contract

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"strings"

	"github.com/ava-labs/avalanche-cli/pkg/contract"
	"github.com/ava-labs/avalanche-cli/sdk/validatormanager"
	avalancheWarp "github.com/ava-labs/avalanchego/vms/platformvm/warp"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type ContractClient struct {
	StakingManagerAddress string
	WarpMessengerAddress  string
	privateKey            *ecdsa.PrivateKey
	publicAddress         string // address of the uptime service wallet (from private key)
}

func NewContractClient(rpcURL, contractAddr, warpMessengerAddr, privateKeyHex string) (*ContractClient, error) {
	privKey, err := crypto.HexToECDSA(strings.TrimPrefix(privateKeyHex, "0x"))
	if err != nil {
		return nil, err
	}

	pubAddr := crypto.PubkeyToAddress(privKey.PublicKey).Hex()

	return &ContractClient{
		StakingManagerAddress: contractAddr,
		WarpMessengerAddress:  warpMessengerAddr,
		privateKey:            privKey,
		publicAddress:         pubAddr,
	}, nil
}

func (c ContractClient) SubmitUptimeProof(validationID [32]byte, signedMessage []byte) error {
	log.Printf("Submitting uptime proof for validation ID %x", validationID)
	log.Printf("Signed message length: %d bytes", len(signedMessage))

	// Parse the byte array to a warp.Message
	signedWarpMsg, err := avalancheWarp.ParseMessage(signedMessage)
	if err != nil {
		return fmt.Errorf("failed to parse signed warp message: %w", err)
	}

	// "https://eu.build.onbeam.com/rpc/testnet/f3dd69ec-e73a-32d2-b0d5-3c352f6fd9ce",
	finalTx, _, err := contract.TxToMethodWithWarpMessage(
		"https://eu.build.onbeam.com/rpc/f3dd69ec-e73a-32d2-b0d5-3c352f6fd9ce",
		hex.EncodeToString(c.privateKey.D.Bytes()),
		common.HexToAddress(c.StakingManagerAddress),
		signedWarpMsg,
		big.NewInt(0),
		"submit uptime proof",
		validatormanager.ErrorSignatureToError,
		"submitUptimeProof(bytes32,uint32)",
		validationID,
		uint32(0),
	)
	if err != nil {
		return fmt.Errorf("failed to send tx to validator manager: %w", err)
	}

	log.Printf("Submitted uptime proof transaction: %s", finalTx.Hash().Hex())
	return nil
}
