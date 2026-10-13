package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadTestConfig(t *testing.T, content string) (gqrlConfig, error) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(file, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	var cfg gqrlConfig
	err := loadConfig(file, &cfg)
	return cfg, err
}

func TestLoadConfigTransactionHistory(t *testing.T) {
	cfg, err := loadTestConfig(t, "[QRL]\nTransactionHistory = 1000\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.QRL.TransactionHistory != 1000 {
		t.Fatalf("TransactionHistory: got %d, want 1000", cfg.QRL.TransactionHistory)
	}
}

// TxLookupLimit was replaced by TransactionHistory and is rejected like any
// other unknown field.
func TestLoadConfigRejectsTxLookupLimit(t *testing.T) {
	_, err := loadTestConfig(t, "[QRL]\nTxLookupLimit = 1000\n")
	if err == nil || !strings.Contains(err.Error(), "TxLookupLimit") {
		t.Fatalf("expected unknown field error for TxLookupLimit, got %v", err)
	}
}
