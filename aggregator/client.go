package aggregator

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/mr-tron/base58/base58"
)

type AggregatorClient struct {
	BaseURL          string
	SigningSubnetID  string
	QuorumPercentage int
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
