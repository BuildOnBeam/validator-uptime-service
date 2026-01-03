package contract

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"uptime-service/logging"

	"github.com/ava-labs/avalanche-tooling-sdk-go/evm"
	"github.com/ava-labs/avalanche-tooling-sdk-go/evm/contract"
	"github.com/ava-labs/avalanche-tooling-sdk-go/key"
	"github.com/ava-labs/avalanche-tooling-sdk-go/validatormanager"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/crypto/secp256k1"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp"
	"github.com/ava-labs/libevm/common"
	"github.com/ava-labs/libevm/crypto"
)

type ContractClient struct {
	RPCURL                string
	StakingManagerAddress string
	WarpMessengerAddress  string
	privateKey            *secp256k1.PrivateKey
	publicAddress         string
}

func NewContractClient(rpcURL, contractAddr, warpMessengerAddr, privateKeyHex string) (*ContractClient, error) {
	pkHex := strings.TrimPrefix(privateKeyHex, "0x")

	raw, err := hex.DecodeString(pkHex)
	if err != nil {
		return nil, fmt.Errorf("invalid hex private key: %w", err)
	}

	secpPriv, err := secp256k1.ToPrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to parse secp256k1 private key: %w", err)
	}

	ecdsaPriv := secpPriv.ToECDSA()
	pubAddr := crypto.PubkeyToAddress(ecdsaPriv.PublicKey).Hex()

	return &ContractClient{
		RPCURL:                rpcURL,
		StakingManagerAddress: contractAddr,
		WarpMessengerAddress:  warpMessengerAddr,
		privateKey:            secpPriv,
		publicAddress:         pubAddr,
	}, nil
}

func (c ContractClient) SubmitUptimeProof(validationID ids.ID, signedMessage *warp.Message) error {
	logging.Infof("Submitting uptime proof for validation ID: %s", validationID.Hex())

	signedWarpMsg, err := warp.ParseMessage(signedMessage.Bytes())
	if err != nil {
		return fmt.Errorf("failed to parse signed warp message: %w", err)
	}

	softKey, err := key.NewSoft(key.WithPrivateKey(c.privateKey))
	if err != nil {
		return fmt.Errorf("failed to initialize soft key from in-memory private key: %w", err)
	}

	signer, err := evm.NewSigner(softKey.KeyChain())
	if err != nil {
		return fmt.Errorf("failed to create signer: %w", err)
	}

	finalTx, _, err := contract.TxToMethodWithWarpMessage(
		nil,
		c.RPCURL,
		signer,
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

	logging.Infof("SUCCESS: Submitted uptime proof transaction: %s", finalTx.Hash().Hex())
	return nil
}
