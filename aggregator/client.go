package aggregator

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/ava-labs/avalanchego/ids"
	avalancheWarp "github.com/ava-labs/avalanchego/vms/platformvm/warp"
	warpPayload "github.com/ava-labs/avalanchego/vms/platformvm/warp/payload"
	"github.com/ava-labs/subnet-evm/warp/messages"
)

type AggregatorClient struct {
	BaseURL          string
	SigningSubnetID  string
	SourceChainId    string
	QuorumPercentage int
}

type AggregatorRequest struct {
	Message         string `json:"message,omitempty"`
	Justification   string `json:"justification,omitempty"` // optional justification bytes? what is this
	SigningSubnetID string `json:"signingSubnetId,omitempty"`
	QuorumPercent   int    `json:"quorumPercentage,omitempty"`
}

type AggregatorResponse struct {
	SignedMessage string `json:"signed-message"`
}

// PackValidationUptimeMessage constructs the 46-byte uptime proof message.
func (c *AggregatorClient) PackValidationUptimeMessage(validationID string, uptimeSeconds uint64) (string, error) {
	uptimePayload, err := messages.NewValidatorUptime(ids.FromStringOrPanic(validationID), uptimeSeconds)
	if err != nil {
		return "", fmt.Errorf("failed to generate uptime payload: %w", err)
	}

	addressedCall, err := warpPayload.NewAddressedCall(nil, uptimePayload.Bytes())
	if err != nil {
		return "", fmt.Errorf("failed to generate addressed call: %w", err)
	}

	uptimeProofUnsignedMessage, err := avalancheWarp.NewUnsignedMessage(
		5, //TODO: Update 5 to mainnet
		ids.FromStringOrPanic(c.SourceChainId),
		addressedCall.Bytes(),
	)
	if err != nil {
		return "", fmt.Errorf("failed to generate unsigned message: %w", err)
	}

	uptimeProofHex := hex.EncodeToString(uptimeProofUnsignedMessage.Bytes())

	return uptimeProofHex, nil
}

func (c *AggregatorClient) SubmitAggregateRequest(unsignedMessage string) ([]byte, error) {
	log.Printf("Submitting aggregate request with message length: %d bytes", len(unsignedMessage))

	req := AggregatorRequest{
		Message: unsignedMessage,
	}
	if c.SigningSubnetID != "" {
		req.SigningSubnetID = c.SigningSubnetID
	}
	if c.QuorumPercentage > 0 {
		req.QuorumPercent = c.QuorumPercentage
	}

	reqData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	log.Printf("Request payload: %s", string(reqData))
	log.Printf("Sending request to: %s", c.BaseURL+"/v1/signatureAggregator/fuji/aggregateSignatures")

	// TODO: Update <fuji> to mainnet
	resp, err := http.Post(c.BaseURL+"/v1/signatureAggregator/fuji/aggregateSignatures", "application/json", bytes.NewReader(reqData))
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	log.Printf("Response status: %d, body: %s", resp.StatusCode, string(body))

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error != "" {
			return nil, errors.New("aggregator error: " + errResp.Error)
		}
		return nil, fmt.Errorf("aggregator request failed with status code: %s, body: %s", resp.Status, string(body))
	}

	var aggResp AggregatorResponse
	if err := json.Unmarshal(body, &aggResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	signedHex := aggResp.SignedMessage
	if strings.HasPrefix(signedHex, "0x") {
		signedHex = signedHex[2:]
	}
	signedBytes, err := hex.DecodeString(signedHex)
	if err != nil {
		return nil, fmt.Errorf("invalid hex in signed-message: %w", err)
	}
	log.Printf("Successfully decoded signed message of length: %d bytes", len(signedBytes))

	return signedBytes, nil
}
