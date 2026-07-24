// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package goabi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/qrlclient"
)

func checkGraphQLEventLog(ctx context.Context, graphqlURL string, deployment *eventDeployment) error {
	blockNumber := deployment.receipt.BlockNumber.String()
	query := fmt.Sprintf(`{
		logs(filter:{fromBlock:"%s",toBlock:"%s",addresses:["%s"],topics:[["%s"]]}) {
			account { address }
			topics
			data
			transaction { hash }
		}
	}`, blockNumber, blockNumber, deployment.address.Hex(), deployment.topic.Hex())
	var response struct {
		Logs []struct {
			Account struct {
				Address string `json:"address"`
			} `json:"account"`
			Topics      []string `json:"topics"`
			Data        string   `json:"data"`
			Transaction struct {
				Hash string `json:"hash"`
			} `json:"transaction"`
		} `json:"logs"`
	}
	if err := postGraphQL(ctx, graphqlURL, query, &response); err != nil {
		return fmt.Errorf("graphql logs query: %w", err)
	}
	if len(response.Logs) != 1 {
		return fmt.Errorf("graphql logs length mismatch: have %d want 1", len(response.Logs))
	}
	log := response.Logs[0]
	if log.Account.Address != deployment.address.Hex() {
		return fmt.Errorf("graphql log address mismatch: have %s want %s", log.Account.Address, deployment.address.Hex())
	}
	if len(log.Topics) != 1 || log.Topics[0] != deployment.topic.Hex() {
		return fmt.Errorf("graphql log topics mismatch: have %v want %s", log.Topics, deployment.topic.Hex())
	}
	wantData := hexutil.Encode(deployment.receipt.Logs[0].Data)
	if log.Data != wantData {
		return fmt.Errorf("graphql log data mismatch: have %s want %s", log.Data, wantData)
	}
	if log.Transaction.Hash != deployment.tx.Hash().Hex() {
		return fmt.Errorf("graphql log tx hash mismatch: have %s want %s", log.Transaction.Hash, deployment.tx.Hash().Hex())
	}
	return nil
}

func checkGraphQLStorage(ctx context.Context, graphqlURL string, contract common.Address, block *big.Int, from common.Address, slot common.Hash, value [common.StorageValue64Length]byte) error {
	query := fmt.Sprintf(`{
		block(number:"%s") {
			account(address:"%s") {
				storage(slot:"%s")
			}
			call(data:{from:"%s",to:"%s",data:"0x"}) {
				data
				status
			}
		}
	}`, block.String(), contract.Hex(), slot.Hex(), from.Hex(), contract.Hex())
	var response struct {
		Block struct {
			Account struct {
				Storage string `json:"storage"`
			} `json:"account"`
			Call struct {
				Data   string `json:"data"`
				Status string `json:"status"`
			} `json:"call"`
		} `json:"block"`
	}
	if err := postGraphQL(ctx, graphqlURL, query, &response); err != nil {
		return fmt.Errorf("graphql storage query: %w", err)
	}
	want := hexutil.Encode(value[:])
	if response.Block.Account.Storage != want {
		return fmt.Errorf("graphql storage mismatch: have %s want %s", response.Block.Account.Storage, want)
	}
	if response.Block.Call.Status != "0x1" {
		return fmt.Errorf("graphql call status mismatch: have %s want 0x1", response.Block.Call.Status)
	}
	if response.Block.Call.Data != want {
		return fmt.Errorf("graphql call data mismatch: have %s want %s", response.Block.Call.Data, want)
	}
	return nil
}

func checkGraphQLSendRawTransaction(ctx context.Context, graphqlURL string, client *qrlclient.Client, w wallet.Wallet, from common.Address) error {
	signed, err := signDynamicFeeTx(ctx, client, w, from, &from, big.NewInt(0), nil)
	if err != nil {
		return fmt.Errorf("sign graphql raw transaction: %w", err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		return fmt.Errorf("marshal graphql raw transaction: %w", err)
	}
	query := fmt.Sprintf(`mutation {
		sendRawTransaction(data:"%s")
	}`, hexutil.Encode(raw))
	var response struct {
		SendRawTransaction string `json:"sendRawTransaction"`
	}
	if err := postGraphQL(ctx, graphqlURL, query, &response); err != nil {
		return fmt.Errorf("graphql sendRawTransaction mutation: %w", err)
	}
	if response.SendRawTransaction != signed.Hash().Hex() {
		return fmt.Errorf("graphql sendRawTransaction hash mismatch: have %s want %s", response.SendRawTransaction, signed.Hash().Hex())
	}
	_, err = waitTransaction(ctx, client, signed, types.ReceiptStatusSuccessful)
	return err
}

func postGraphQL(ctx context.Context, endpoint, query string, out any) error {
	body, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": nil,
	})
	if err != nil {
		return err
	}
	url := strings.TrimRight(endpoint, "/")
	if !strings.HasSuffix(url, "/graphql") {
		url += "/graphql"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %s: %s", resp.Status, responseBody)
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return err
	}
	if len(envelope.Errors) != 0 {
		return fmt.Errorf("graphql errors: %+v", envelope.Errors)
	}
	if len(envelope.Data) == 0 {
		return fmt.Errorf("graphql response missing data: %s", responseBody)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return err
	}
	return nil
}
