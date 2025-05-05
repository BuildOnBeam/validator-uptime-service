// main.go

package main

import (
	"flag"
	"log"
	"math"
	"time"

	"uptime-service/aggregator"
	"uptime-service/config"
	"uptime-service/contract"
	"uptime-service/db"
	"uptime-service/delegation"
	"uptime-service/errutil"
	"uptime-service/logging"
	"uptime-service/validator"

	"github.com/ava-labs/avalanchego/ids"
	avalancheWarp "github.com/ava-labs/avalanchego/vms/platformvm/warp"
)

func main() {
	configPath := flag.String("config", "config.json", "Path to config file")
	flag.Parse()

	if len(flag.Args()) == 0 {
		log.Fatalf("Command required: 'generate' or 'submit'")
	}

	cmd := flag.Args()[0]
	if cmd != "generate" && cmd != "submit" {
		log.Fatalf("Unknown command: %s. Must be 'generate' or 'submit'", cmd)
	}

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	logging.SetLevel(cfg.LogLevel)

	dbClient, err := db.NewDBClient(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to initialize database client: %v", err)
	}
	defer dbClient.Close()

	start := time.Now()

	switch cmd {
	case "generate":
		err = generateUptimeProofs(cfg, dbClient)
	case "submit":
		err = submitUptimeProofs(cfg, dbClient)
	}

	if err != nil {
		log.Fatalf("Command %s failed: %v", cmd, err)
	}
	logging.Info("Command completed successfully")
	logging.Infof("Execution time: %s", time.Since(start))
}

// generateUptimeProofs fetches validator uptimes, generates signatures, and stores them in the db
func generateUptimeProofs(cfg *config.Config, dbClient *db.DBClient) error {
	aggClient, err := aggregator.NewAggregatorClient(cfg.AggregatorURL, uint32(cfg.NetworkID), cfg.SigningSubnetID, cfg.SourceChainId, cfg.LogLevel)
	if err != nil {
		return err
	}

	uptimeMap := validator.FetchAggregatedUptimes(cfg.AvalancheAPIList)
	logging.Infof("Fetched uptime info for %d validators from %d nodes", len(uptimeMap), len(cfg.AvalancheAPIList))

	for validationID, uptimeSamples := range uptimeMap {
		if len(uptimeSamples) == 0 {
			continue
		}

		validationIDBytes, err := ids.FromString(validationID)
		if errutil.HandleError("parsing validation ID for "+validationID, err) {
			continue
		}
		valID := ids.ID(validationIDBytes)

		var successfulUptimeSeconds uint64
		var successfulSignedMsg *avalancheWarp.Message

		success := false

		for _, uptime := range uptimeSamples {
			current := uptime
			unsignedMsg, err := aggClient.PackValidationUptimeMessage(validationID, current, uint32(cfg.NetworkID))
			if errutil.HandleError("packing uptime msg for "+validationID, err) {
				continue
			}

			signedMsg, err := aggClient.SubmitAggregateRequest(unsignedMsg)
			if err != nil {
				continue
			}

			// success - try to increase
			success = true
			successfulUptimeSeconds = current
			successfulSignedMsg = signedMsg

			for {
				next := uint64(math.Ceil(float64(current) * 1.05))
				if next <= current {
					next = current + 1
				}
				unsignedMsg, err := aggClient.PackValidationUptimeMessage(validationID, next, uint32(cfg.NetworkID))
				if err != nil {
					break
				}
				tempSignedMsg, err := aggClient.SubmitAggregateRequest(unsignedMsg)
				if err != nil {
					break
				}
				current = next
				successfulUptimeSeconds = current
				successfulSignedMsg = tempSignedMsg
			}
			break // break outer loop after first accepted + increase sweep
		}

		if !success {
			// fallback: decrease
			current := uptimeSamples[0] // start with highest
			for {
				current = uint64(math.Floor(float64(current) * 0.95))
				if current == 0 {
					break
				}
				unsignedMsg, err := aggClient.PackValidationUptimeMessage(validationID, current, uint32(cfg.NetworkID))
				if err != nil {
					break
				}
				signedMsg, err := aggClient.SubmitAggregateRequest(unsignedMsg)
				if err == nil {
					successfulUptimeSeconds = current
					successfulSignedMsg = signedMsg
					break
				}
			}
		}

		if successfulSignedMsg == nil {
			logging.Errorf("Failed to get any successful signature for validator %s", validationID)
			continue
		}

		err = dbClient.StoreUptimeProof(valID, successfulUptimeSeconds, successfulSignedMsg)
		if errutil.HandleError("storing uptime proof for "+validationID, err) {
			continue
		}
		logging.Infof("Successfully stored uptime proof for validator %s with uptime %d", validationID, successfulUptimeSeconds)
	}

	return nil
}

// submitUptimeProofs submits uptime proofs from the database to the smart contract and resolves rewards
func submitUptimeProofs(cfg *config.Config, dbClient *db.DBClient) error {
	contractClient, err := contract.NewContractClient(cfg.BeamRPC, cfg.StakingManagerAddress, cfg.WarpMessengerAddress, cfg.PrivateKey)
	if err != nil {
		return err
	}
	logging.Infof("Connected to staking manager contract at %s", cfg.StakingManagerAddress)

	delegationClient, err := delegation.NewDelegationClient(
		cfg.GraphQLEndpoint,
		cfg.BeamRPC,
		cfg.StakingManagerAddress,
		cfg.PrivateKey,
	)
	if err != nil {
		return err
	}
	logging.Info("Connected to delegation service")

	// Get all uptime proofs from the database
	proofs, err := dbClient.GetAllUptimeProofs()
	if err != nil {
		return err
	}

	if len(proofs) == 0 {
		logging.Info("No uptime proofs found in database")
		return nil
	}

	logging.Infof("Found %d uptime proofs in database", len(proofs))

	// Track validators with successful uptime submissions
	successfulValidators := make([]string, 0)

	for validationIDStr, proof := range proofs {
		validationID := proof.ValidationID
		signedMessage := proof.SignedMessage
		uptimeSeconds := proof.UptimeSeconds

		// Submit the signed uptime proof to the smart contract
		err = contractClient.SubmitUptimeProof(validationID, signedMessage)
		if errutil.HandleError("submitting uptime proof for "+validationIDStr, err) {
			continue
		}

		logging.Infof("Submitted uptime proof for validator %s to contract with uptime %d seconds", validationIDStr, uptimeSeconds)

		// Add this validator to the successful list for delegation resolution
		successfulValidators = append(successfulValidators, validationIDStr)
	}

	// For each successful validator, fetch and resolve delegations
	logging.Infof("Processing delegations for %d successful validators", len(successfulValidators))
	for _, validationID := range successfulValidators {
		// Fetch delegations for this validator
		delegations, err := delegationClient.GetDelegationsForValidator(validationID)
		if errutil.HandleError("fetching delegations for "+validationID, err) {
			continue
		}

		logging.Infof("Found %d delegations for validator %s", len(delegations), validationID)

		// Resolve rewards for these delegations
		if len(delegations) > 0 {
			err = delegationClient.ResolveRewards(delegations)
			if errutil.HandleError("resolving rewards for validator "+validationID, err) {
				continue
			}
			logging.Infof("Successfully resolved rewards for %d delegations of validator %s", len(delegations), validationID)
		}
	}

	return nil
}
