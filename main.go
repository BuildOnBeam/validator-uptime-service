package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"uptime-service/config"
	"uptime-service/db"
	"uptime-service/delegation"
	"uptime-service/logging"
	"uptime-service/service"
)

func main() {
	// Global flags
	configPath := flag.String("config", "config.json", "Path to config file")
	flag.Parse()

	if flag.NArg() == 0 {
		printUsageAndExit("missing command")
	}
	cmd := flag.Arg(0)

	// Load config
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// Configure logging
	logging.SetLevel(cfg.LogLevel)

	// Init DB store
	//
	// NOTE: if your DB constructor is still called NewDBClient, either:
	//   - rename it to NewUptimeStore and return *UptimeStore
	//   - or change this line accordingly.
	store, err := db.NewUptimeStore(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("failed to initialize database: %v", err)
	}
	defer func() {
		if cerr := store.Close(); cerr != nil {
			log.Printf("failed to close database: %v", cerr)
		}
	}()

	// Init service layer
	uptimeSvc, err := service.NewUptimeService(cfg, store)
	if err != nil {
		log.Fatalf("failed to initialize uptime service: %v", err)
	}

	ctx := context.Background()
	start := time.Now()

	// Dispatch command
	switch cmd {
	case "resolve-rewards":
		err = uptimeSvc.ResolveRewards(ctx)

	case "generate-and-submit":
		err = uptimeSvc.GenerateAndSubmitUptimeProofs(ctx)

	case "submit-missing-uptime-proofs":
		err = service.SubmitMissingUptimeProofs(ctx, cfg, store)

	default:
		printUsageAndExit(fmt.Sprintf("unknown command: %s", cmd))
	}

	if err != nil {
		log.Fatalf("command %s failed: %v", cmd, err)
	}

	logging.Info("command completed successfully")
	logging.Infof("execution time: %s", time.Since(start))
}

func printUsageAndExit(msg string) {
	if msg != "" {
		fmt.Fprintf(os.Stderr, "%s\n\n", msg)
	}
	fmt.Fprintln(os.Stderr, `Usage:
  uptime-service -config=config.json <command> [args]

  Commands:
    resolve-rewards               Resolve rewards for all validators with proofs
    generate-and-submit           End-to-end: fetch → sign → submit → store
    submit-missing-uptime-proofs  Re-submit missing/expired proofs for an epoch`)
	os.Exit(1)
}

// resolveSingleValidator replicates your old "submit-single" behavior:
// - look up uptime for a specific validation ID from DB
// - fetch delegations
// - call resolveRewards for that one validator.
func resolveSingleValidator(
	_ context.Context,
	cfg *config.Config,
	store *db.UptimeStore,
	validationID string,
) error {
	proofs, err := store.GetAllUptimeProofs()
	if err != nil {
		return fmt.Errorf("failed to query proofs: %w", err)
	}

	proof, ok := proofs[validationID]
	if !ok {
		return fmt.Errorf("validation ID %s not found in DB", validationID)
	}

	logging.Infof("found DB entry with uptime = %d for %s", proof.UptimeSeconds, validationID)

	delegationClient, err := delegation.NewClient(
		cfg.GraphQLEndpoint,
		cfg.BeamRPC,
		cfg.StakingManagerAddress,
		cfg.PrivateKey,
	)
	if err != nil {
		return fmt.Errorf("failed to init delegation client: %w", err)
	}

	delegations, err := delegationClient.GetDelegationsForValidator(validationID)
	if err != nil {
		return fmt.Errorf("failed to fetch delegations: %w", err)
	}

	if len(delegations) == 0 {
		logging.Infof("no delegations found for validator %s", validationID)
		return nil
	}

	logging.Infof("found %d delegations for validator %s", len(delegations), validationID)

	if err := delegationClient.ResolveRewards(delegations); err != nil {
		return fmt.Errorf("failed to resolve rewards: %w", err)
	}

	logging.Infof("✓ successfully resolved rewards for %s", validationID)
	return nil
}
