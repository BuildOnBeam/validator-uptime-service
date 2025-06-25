package commands

import (
	"fmt"
	// "os"

	"uptime-service/config"
	"uptime-service/delegation"
	"uptime-service/logging"
)

// ResolveDelegationsForValidator resolves delegations for a specific validator
func ResolveDelegationsForValidator(cfg *config.Config, validationID string) error {
	logging.Infof("Resolving delegations for validator %s", validationID)

	delegationClient, err := delegation.NewDelegationClient(cfg.GraphQLEndpoint, cfg.BeamRPC, cfg.StakingManagerAddress, cfg.PrivateKey)
	if err != nil {
		return fmt.Errorf("failed to initialize delegation client: %w", err)
	}

	delegations, err := delegationClient.GetDelegationsForValidator(validationID)
	if err != nil {
		return fmt.Errorf("failed to fetch delegations: %w", err)
	}

	if len(delegations) == 0 {
		logging.Infof("No delegations found for validator %s", validationID)
		return nil
	}

	logging.Infof("Found %d delegations — submitting resolveRewards", len(delegations))
	if err := delegationClient.ResolveRewards(delegations); err != nil {
		return fmt.Errorf("resolveRewards failed: %w", err)
	}

	logging.Infof("✓ Successfully resolved rewards for validator %s", validationID)
	return nil
}
