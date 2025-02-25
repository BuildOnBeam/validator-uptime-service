package aggregator

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	req := AggregatorRequest{
		Message: hex.EncodeToString(unsignedMessage),
	}
	if c.SigningSubnetID != "" {
		req.SigningSubnetID = c.SigningSubnetID
	}
	if c.QuorumPercentage > 0 {
		req.QuorumPercent = c.QuorumPercentage
	}
	reqData, _ := json.Marshal(req)

	resp, err := http.Post(c.BaseURL+"/v1/signatureAggregator/fuji/aggregateSignatures", "application/json", bytes.NewReader(reqData))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		if errResp.Error != "" {
			return nil, errors.New("aggregator error: " + errResp.Error)
		}
		return nil, errors.New("aggregator request failed with status code: " + resp.Status)
	}

	var aggResp AggregatorResponse
	if err := json.NewDecoder(resp.Body).Decode(&aggResp); err != nil {
		return nil, err
	}

	signedHex := aggResp.SignedMessage
	if strings.HasPrefix(signedHex, "0x") {
		signedHex = signedHex[2:]
	}
	signedBytes, err := hex.DecodeString(signedHex)
	if err != nil {
		return nil, errors.New("invalid hex in signed-message: " + err.Error())
	}
	return signedBytes, nil
}
