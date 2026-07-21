// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// GraphQL is a bounded GraphQL-over-HTTP client.
type GraphQL struct {
	http *HTTP
}

type GraphQLError struct {
	Message    string         `json:"message"`
	Path       []any          `json:"path,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

type GraphQLResponseError struct {
	Errors []GraphQLError
}

func (e *GraphQLResponseError) Error() string {
	if len(e.Errors) == 0 {
		return "GraphQL request failed"
	}
	return fmt.Sprintf("GraphQL request failed: %s", e.Errors[0].Message)
}

func NewGraphQL(endpoint string, options HTTPOptions) (*GraphQL, error) {
	transport, err := NewHTTP(endpoint, options)
	if err != nil {
		return nil, err
	}
	return &GraphQL{http: transport}, nil
}

func NewGraphQLClient(transport *HTTP) *GraphQL {
	return &GraphQL{http: transport}
}

// Query submits exactly once. Query text and variables are intentionally not
// included in returned errors because they may contain sensitive test data.
func (c *GraphQL) Query(ctx context.Context, query string, variables map[string]any, out any) error {
	if c == nil || c.http == nil {
		return errors.New("GraphQL client is nil")
	}
	if query == "" {
		return errors.New("GraphQL query is required")
	}
	request := struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables,omitempty"`
	}{Query: query, Variables: variables}
	var response struct {
		Data   json.RawMessage `json:"data"`
		Errors []GraphQLError  `json:"errors"`
	}
	if err := c.http.PostJSON(ctx, "", request, &response); err != nil {
		return err
	}
	if len(response.Errors) != 0 {
		return &GraphQLResponseError{Errors: response.Errors}
	}
	if len(response.Data) == 0 {
		return errors.New("GraphQL response has neither data nor errors")
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(response.Data, out); err != nil {
		return fmt.Errorf("decode GraphQL data: %w", err)
	}
	return nil
}
