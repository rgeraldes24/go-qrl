// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-qrl library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-qrl library. If not, see <http://www.gnu.org/licenses/>.

package system

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/theQRL/go-qrl/common"
)

const (
	syncStatusPath     = "/qrl/v1/node/syncing"
	peerCountPath      = "/qrl/v1/node/peer_count"
	finalityPath       = "/qrl/v1/beacon/states/head/finality_checkpoints"
	finalizedBlockPath = "/qrl/v1/beacon/blocks/finalized"
	beaconHeaderPath   = "/qrl/v1/beacon/headers/%d"
	genesisPath        = "/qrl/v1/beacon/genesis"
	specPath           = "/qrl/v1/config/spec"
	proposerDutiesPath = "/qrl/v1/validator/duties/proposer/%d"

	// The pinned qrl-package topology creates 64 validator keys per participant,
	// and network_params.yaml does not override that package default. There is no
	// topology API that reports this value, so keep the expected cardinality
	// explicit and fail closed if the package topology changes.
	expectedValidatorsPerClient = 64
	beaconSlotsPerEpoch         = uint64(128)
	beaconSecondsPerSlot        = uint64(5)

	// Prometheus reconstructs process_start_time_seconds from procfs timing
	// data. Independent one-second clock/rounding shifts can put two scrapes up
	// to two seconds apart even when the process did not restart. Counter
	// continuity below remains authoritative for detecting a reset inside this
	// window.
	validatorProcessStartTimeToleranceSeconds = 2.0
)

var expectedBeaconSpec = map[string]string{
	"PRESET_BASE":      "mainnet",
	"SECONDS_PER_SLOT": "5",
	"SLOTS_PER_EPOCH":  "128",
}

type beaconSyncResponse struct {
	Data struct {
		HeadSlot     string `json:"head_slot"`
		SyncDistance string `json:"sync_distance"`
		IsSyncing    bool   `json:"is_syncing"`
		IsOptimistic bool   `json:"is_optimistic"`
		ELOffline    bool   `json:"el_offline"`
	} `json:"data"`
}

type beaconPeerCountResponse struct {
	Data struct {
		Connected string `json:"connected"`
	} `json:"data"`
}

type finalityResponse struct {
	ExecutionOptimistic bool `json:"execution_optimistic"`
	Data                struct {
		Finalized checkpoint `json:"finalized"`
	} `json:"data"`
}

type beaconSpecResponse struct {
	Data map[string]string `json:"data"`
}

type beaconGenesisResponse struct {
	Data *struct {
		GenesisTime string `json:"genesis_time"`
	} `json:"data"`
}

type proposerDutiesResponse struct {
	DependentRoot       string `json:"dependent_root"`
	ExecutionOptimistic bool   `json:"execution_optimistic"`
	Data                []struct {
		Pubkey         string `json:"pubkey"`
		ValidatorIndex string `json:"validator_index"`
		Slot           string `json:"slot"`
	} `json:"data"`
}

type proposerDuty struct {
	pubkey         string
	validatorIndex uint64
	slot           uint64
}

type proposerDutySet struct {
	dependentRoot string
	duties        []proposerDuty
}

type beaconHeaderResponse struct {
	ExecutionOptimistic bool `json:"execution_optimistic"`
	Data                *struct {
		Root      string `json:"root"`
		Canonical bool   `json:"canonical"`
		Header    *struct {
			Message *struct {
				Slot          string `json:"slot"`
				ProposerIndex string `json:"proposer_index"`
			} `json:"message"`
		} `json:"header"`
	} `json:"data"`
}

type canonicalBeaconHeader struct {
	root          string
	slot          uint64
	proposerIndex uint64
}

type finalizedBlockResponse struct {
	Version             string `json:"version"`
	ExecutionOptimistic bool   `json:"execution_optimistic"`
	Finalized           bool   `json:"finalized"`
	Data                *struct {
		Message *struct {
			Body *struct {
				ExecutionPayload *struct {
					BlockNumber string      `json:"block_number"`
					BlockHash   common.Hash `json:"block_hash"`
				} `json:"execution_payload"`
			} `json:"body"`
		} `json:"message"`
	} `json:"data"`
}

type finalizedExecutionPayload struct {
	blockNumber uint64
	blockHash   common.Hash
}

type checkpoint struct {
	Epoch string `json:"epoch"`
	Root  string `json:"root"`
}

type beaconStatus struct {
	headSlot       uint64
	syncDistance   uint64
	finalizedEpoch uint64
	finalizedRoot  string
	connectedPeers uint64
}

type httpReader struct {
	client *http.Client
}

// responds reports whether an HTTP endpoint is reachable. Any HTTP response,
// including a non-2xx status, proves that the stopped service still owns the
// published endpoint.
func (r httpReader) responds(ctx context.Context, rawURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (r httpReader) getJSON(ctx context.Context, baseURL, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+path, nil)
	if err != nil {
		return err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("GET %s: status %s: %s", req.URL, resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", req.URL, err)
	}
	return nil
}

func (r httpReader) getText(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("GET %s: status %s: %s", req.URL, resp.Status, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (r httpReader) beaconSpec(ctx context.Context, baseURL string) (map[string]string, error) {
	var response beaconSpecResponse
	if err := r.getJSON(ctx, baseURL, specPath, &response); err != nil {
		return nil, err
	}
	if len(response.Data) == 0 {
		return nil, fmt.Errorf("beacon spec response is empty")
	}
	return response.Data, nil
}

func (r httpReader) beaconGenesisTime(ctx context.Context, baseURL string) (uint64, error) {
	var response beaconGenesisResponse
	if err := r.getJSON(ctx, baseURL, genesisPath, &response); err != nil {
		return 0, err
	}
	if response.Data == nil {
		return 0, fmt.Errorf("beacon genesis response has no data")
	}
	genesisTime, err := strconv.ParseUint(response.Data.GenesisTime, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid beacon genesis time %q: %w", response.Data.GenesisTime, err)
	}
	if genesisTime == 0 {
		return 0, fmt.Errorf("beacon genesis time is zero")
	}
	return genesisTime, nil
}

func (r httpReader) proposerDuties(ctx context.Context, baseURL string, epoch uint64) (proposerDutySet, error) {
	var response proposerDutiesResponse
	if err := r.getJSON(ctx, baseURL, fmt.Sprintf(proposerDutiesPath, epoch), &response); err != nil {
		return proposerDutySet{}, err
	}
	if response.ExecutionOptimistic {
		return proposerDutySet{}, fmt.Errorf("proposer duties response is execution optimistic")
	}
	if response.DependentRoot == "" {
		return proposerDutySet{}, fmt.Errorf("proposer duties response has an empty dependent root")
	}
	if len(response.Data) == 0 {
		return proposerDutySet{}, fmt.Errorf("proposer duties response is empty")
	}
	firstSlot := epoch * beaconSlotsPerEpoch
	lastSlot := firstSlot + beaconSlotsPerEpoch
	duties := make([]proposerDuty, 0, len(response.Data))
	seenSlots := make(map[uint64]struct{}, len(response.Data))
	for i, raw := range response.Data {
		if strings.TrimSpace(raw.Pubkey) == "" {
			return proposerDutySet{}, fmt.Errorf("proposer duty %d has an empty pubkey", i)
		}
		slot, err := strconv.ParseUint(raw.Slot, 10, 64)
		if err != nil {
			return proposerDutySet{}, fmt.Errorf("proposer duty %d has invalid slot %q: %w", i, raw.Slot, err)
		}
		if slot < firstSlot || slot >= lastSlot {
			return proposerDutySet{}, fmt.Errorf("proposer duty %d slot %d is outside epoch %d", i, slot, epoch)
		}
		if _, exists := seenSlots[slot]; exists {
			return proposerDutySet{}, fmt.Errorf("proposer duties repeat slot %d", slot)
		}
		validatorIndex, err := strconv.ParseUint(raw.ValidatorIndex, 10, 64)
		if err != nil {
			return proposerDutySet{}, fmt.Errorf("proposer duty %d has invalid validator index %q: %w", i, raw.ValidatorIndex, err)
		}
		seenSlots[slot] = struct{}{}
		duties = append(duties, proposerDuty{pubkey: strings.ToLower(raw.Pubkey), validatorIndex: validatorIndex, slot: slot})
	}
	return proposerDutySet{dependentRoot: strings.ToLower(response.DependentRoot), duties: duties}, nil
}

func equalProposerDutySets(a, b proposerDutySet) error {
	if a.dependentRoot != b.dependentRoot {
		return fmt.Errorf("proposer duty dependent roots differ: %s != %s", a.dependentRoot, b.dependentRoot)
	}
	if len(a.duties) != len(b.duties) {
		return fmt.Errorf("proposer duty counts differ: %d != %d", len(a.duties), len(b.duties))
	}
	for i := range a.duties {
		if a.duties[i] != b.duties[i] {
			return fmt.Errorf("proposer duty %d differs: %+v != %+v", i, a.duties[i], b.duties[i])
		}
	}
	return nil
}

func (r httpReader) canonicalBeaconHeader(ctx context.Context, baseURL string, slot uint64) (canonicalBeaconHeader, error) {
	var response beaconHeaderResponse
	if err := r.getJSON(ctx, baseURL, fmt.Sprintf(beaconHeaderPath, slot), &response); err != nil {
		return canonicalBeaconHeader{}, err
	}
	if response.ExecutionOptimistic {
		return canonicalBeaconHeader{}, fmt.Errorf("beacon header response is execution optimistic")
	}
	if response.Data == nil || response.Data.Header == nil || response.Data.Header.Message == nil {
		return canonicalBeaconHeader{}, fmt.Errorf("beacon header response is incomplete")
	}
	if !response.Data.Canonical {
		return canonicalBeaconHeader{}, fmt.Errorf("beacon header is not canonical")
	}
	if response.Data.Root == "" {
		return canonicalBeaconHeader{}, fmt.Errorf("beacon header has an empty root")
	}
	headerSlot, err := strconv.ParseUint(response.Data.Header.Message.Slot, 10, 64)
	if err != nil {
		return canonicalBeaconHeader{}, fmt.Errorf("invalid beacon header slot %q: %w", response.Data.Header.Message.Slot, err)
	}
	if headerSlot != slot {
		return canonicalBeaconHeader{}, fmt.Errorf("beacon header returned slot %d, want %d", headerSlot, slot)
	}
	proposerIndex, err := strconv.ParseUint(response.Data.Header.Message.ProposerIndex, 10, 64)
	if err != nil {
		return canonicalBeaconHeader{}, fmt.Errorf("invalid beacon header proposer index %q: %w", response.Data.Header.Message.ProposerIndex, err)
	}
	return canonicalBeaconHeader{root: strings.ToLower(response.Data.Root), slot: headerSlot, proposerIndex: proposerIndex}, nil
}

func (r httpReader) finalizedExecutionPayload(ctx context.Context, baseURL string) (finalizedExecutionPayload, error) {
	var response finalizedBlockResponse
	if err := r.getJSON(ctx, baseURL, finalizedBlockPath, &response); err != nil {
		return finalizedExecutionPayload{}, err
	}
	if !strings.EqualFold(response.Version, "zond") {
		return finalizedExecutionPayload{}, fmt.Errorf("finalized beacon block version is %q, want zond", response.Version)
	}
	if response.ExecutionOptimistic {
		return finalizedExecutionPayload{}, fmt.Errorf("finalized beacon block is execution optimistic")
	}
	if !response.Finalized {
		return finalizedExecutionPayload{}, fmt.Errorf("beacon API did not mark the finalized block as finalized")
	}
	if response.Data == nil || response.Data.Message == nil || response.Data.Message.Body == nil || response.Data.Message.Body.ExecutionPayload == nil {
		return finalizedExecutionPayload{}, fmt.Errorf("finalized beacon block has no execution payload")
	}
	payload := response.Data.Message.Body.ExecutionPayload
	blockNumber, err := strconv.ParseUint(payload.BlockNumber, 10, 64)
	if err != nil {
		return finalizedExecutionPayload{}, fmt.Errorf("invalid finalized execution block number %q: %w", payload.BlockNumber, err)
	}
	if blockNumber == 0 {
		return finalizedExecutionPayload{}, fmt.Errorf("finalized execution payload remains at genesis")
	}
	if payload.BlockHash == (common.Hash{}) {
		return finalizedExecutionPayload{}, fmt.Errorf("finalized execution payload has a zero block hash")
	}
	return finalizedExecutionPayload{blockNumber: blockNumber, blockHash: payload.BlockHash}, nil
}

func validateBeaconSpecs(specs [2]map[string]string) error {
	for i, spec := range specs {
		for key, want := range expectedBeaconSpec {
			got, ok := spec[key]
			if !ok {
				return fmt.Errorf("CL%d beacon spec is missing %s", i+1, key)
			}
			if got != want {
				return fmt.Errorf("CL%d beacon spec %s=%q, want %q", i+1, key, got, want)
			}
		}
	}
	if !equalStringMaps(specs[0], specs[1]) {
		return fmt.Errorf("beacon node specs differ")
	}
	return nil
}

func equalStringMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if other, ok := b[key]; !ok || other != value {
			return false
		}
	}
	return true
}

func (r httpReader) beaconStatus(ctx context.Context, baseURL string) (beaconStatus, error) {
	var syncResp beaconSyncResponse
	if err := r.getJSON(ctx, baseURL, syncStatusPath, &syncResp); err != nil {
		return beaconStatus{}, err
	}
	if syncResp.Data.IsSyncing {
		return beaconStatus{}, fmt.Errorf("beacon node is syncing")
	}
	if syncResp.Data.IsOptimistic {
		return beaconStatus{}, fmt.Errorf("beacon node is optimistic")
	}
	if syncResp.Data.ELOffline {
		return beaconStatus{}, fmt.Errorf("beacon node reports its execution client offline")
	}
	headSlot, err := strconv.ParseUint(syncResp.Data.HeadSlot, 10, 64)
	if err != nil {
		return beaconStatus{}, fmt.Errorf("invalid beacon head slot %q: %w", syncResp.Data.HeadSlot, err)
	}
	syncDistance, err := strconv.ParseUint(syncResp.Data.SyncDistance, 10, 64)
	if err != nil {
		return beaconStatus{}, fmt.Errorf("invalid beacon sync distance %q: %w", syncResp.Data.SyncDistance, err)
	}
	if syncDistance != 0 {
		return beaconStatus{}, fmt.Errorf("beacon sync distance is %d", syncDistance)
	}

	var peers beaconPeerCountResponse
	if err := r.getJSON(ctx, baseURL, peerCountPath, &peers); err != nil {
		return beaconStatus{}, err
	}
	connected, err := strconv.ParseUint(peers.Data.Connected, 10, 64)
	if err != nil {
		return beaconStatus{}, fmt.Errorf("invalid connected peer count %q: %w", peers.Data.Connected, err)
	}
	if connected == 0 {
		return beaconStatus{}, fmt.Errorf("beacon node has no connected peers")
	}

	var finality finalityResponse
	if err := r.getJSON(ctx, baseURL, finalityPath, &finality); err != nil {
		return beaconStatus{}, err
	}
	if finality.ExecutionOptimistic {
		return beaconStatus{}, fmt.Errorf("finality response is execution optimistic")
	}
	finalizedEpoch, err := strconv.ParseUint(finality.Data.Finalized.Epoch, 10, 64)
	if err != nil {
		return beaconStatus{}, fmt.Errorf("invalid finalized epoch %q: %w", finality.Data.Finalized.Epoch, err)
	}
	if finality.Data.Finalized.Root == "" {
		return beaconStatus{}, fmt.Errorf("finalized checkpoint has an empty root")
	}
	return beaconStatus{
		headSlot:       headSlot,
		syncDistance:   syncDistance,
		finalizedEpoch: finalizedEpoch,
		finalizedRoot:  finality.Data.Finalized.Root,
		connectedPeers: connected,
	}, nil
}

type metricSample struct {
	labels map[string]string
	value  float64
}

type metricSet map[string][]metricSample

var validatorMetricNames = [...]string{
	"process_start_time_seconds",
	"validator_statuses",
	"validator_last_attested_slot",
	"validator_successful_attestations",
	"validator_successful_proposals",
	"validator_failed_attestations",
	"validator_failed_proposals",
}

// validatorDutySnapshot captures every validator duty signal used by the
// system check at one point in time. Counter deltas are used so failures that
// occur after the initial readiness sample cannot be hidden by an earlier
// baseline sample.
type validatorDutySnapshot struct {
	reportedValidators     int
	activeValidators       int
	attestedValidators     int
	activePubkeys          map[string]struct{}
	processStartTime       float64
	successfulAttestations float64
	successfulProposals    float64
	failedAttestations     float64
	failedProposals        float64
}

type validatorDutySnapshots [2]validatorDutySnapshot

type validatorDutyFailureError struct {
	duty  string
	count float64
}

func (e *validatorDutyFailureError) Error() string {
	return fmt.Sprintf("validator client reports %.0f failed %s", e.count, e.duty)
}

type validatorCounterRegressionError struct {
	metric string
	before float64
	after  float64
}

func (e *validatorCounterRegressionError) Error() string {
	return fmt.Sprintf("validator metric %s regressed from %.0f to %.0f", e.metric, e.before, e.after)
}

type validatorProcessResetError struct {
	before float64
	after  float64
}

func (e *validatorProcessResetError) Error() string {
	return fmt.Sprintf("validator process start time changed from %.3f to %.3f", e.before, e.after)
}

type validatorTopologyError struct {
	err error
}

func (e *validatorTopologyError) Error() string {
	return fmt.Sprintf("validator topology is invalid: %v", e.err)
}

func (e *validatorTopologyError) Unwrap() error {
	return e.err
}

func parseMetrics(input string) (metricSet, error) {
	var parser expfmt.TextParser
	families, err := parser.TextToMetricFamilies(strings.NewReader(input))
	if err != nil {
		return nil, fmt.Errorf("parse Prometheus exposition: %w", err)
	}
	metrics := make(metricSet)
	for _, name := range validatorMetricNames {
		family := families[name]
		if family == nil {
			continue
		}
		for _, metric := range family.Metric {
			value, err := scalarMetricValue(family.GetType(), metric)
			if err != nil {
				return nil, fmt.Errorf("metric %s: %w", name, err)
			}
			labels := make(map[string]string, len(metric.Label))
			for _, pair := range metric.Label {
				labelName := pair.GetName()
				if labelName == "" {
					return nil, fmt.Errorf("metric %s has an empty label name", name)
				}
				if _, exists := labels[labelName]; exists {
					return nil, fmt.Errorf("metric %s repeats label %q", name, labelName)
				}
				labels[labelName] = pair.GetValue()
			}
			metrics[name] = append(metrics[name], metricSample{labels: labels, value: value})
		}
	}
	return metrics, nil
}

func scalarMetricValue(metricType dto.MetricType, metric *dto.Metric) (float64, error) {
	switch metricType {
	case dto.MetricType_COUNTER:
		if metric.Counter == nil {
			return 0, fmt.Errorf("counter sample has no value")
		}
		return metric.Counter.GetValue(), nil
	case dto.MetricType_GAUGE:
		if metric.Gauge == nil {
			return 0, fmt.Errorf("gauge sample has no value")
		}
		return metric.Gauge.GetValue(), nil
	case dto.MetricType_UNTYPED:
		if metric.Untyped == nil {
			return 0, fmt.Errorf("untyped sample has no value")
		}
		return metric.Untyped.GetValue(), nil
	default:
		return 0, fmt.Errorf("unsupported scalar metric type %s", metricType.String())
	}
}

func snapshotValidatorMetrics(metrics metricSet) (validatorDutySnapshot, error) {
	statuses, err := pubkeyMetricValues(metrics, "validator_statuses")
	if err != nil {
		return validatorDutySnapshot{}, err
	}
	snapshot := validatorDutySnapshot{
		reportedValidators: len(statuses),
		activePubkeys:      make(map[string]struct{}, len(statuses)),
	}
	if snapshot.reportedValidators != expectedValidatorsPerClient {
		return validatorDutySnapshot{}, fmt.Errorf("reported %d validators, want exactly %d", snapshot.reportedValidators, expectedValidatorsPerClient)
	}
	for pubkey, status := range statuses {
		if !finiteNonNegative(status) {
			return validatorDutySnapshot{}, fmt.Errorf("validator %q status has invalid value %v", pubkey, status)
		}
		if status == 3 {
			snapshot.activeValidators++
			snapshot.activePubkeys[pubkey] = struct{}{}
		}
	}
	if snapshot.activeValidators != expectedValidatorsPerClient {
		return validatorDutySnapshot{}, fmt.Errorf("reported %d active validators, want exactly %d", snapshot.activeValidators, expectedValidatorsPerClient)
	}

	if snapshot.processStartTime, err = checkedSingleMetric(metrics, "process_start_time_seconds"); err != nil {
		return validatorDutySnapshot{}, err
	}
	if snapshot.processStartTime <= 0 {
		return validatorDutySnapshot{}, fmt.Errorf("validator process start time has invalid value %v", snapshot.processStartTime)
	}

	lastAttestedSlots, err := pubkeyMetricValues(metrics, "validator_last_attested_slot")
	if err != nil {
		return validatorDutySnapshot{}, err
	}
	successfulByPubkey, err := pubkeyMetricValues(metrics, "validator_successful_attestations")
	if err != nil {
		return validatorDutySnapshot{}, err
	}
	pubkeyMetrics := map[string]map[string]float64{
		"validator_last_attested_slot":      lastAttestedSlots,
		"validator_successful_attestations": successfulByPubkey,
	}
	for _, name := range []string{"validator_successful_proposals", "validator_failed_attestations", "validator_failed_proposals"} {
		values, err := pubkeyMetricValues(metrics, name)
		if err != nil {
			return validatorDutySnapshot{}, err
		}
		pubkeyMetrics[name] = values
	}
	for name, values := range pubkeyMetrics {
		if err := validateMetricPubkeySubset(name, values, snapshot.activePubkeys); err != nil {
			return validatorDutySnapshot{}, err
		}
	}
	for pubkey := range snapshot.activePubkeys {
		if lastAttestedSlots[pubkey] > 0 && successfulByPubkey[pubkey] > 0 {
			snapshot.attestedValidators++
		}
	}

	if snapshot.successfulAttestations, err = checkedMetricSum(metrics, "validator_successful_attestations"); err != nil {
		return validatorDutySnapshot{}, err
	}
	if snapshot.successfulProposals, err = checkedMetricSum(metrics, "validator_successful_proposals"); err != nil {
		return validatorDutySnapshot{}, err
	}
	if snapshot.failedAttestations, err = checkedMetricSum(metrics, "validator_failed_attestations"); err != nil {
		return validatorDutySnapshot{}, err
	}
	if snapshot.failedProposals, err = checkedMetricSum(metrics, "validator_failed_proposals"); err != nil {
		return validatorDutySnapshot{}, err
	}
	return snapshot, nil
}

func validateValidatorSnapshot(snapshot validatorDutySnapshot, requireEveryValidatorAttested, requireProposal, requireCleanDuties bool) error {
	if requireCleanDuties {
		if snapshot.failedAttestations > 0 {
			return &validatorDutyFailureError{duty: "attestations", count: snapshot.failedAttestations}
		}
		if snapshot.failedProposals > 0 {
			return &validatorDutyFailureError{duty: "proposals", count: snapshot.failedProposals}
		}
	}
	if requireEveryValidatorAttested && snapshot.attestedValidators != expectedValidatorsPerClient {
		return fmt.Errorf("only %d of %d active validators report both a successful attestation and a nonzero last-attested slot", snapshot.attestedValidators, expectedValidatorsPerClient)
	}
	if requireProposal && snapshot.successfulProposals <= 0 {
		return fmt.Errorf("no successful validator proposal reported")
	}
	return nil
}

func validateValidatorProgress(before, after validatorDutySnapshot) error {
	if err := validateValidatorContinuity(before, after); err != nil {
		return err
	}
	// Every validator has a recurring attestation duty, while proposal selection
	// is sparse. Require deterministic aggregate attestation progress and still
	// capture/check the proposal counter for regressions and failures below.
	if after.successfulAttestations <= before.successfulAttestations {
		return fmt.Errorf("no successful validator attestation since baseline (still %.0f)", after.successfulAttestations)
	}
	return nil
}

func validateValidatorContinuity(before, after validatorDutySnapshot) error {
	if math.Abs(after.processStartTime-before.processStartTime) > validatorProcessStartTimeToleranceSeconds {
		return &validatorProcessResetError{before: before.processStartTime, after: after.processStartTime}
	}
	if !equalPubkeySets(before.activePubkeys, after.activePubkeys) {
		return &validatorTopologyError{err: fmt.Errorf("active validator key set changed")}
	}
	counters := []struct {
		name   string
		before float64
		after  float64
	}{
		{name: "successful attestations", before: before.successfulAttestations, after: after.successfulAttestations},
		{name: "successful proposals", before: before.successfulProposals, after: after.successfulProposals},
		{name: "failed attestations", before: before.failedAttestations, after: after.failedAttestations},
		{name: "failed proposals", before: before.failedProposals, after: after.failedProposals},
	}
	for _, counter := range counters {
		if counter.after < counter.before {
			return &validatorCounterRegressionError{metric: counter.name, before: counter.before, after: counter.after}
		}
	}
	if delta := after.failedAttestations - before.failedAttestations; delta > 0 {
		return &validatorDutyFailureError{duty: "attestations since baseline", count: delta}
	}
	if delta := after.failedProposals - before.failedProposals; delta > 0 {
		return &validatorDutyFailureError{duty: "proposals since baseline", count: delta}
	}
	return nil
}

func checkedMetricSum(metrics metricSet, name string) (float64, error) {
	var total float64
	for _, sample := range metrics[name] {
		value := sample.value
		if !finiteNonNegative(value) {
			return 0, fmt.Errorf("validator metric %s has invalid value %v", name, value)
		}
		total += value
	}
	if !finiteNonNegative(total) {
		return 0, fmt.Errorf("validator metric %s has invalid total %v", name, total)
	}
	return total, nil
}

func checkedSingleMetric(metrics metricSet, name string) (float64, error) {
	samples := metrics[name]
	if len(samples) != 1 {
		return 0, fmt.Errorf("validator metric %s has %d samples, want exactly one", name, len(samples))
	}
	if len(samples[0].labels) != 0 {
		return 0, fmt.Errorf("validator metric %s unexpectedly has labels", name)
	}
	if !finiteNonNegative(samples[0].value) {
		return 0, fmt.Errorf("validator metric %s has invalid value %v", name, samples[0].value)
	}
	return samples[0].value, nil
}

func pubkeyMetricValues(metrics metricSet, name string) (map[string]float64, error) {
	values := make(map[string]float64, len(metrics[name]))
	for _, sample := range metrics[name] {
		pubkey := sample.labels["pubkey"]
		if pubkey == "" {
			return nil, fmt.Errorf("validator metric %s has a sample without a pubkey label", name)
		}
		if _, exists := values[pubkey]; exists {
			return nil, fmt.Errorf("validator metric %s repeats pubkey %q", name, pubkey)
		}
		if !finiteNonNegative(sample.value) {
			return nil, fmt.Errorf("validator metric %s for pubkey %q has invalid value %v", name, pubkey, sample.value)
		}
		values[pubkey] = sample.value
	}
	return values, nil
}

func validateMetricPubkeySubset(name string, values map[string]float64, active map[string]struct{}) error {
	for pubkey := range values {
		if _, ok := active[pubkey]; !ok {
			return fmt.Errorf("validator metric %s reports unknown pubkey %q", name, pubkey)
		}
	}
	return nil
}

func validateDisjointValidatorKeys(snapshots validatorDutySnapshots) error {
	for pubkey := range snapshots[0].activePubkeys {
		if _, overlap := snapshots[1].activePubkeys[pubkey]; overlap {
			return &validatorTopologyError{err: fmt.Errorf("VC1 and VC2 both manage pubkey %q", pubkey)}
		}
	}
	return nil
}

func equalPubkeySets(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for pubkey := range a {
		if _, ok := b[pubkey]; !ok {
			return false
		}
	}
	return true
}

func finiteNonNegative(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
