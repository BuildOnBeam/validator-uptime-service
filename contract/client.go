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
	"github.com/ava-labs/avalanchego/ids"
	avalancheWarp "github.com/ava-labs/avalanchego/vms/platformvm/warp"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type ContractClient struct {
	RPCURL                string
	StakingManagerAddress string
	WarpMessengerAddress  string
	privateKey            *ecdsa.PrivateKey
	publicAddress         string
}

func NewContractClient(rpcURL, contractAddr, warpMessengerAddr, privateKeyHex string) (*ContractClient, error) {
	privKey, err := crypto.HexToECDSA(strings.TrimPrefix(privateKeyHex, "0x"))
	if err != nil {
		return nil, err
	}

	pubAddr := crypto.PubkeyToAddress(privKey.PublicKey).Hex()

	return &ContractClient{
		RPCURL:                rpcURL,
		StakingManagerAddress: contractAddr,
		WarpMessengerAddress:  warpMessengerAddr,
		privateKey:            privKey,
		publicAddress:         pubAddr,
	}, nil
}

func (c ContractClient) SubmitUptimeProof(validationID ids.ID, signedMessage *avalancheWarp.Message) error {
	log.Printf("Submitting uptime proof for validation ID: %s", validationID.Hex())

	// Parse the byte array to a warp.Message
	signedWarpMsg, err := avalancheWarp.ParseMessage(signedMessage.Bytes())
	if err != nil {
		return fmt.Errorf("failed to parse signed warp message: %w", err)
	}

	finalTx, _, err := contract.TxToMethodWithWarpMessage(
		c.RPCURL,
		false,
		common.Address{},
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

	log.Printf("SUCCESS: Submitted uptime proof transaction: %s", finalTx.Hash().Hex())
	return nil
}
