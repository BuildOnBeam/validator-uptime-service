package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"uptime-service/logging"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp"
	_ "github.com/lib/pq"
)

const refreshPrefix = "refresh_required:"

type UptimeProof struct {
	ValidationID  ids.ID
	UptimeSeconds uint64
	SignedMessage *warp.Message
}

type UptimeStore struct {
	db *sql.DB
}

func NewUptimeStore(dbURL string) (*UptimeStore, error) {
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS uptime_proofs (
			validation_id TEXT PRIMARY KEY,
			uptime_seconds BIGINT NOT NULL,
			signed_message BYTEA NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return nil, fmt.Errorf("create schema: %w", err)
	}

	logging.Info("connected to database and verified schema")

	return &UptimeStore{db: db}, nil
}

func (s *UptimeStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// StoreUptimeProof stores or updates an uptime proof in the database.
// If a higher uptime already exists, it returns an error with a special prefix
// so the caller can re-sign the stored value.
func (s *UptimeStore) StoreUptimeProof(
	validationID ids.ID,
	uptimeSeconds uint64,
	signedMessage *warp.Message,
) error {
	var existingUptime uint64
	var existingMsgBytes []byte

	err := s.db.QueryRow(
		`SELECT uptime_seconds, signed_message FROM uptime_proofs WHERE validation_id = $1`,
		validationID.String(),
	).Scan(&existingUptime, &existingMsgBytes)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err := s.db.Exec(`
			INSERT INTO uptime_proofs (validation_id, uptime_seconds, signed_message, updated_at)
			VALUES ($1, $2, $3, $4)
		`, validationID.String(), uptimeSeconds, signedMessage.Bytes(), time.Now())
		if err != nil {
			return fmt.Errorf("insert uptime proof: %w", err)
		}
		return nil

	case err != nil:
		return fmt.Errorf("query existing uptime: %w", err)
	}

	switch {
	case uptimeSeconds > existingUptime:
		_, err = s.db.Exec(`
			UPDATE uptime_proofs
			SET uptime_seconds = $2, signed_message = $3, updated_at = $4
			WHERE validation_id = $1
		`, validationID.String(), uptimeSeconds, signedMessage.Bytes(), time.Now())
		if err != nil {
			return fmt.Errorf("update uptime proof: %w", err)
		}
		return nil

	case uptimeSeconds == existingUptime:
		logging.Infof("overwriting signed message for %s with same uptime %d", validationID.String(), uptimeSeconds)
		_, err = s.db.Exec(`
			UPDATE uptime_proofs
			SET signed_message = $2, updated_at = $3
			WHERE validation_id = $1
		`, validationID.String(), signedMessage.Bytes(), time.Now())
		if err != nil {
			return fmt.Errorf("refresh signed message: %w", err)
		}
		return nil

	default:
		logging.Infof("re-signing with stored higher uptime %d for %s", existingUptime, validationID.String())
		return fmt.Errorf("%s%d", refreshPrefix, existingUptime)
	}
}

func IsRefreshRequiredError(err error) (bool, uint64) {
	if err == nil {
		return false, 0
	}
	msg := err.Error()
	if len(msg) <= len(refreshPrefix) || msg[:len(refreshPrefix)] != refreshPrefix {
		return false, 0
	}
	var v uint64
	fmt.Sscanf(msg, refreshPrefix+"%d", &v)
	return true, v
}

func (s *UptimeStore) GetAllUptimeProofs() (map[string]UptimeProof, error) {
	rows, err := s.db.Query(
		`SELECT validation_id, uptime_seconds, signed_message FROM uptime_proofs`,
	)
	if err != nil {
		return nil, fmt.Errorf("query uptime proofs: %w", err)
	}
	defer rows.Close()

	proofs := make(map[string]UptimeProof)

	for rows.Next() {
		var validationIDStr string
		var uptimeSeconds uint64
		var signedMessageBytes []byte

		if err := rows.Scan(&validationIDStr, &uptimeSeconds, &signedMessageBytes); err != nil {
			return nil, fmt.Errorf("scan uptime proof: %w", err)
		}

		validationID, err := ids.FromString(validationIDStr)
		if err != nil {
			return nil, fmt.Errorf("invalid validation ID in db: %w", err)
		}

		signedMessage, err := warp.ParseMessage(signedMessageBytes)
		if err != nil {
			return nil, fmt.Errorf("invalid warp message in db: %w", err)
		}

		proofs[validationIDStr] = UptimeProof{
			ValidationID:  validationID,
			UptimeSeconds: uptimeSeconds,
			SignedMessage: signedMessage,
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate uptime proofs: %w", err)
	}

	return proofs, nil
}
