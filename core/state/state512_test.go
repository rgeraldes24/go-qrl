package state

import (
	"bytes"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/rawdb"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/trie"
	"github.com/theQRL/go-qrl/trie/triedb/pathdb"
)

func TestState512PersistenceAndProofs(t *testing.T) {
	for _, scheme := range []string{rawdb.HashScheme, rawdb.PathScheme} {
		t.Run(scheme, func(t *testing.T) {
			disk := rawdb.NewMemoryDatabase()
			config := &trie.Config{Preimages: true}
			if scheme == rawdb.PathScheme {
				config.PathDB = pathdb.Defaults
			}
			tdb := trie.NewDatabase(disk, config)
			defer tdb.Close()
			db := NewDatabaseWithNodeDB(disk, tdb)
			s, err := New(types.EmptyRootHash, db, nil)
			if err != nil {
				t.Fatal(err)
			}
			addresses := []common.Address{{63: 1}, {0: 0x80, 63: 1}}
			keys := []common.StorageKey{{63: 5}, {31: 1, 63: 5}, {0: 0x80, 63: 5}}
			for i, addr := range addresses {
				s.SetCode(addr, []byte{byte(i + 1)})
				for j, key := range keys {
					s.SetState(addr, key, common.StorageValue64{0: byte(i + 1), 63: byte(j + 1)})
				}
			}
			root, err := s.Commit(1, false)
			if err != nil {
				t.Fatal(err)
			}
			if err := tdb.Commit(root, false); err != nil {
				t.Fatal(err)
			}
			s, err = New(root, db, nil)
			if err != nil {
				t.Fatal(err)
			}
			accounts, err := trie.NewStateTrie(trie.StateTrieID(root), tdb)
			if err != nil {
				t.Fatal(err)
			}
			for i, addr := range addresses {
				code := []byte{byte(i + 1)}
				if !bytes.Equal(s.GetCode(addr), code) || s.GetCodeHash(addr) != crypto.Keccak512Hash(code) {
					t.Fatal("code or its 512-bit commitment changed")
				}
				accountPath := crypto.Keccak512(addr[:])
				proof := rawdb.NewMemoryDatabase()
				if err := accounts.Prove(accountPath, proof); err != nil {
					t.Fatal(err)
				}
				if blob, err := trie.VerifyProof(root, accountPath, proof); err != nil || len(blob) == 0 {
					t.Fatalf("account proof: %v", err)
				}
				storageRoot := s.GetStorageRoot(addr)
				storage, err := trie.NewStateTrie(trie.StorageTrieID(root, crypto.Keccak512Hash(addr[:]), storageRoot), tdb)
				if err != nil {
					t.Fatal(err)
				}
				for j, key := range keys {
					want := common.StorageValue64{0: byte(i + 1), 63: byte(j + 1)}
					if got := s.GetState(addr, key); got != want {
						t.Fatalf("account %d slot %d: %x, want %x", i, j, got, want)
					}
					proof := rawdb.NewMemoryDatabase()
					path := crypto.Keccak512(key[:])
					if err := storage.Prove(path, proof); err != nil {
						t.Fatal(err)
					}
					if blob, err := trie.VerifyProof(storageRoot, path, proof); err != nil || len(blob) == 0 {
						t.Fatalf("storage proof: %v", err)
					}
				}
			}
		})
	}
}
