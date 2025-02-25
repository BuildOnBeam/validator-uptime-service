package aggregator

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/mr-tron/base58/base58"
)

type AggregatorClient struct {
	BaseURL          string
	SigningSubnetID  string
	QuorumPercentage int
}

type AggregatorRequest struct {
	Message         string `json:"message,omitempty"`
	Justification   string `json:"justification,omitempty"` // optional justification bytes? what is this
	SigningSubnetID string `json:"signing-subnet-id,omitempty"`
	QuorumPercent   int    `json:"quorum-percentage,omitempty"`
}

type AggregatorResponse struct {
	SignedMessage string `json:"signed-message"`
}

// TODO: REALLY HAVE TO CHECK THIS LOL
// PackValidationUptimeMessage constructs the 46-byte uptime proof message.
func (c *AggregatorClient) PackValidationUptimeMessage(validationID string, uptimeSeconds uint64) ([]byte, error) {
	// 1. Remove "NodeID-" prefix
	if strings.HasPrefix(validationID, "NodeID-") {
		validationID = validationID[len("NodeID-"):]
	}

	// 2. Decode the CB58 string to bytes (data + checksum)
	decoded, err := base58.Decode(validationID)
	if err != nil {
		return nil, fmt.Errorf("failed to decode validationID from CB58: %w", err)
	}
	if len(decoded) < 4 {
		return nil, fmt.Errorf("decoded validationID is too short")
	}

	// 3. Separate data bytes and the 4-byte checksum
	data := decoded[:len(decoded)-4]
	checksum := decoded[len(decoded)-4:]

	// 4. Verify checksum (last 4 bytes of SHA-256 of data)
	hash := sha256.Sum256(data)
	expectedChecksum := hash[len(hash)-4:]
	if !bytes.Equal(checksum, expectedChecksum) {
		return nil, fmt.Errorf("validationID checksum mismatch")
	}

	// 5. Ensure data is 32 bytes, pad with leading zeros if shorter
	if len(data) > 32 {
		return nil, fmt.Errorf("validationID raw data is %d bytes, exceeds 32 bytes", len(data))
	}
	idBytes := make([]byte, 32)
	copy(idBytes[32-len(data):], data) // right-align the data in the 32-byte array (pad front with zeros)

	// 6. Allocate a 46-byte slice for the message and pack fields in order
	msg := make([]byte, 46)
	// codecID (uint16) at bytes [0:2] – big-endian
	binary.BigEndian.PutUint16(msg[0:2], 0)
	// typeID (uint32) at bytes [2:6] – big-endian
	binary.BigEndian.PutUint32(msg[2:6], 0)
	// validationID (32 bytes) at bytes [6:38]
	copy(msg[6:38], idBytes)
	// uptime (uint64) at bytes [38:46] – big-endian
	binary.BigEndian.PutUint64(msg[38:46], uptimeSeconds)

	return msg, nil
}

func (c *AggregatorClient) SubmitAggregateRequest(unsignedMessage []byte) ([]byte, error) {
	log.Printf("Submitting aggregate request with message length: %d bytes", len(unsignedMessage))

	req := AggregatorRequest{
		Message: hex.EncodeToString(unsignedMessage),
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
