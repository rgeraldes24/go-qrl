// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

type suiteAttemptPresence struct {
	Attempt json.RawMessage `json:"attempt"`
}

type resultsAttemptPresence struct {
	Suites       []suiteAttemptPresence `json:"suites"`
	SuiteHistory []suiteAttemptPresence `json:"suite_history"`
}

// LoadResults strictly reads and validates a previously published aggregate.
func LoadResults(path string) (Results, error) {
	var results Results
	if err := loadStrictJSON(path, &results); err != nil {
		return Results{}, err
	}
	presence, err := loadResultsAttemptPresence(path)
	if err != nil {
		return Results{}, err
	}
	if len(presence.Suites) != len(results.Suites) || len(presence.SuiteHistory) != len(results.SuiteHistory) {
		return Results{}, errors.New("results suite-attempt presence does not match decoded evidence")
	}
	for index := range results.Suites {
		if len(presence.Suites[index].Attempt) == 0 && results.Suites[index].Attempt == 0 {
			results.Suites[index].Attempt = 1
		}
	}
	for index := range results.SuiteHistory {
		if len(presence.SuiteHistory[index].Attempt) == 0 && results.SuiteHistory[index].Attempt == 0 {
			results.SuiteHistory[index].Attempt = 1
		}
	}
	if err := migrateLegacySuiteRetries(&results, presence.Suites); err != nil {
		return Results{}, err
	}
	normalizeResultsShape(&results)
	if err := results.validate(); err != nil {
		return Results{}, err
	}
	return results, nil
}

func loadResultsAttemptPresence(path string) (resultsAttemptPresence, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return resultsAttemptPresence{}, err
	}
	var presence resultsAttemptPresence
	if err := json.Unmarshal(payload, &presence); err != nil {
		return resultsAttemptPresence{}, fmt.Errorf("decode suite-attempt presence: %w", err)
	}
	return presence, nil
}

// migrateLegacySuiteRetries upgrades the original schema-v1 representation,
// where retries were appended to suites without attempt numbers. File order is
// the durable attempt order: every prior entry becomes history and the final
// entry remains active. Explicit-attempt duplicates are rejected as ambiguous.
func migrateLegacySuiteRetries(results *Results, presence []suiteAttemptPresence) error {
	type suiteKey struct{ stage, name string }
	counts := make(map[suiteKey]int, len(results.Suites))
	allMissing := make(map[suiteKey]bool, len(results.Suites))
	for index, suite := range results.Suites {
		key := suiteKey{stage: suite.Stage, name: suite.Name}
		if counts[key] == 0 {
			allMissing[key] = true
		}
		counts[key]++
		if len(presence[index].Attempt) != 0 {
			allMissing[key] = false
		}
	}
	for key, count := range counts {
		if count > 1 && !allMissing[key] {
			return fmt.Errorf("duplicate active suite %q in stage %q has explicit or mixed attempt evidence", key.name, key.stage)
		}
	}
	active := make([]SuiteResult, 0, len(counts))
	history := append([]SuiteResult(nil), results.SuiteHistory...)
	seen := make(map[suiteKey]int, len(counts))
	for _, suite := range results.Suites {
		key := suiteKey{stage: suite.Stage, name: suite.Name}
		if counts[key] == 1 {
			active = append(active, suite)
			continue
		}
		seen[key]++
		suite.Attempt = seen[key]
		if seen[key] == counts[key] {
			active = append(active, suite)
		} else {
			history = append(history, suite)
		}
	}
	results.Suites = active
	results.SuiteHistory = history
	return nil
}

// LoadTimeline strictly reads and validates a previously published timeline.
func LoadTimeline(path string) (Timeline, error) {
	var timeline Timeline
	if err := loadStrictJSON(path, &timeline); err != nil {
		return Timeline{}, err
	}
	if err := timeline.validate(); err != nil {
		return Timeline{}, err
	}
	return timeline, nil
}

func loadStrictJSON(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON artifact contains trailing data")
		}
		return err
	}
	return nil
}
