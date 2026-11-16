package contract

import (
	"encoding/hex"
	"errors"
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
	"github.com/ava-labs/libevm/core/types"
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

// ErrUptimeNotRecorded means the submission transaction succeeded but the
// staking manager did not emit UptimeUpdated for the validation, which happens
// when the submitted uptime does not exceed the one already recorded.
var ErrUptimeNotRecorded = errors.New("uptime proof transaction did not record an uptime update")

// uptimeUpdatedTopic is the topic of UptimeUpdated(bytes32 indexed validationID, uint64 uptime, uint64 epoch).
var uptimeUpdatedTopic = crypto.Keccak256Hash([]byte("UptimeUpdated(bytes32,uint64,uint64)"))

// SubmitUptimeProof submits a signed uptime proof and returns the uptime the
// staking manager recorded. It returns ErrUptimeNotRecorded when the
// transaction succeeded without updating the validation's uptime.
func (c ContractClient) SubmitUptimeProof(validationID ids.ID, signedMessage *warp.Message) (uint64, error) {
	logging.Infof("Submitting uptime proof for validation ID: %s", validationID.Hex())

	signedWarpMsg, err := warp.ParseMessage(signedMessage.Bytes())
	if err != nil {
		return 0, fmt.Errorf("failed to parse signed warp message: %w", err)
	}

	softKey, err := key.NewSoft(key.WithPrivateKey(c.privateKey))
	if err != nil {
		return 0, fmt.Errorf("failed to initialize soft key from in-memory private key: %w", err)
	}

	signer, err := evm.NewSigner(softKey.KeyChain())
	if err != nil {
		return 0, fmt.Errorf("failed to create signer: %w", err)
	}

	finalTx, receipt, err := contract.TxToMethodWithWarpMessage(
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
		return 0, fmt.Errorf("failed to send tx to validator manager: %w", err)
	}
	if receipt == nil {
		return 0, fmt.Errorf("no receipt for uptime proof transaction %s", finalTx.Hash().Hex())
	}

	recorded, ok := c.recordedUptime(receipt, validationID)
	if !ok {
		return 0, fmt.Errorf("%w (tx %s)", ErrUptimeNotRecorded, finalTx.Hash().Hex())
	}

	logging.Infof("SUCCESS: recorded uptime %d in transaction %s", recorded, finalTx.Hash().Hex())
	return recorded, nil
}

// recordedUptime extracts the uptime from the receipt's UptimeUpdated event
// for validationID, if present.
func (c ContractClient) recordedUptime(receipt *types.Receipt, validationID ids.ID) (uint64, bool) {
	manager := common.HexToAddress(c.StakingManagerAddress)
	for _, l := range receipt.Logs {
		if l.Address != manager || len(l.Topics) < 2 || len(l.Data) < 32 {
			continue
		}
		if l.Topics[0] != uptimeUpdatedTopic || l.Topics[1] != common.Hash(validationID) {
			continue
		}
		return new(big.Int).SetBytes(l.Data[:32]).Uint64(), true
	}
	return 0, false
}
