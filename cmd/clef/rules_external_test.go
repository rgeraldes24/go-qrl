// Copyright 2026 The go-qrl Authors
// This file is part of go-qrl.
//
// go-qrl is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testSeedHex         = "0100003bf5fbadaa64ce2974d2464dc17fdac669557f05bd5329c35aa84e85636d0565f5cb4d3427d6033ccabf0b6c3f157bb5"
	testAccountPassword = "myverylongpassword"
	testMasterPassword  = "myverylongmasterpassword"
)

func TestExternalAPIRulesApproveAndRejectSignData(t *testing.T) {
	keystore := t.TempDir()
	configDir := t.TempDir()
	workdir := t.TempDir()
	keyPath := filepath.Join(workdir, "seed.hex")
	passwordPath := filepath.Join(workdir, "password.txt")
	rulesPath := filepath.Join(workdir, "rules.js")

	mustWriteFile(t, keyPath, []byte(testSeedHex), 0600)
	mustWriteFile(t, passwordPath, []byte(testAccountPassword+"\n"), 0600)

	rulesJS := `
function ApproveListing() {
	return "Approve";
}

function ApproveSignData(r) {
	if (r.address.toLowerCase() != "` + strings.ToLower(testAddress) + `") {
		return "Reject";
	}
	if (r.messages[0].value.indexOf("allow") >= 0) {
		return "Approve";
	}
	return "Reject";
}
`
	mustWriteFile(t, rulesPath, []byte(rulesJS), 0600)

	init := runWithKeystore(t, keystore, "--suppress-bootwarn", "--configdir", configDir, "--lightkdf", "init")
	init.input(testMasterPassword).input(testMasterPassword)
	waitClefOK(t, init)
	waitClefOK(t, runWithKeystore(t, keystore, "--suppress-bootwarn", "--configdir", configDir, "--lightkdf", "importraw", "--password", passwordPath, keyPath))

	rulesHash := sha256.Sum256([]byte(rulesJS))
	attest := runWithKeystore(t, keystore, "--suppress-bootwarn", "--configdir", configDir, "attest", hex.EncodeToString(rulesHash[:]))
	attest.input(testMasterPassword)
	waitClefOK(t, attest)

	setpw := runWithKeystore(t, keystore, "--suppress-bootwarn", "--configdir", configDir, "setpw", testAddress)
	setpw.input(testAccountPassword).input(testAccountPassword).input(testMasterPassword)
	waitClefOK(t, setpw)

	endpoint, stop := startClefSigner(t, keystore, configDir, rulesPath)
	defer stop()

	var accounts []string
	mustClefRPC(t, endpoint, "account_list", []any{}, &accounts)
	if len(accounts) != 1 || !strings.EqualFold(accounts[0], testAddress) {
		t.Fatalf("unexpected account list: %#v", accounts)
	}

	var signature string
	mustClefRPC(t, endpoint, "account_signData", []any{"text/plain", testAddress, "0x" + hex.EncodeToString([]byte("allow this message"))}, &signature)
	if !strings.HasPrefix(signature, "0x") || len(signature) <= 2 {
		t.Fatalf("unexpected signature result %q", signature)
	}

	if err := clefRPCError(t, endpoint, "account_signData", []any{"text/plain", testAddress, "0x" + hex.EncodeToString([]byte("deny this message"))}); err == nil {
		t.Fatal("expected rule-based signData rejection")
	} else if !strings.Contains(strings.ToLower(err.Error()), "denied") {
		t.Fatalf("unexpected rejection error: %v", err)
	}
}

func waitClefOK(t *testing.T, proc *testproc) {
	t.Helper()
	proc.WaitExit()
	if proc.ExitStatus() != 0 {
		t.Fatalf("clef exited with status %d\nstdout:\n%s\nstderr:\n%s", proc.ExitStatus(), proc.Output(), proc.StderrText())
	}
}

func startClefSigner(t *testing.T, keystore, configDir, rulesPath string) (string, func()) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	proc := runWithKeystore(
		t,
		keystore,
		"--suppress-bootwarn",
		"--configdir", configDir,
		"--chainid", "1337",
		"--lightkdf",
		"--http",
		"--http.addr", "127.0.0.1",
		"--http.port", fmt.Sprintf("%d", port),
		"--http.vhosts", "*",
		"--ipcdisable",
		"--auditlog", "",
		"--rules", rulesPath,
	)
	proc.input(testMasterPassword)

	endpoint := fmt.Sprintf("http://127.0.0.1:%d/", port)
	deadline := time.Now().Add(10 * time.Second)
	for {
		var version string
		if err := clefRPC(t, endpoint, "account_version", []any{}, &version); err == nil {
			return endpoint, func() {
				proc.Interrupt()
				proc.WaitExit()
			}
		}
		if time.Now().After(deadline) {
			proc.Kill()
			t.Fatalf("clef HTTP endpoint did not open\nstderr:\n%s", proc.StderrText())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func mustWriteFile(t *testing.T, path string, content []byte, perm os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, content, perm); err != nil {
		t.Fatal(err)
	}
}

func mustClefRPC(t *testing.T, endpoint, method string, params any, result any) {
	t.Helper()
	if err := clefRPC(t, endpoint, method, params, result); err != nil {
		t.Fatalf("%s failed: %v", method, err)
	}
}

func clefRPCError(t *testing.T, endpoint, method string, params any) error {
	t.Helper()
	var ignored json.RawMessage
	return clefRPC(t, endpoint, method, params, &ignored)
}

func clefRPC(t *testing.T, endpoint, method string, params any, result any) error {
	t.Helper()

	requestBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}
	client := http.Client{Timeout: 5 * time.Second}
	response, err := client.Post(endpoint, "application/json", bytes.NewReader(requestBody))
	if err != nil {
		return err
	}
	defer response.Body.Close()

	var rpcResponse struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&rpcResponse); err != nil {
		return err
	}
	if rpcResponse.Error != nil {
		return fmt.Errorf("rpc error %d: %s", rpcResponse.Error.Code, rpcResponse.Error.Message)
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(rpcResponse.Result, result)
}
