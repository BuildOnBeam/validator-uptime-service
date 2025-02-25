package main

import (
	"log"
	"time"

	"uptime-service/config"
	"uptime-service/logging"
	"uptime-service/validator"
)

func main() {
	cfg, err := config.LoadConfig("config.json")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	logging.SetLevel(cfg.LogLevel)

	logging.Info("Starting uptime-service loop...")
	for {
		// 1. Fetch current validators and their uptime from Avalanche P-Chain
		validators, err := validator.FetchUptimes(cfg.AvalancheAPI)
		if err != nil {
			// If we can't fetch validator info, log and retry next day.
			logging.Errorf("Error fetching validator uptimes: %v", err)
		} else {
			logging.Infof("Fetched %d validators' uptime info", len(validators))
			for _, val := range validators {
				logging.Infof("Validator: %v", val)
			}
		}

		logging.Info("Uptime proof cycle completed. Sleeping for 24 hours...")
		time.Sleep(24 * time.Hour)
	}
}
