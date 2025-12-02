package service

import (
  "bytes"
	"context"
  "encoding/json"
	"fmt"
	"math"
  "net/http"
	"strings"
	"time"

	"uptime-service/aggregator"
	"uptime-service/config"
	"uptime-service/contract"
	"uptime-service/db"
	"uptime-service/delegation"
	"uptime-service/logging"
	"uptime-service/validator"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp"
)

const refreshPrefix = "refresh_required:"

type UptimeService struct {
	cfg           *config.Config
	store         *db.UptimeStore
	aggClient     *aggregator.Client
	contractCli   *contract.ContractClient
	delegationCli *delegation.Client
}

func normalizeHex(hexStr string) string {
	return strings.TrimPrefix(strings.ToLower(hexStr), "0x")
}

// NewUptimeService wires all dependencies together using your existing clients.
func NewUptimeService(cfg *config.Config, store *db.UptimeStore) (*UptimeService, error) {
	agg, err := aggregator.NewClient(
		cfg.AggregatorURL,
		uint32(cfg.NetworkID),
		cfg.SigningSubnetID,
		cfg.SourceChainId,
		cfg.LogLevel,
		cfg.QuorumPercentage,
	)
	if err != nil {
		return nil, fmt.Errorf("init aggregator: %w", err)
	}

	contractCli, err := contract.NewContractClient(
		cfg.BeamRPC,
		cfg.StakingManagerAddress,
		cfg.WarpMessengerAddress,
		cfg.PrivateKey,
	)
	if err != nil {
		return nil, fmt.Errorf("init contract client: %w", err)
	}

	delegationCli, err := delegation.NewClient(
		cfg.GraphQLEndpoint,
		cfg.BeamRPC,
		cfg.StakingManagerAddress,
		cfg.PrivateKey,
	)
	if err != nil {
		return nil, fmt.Errorf("init delegation client: %w", err)
	}

	return &UptimeService{
		cfg:           cfg,
		store:         store,
		aggClient:     agg,
		contractCli:   contractCli,
		delegationCli: delegationCli,
	}, nil
}

// computeSignedUptime encapsulates the “try highest sample, then ramp up by 5%, then
// fall back to decreasing/DB-stored value” logic and is reused by both generate-only
// and generate-and-submit flows.
func (s *UptimeService) computeSignedUptime(
	validationID string,
	uptimeSamples []uint64,
	storedProofs map[string]db.UptimeProof,
) (finalUptime uint64, signedMsg *warp.Message) {
	networkID := uint32(s.cfg.NetworkID)

	trySign := func(uptime uint64) (*warp.Message, error) {
		unsignedMsg, err := s.aggClient.PackValidationUptimeMessage(validationID, uptime, networkID)
		if err != nil {
			return nil, err
		}
		return s.aggClient.SubmitAggregateRequest(unsignedMsg)
	}

	var attempted bool

	for idx, sample := range uptimeSamples {
		logging.Infof("trying sample #%d with uptime = %d for %s", idx+1, sample, validationID)

		signed, err := trySign(sample)
		if err != nil {
			logging.Infof("signing failed at sample %d (%d seconds) for %s: %v", idx+1, sample, validationID, err)
			continue
		}

		attempted = true
		signedMsg = signed
		finalUptime = sample
		logging.Infof("initial signature succeeded with uptime = %d for %s", sample, validationID)

		if idx == 0 {
			current := sample
			for {
				next := uint64(math.Ceil(float64(current) * 1.05))
				if next <= current {
					next = current + 1
				}
				logging.Infof("trying increased uptime = %d for %s", next, validationID)

				signedNext, err := trySign(next)
				if err != nil {
					logging.Infof("failed at increased uptime = %d for %s, keeping %d", next, validationID, current)
					break
				}
				current = next
				finalUptime = current
				signedMsg = signedNext
			}
		}
		break
	}

	if attempted {
		return finalUptime, signedMsg
	}

	// All initial samples failed – decrease from the lowest sample and/or stored DB uptime.
	current := uptimeSamples[len(uptimeSamples)-1]
	var storedUptime uint64
	if proof, exists := storedProofs[validationID]; exists {
		storedUptime = proof.UptimeSeconds
	}

	logging.Infof(
		"all samples failed for %s. decreasing from %d by 5%% until <= stored (%d)",
		validationID,
		current,
		storedUptime,
	)

	for {
		current = uint64(math.Floor(float64(current) * 0.95))
		if current == 0 {
			logging.Infof("uptime reached 0 for %s, aborting", validationID)
			break
		}
		if storedUptime > 0 && current <= storedUptime {
			logging.Infof("trying stored uptime %d for %s", storedUptime, validationID)
			signed, err := trySign(storedUptime)
			if err == nil {
				return storedUptime, signed
			}
			logging.Infof("stored uptime signing failed for %s: %v", validationID, err)
			break
		}

		logging.Infof("trying decreased uptime = %d for %s", current, validationID)
		signed, err := trySign(current)
		if err == nil {
			return current, signed
		}
	}

	return 0, nil
}

func (s *UptimeService) storeUptimeProofWithRefresh(
	validationID ids.ID,
	uptimeSeconds uint64,
	signedMsg *warp.Message,
) error {
	err := s.store.StoreUptimeProof(validationID, uptimeSeconds, signedMsg)
	ok, stored := parseRefreshRequired(err)
	if !ok {
		return err
	}

	logging.Infof("re-signing with stored higher uptime %d for %s", stored, validationID.String())
	unsigned, packErr := s.aggClient.PackValidationUptimeMessage(
		validationID.String(),
		stored,
		uint32(s.cfg.NetworkID),
	)
	if packErr != nil {
		return fmt.Errorf("repack for refresh: %w", packErr)
	}
	signed, signErr := s.aggClient.SubmitAggregateRequest(unsigned)
	if signErr != nil {
		return fmt.Errorf("refresh signature failed: %w", signErr)
	}
	if storeErr := s.store.StoreUptimeProof(validationID, stored, signed); storeErr != nil {
		return fmt.Errorf("refresh store failed: %w", storeErr)
	}
	logging.Infof("refreshed record for %s at stored uptime %d", validationID.String(), stored)
	return nil
}

// parseRefreshRequired checks if an error is of the form "refresh_required:<N>".
func parseRefreshRequired(err error) (bool, uint64) {
	if err == nil {
		return false, 0
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, refreshPrefix) {
		return false, 0
	}
	var v uint64
	_, scanErr := fmt.Sscanf(msg, refreshPrefix+"%d", &v)
	if scanErr != nil {
		return false, 0
	}
	return true, v
}

// GenerateAndSubmitUptimeProofs is the end-to-end path: fetch -> sign -> submit -> store.
func (s *UptimeService) GenerateAndSubmitUptimeProofs(ctx context.Context) error {
	_ = ctx

	logging.Info("starting end-to-end uptime proof generation and submission")

	bootstrapMap := make(map[string]bool, len(s.cfg.BootstrapValidators))
	for _, id := range s.cfg.BootstrapValidators {
		bootstrapMap[id] = true
	}

	uptimeMap := validator.FetchAggregatedUptimes(s.cfg.AvalancheAPIList)
	logging.Infof(
		"fetched uptime info for %d validationIDs from %d nodes",
		len(uptimeMap),
		len(s.cfg.AvalancheAPIList),
	)

	storedProofs, err := s.store.GetAllUptimeProofs()
	if err != nil {
		return fmt.Errorf("load stored proofs: %w", err)
	}

	for validationID, uptimeSamples := range uptimeMap {
		if bootstrapMap[validationID] {
			logging.Infof("⏩ skipping bootstrap validator %s", validationID)
			continue
		}

		start := time.Now()
		logging.Infof("==== processing validator %s ====", validationID)

		if len(uptimeSamples) == 0 {
			logging.Infof("no uptime samples for %s", validationID)
			continue
		}

		finalUptime, signedMsg := s.computeSignedUptime(
			validationID,
			uptimeSamples,
			storedProofs,
		)
		if signedMsg == nil {
			logging.Errorf("❌ could not get any valid signature for %s", validationID)
			continue
		}

		valID, err := ids.FromString(validationID)
		if err != nil {
			logging.Errorf("invalid validator ID format for %s: %v", validationID, err)
			continue
		}

		if err := s.contractCli.SubmitUptimeProof(valID, signedMsg); err != nil {
			logging.Errorf("❌ contract submission failed for %s: %v", validationID, err)
			continue
		}

		if err := s.storeUptimeProofWithRefresh(valID, finalUptime, signedMsg); err != nil {
			logging.Errorf("❌ failed to store uptime proof for %s: %v", validationID, err)
			continue
		}

		logging.Infof("✅ stored and submitted uptime proof for %s at %d seconds", validationID, finalUptime)
		logging.Infof("finished processing %s in %s", validationID, time.Since(start))
	}

	return nil
}

// Resolves delegations for all the validators.
func (s *UptimeService) ResolveRewards(ctx context.Context) error {
	_ = ctx

	proofs, err := s.store.GetAllUptimeProofs()
	if err != nil {
		return fmt.Errorf("load uptime proofs: %w", err)
	}
	if len(proofs) == 0 {
		logging.Info("no uptime proofs in database for resolving rewards")
		return nil
	}

	unique := make(map[string]bool, len(proofs))
	for validationID := range proofs {
		unique[validationID] = true
	}

	logging.Infof("resolving rewards for %d validators", len(unique))

	for validationID := range unique {
		delegations, err := s.delegationCli.GetDelegationsForValidator(validationID)
		if err != nil {
			logging.Errorf("fetch delegations for %s: %v", validationID, err)
			continue
		}

		if len(delegations) == 0 {
			logging.Infof("no delegations for %s", validationID)
			continue
		}

		if err := s.delegationCli.ResolveRewards(delegations); err != nil {
			logging.Errorf("resolve rewards for %s: %v", validationID, err)
			continue
		}
		logging.Infof("successfully resolved rewards for validator %s", validationID)
	}

	return nil
}

// SubmitMissingUptimeProofs checks the subgraph for missing uptime submissions
// for a given epoch, and submits/re-signs proofs as needed.
func SubmitMissingUptimeProofs(
	_ context.Context,
	cfg *config.Config,
	store *db.UptimeStore,
) error {
	const epochID = "663"
	logging.Infof("checking for missing uptime submissions in epoch %s", epochID)

	proofs, err := store.GetAllUptimeProofs()
	if err != nil {
		return fmt.Errorf("failed to fetch from DB: %w", err)
	}

	hexToCB58 := map[string]string{}
	hexToProof := map[string]db.UptimeProof{}

	for cb58ID, proof := range proofs {
		hexID := normalizeHex(proof.ValidationID.Hex())
		hexToCB58[hexID] = cb58ID
		hexToProof[hexID] = proof
	}

	query := `
	query getUptimeUpdates {
		uptimeUpdates(first: 1000, where: { epoch: "` + epochID + `" }) {
			validationID
		}
	}`
	reqBody, _ := json.Marshal(map[string]string{
		"query": query,
	})
	resp, err := http.Post(cfg.GraphQLEndpoint, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return fmt.Errorf("failed to send GraphQL request: %w", err)
	}
	defer resp.Body.Close()

	var gqlResp struct {
		Data struct {
			UptimeUpdates []struct {
				ValidationID string `json:"validationID"`
			} `json:"uptimeUpdates"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gqlResp); err != nil {
		return fmt.Errorf("failed to decode GraphQL response: %w", err)
	}

	submitted := make(map[string]bool)
	for _, update := range gqlResp.Data.UptimeUpdates {
		submitted[normalizeHex(update.ValidationID)] = true
	}

	var missingHexIDs []string
	for hexID := range hexToProof {
		if !submitted[hexID] {
			missingHexIDs = append(missingHexIDs, hexID)
		}
	}

	logging.Infof(
		"found %d validators missing from subgraph uptimeUpdates (epoch %s)",
		len(missingHexIDs),
		epochID,
	)

	if len(missingHexIDs) == 0 {
		logging.Info("all uptime proofs appear to be submitted.")
		return nil
	}

	contractClient, err := contract.NewContractClient(
		cfg.BeamRPC,
		cfg.StakingManagerAddress,
		cfg.WarpMessengerAddress,
		cfg.PrivateKey,
	)
	if err != nil {
		return fmt.Errorf("failed to init contract client: %w", err)
	}

	aggClient, err := aggregator.NewClient(
		cfg.AggregatorURL,
		uint32(cfg.NetworkID),
		cfg.SigningSubnetID,
		cfg.SourceChainId,
		cfg.LogLevel,
		cfg.QuorumPercentage,
	)
	if err != nil {
		return fmt.Errorf("failed to init aggregator client: %w", err)
	}

	failedValidators := make(map[string]string)

	for _, hexID := range missingHexIDs {
		proof := hexToProof[hexID]

		err := contractClient.SubmitUptimeProof(proof.ValidationID, proof.SignedMessage)
		if err != nil && strings.Contains(err.Error(), "invalid warp message") {
			logging.Infof("expired warp message for %s — re-signing", hexID)
			unsignedMsg, err := aggClient.PackValidationUptimeMessage(
				hexToCB58[hexID],
				proof.UptimeSeconds,
				uint32(cfg.NetworkID),
			)
			if err != nil {
				failedValidators[hexID] = fmt.Sprintf("re-sign pack error: %v", err)
				continue
			}
			signedMsg, err := aggClient.SubmitAggregateRequest(unsignedMsg)
			if err != nil {
				failedValidators[hexID] = fmt.Sprintf("re-sign submit error: %v", err)
				continue
			}
			err = contractClient.SubmitUptimeProof(proof.ValidationID, signedMsg)
			if err != nil {
				failedValidators[hexID] = fmt.Sprintf("resubmit error: %v", err)
				continue
			}
			logging.Infof("✓ re-signed and submitted proof for %s (CB58: %s)", hexID, hexToCB58[hexID])
		} else if err != nil {
			failedValidators[hexID] = fmt.Sprintf("initial error: %v", err)
			continue
		} else {
			logging.Infof("✓ submitted proof for %s (CB58: %s)", hexID, hexToCB58[hexID])
		}
	}

	if len(failedValidators) > 0 {
		logging.Error("❌ the following validators failed and were skipped:")
		for hexID, reason := range failedValidators {
			fmt.Printf("- %s (CB58: %s): %s\n", hexID, hexToCB58[hexID], reason)
		}
	} else {
		logging.Info("all missing uptime proofs successfully submitted.")
	}

	return nil
}
