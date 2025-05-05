package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
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
	logging.Infof("Fetched uptime info for %d validationIDs from %d nodes", len(uptimeMap), len(cfg.AvalancheAPIList))

	// Preload stored uptimes from DB
	storedProofs, err := dbClient.GetAllUptimeProofs()
	if err != nil {
		return fmt.Errorf("failed to load existing uptime proofs: %w", err)
	}

	for validationID, uptimeSamples := range uptimeMap {
		startVal := time.Now()
		logging.Infof("==== Processing validationID %s ====", validationID)
		logging.Infof("Uptime samples for %s: %v", validationID, uptimeSamples)

    sort.Slice(uptimeSamples, func(i, j int) bool {
      return uptimeSamples[i] > uptimeSamples[j]
    })

		logging.Infof("Sorted uptime samples for %s: %v", validationID, uptimeSamples)

		if len(uptimeSamples) == 0 {
			continue
		}

		validationIDBytes, err := ids.FromString(validationID)
		if errutil.HandleError("parsing validation ID for "+validationID, err) {
			continue
		}
		valID := ids.ID(validationIDBytes)

		var signedMsg *avalancheWarp.Message
		var finalUptime uint64
		var attempted bool

		for idx, initialUptime := range uptimeSamples {
			logging.Infof("Trying sample #%d with uptime = %d seconds for validationID %s", idx+1, initialUptime, validationID)
			current := initialUptime
			unsignedMsg, err := aggClient.PackValidationUptimeMessage(validationID, current, uint32(cfg.NetworkID))
			if err != nil {
				continue
			}
			signed, err := aggClient.SubmitAggregateRequest(unsignedMsg)
			if err != nil {
				continue
			}

			attempted = true
			signedMsg = signed
			finalUptime = current
			logging.Infof("Initial signature succeeded with uptime = %d for validationID %s", current, validationID)

			for {
				next := uint64(math.Ceil(float64(current) * 1.05))
				if next <= current {
					next = current + 1
				}
				logging.Infof("Trying with increased uptime = %d for validationID %s", next, validationID)
				unsignedMsg, err := aggClient.PackValidationUptimeMessage(validationID, next, uint32(cfg.NetworkID))
				if err != nil {
					break
				}
				signedTemp, err := aggClient.SubmitAggregateRequest(unsignedMsg)
				if err != nil {
					logging.Infof("Failed to sign at increased uptime = %d for %s, using last successful value %d", next, validationID, current)
					break
				}
				current = next
				finalUptime = current
				signedMsg = signedTemp
			}
			break
		}

		if !attempted {
			current := uptimeSamples[len(uptimeSamples)-1]
			storedUptime := uint64(0)
			if proof, exists := storedProofs[validationID]; exists {
				storedUptime = proof.UptimeSeconds
			}

			logging.Infof("Initial signature attempts failed for %s. Trying with decreasing values from lowest sample: %d", validationID, current)
			for {
				current = uint64(math.Floor(float64(current) * 0.95))
				if current == 0 {
					logging.Infof("Uptime seconds reached zero for %s, aborting retry", validationID)
					break
				}

				// Stop if we reach or drop below stored uptime
				if storedUptime > 0 && current <= storedUptime {
					logging.Infof("Reached stored uptime (%d) or below for %s; attempting stored value", storedUptime, validationID)
					unsignedMsg, err := aggClient.PackValidationUptimeMessage(validationID, storedUptime, uint32(cfg.NetworkID))
					if err == nil {
						signed, err := aggClient.SubmitAggregateRequest(unsignedMsg)
						if err == nil {
							finalUptime = storedUptime
							signedMsg = signed
						} else {
							logging.Infof("Signing with stored uptime %d failed for %s: %v", storedUptime, validationID, err)
						}
					}
					break
				}

				logging.Infof("Trying with decreased uptime = %d for %s", current, validationID)
				unsignedMsg, err := aggClient.PackValidationUptimeMessage(validationID, current, uint32(cfg.NetworkID))
				if err != nil {
					break
				}
				signed, err := aggClient.SubmitAggregateRequest(unsignedMsg)
				if err == nil {
					finalUptime = current
					signedMsg = signed
					break
				}
			}
		}

		if signedMsg == nil {
			logging.Errorf("No valid signature for validationID %s", validationID)
			continue
		}

		err = dbClient.StoreUptimeProof(valID, finalUptime, signedMsg)
		if err != nil {
			if strings.HasPrefix(err.Error(), "refresh_required:") {
				var storedUptime uint64
				fmt.Sscanf(err.Error(), "refresh_required:%d", &storedUptime)
				logging.Infof("Re-signing with stored higher uptime %d for %s", storedUptime, validationID)
				unsignedMsg, err := aggClient.PackValidationUptimeMessage(validationID, storedUptime, uint32(cfg.NetworkID))
				if err != nil {
					logging.Errorf("failed to repack for refresh: %v", err)
					continue
				}
				signed, err := aggClient.SubmitAggregateRequest(unsignedMsg)
				if err != nil {
					logging.Errorf("refresh signature failed: %v", err)
					continue
				}
				err = dbClient.StoreUptimeProof(valID, storedUptime, signed)
				if err != nil {
					logging.Errorf("failed to refresh store: %v", err)
					continue
				}
				logging.Infof("Refreshed record for %s at stored uptime %d", validationID, storedUptime)
			} else {
				logging.Errorf("store failed: %v", err)
			}
			continue
		}

		logging.Infof("[✓] Stored uptime proof for %s at %d seconds", validationID, finalUptime)
		logging.Infof("Finished processing %s in %s", validationID, time.Since(startVal))
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
