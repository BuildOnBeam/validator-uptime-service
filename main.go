package main

import (
	"log"
	"math"
	"time"

	"uptime-service/aggregator"
	"uptime-service/config"
	"uptime-service/contract"
	"uptime-service/errutil"
	"uptime-service/logging"
	"uptime-service/validator"

	"github.com/ava-labs/avalanchego/ids"
	avalancheWarp "github.com/ava-labs/avalanchego/vms/platformvm/warp"
)

func main() {
	cfg, err := config.LoadConfig("config.json")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	logging.SetLevel(cfg.LogLevel)

	aggClient, err := aggregator.NewAggregatorClient(cfg.AggregatorURL, uint32(cfg.NetworkID), cfg.SigningSubnetID, cfg.SourceChainId, cfg.LogLevel)
	if err != nil {
		log.Fatalf("Failed to initialize aggregator client: %v", err)
	}

	contractClient, err := contract.NewContractClient(cfg.BeamRPC, cfg.StakingManagerAddress, cfg.WarpMessengerAddress, cfg.PrivateKey)
	if err != nil {
		log.Fatalf("Failed to initialize contract client: %v", err)
	}
	logging.Infof("Connected to staking manager contract at %s", cfg.StakingManagerAddress)

	logging.Info("Starting uptime-service loop...")
	for {
		// 1. Fetch current validators and their uptime from Avalanche P-Chain
		validators, err := validator.FetchUptimes(cfg.AvalancheAPI)
		if err != nil {
			logging.Errorf("Error fetching validator uptimes: %v", err)
		} else {
			logging.Infof("Fetched %d validators' uptime info", len(validators))
			// 2. For each validator, build message, aggregate signatures, and submit proof
			for _, val := range validators {
				// Parse the validation ID into a 32-byte array for the contract call
				validationIDBytes, err := ids.FromString(val.ValidationID)
				if errutil.HandleError("parsing validation ID for "+val.ValidationID, err) {
					continue
				}
				validationID := ids.ID(validationIDBytes)

				// Keep track of the successful uptime seconds and message
				var successfulUptimeSeconds uint64
				var successfulSignedMsg *avalancheWarp.Message

				// First try with the reported uptime seconds
				currentUptimeSeconds := val.UptimeSeconds
				unsignedMsg, err := aggClient.PackValidationUptimeMessage(val.ValidationID, currentUptimeSeconds, uint32(cfg.NetworkID))
				if errutil.HandleError("building initial uptime message for "+val.ValidationID, err) {
					continue
				}

				logging.Infof("Built uptime message for validator %s (uptime=%d seconds)", val.ValidationID, currentUptimeSeconds)

				// Try to get the first signature
				signedMsg, err := aggClient.SubmitAggregateRequest(unsignedMsg)

				if err != nil {
					// Path 1: Initial attempt failed, try decreasing by 5% until it succeeds
					logging.Infof("Initial signature attempt failed for %s. Trying with decreasing uptime values...", val.ValidationID)

					for {
						// Decrease by 5%, ensuring we get an integer
						currentUptimeSeconds = uint64(math.Floor(float64(currentUptimeSeconds) * 0.95))

						// Safety check to avoid very small values
						if currentUptimeSeconds == 0 {
							logging.Errorf("Uptime seconds reached zero for %s, aborting retry", val.ValidationID)
							break
						}

						logging.Infof("Trying with decreased uptime = %d seconds for %s", currentUptimeSeconds, val.ValidationID)

						unsignedMsg, err := aggClient.PackValidationUptimeMessage(val.ValidationID, currentUptimeSeconds, uint32(cfg.NetworkID))
						if errutil.HandleError("building decreased uptime message for "+val.ValidationID, err) {
							break
						}

						signedMsg, err = aggClient.SubmitAggregateRequest(unsignedMsg)
						if err == nil {
							// Success with decreased value
							successfulUptimeSeconds = currentUptimeSeconds
							successfulSignedMsg = signedMsg
							logging.Infof("Success with decreased uptime = %d seconds for %s", currentUptimeSeconds, val.ValidationID)
							break
						}

						logging.Infof("Still failed with uptime = %d seconds, continuing to decrease", currentUptimeSeconds)
					}
				} else {
					// Path 2: Initial attempt succeeded, try increasing by 5% until it fails
					logging.Infof("Initial signature attempt succeeded for %s. Trying with increasing uptime values...", val.ValidationID)
					successfulUptimeSeconds = currentUptimeSeconds
					successfulSignedMsg = signedMsg

					for {
						// Increase by 5%, ensuring we get an integer
						nextUptimeSeconds := uint64(math.Ceil(float64(currentUptimeSeconds) * 1.05))

						// Safety check to ensure we actually increased (for small values)
						if nextUptimeSeconds <= currentUptimeSeconds {
							nextUptimeSeconds = currentUptimeSeconds + 1
						}

						logging.Infof("Trying with increased uptime = %d seconds for %s", nextUptimeSeconds, val.ValidationID)

						unsignedMsg, err := aggClient.PackValidationUptimeMessage(val.ValidationID, nextUptimeSeconds, uint32(cfg.NetworkID))
						if errutil.HandleError("building increased uptime message for "+val.ValidationID, err) {
							break
						}

						tempSignedMsg, err := aggClient.SubmitAggregateRequest(unsignedMsg)
						if err != nil {
							// Failed with increased value, keep the last successful values
							logging.Infof("Failed with increased uptime = %d seconds, using last successful value = %d seconds",
								nextUptimeSeconds, successfulUptimeSeconds)
							break
						}

						// Update successful values
						currentUptimeSeconds = nextUptimeSeconds
						successfulUptimeSeconds = currentUptimeSeconds
						successfulSignedMsg = tempSignedMsg
						logging.Infof("Success with increased uptime = %d seconds, continuing to increase", successfulUptimeSeconds)
					}
				}

				// Check if we have a successful signed message
				if successfulSignedMsg == nil {
					logging.Errorf("Failed to get any successful signature for validator %s, skipping", val.ValidationID)
					continue
				}

				logging.Infof("Proceeding with uptime = %d seconds for validator %s", successfulUptimeSeconds, val.ValidationID)

				// 4. Submit the signed uptime proof to the smart contract
				err = contractClient.SubmitUptimeProof(validationID, successfulSignedMsg)
				if errutil.HandleError("submitting uptime proof for "+val.ValidationID, err) {
					continue
				}
				logging.Infof("Submitted uptime proof for validator %s to contract with uptime %d seconds", val.ValidationID, successfulUptimeSeconds)
			}
		}

		// 5. Sleep until the next day (24 hours).
		logging.Info("Uptime proof cycle completed. Sleeping for 24 hours...")
		time.Sleep(24 * time.Hour)
	}
}
