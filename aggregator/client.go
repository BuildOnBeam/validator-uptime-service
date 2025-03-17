package aggregator

import (
	"context"
	"fmt"

	"github.com/ava-labs/avalanche-cli/cmd/blockchaincmd"
	"github.com/ava-labs/avalanche-cli/pkg/models"
	"github.com/ava-labs/avalanche-cli/sdk/interchain"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/logging"
	avalancheWarp "github.com/ava-labs/avalanchego/vms/platformvm/warp"
	warpPayload "github.com/ava-labs/avalanchego/vms/platformvm/warp/payload"
	"github.com/ava-labs/subnet-evm/warp/messages"
)

type AggregatorClient struct {
	signatureAggregator *interchain.SignatureAggregator
	SourceChainId       ids.ID
}

// NewAggregatorClient creates a new AggregatorClient instance
func NewAggregatorClient(nodeURI string, networkID uint32, subnetID string, blockchainID string, logLevel string) (*AggregatorClient, error) {
	id, err := ids.FromString(subnetID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse subnet ID: %w", err)
	}
	chainID, err := ids.FromString(blockchainID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse blockchain ID: %w", err)
	}

	peers, err := blockchaincmd.ConvertURIToPeers([]string{nodeURI})
	if err != nil {
		return nil, fmt.Errorf("failed to get extra peers: %w", err)
	}

	var network models.Network
	if networkID == 1 {
		network = models.NewMainnetNetwork()
	} else {
		network = models.NewFujiNetwork()
	}

	level, err := logging.ToLevel("off")
	if err != nil {
		return nil, fmt.Errorf("invalid log level %s: %w", logLevel, err)
	}
	logger, err := logging.NewFactory(logging.Config{
		DisplayLevel: level,
		LogLevel:     level,
	}).Make("signature-aggregator")
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}
	signatureAggregator, err := interchain.NewSignatureAggregator(
		context.Background(),
		network,
		logger,
		id,
		interchain.DefaultQuorumPercentage,
		true,
		peers,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create signature aggregator: %w", err)
	}

	return &AggregatorClient{
		signatureAggregator: signatureAggregator,
		SourceChainId:       chainID,
	}, nil
}

// PackValidationUptimeMessage constructs the 46-byte uptime proof message.
func (c *AggregatorClient) PackValidationUptimeMessage(validationID string, uptimeSeconds uint64, networkID uint32) (*avalancheWarp.UnsignedMessage, error) {
	uptimePayload, err := messages.NewValidatorUptime(ids.FromStringOrPanic(validationID), uptimeSeconds)
	if err != nil {
		return nil, fmt.Errorf("failed to generate uptime payload: %w", err)
	}

	addressedCall, err := warpPayload.NewAddressedCall(nil, uptimePayload.Bytes())
	if err != nil {
		return nil, fmt.Errorf("failed to generate addressed call: %w", err)
	}

	uptimeProofUnsignedMessage, err := avalancheWarp.NewUnsignedMessage(
		networkID,
		c.SourceChainId,
		addressedCall.Bytes(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate unsigned message: %w", err)
	}

	return uptimeProofUnsignedMessage, nil
}

func (c *AggregatorClient) SubmitAggregateRequest(unsignedMessage *avalancheWarp.UnsignedMessage) (*avalancheWarp.Message, error) {

	signedBytes, err := c.signatureAggregator.Sign(unsignedMessage, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate signatures: %w", err)
	}

	return signedBytes, nil
}
