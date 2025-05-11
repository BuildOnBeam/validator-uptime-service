package commands

import (
	"fmt"

	"uptime-service/config"
	"uptime-service/db"
	"uptime-service/delegation"
	"uptime-service/logging"
)

func SubmitAndResolveSingleValidator(cfg *config.Config, dbClient *db.DBClient, validationID string) error {
	// Step 1: Load uptime_seconds from database using CB58 ID
	proofs, err := dbClient.GetAllUptimeProofs()
	if err != nil {
		return fmt.Errorf("failed to query proofs: %w", err)
	}

	proof, ok := proofs[validationID]
	if !ok {
		return fmt.Errorf("validation ID %s not found in DB", validationID)
	}

	logging.Infof("Found DB entry with uptime = %d for %s", proof.UptimeSeconds, validationID)

	// Step 2: Initialize aggregator client
	// aggClient, err := aggregator.NewAggregatorClient(cfg.AggregatorURL, uint32(cfg.NetworkID), cfg.SigningSubnetID, cfg.SourceChainId, cfg.LogLevel)
	// if err != nil {
	// 	return fmt.Errorf("failed to init aggregator: %w", err)
	// }

	// // Step 3: Pack and sign message with stored uptime
	// unsignedMsg, err := aggClient.PackValidationUptimeMessage(validationID, proof.UptimeSeconds, uint32(cfg.NetworkID))
	// if err != nil {
	// 	return fmt.Errorf("failed to pack unsigned uptime message: %w", err)
	// }

	// signedMsg, err := aggClient.SubmitAggregateRequest(unsignedMsg)
	// if err != nil {
	// 	return fmt.Errorf("failed to sign uptime message: %w", err)
	// }

	// // Step 4: Submit to contract
	// contractClient, err := contract.NewContractClient(cfg.BeamRPC, cfg.StakingManagerAddress, cfg.WarpMessengerAddress, cfg.PrivateKey)
	// if err != nil {
	// 	return fmt.Errorf("failed to init contract client: %w", err)
	// }

	// if err := contractClient.SubmitUptimeProof(proof.ValidationID, signedMsg); err != nil {
	// 	return fmt.Errorf("failed to submit uptime proof: %w", err)
	// }
	// logging.Infof("✓ Submitted uptime proof for %s", validationID)

	// Step 5: Resolve delegations
	delegationClient, err := delegation.NewDelegationClient(cfg.GraphQLEndpoint, cfg.BeamRPC, cfg.StakingManagerAddress, cfg.PrivateKey)
	if err != nil {
		return fmt.Errorf("failed to init delegation client: %w", err)
	}

	delegations, err := delegationClient.GetDelegationsForValidator(validationID)
	if err != nil {
		return fmt.Errorf("failed to fetch delegations: %w", err)
	}
	if len(delegations) == 0 {
		logging.Infof("No delegations found for validator %s", validationID)
		return nil
	}

	logging.Infof("Found %d delegations for validator %s", len(delegations), validationID)

	if err := delegationClient.ResolveRewards(delegations); err != nil {
		return fmt.Errorf("failed to resolve rewards: %w", err)
	}
	logging.Infof("✓ Successfully resolved rewards for %s", validationID)

	return nil
}
