package main

import (
	"log"
	"time"

	"uptime-service/aggregator"
	"uptime-service/config"
	"uptime-service/contract"
	"uptime-service/errutil"
	"uptime-service/logging"
	"uptime-service/validator"
)

func main() {
	cfg, err := config.LoadConfig("config.json")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	logging.SetLevel(cfg.LogLevel)

	aggClient := &aggregator.AggregatorClient{
		BaseURL:          cfg.AggregatorURL,
		SigningSubnetID:  cfg.SigningSubnetID,
		QuorumPercentage: cfg.QuorumPercentage,
	}

	contractClient, err := contract.NewContractClient(cfg.BeamRPC, cfg.StakingManagerAddress, cfg.PrivateKey)
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
				// Build the unsigned uptime message for this validator
				msgBytes, err := aggClient.PackValidationUptimeMessage(val.ValidationID, val.UptimeSeconds)
				if errutil.HandleError("building uptime message for "+val.NodeID, err) {
					continue // skip this validator on error
				}
				logging.Infof("Built uptime message for validator %s (uptime=%d seconds)", val.NodeID, val.UptimeSeconds)

				// 3. Submit to signature-aggregator service to get aggregated signature
				signedMsg, err := aggClient.SubmitAggregateRequest(msgBytes)
				if errutil.HandleError("aggregating signature for "+val.NodeID, err) {
					continue
				}
				logging.Infof("Received aggregated signature for validator %s", val.NodeID)
				logging.Infof("signedmsg: %v", signedMsg)

				// // 4. Submit the signed uptime proof to the smart contract
				err = contractClient.SubmitUptimeProof(signedMsg)
				if errutil.HandleError("submitting uptime proof for "+val.NodeID, err) {
					continue
				}
				logging.Infof("Submitted uptime proof for validator %s to contract", val.NodeID)
			}
		}

		// 5. Sleep until the next day (24 hours).
		logging.Info("Uptime proof cycle completed. Sleeping for 24 hours...")
		time.Sleep(24 * time.Hour)
	}
}
