// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package report

import (
	"encoding/xml"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type junitSuites struct {
	XMLName   xml.Name     `xml:"testsuites"`
	Name      string       `xml:"name,attr"`
	Tests     int          `xml:"tests,attr"`
	Failures  int          `xml:"failures,attr"`
	Errors    int          `xml:"errors,attr"`
	Skipped   int          `xml:"skipped,attr"`
	Time      string       `xml:"time,attr"`
	Timestamp string       `xml:"timestamp,attr,omitempty"`
	Suites    []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name      string      `xml:"name,attr"`
	Tests     int         `xml:"tests,attr"`
	Failures  int         `xml:"failures,attr"`
	Errors    int         `xml:"errors,attr"`
	Skipped   int         `xml:"skipped,attr"`
	Time      string      `xml:"time,attr"`
	Timestamp string      `xml:"timestamp,attr,omitempty"`
	Cases     []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name           string        `xml:"name,attr"`
	Classname      string        `xml:"classname,attr"`
	Time           string        `xml:"time,attr"`
	Failure        *junitIssue   `xml:"failure,omitempty"`
	Error          *junitIssue   `xml:"error,omitempty"`
	Skipped        *junitSkipped `xml:"skipped,omitempty"`
	SystemOut      string        `xml:"system-out,omitempty"`
	durationMillis int64
}

type junitIssue struct {
	Type    string `xml:"type,attr,omitempty"`
	Message string `xml:"message,attr,omitempty"`
	Body    string `xml:",chardata"`
}

type junitSkipped struct {
	Message string `xml:"message,attr,omitempty"`
}

// WriteJUnit emits one testsuite for lifecycle stages and one for behavioral
// suites. Failure categories remain visible through the JUnit issue type.
func (writer *Writer) WriteJUnit(results Results) error {
	if results.Schema == 0 {
		results.Schema = SchemaVersion
	}
	normalizeResults(&results)
	if err := results.validate(); err != nil {
		return err
	}
	document := buildJUnit(results)
	payload, err := xml.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JUnit: %w", err)
	}
	payload = append([]byte(xml.Header), append(payload, '\n')...)
	return writer.writeBytes(JUnitFilename, payload, 0o644)
}

func buildJUnit(results Results) junitSuites {
	stageSuite := junitSuite{Name: "stages", Cases: make([]junitCase, 0, len(results.Stages))}
	for _, result := range results.Stages {
		stageSuite.Cases = append(stageSuite.Cases, junitCaseForResult(
			result.Name, "vm64e2e.stage", result.Status, result.DurationMillis,
			result.FailureCategory, result.Message, result.Details, result.LogPath,
		))
	}
	suiteSuite := junitSuite{Name: "suites", Cases: make([]junitCase, 0, len(results.Suites))}
	for _, result := range results.Suites {
		class := "vm64e2e.suite"
		if result.Stage != "" {
			class += "." + result.Stage
		}
		suiteSuite.Cases = append(suiteSuite.Cases, junitCaseForResult(
			result.Name, class, result.Status, result.DurationMillis,
			result.FailureCategory, result.Message, result.Details, result.LogPath,
		))
	}
	historySuite := junitSuite{Name: "suite-history", Cases: make([]junitCase, 0, len(results.SuiteHistory))}
	for _, result := range results.SuiteHistory {
		historySuite.Cases = append(historySuite.Cases, junitCaseForHistory(result))
	}
	started := results.StartedAt.UTC()
	stageSuite.Timestamp = timestamp(started)
	suiteSuite.Timestamp = timestamp(started)
	historySuite.Timestamp = timestamp(started)
	populateJUnitCounts(&stageSuite)
	populateJUnitCounts(&suiteSuite)
	populateJUnitCounts(&historySuite)
	suites := []junitSuite{stageSuite, suiteSuite}
	if len(historySuite.Cases) != 0 {
		suites = append(suites, historySuite)
	}
	document := junitSuites{
		Name:      "vm64e2e",
		Time:      junitSeconds(results.DurationMillis),
		Timestamp: timestamp(started),
		Suites:    suites,
	}
	for _, suite := range document.Suites {
		document.Tests += suite.Tests
		document.Failures += suite.Failures
		document.Errors += suite.Errors
		document.Skipped += suite.Skipped
	}
	return document
}

// junitCaseForHistory retains the complete prior-attempt evidence while
// making it explicitly non-gating. A historical failure must never turn a
// successfully resumed lifecycle red in JUnit consumers.
func junitCaseForHistory(result SuiteResult) junitCase {
	class := "vm64e2e.suite-history"
	if result.Stage != "" {
		class += "." + result.Stage
	}
	name := fmt.Sprintf("%s [attempt %d]", result.Name, result.Attempt)
	testCase := junitCase{
		Name: name, Classname: class, Time: junitSeconds(result.DurationMillis),
		durationMillis: result.DurationMillis,
		Skipped:        &junitSkipped{Message: fmt.Sprintf("historical %s attempt; superseded by resume", result.Status)},
	}
	lines := []string{
		"historical status: " + string(result.Status),
		"historical attempt: " + strconv.Itoa(result.Attempt),
	}
	if result.FailureCategory != "" {
		lines = append(lines, "historical failure category: "+string(result.FailureCategory))
	}
	if result.Message != "" {
		lines = append(lines, "historical message: "+result.Message)
	}
	if result.Details != "" {
		lines = append(lines, "historical details: "+result.Details)
	}
	if result.LogPath != "" {
		lines = append(lines, "artifact log: "+filepath.ToSlash(result.LogPath))
	}
	testCase.SystemOut = strings.Join(lines, "\n")
	return testCase
}

func junitCaseForResult(name, class string, status Status, duration int64, category FailureCategory, message, details, logPath string) junitCase {
	testCase := junitCase{Name: name, Classname: class, Time: junitSeconds(duration), durationMillis: duration}
	if logPath != "" {
		testCase.SystemOut = "artifact log: " + filepath.ToSlash(logPath)
	}
	body := details
	if body == "" {
		body = message
	}
	issue := &junitIssue{Type: string(category), Message: message, Body: body}
	switch status {
	case StatusFailed:
		if category == FailureAssertion {
			testCase.Failure = issue
		} else {
			testCase.Error = issue
		}
	case StatusCanceled:
		testCase.Error = issue
	case StatusSkipped:
		testCase.Skipped = &junitSkipped{Message: message}
	case StatusPending, StatusRunning:
		testCase.Skipped = &junitSkipped{Message: "not completed: " + string(status)}
	}
	return testCase
}

func populateJUnitCounts(suite *junitSuite) {
	for _, testCase := range suite.Cases {
		suite.Tests++
		if testCase.Failure != nil {
			suite.Failures++
		}
		if testCase.Error != nil {
			suite.Errors++
		}
		if testCase.Skipped != nil {
			suite.Skipped++
		}
	}
	var milliseconds int64
	for _, testCase := range suite.Cases {
		milliseconds += testCase.durationMillis
	}
	suite.Time = junitSeconds(milliseconds)
}

func junitSeconds(milliseconds int64) string {
	if milliseconds < 0 {
		milliseconds = 0
	}
	return strconv.FormatFloat(float64(milliseconds)/1000, 'f', 3, 64)
}

func timestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
