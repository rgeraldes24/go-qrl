// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package deposit

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseValidatorStatusVariants(t *testing.T) {
	for _, test := range []struct {
		body       string
		wantStatus string
		wantBlock  uint64
	}{
		{body: `{"status":"DEPOSITED","executionDepositBlockNumber":"42"}`, wantStatus: "DEPOSITED", wantBlock: 42},
		{body: `{"status":1,"execution_deposit_block_number":42}`, wantStatus: "DEPOSITED", wantBlock: 42},
		{body: `{"status":"UNKNOWN_STATUS"}`, wantStatus: "UNKNOWN_STATUS", wantBlock: 0},
	} {
		got, err := parseValidatorStatus([]byte(test.body))
		if err != nil {
			t.Fatal(err)
		}
		if got.status != test.wantStatus || got.executionDepositBlock != test.wantBlock {
			t.Fatalf("parseValidatorStatus(%s) = %+v, want %s/%d", test.body, got, test.wantStatus, test.wantBlock)
		}
	}
}

func TestBeaconDepositLifecycle(t *testing.T) {
	publicKey := []byte{1, 2, 3, 4}
	var requests [2]atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/qrl/v1alpha1/validator/status" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("public_key"); got != base64.StdEncoding.EncodeToString(publicKey) {
			t.Errorf("public_key = %q", got)
		}
		index := 0
		if r.URL.Host == "two" {
			index = 1
		}
		body := `{"status":"DEPOSITED","executionDepositBlockNumber":"77"}`
		if requests[index].Add(1) == 1 {
			body = `{"status":"UNKNOWN_STATUS"}`
		}
		return jsonResponse(body), nil
	})}
	endpoints := [2]string{"http://one", "http://two"}
	if err := requireUnknownValidator(t.Context(), client, endpoints, publicKey); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	statuses, err := waitForBeaconIngestion(ctx, client, endpoints, publicKey, 77, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if statuses[0].status != "DEPOSITED" || statuses[1].status != "DEPOSITED" {
		t.Fatalf("statuses = %+v", statuses)
	}
}

func TestBeaconDepositBlockMismatchIsFatal(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(`{"status":"DEPOSITED","executionDepositBlockNumber":"76"}`), nil
	})}
	_, err := waitForBeaconIngestion(t.Context(), client, [2]string{"http://one", "http://two"}, []byte{1}, 77, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "want 77") {
		t.Fatalf("block mismatch error = %v", err)
	}
}

func TestBeaconStateInclusionIsAcceptedAsStrongerEvidence(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "one" {
			return jsonResponse(`{"status":"PENDING","executionDepositBlockNumber":"77"}`), nil
		}
		return jsonResponse(`{"status":"ACTIVE"}`), nil
	})}
	statuses, err := waitForBeaconIngestion(t.Context(), client, [2]string{"http://one", "http://two"}, []byte{1}, 77, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if statuses[0].status != "PENDING" || statuses[1].status != "ACTIVE" {
		t.Fatalf("statuses = %+v", statuses)
	}
}

func TestBeaconIngestionRequiresStableRefetch(t *testing.T) {
	var requests [2]atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		index := 0
		if request.URL.Host == "two" {
			index = 1
		}
		call := requests[index].Add(1)
		if index == 0 && call == 2 {
			return jsonResponse(`{"status":"UNKNOWN_STATUS"}`), nil
		}
		return jsonResponse(`{"status":"DEPOSITED","executionDepositBlockNumber":"77"}`), nil
	})}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	statuses, err := waitForBeaconIngestion(ctx, client, [2]string{"http://one", "http://two"}, []byte{1}, 77, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if statuses[0].status != "DEPOSITED" || requests[0].Load() < 4 || requests[1].Load() < 3 {
		t.Fatalf("statuses/requests = %+v/%d/%d", statuses, requests[0].Load(), requests[1].Load())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
