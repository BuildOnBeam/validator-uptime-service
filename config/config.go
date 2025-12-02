package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	AvalancheAPI          string   `json:"avalanche_api"`
	AvalancheAPIList      []string `json:"avalanche_api_list"`
	AggregatorURL         string   `json:"aggregator_url"`
	GraphQLEndpoint       string   `json:"graphql_endpoint"`
	SigningSubnetID       string   `json:"signing_subnet_id"`
	SourceChainId         string   `json:"source_chain_id"`
	QuorumPercentage      int      `json:"quorum_percentage"`
	BeamRPC               string   `json:"beam_rpc"`
	StakingManagerAddress string   `json:"contract_address"`
	WarpMessengerAddress  string   `json:"warp_messenger_address"`
	PrivateKey            string   `json:"private_key"`
	LogLevel              string   `json:"log_level"`
	NetworkID             int      `json:"network_id"`
	DatabaseURL           string   `json:"database_url"`
	BootstrapValidators   []string `json:"bootstrap_validators"`
}

func LoadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	cfg := &Config{
		QuorumPercentage: 67,
		LogLevel:         "info",
	}
	if err := json.NewDecoder(f).Decode(cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}

	return cfg, nil
}
