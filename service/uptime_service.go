package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"uptime-service/aggregator"
	"uptime-service/config"
	"uptime-service/contract"
	"uptime-service/db"
	"uptime-service/delegation"
	"uptime-service/logging"
	"uptime-service/notifier"
	"uptime-service/subgraph"
	"uptime-service/uptimesign"
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
	subgraphCli   *subgraph.Client
	slack         *notifier.Slack
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
		subgraphCli:   subgraph.NewClient(cfg.GraphQLEndpoint),
		slack:         notifier.NewSlack(cfg.SlackWebhookURL),
	}, nil
}

// errNoCandidateAboveFloor means no node reported an uptime above the value
// already recorded on-chain, so there is nothing that could update it.
var errNoCandidateAboveFloor = errors.New("no uptime sample above the on-chain uptime")

// computeSignedUptime finds the highest uptime the quorum will sign for a
// validator. Only values strictly above floor (the uptime already recorded
// on-chain) are ever signed: the staking manager silently ignores proofs that
// do not increase the recorded uptime.
func (s *UptimeService) computeSignedUptime(
	validationID string,
	uptimeSamples []uint64,
	floor uint64,
) (finalUptime uint64, signedMsg *warp.Message, err error) {
	networkID := uint32(s.cfg.NetworkID)

	trySign := func(uptime uint64) (*warp.Message, error) {
		unsignedMsg, err := s.aggClient.PackValidationUptimeMessage(validationID, uptime, networkID)
		if err != nil {
			return nil, err
		}
		return s.aggClient.SubmitAggregateRequest(unsignedMsg)
	}

	candidates := samplesAbove(uptimeSamples, floor)
	if dropped := len(uptimeSamples) - len(candidates); dropped > 0 {
		logging.Infof("dropped %d sample(s) at or below on-chain uptime %d for %s", dropped, floor, validationID)
	}
	if len(candidates) == 0 {
		return 0, nil, errNoCandidateAboveFloor
	}

	attempts := 0
	sign := func(uptime uint64) (*warp.Message, error) {
		attempts++
		signed, err := trySign(uptime)
		if err != nil {
			logging.Infof("signing rejected at uptime = %d for %s", uptime, validationID)
		}
		return signed, err
	}

	finalUptime, signedMsg, err = uptimesign.Highest(candidates, floor, uptimesign.DefaultTolerance, sign)
	if err != nil {
		return 0, nil, err
	}

	logging.Infof(
		"quorum signed uptime = %d for %s (on-chain %d, highest sample %d, %d attempts)",
		finalUptime, validationID, floor, candidates[0], attempts,
	)
	return finalUptime, signedMsg, nil
}

// samplesAbove returns the samples strictly greater than floor, preserving order.
func samplesAbove(samples []uint64, floor uint64) []uint64 {
	out := make([]uint64, 0, len(samples))
	for _, s := range samples {
		if s > floor {
			out = append(out, s)
		}
	}
	return out
}

// checkExpectedValidations compares the validations the subgraph reports as
// active (excluding bootstrap validators) against those the API nodes returned
// uptime for, recording the expected count and any that are missing.
func (s *UptimeService) checkExpectedValidations(
	uptimeMap map[string][]uint64,
	bootstrapMap map[string]bool,
	outcome *runOutcome,
) {
	active, err := s.subgraphCli.ActiveValidations()
	if err != nil {
		logging.Errorf("❌ failed to load active validations from subgraph, skipping coverage check: %v", err)
		outcome.expected = -1
		return
	}

	for _, id := range active {
		validationID := id.String()
		if bootstrapMap[validationID] {
			continue
		}
		outcome.expected++
		if _, ok := uptimeMap[validationID]; !ok {
			logging.Errorf("❌ active validation %s missing from all uptime API node responses", validationID)
			outcome.missingFromNodes = append(outcome.missingFromNodes, validationID)
		}
	}

	logging.Infof(
		"expecting proofs for %d active validations; %d missing from node responses",
		outcome.expected, len(outcome.missingFromNodes),
	)
}

// loadUptimeFloors returns, per validation ID, the uptime already recorded
// on-chain. It reads the latest UptimeUpdated value from the subgraph and falls
// back to the locally stored proof when the subgraph has no record (or is
// unreachable).
func (s *UptimeService) loadUptimeFloors(
	validationIDs []string,
	storedProofs map[string]db.UptimeProof,
) map[string]uint64 {
	parsed := make([]ids.ID, 0, len(validationIDs))
	for _, v := range validationIDs {
		id, err := ids.FromString(v)
		if err != nil {
			continue
		}
		parsed = append(parsed, id)
	}

	onChain, err := s.subgraphCli.LatestUptimes(parsed)
	if err != nil {
		logging.Errorf("❌ failed to load on-chain uptimes from subgraph, falling back to stored proofs: %v", err)
		onChain = nil
	}

	floors := make(map[string]uint64, len(validationIDs))
	var fromChain, fromStore int
	for _, v := range validationIDs {
		id, err := ids.FromString(v)
		if err == nil {
			if uptime, ok := onChain[id]; ok {
				floors[v] = uptime
				fromChain++
				continue
			}
		}
		if proof, ok := storedProofs[v]; ok {
			floors[v] = proof.UptimeSeconds
			fromStore++
		}
	}

	logging.Infof("loaded uptime floors: %d from on-chain records, %d from stored proofs", fromChain, fromStore)
	return floors
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

// runOutcome tracks per-validator results across a single
// generate-and-submit run, so we can surface a summary at the end.
type runOutcome struct {
	submitted        []string // proof recorded on-chain (regardless of DB store result)
	notRecorded      []string // tx succeeded but the staking manager did not record the uptime
	failedSign       []string // no sample/fallback could be signed by quorum
	belowFloor       []string // every node reading was at or below the on-chain uptime
	failedSubmit     []string // signed but the staking-manager tx reverted/failed
	failedStore      []string // submitted on-chain but local DB store failed
	noSamples        []string // node fleet returned zero uptime samples (often: deactivated)
	failedNodes      []string // API nodes whose uptime fetch failed this run
	nodesTotal       int
	missingFromNodes []string // active validations no API node returned uptime for
	expected         int      // active non-bootstrap validations; -1 if unknown
	bootstrapSkipped int
	parseSkipped     int
}

// GenerateAndSubmitUptimeProofs is the end-to-end path: fetch -> sign -> submit -> store.
func (s *UptimeService) GenerateAndSubmitUptimeProofs(ctx context.Context) error {
	_ = ctx

	runStart := time.Now()
	logging.Info("starting end-to-end uptime proof generation and submission")

	if err := s.slack.Post(s.formatStartMessage(runStart)); err != nil {
		logging.Errorf("slack start notification failed: %v", err)
	}

	bootstrapMap := make(map[string]bool, len(s.cfg.BootstrapValidators))
	for _, id := range s.cfg.BootstrapValidators {
		bootstrapMap[id] = true
	}

	var outcome runOutcome

	nodeResults := validator.FetchNodeUptimes(s.cfg.AvalancheAPIList)
	outcome.nodesTotal = len(nodeResults)
	for _, r := range nodeResults {
		label := validator.NodeLabel(r.Endpoint)
		if r.Err != nil {
			logging.Errorf("❌ uptime fetch failed for node %s: %v", label, r.Err)
			outcome.failedNodes = append(outcome.failedNodes, label)
			continue
		}
		logging.Infof("node %s returned uptime for %d validationIDs", label, len(r.Uptimes))
	}

	uptimeMap := validator.AggregateUptimes(nodeResults)
	logging.Infof(
		"fetched uptime info for %d validationIDs from %d/%d nodes",
		len(uptimeMap),
		outcome.nodesTotal-len(outcome.failedNodes),
		outcome.nodesTotal,
	)

	storedProofs, err := s.store.GetAllUptimeProofs()
	if err != nil {
		return fmt.Errorf("load stored proofs: %w", err)
	}

	validationIDs := make([]string, 0, len(uptimeMap))
	for validationID := range uptimeMap {
		validationIDs = append(validationIDs, validationID)
	}
	floors := s.loadUptimeFloors(validationIDs, storedProofs)
	s.checkExpectedValidations(uptimeMap, bootstrapMap, &outcome)

	for validationID, uptimeSamples := range uptimeMap {
		if bootstrapMap[validationID] {
			logging.Infof("⏩ skipping bootstrap validator %s", validationID)
			outcome.bootstrapSkipped++
			continue
		}

		start := time.Now()
		logging.Infof("==== processing validator %s ====", validationID)

		if len(uptimeSamples) == 0 {
			logging.Infof("no uptime samples for %s", validationID)
			outcome.noSamples = append(outcome.noSamples, validationID)
			continue
		}

		finalUptime, signedMsg, err := s.computeSignedUptime(
			validationID,
			uptimeSamples,
			floors[validationID],
		)
		if errors.Is(err, errNoCandidateAboveFloor) {
			logging.Errorf("❌ no node reported uptime above on-chain value %d for %s", floors[validationID], validationID)
			outcome.belowFloor = append(outcome.belowFloor, validationID)
			continue
		}
		if err != nil {
			logging.Errorf("❌ could not get any valid signature for %s: %v", validationID, err)
			outcome.failedSign = append(outcome.failedSign, validationID)
			continue
		}

		valID, err := ids.FromString(validationID)
		if err != nil {
			logging.Errorf("invalid validator ID format for %s: %v", validationID, err)
			outcome.parseSkipped++
			continue
		}

		recorded, err := s.contractCli.SubmitUptimeProof(valID, signedMsg)
		if errors.Is(err, contract.ErrUptimeNotRecorded) {
			logging.Errorf("❌ uptime proof for %s was accepted but not recorded: %v", validationID, err)
			outcome.notRecorded = append(outcome.notRecorded, validationID)
			continue
		}
		if err != nil {
			logging.Errorf("❌ contract submission failed for %s: %v", validationID, err)
			outcome.failedSubmit = append(outcome.failedSubmit, validationID)
			continue
		}
		if recorded != finalUptime {
			logging.Infof("staking manager recorded uptime %d for %s (submitted %d)", recorded, validationID, finalUptime)
		}

		// Proof is recorded on-chain at this point — count it as submitted
		// regardless of whether the local DB write succeeds.
		outcome.submitted = append(outcome.submitted, validationID)

		if err := s.storeUptimeProofWithRefresh(valID, finalUptime, signedMsg); err != nil {
			logging.Errorf("❌ failed to store uptime proof for %s: %v", validationID, err)
			outcome.failedStore = append(outcome.failedStore, validationID)
			continue
		}

		logging.Infof("✅ stored and submitted uptime proof for %s at %d seconds", validationID, finalUptime)
		logging.Infof("finished processing %s in %s", validationID, time.Since(start))
	}

	if err := s.slack.Post(s.formatSummaryMessage(outcome, time.Since(runStart))); err != nil {
		logging.Errorf("slack completion notification failed: %v", err)
	}

	return nil
}

func (s *UptimeService) networkLabel() string {
	switch s.cfg.NetworkID {
	case 1:
		return "mainnet"
	case 5:
		return "fuji"
	default:
		return fmt.Sprintf("network-%d", s.cfg.NetworkID)
	}
}

func (s *UptimeService) formatStartMessage(runStart time.Time) string {
	return fmt.Sprintf(
		":rocket: *Uptime proof run started* — `%s`\nStarted at %s",
		s.networkLabel(),
		runStart.UTC().Format(time.RFC3339),
	)
}

func (s *UptimeService) formatSummaryMessage(o runOutcome, dur time.Duration) string {
	var sb strings.Builder

	hasFailures := len(o.failedSign) > 0 || len(o.failedSubmit) > 0 || len(o.failedStore) > 0 ||
		len(o.failedNodes) > 0 || len(o.notRecorded) > 0 || len(o.missingFromNodes) > 0 ||
		o.expected < 0
	if hasFailures {
		sb.WriteString(":warning: ")
	} else {
		sb.WriteString(":white_check_mark: ")
	}
	fmt.Fprintf(&sb, "*Uptime proof run completed* — `%s` (took %s)\n",
		s.networkLabel(), dur.Round(time.Second))

	if o.expected >= 0 {
		fmt.Fprintf(&sb, "• Coverage: *%d/%d* active validations recorded on-chain this run\n",
			len(o.submitted), o.expected)
		fmt.Fprintf(&sb, "• Active validations missing from node responses: *%d*\n", len(o.missingFromNodes))
	} else {
		sb.WriteString("• Coverage: *unknown* (could not load active validations from subgraph)\n")
	}
	fmt.Fprintf(&sb, "• Uptime API nodes responding: *%d/%d*\n", o.nodesTotal-len(o.failedNodes), o.nodesTotal)
	fmt.Fprintf(&sb, "• Submitted on-chain: *%d*\n", len(o.submitted))
	fmt.Fprintf(&sb, "• Failed to sign (quorum): *%d*\n", len(o.failedSign))
	fmt.Fprintf(&sb, "• No reading above on-chain uptime: *%d*\n", len(o.belowFloor))
	fmt.Fprintf(&sb, "• Failed to submit (tx revert): *%d*\n", len(o.failedSubmit))
	fmt.Fprintf(&sb, "• Submitted but not recorded (no uptime update): *%d*\n", len(o.notRecorded))
	fmt.Fprintf(&sb, "• No uptime samples (likely deactivated): *%d*\n", len(o.noSamples))
	if o.bootstrapSkipped > 0 {
		fmt.Fprintf(&sb, "• Skipped bootstrap validators: %d\n", o.bootstrapSkipped)
	}
	if len(o.failedStore) > 0 {
		fmt.Fprintf(&sb, "• Submitted but DB store failed: *%d*\n", len(o.failedStore))
	}
	if o.parseSkipped > 0 {
		fmt.Fprintf(&sb, "• Skipped malformed validation IDs: %d\n", o.parseSkipped)
	}

	appendIDs(&sb, "Uptime API nodes that failed to respond", o.failedNodes)
	appendIDs(&sb, "Active validations missing from node responses", o.missingFromNodes)
	appendIDs(&sb, "Signature failures", o.failedSign)
	appendIDs(&sb, "No reading above on-chain uptime", o.belowFloor)
	appendIDs(&sb, "Submission failures", o.failedSubmit)
	appendIDs(&sb, "Submitted but not recorded", o.notRecorded)
	appendIDs(&sb, "DB store failures (already on-chain)", o.failedStore)
	appendIDs(&sb, "No samples (likely deactivated)", o.noSamples)

	return sb.String()
}

// appendIDs renders a labeled bullet list of validation IDs into sb,
// capping at 20 entries to keep Slack messages readable.
func appendIDs(sb *strings.Builder, label string, ids []string) {
	if len(ids) == 0 {
		return
	}
	const max = 20
	fmt.Fprintf(sb, "\n*%s:*\n", label)
	n := len(ids)
	if n > max {
		n = max
	}
	for _, id := range ids[:n] {
		fmt.Fprintf(sb, "• `%s`\n", id)
	}
	if len(ids) > max {
		fmt.Fprintf(sb, "_…and %d more_\n", len(ids)-max)
	}
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

		_, err := contractClient.SubmitUptimeProof(proof.ValidationID, proof.SignedMessage)
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
			_, err = contractClient.SubmitUptimeProof(proof.ValidationID, signedMsg)
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
			logging.Errorf("- %s (CB58: %s): %s\n", hexID, hexToCB58[hexID], reason)
		}
	} else {
		logging.Info("all missing uptime proofs successfully submitted.")
	}

	return nil
}
