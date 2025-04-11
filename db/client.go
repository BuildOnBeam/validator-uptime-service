package db

import (
	"database/sql"
	"fmt"
	"time"

	"uptime-service/logging"

	"github.com/ava-labs/avalanchego/ids"
	avalancheWarp "github.com/ava-labs/avalanchego/vms/platformvm/warp"
	_ "github.com/lib/pq"
)

// DBClient handles database operations for uptime proofs
type DBClient struct {
	db *sql.DB
}

// NewDBClient creates a new database client and ensures the schema exists
func NewDBClient(dbURL string) (*DBClient, error) {
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Create table if not exists
	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS uptime_proofs (
		validation_id TEXT PRIMARY KEY,
		uptime_seconds BIGINT NOT NULL,
		signed_message BYTEA NOT NULL,
		created_at TIMESTAMP NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMP NOT NULL DEFAULT NOW()
	)
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to create uptime_proofs table: %w", err)
	}

	logging.Info("Connected to database and verified schema")
	return &DBClient{db: db}, nil
}

// Close closes the database connection
func (c *DBClient) Close() error {
	return c.db.Close()
}

// StoreUptimeProof stores or updates an uptime proof in the database
func (c *DBClient) StoreUptimeProof(validationID ids.ID, uptimeSeconds uint64, signedMessage *avalancheWarp.Message) error {
	// Check if there's an existing record with higher uptime
	var existingUptime uint64
	err := c.db.QueryRow("SELECT uptime_seconds FROM uptime_proofs WHERE validation_id = $1", validationID.String()).Scan(&existingUptime)

	// If record exists and the new uptime is not greater, don't update
	if err == nil && uptimeSeconds <= existingUptime {
		logging.Infof("Existing uptime for %s is higher or equal (%d >= %d), not updating",
			validationID.String(), existingUptime, uptimeSeconds)
		return nil
	}

	// Either record doesn't exist or new uptime is higher, store/update
	_, err = c.db.Exec(`
	INSERT INTO uptime_proofs (validation_id, uptime_seconds, signed_message, updated_at)
	VALUES ($1, $2, $3, $4)
	ON CONFLICT (validation_id) 
	DO UPDATE SET 
		uptime_seconds = $2,
		signed_message = $3,
		updated_at = $4
	`, validationID.String(), uptimeSeconds, signedMessage.Bytes(), time.Now())

	if err != nil {
		return fmt.Errorf("failed to store uptime proof: %w", err)
	}

	return nil
}

// GetAllUptimeProofs retrieves all uptime proofs from the database
func (c *DBClient) GetAllUptimeProofs() (map[string]struct {
	ValidationID  ids.ID
	UptimeSeconds uint64
	SignedMessage *avalancheWarp.Message
}, error) {
	rows, err := c.db.Query("SELECT validation_id, uptime_seconds, signed_message FROM uptime_proofs")
	if err != nil {
		return nil, fmt.Errorf("failed to query uptime proofs: %w", err)
	}
	defer rows.Close()

	proofs := make(map[string]struct {
		ValidationID  ids.ID
		UptimeSeconds uint64
		SignedMessage *avalancheWarp.Message
	})

	for rows.Next() {
		var validationIDStr string
		var uptimeSeconds uint64
		var signedMessageBytes []byte

		if err := rows.Scan(&validationIDStr, &uptimeSeconds, &signedMessageBytes); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		validationID, err := ids.FromString(validationIDStr)
		if err != nil {
			return nil, fmt.Errorf("invalid validation ID in database: %w", err)
		}

		signedMessage, err := avalancheWarp.ParseMessage(signedMessageBytes)
		if err != nil {
			return nil, fmt.Errorf("invalid signed message in database: %w", err)
		}

		proofs[validationIDStr] = struct {
			ValidationID  ids.ID
			UptimeSeconds uint64
			SignedMessage *avalancheWarp.Message
		}{
			ValidationID:  validationID,
			UptimeSeconds: uptimeSeconds,
			SignedMessage: signedMessage,
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return proofs, nil
}
