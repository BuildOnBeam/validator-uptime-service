package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

  "uptime-service/aggregator"
	"uptime-service/config"
	"uptime-service/contract"
	"uptime-service/db"
	// "uptime-service/errutil"
	"uptime-service/logging"

	"github.com/ava-labs/avalanchego/ids"
	avalancheWarp "github.com/ava-labs/avalanchego/vms/platformvm/warp"
)

func SubmitMissingUptimeProofs(cfg *config.Config, dbClient *db.DBClient) error {
	const epochID = "663"
	logging.Infof("Checking for missing uptime submissions in epoch %s", epochID)

	proofs, err := dbClient.GetAllUptimeProofs()
	if err != nil {
		return fmt.Errorf("failed to fetch from DB: %w", err)
	}

	hexToCB58 := map[string]string{}
	hexToProof := map[string]struct {
		ValidationID  ids.ID
		UptimeSeconds uint64
		SignedMessage *avalancheWarp.Message
	}{}
	for cb58ID, proof := range proofs {
		hexID := normalizeHex(proof.ValidationID.Hex())
		hexToCB58[hexID] = cb58ID
		hexToProof[hexID] = proof
	}

	query := `
	query getUptimeUpdates {
		uptimeUpdates(first: 1000, where: { epoch: "` + epochID + `" }) {
			id
			validationID
			epoch
			uptimeSeconds
		}
	}`
	reqBody, _ := json.Marshal(map[string]string{
		"query": query,
	})
	resp, err := http.Post(cfg.GraphQLEndpoint, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return fmt.Errorf("failed to send GraphQL request: %w", err)
	}
	defer resp.Body.Close()

	var gqlResp struct {
		Data struct {
			UptimeUpdates []struct {
				ValidationID string `json:"validationID"`
			} `json:"uptimeUpdates"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gqlResp); err != nil {
		return fmt.Errorf("failed to decode GraphQL response: %w", err)
	}

	submitted := make(map[string]bool)
	for _, update := range gqlResp.Data.UptimeUpdates {
		submitted[normalizeHex(update.ValidationID)] = true
	}

	var missingHexIDs []string
	for hexID := range hexToProof {
		if !submitted[hexID] {
			missingHexIDs = append(missingHexIDs, hexID)
		}
	}

	logging.Infof("Found %d validators missing from subgraph uptimeUpdates (epoch %s)", len(missingHexIDs), epochID)
	for _, hexID := range missingHexIDs {
		fmt.Println(hexID)
	}

	if len(missingHexIDs) == 0 {
		logging.Info("All uptime proofs appear to be submitted.")
		return nil
	}

	contractClient, err := contract.NewContractClient(cfg.BeamRPC, cfg.StakingManagerAddress, cfg.WarpMessengerAddress, cfg.PrivateKey)
	if err != nil {
		return fmt.Errorf("failed to init contract client: %w", err)
	}
	aggClient, err := aggregator.NewAggregatorClient(cfg.AggregatorURL, uint32(cfg.NetworkID), cfg.SigningSubnetID, cfg.SourceChainId, cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("failed to init aggregator client: %w", err)
	}

	failedValidators := make(map[string]string)

	for _, hexID := range missingHexIDs {
		proof := hexToProof[hexID]

		err := contractClient.SubmitUptimeProof(proof.ValidationID, proof.SignedMessage)
		if err != nil && strings.Contains(err.Error(), "invalid warp message") {
			// Re-sign and retry
			logging.Infof("Expired warp message for %s — re-signing", hexID)
			unsignedMsg, err := aggClient.PackValidationUptimeMessage(hexToCB58[hexID], proof.UptimeSeconds, uint32(cfg.NetworkID))
			if err != nil {
				failedValidators[hexID] = fmt.Sprintf("re-sign pack error: %v", err)
				continue
			}
			signedMsg, err := aggClient.SubmitAggregateRequest(unsignedMsg)
			if err != nil {
				failedValidators[hexID] = fmt.Sprintf("re-sign submit error: %v", err)
				continue
			}
			err = contractClient.SubmitUptimeProof(proof.ValidationID, signedMsg)
			if err != nil {
				failedValidators[hexID] = fmt.Sprintf("resubmit error: %v", err)
				continue
			}
			logging.Infof("✓ Re-signed and submitted proof for %s (CB58: %s)", hexID, hexToCB58[hexID])
		} else if err != nil {
			failedValidators[hexID] = fmt.Sprintf("initial error: %v", err)
			continue
		} else {
			logging.Infof("✓ Submitted proof for %s (CB58: %s)", hexID, hexToCB58[hexID])
		}
	}

	// Final log of failed validators
	if len(failedValidators) > 0 {
		logging.Error("❌ The following validators failed and were skipped:")
		for hexID, reason := range failedValidators {
			fmt.Printf("- %s (CB58: %s): %s\n", hexID, hexToCB58[hexID], reason)
		}
	} else {
		logging.Info("All missing uptime proofs successfully submitted.")
	}

	return nil
}

func normalizeHex(hex string) string {
	return strings.TrimPrefix(strings.ToLower(hex), "0x")
}
