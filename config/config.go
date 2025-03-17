package config

import (
	"encoding/json"
	"log"
	"os"
)

type Config struct {
	AvalancheAPI          string `json:"avalanche_api"`
	AggregatorURL         string `json:"aggregator_url"`
	SigningSubnetID       string `json:"signing_subnet_id"`
	SourceChainId         string `json:"source_chain_id"`
	QuorumPercentage      int    `json:"quorum_percentage"`
	BeamRPC               string `json:"beam_rpc"`
	StakingManagerAddress string `json:"contract_address"`
	WarpMessengerAddress  string `json:"warp_messenger_address"`
	PrivateKey            string `json:"private_key"`
	LogLevel              string `json:"log_level"`
	NetworkID             int    `json:"network_id"`
}

func LoadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	cfg := &Config{
		QuorumPercentage: 67,
		LogLevel:         "info",
	}
	if err := decoder.Decode(cfg); err != nil {
		return nil, err
	}

	log.Printf("Loaded configuration from %s", path)
	return cfg, nil
}
