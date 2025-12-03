package aggregator

import (
	"encoding/hex"
	"fmt"

	"github.com/ava-labs/avalanche-tooling-sdk-go/interchain"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp/payload"
	"github.com/ava-labs/subnet-evm/warp/messages"
)

type Client struct {
	aggregatorURL   string
	sourceChainID   ids.ID
	signingSubnetID string
	logger          logging.Logger
	quorum          uint64
}

func NewClient(
	aggregatorURL string,
	networkID uint32, // kept for future use, not used directly yet
	subnetID string,
	blockchainID string,
	logLevel string,
	quorumPercentage int,
) (*Client, error) {
	chainID, err := ids.FromString(blockchainID)
	if err != nil {
		return nil, fmt.Errorf("parse blockchain ID: %w", err)
	}

	if logLevel == "" {
		logLevel = "off"
	}
	level, err := logging.ToLevel(logLevel)
	if err != nil {
		level, _ = logging.ToLevel("off")
	}

	logger, err := logging.NewFactory(logging.Config{
		DisplayLevel: level,
		LogLevel:     level,
	}).Make("uptime-signature-aggregator")
	if err != nil {
		return nil, fmt.Errorf("create logger: %w", err)
	}

	if quorumPercentage <= 0 {
		quorumPercentage = 67
	}

	return &Client{
		aggregatorURL:   aggregatorURL,
		sourceChainID:   chainID,
		signingSubnetID: subnetID,
		logger:          logger,
		quorum:          uint64(quorumPercentage),
	}, nil
}

// PackValidationUptimeMessage constructs the unsigned warp message for a validator's uptime.
func (c *Client) PackValidationUptimeMessage(
	validationID string,
	uptimeSeconds uint64,
	networkID uint32,
) (*warp.UnsignedMessage, error) {
	uptimePayload, err := messages.NewValidatorUptime(
		ids.FromStringOrPanic(validationID),
		uptimeSeconds,
	)
	if err != nil {
		return nil, fmt.Errorf("generate uptime payload: %w", err)
	}

	addressedCall, err := payload.NewAddressedCall(nil, uptimePayload.Bytes())
	if err != nil {
		return nil, fmt.Errorf("generate addressed call: %w", err)
	}

	unsignedMsg, err := warp.NewUnsignedMessage(
		networkID,
		c.sourceChainID,
		addressedCall.Bytes(),
	)
	if err != nil {
		return nil, fmt.Errorf("generate unsigned warp message: %w", err)
	}

	return unsignedMsg, nil
}

func (c *Client) SubmitAggregateRequest(
	unsignedMessage *warp.UnsignedMessage,
) (*warp.Message, error) {
	if unsignedMessage == nil {
		return nil, fmt.Errorf("unsigned message is nil")
	}

	// uptime proofs have no justification
	messageHex := hex.EncodeToString(unsignedMessage.Bytes())
	justificationHex := ""

	signedMsg, err := interchain.SignMessage(
		c.logger,
		c.aggregatorURL,
		messageHex,
		justificationHex,
		c.signingSubnetID,
		c.quorum,
		0,
		interchain.WithMaxRetries(1),
		interchain.WithInitialBackoff(1),
	)
	if err != nil {
		return nil, fmt.Errorf("aggregate signatures: %w", err)
	}

	return signedMsg, nil
}
