package lifecycle

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

type Store struct {
	Path       string
	StageOrder []string
	Now        func() time.Time
}

func (s Store) Load() (Checkpoint, error) {
	var state Checkpoint
	if err := readJSON(s.Path, &state); err != nil {
		return Checkpoint{}, err
	}
	if state.hasLegacyPythonHistory() && (len(s.StageOrder) == 0 || s.StageOrder[0] != "fixture") {
		return Checkpoint{}, fmt.Errorf("checkpoint %s contains legacy Python stage history; resume it with scripts/testing/e2e/run_e2e_from_scratch.sh -r %s rather than reinterpreting completed stages", s.Path, s.Path)
	}
	if err := state.Validate(s.StageOrder); err != nil {
		return Checkpoint{}, fmt.Errorf("validate checkpoint %s: %w", s.Path, err)
	}
	return state, nil
}

func (state Checkpoint) hasLegacyPythonHistory() bool {
	if len(state.Completed) > 0 {
		return state.Completed[0] == "fixture"
	}
	if len(state.Attempts) > 0 {
		return state.Attempts[0].Stage == "fixture"
	}
	return false
}

func (s Store) Create(state Checkpoint) error {
	if err := state.Validate(s.StageOrder); err != nil {
		return err
	}
	return writeJSONNew(s.Path, state)
}

func (s Store) Save(state Checkpoint) error {
	if err := state.Validate(s.StageOrder); err != nil {
		return err
	}
	return writeJSONAtomic(s.Path, state)
}

func (s Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

type lockOwner struct {
	Host      string    `json:"host"`
	PID       int       `json:"pid"`
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"created_at"`
}

type Lock struct {
	path   string
	owner  lockOwner
	closed bool
}

func (s Store) Acquire(allowStale bool) (*Lock, error) {
	path := s.Path + ".lock"
	recoveryPath := path + ".recovery"
	host, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("identify checkpoint lock host: %w", err)
	}
	tokenBytes := make([]byte, 24)
	if _, err := io.ReadFull(rand.Reader, tokenBytes); err != nil {
		return nil, fmt.Errorf("generate checkpoint lock token: %w", err)
	}
	owner := lockOwner{Host: host, PID: os.Getpid(), Token: hex.EncodeToString(tokenBytes), CreatedAt: s.now()}
	if err := reconcileInterruptedLockRecovery(path, recoveryPath, host, allowStale); err != nil {
		return nil, err
	}
	if err := writeJSONNew(path, owner); err == nil {
		return &Lock{path: path, owner: owner}, nil
	} else if !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("publish checkpoint lock owner: %w", err)
	}
	if !allowStale {
		return nil, fmt.Errorf("checkpoint is already locked: %s", s.Path)
	}
	var existing lockOwner
	if err := readJSON(path, &existing); err != nil {
		return nil, fmt.Errorf("checkpoint lock owner is unverifiable: %w", err)
	}
	if !validLockOwner(existing) || existing.Host != host || processAlive(existing.PID) {
		return nil, fmt.Errorf("checkpoint lock is live, remote, or unverifiable: %s", s.Path)
	}
	if err := replaceStaleLock(path, recoveryPath, existing, owner); err != nil {
		return nil, err
	}
	return &Lock{path: path, owner: owner}, nil
}

func (lock *Lock) Close() error {
	if lock == nil || lock.closed {
		return nil
	}
	var current lockOwner
	if err := readJSON(lock.path, &current); err != nil {
		return fmt.Errorf("read checkpoint lock during release: %w", err)
	}
	if !sameLockOwner(current, lock.owner) {
		return errors.New("refusing to release checkpoint lock after ownership changed")
	}
	if err := os.Remove(lock.path); err != nil {
		return fmt.Errorf("remove checkpoint lock: %w", err)
	}
	if err := syncDirectory(filepath.Dir(lock.path)); err != nil {
		return fmt.Errorf("sync checkpoint lock release: %w", err)
	}
	lock.closed = true
	return nil
}

func validLockOwner(owner lockOwner) bool {
	if owner.Host == "" || owner.PID < 1 || owner.CreatedAt.IsZero() || len(owner.Token) != 48 {
		return false
	}
	_, err := hex.DecodeString(owner.Token)
	return err == nil
}

func sameLockOwner(left, right lockOwner) bool {
	return left.Host == right.Host && left.PID == right.PID && left.Token == right.Token && left.CreatedAt.Equal(right.CreatedAt)
}

// replaceStaleLock uses a hard-link recovery claim so an interruption at every
// replacement boundary leaves at least one complete owner record. Cooperating
// acquirers refuse to publish while the fixed recovery claim exists.
func replaceStaleLock(path, recoveryPath string, existing, replacement lockOwner) error {
	if err := os.Link(path, recoveryPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("checkpoint lock recovery is already in progress")
		}
		return fmt.Errorf("claim stale checkpoint lock: %w", err)
	}
	claimed := true
	defer func() {
		if claimed {
			_ = os.Remove(recoveryPath)
		}
	}()
	var claimedOwner lockOwner
	if err := readJSON(recoveryPath, &claimedOwner); err != nil || !sameLockOwner(claimedOwner, existing) {
		return errors.Join(errors.New("stale checkpoint lock changed while it was claimed"), err)
	}
	pathInfo, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect claimed checkpoint lock: %w", err)
	}
	claimInfo, err := os.Stat(recoveryPath)
	if err != nil {
		return fmt.Errorf("inspect checkpoint lock recovery claim: %w", err)
	}
	if !os.SameFile(pathInfo, claimInfo) {
		return errors.New("checkpoint lock changed before stale-owner replacement")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale checkpoint lock: %w", err)
	}
	if err := writeJSONNew(path, replacement); err != nil {
		if restoreErr := os.Link(recoveryPath, path); restoreErr != nil && !errors.Is(restoreErr, os.ErrExist) {
			err = errors.Join(err, fmt.Errorf("restore stale checkpoint lock after replacement failure: %w", restoreErr))
		}
		return fmt.Errorf("publish replacement checkpoint lock: %w", err)
	}
	if err := os.Remove(recoveryPath); err != nil {
		return fmt.Errorf("remove checkpoint lock recovery claim: %w", err)
	}
	claimed = false
	return syncDirectory(filepath.Dir(path))
}

// reconcileInterruptedLockRecovery repairs only complete, stale recovery
// claims created by replaceStaleLock. A malformed claim remains fail-closed.
func reconcileInterruptedLockRecovery(path, recoveryPath, host string, allowStale bool) error {
	var recovery lockOwner
	if err := readJSON(recoveryPath, &recovery); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("checkpoint lock recovery claim is unverifiable: %w", err)
	}
	if !allowStale {
		return errors.New("checkpoint lock recovery is already in progress")
	}
	if !validLockOwner(recovery) || recovery.Host != host || processAlive(recovery.PID) {
		return errors.New("checkpoint lock recovery claim is live, remote, or unverifiable")
	}
	pathInfo, pathErr := os.Stat(path)
	claimInfo, claimErr := os.Stat(recoveryPath)
	if claimErr != nil {
		if errors.Is(claimErr, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect checkpoint lock recovery claim: %w", claimErr)
	}
	if errors.Is(pathErr, os.ErrNotExist) {
		if err := os.Link(recoveryPath, path); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("restore checkpoint lock from recovery claim: %w", err)
		}
		pathInfo, pathErr = os.Stat(path)
	}
	if pathErr != nil {
		return fmt.Errorf("inspect checkpoint lock during recovery: %w", pathErr)
	}
	if os.SameFile(pathInfo, claimInfo) {
		if err := os.Remove(recoveryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("retire restored checkpoint lock recovery claim: %w", err)
		}
		return syncDirectory(filepath.Dir(path))
	}
	// Replacement was published before the previous process was interrupted.
	// The stale claim no longer protects the current lock inode and can be
	// retired without changing the current owner record.
	if err := os.Remove(recoveryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("retire completed checkpoint lock recovery claim: %w", err)
	}
	return syncDirectory(filepath.Dir(path))
}

func processAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

func NewOwnership(path, runID, requestedName string, now time.Time) (OwnershipRecord, error) {
	record := OwnershipRecord{Schema: SchemaVersion, RunID: runID, RequestedName: requestedName, CreatedAt: now.UTC()}
	if err := record.Validate(); err != nil {
		return OwnershipRecord{}, err
	}
	if err := writeJSONNew(path, record); err != nil {
		return OwnershipRecord{}, err
	}
	return record, nil
}

func LoadOwnership(path string) (OwnershipRecord, error) {
	var record OwnershipRecord
	if err := readJSON(path, &record); err != nil {
		return OwnershipRecord{}, err
	}
	if err := record.Validate(); err != nil {
		return OwnershipRecord{}, err
	}
	return record, nil
}

func CaptureOwnershipUUID(path, expectedName, uuid string) (OwnershipRecord, error) {
	record, err := LoadOwnership(path)
	if err != nil {
		return OwnershipRecord{}, err
	}
	if record.RequestedName != expectedName || record.UUID != nil {
		return OwnershipRecord{}, errors.New("ownership intent changed or already captured")
	}
	if !uuidPattern.MatchString(uuid) {
		return OwnershipRecord{}, fmt.Errorf("refusing invalid enclave UUID %q", uuid)
	}
	record.UUID = &uuid
	if err := writeJSONAtomic(path, record); err != nil {
		return OwnershipRecord{}, err
	}
	return record, nil
}

func MarkOwnershipDestroyed(path, uuid string, now time.Time) error {
	record, err := LoadOwnership(path)
	if err != nil {
		return err
	}
	if record.UUID == nil || *record.UUID != uuid {
		return errors.New("ownership UUID does not match cleanup UUID")
	}
	when := now.UTC()
	if when.IsZero() || when.Before(record.CreatedAt) || record.DestroyRequestedAt != nil && when.Before(*record.DestroyRequestedAt) {
		return errors.New("ownership destruction time is invalid")
	}
	record.DestroyedAt = &when
	record.Preserved = false
	record.PreserveReason = ""
	return writeJSONAtomic(path, record)
}

// MarkOwnershipDestroyRequested journals the exact full-UUID destructive
// intent before the external Kurtosis call. If the process loses the destroy
// response, a later finalizer can safely retry that same request and reconcile
// an already-absent enclave without widening cleanup scope.
func MarkOwnershipDestroyRequested(path, uuid string, now time.Time) error {
	record, err := LoadOwnership(path)
	if err != nil {
		return err
	}
	if record.UUID == nil || *record.UUID != uuid {
		return errors.New("ownership UUID does not match cleanup UUID")
	}
	if record.DestroyedAt != nil {
		return errors.New("ownership is already recorded as destroyed")
	}
	if record.DestroyRequestedAt != nil {
		return nil
	}
	when := now.UTC()
	if when.IsZero() || when.Before(record.CreatedAt) {
		return errors.New("ownership destruction request time is invalid")
	}
	record.DestroyRequestedAt = &when
	record.Preserved = false
	record.PreserveReason = ""
	return writeJSONAtomic(path, record)
}

func MarkOwnershipPreserved(path, reason string) error {
	record, err := LoadOwnership(path)
	if err != nil {
		return err
	}
	record.Preserved = true
	record.PreserveReason = reason
	return writeJSONAtomic(path, record)
}

func readJSON(path string, out any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("JSON file contains trailing data")
	}
	return nil
}

func writeJSONNew(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := writeJSONTemp(path, value)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err := os.Link(temporary, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("refusing to replace existing file %s: %w", path, os.ErrExist)
		}
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := writeJSONTemp(path, value)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func writeJSONTemp(path string, value any) (string, error) {
	file, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return "", err
	}
	temporary := file.Name()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		file.Close()
		os.Remove(temporary)
		return "", err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		os.Remove(temporary)
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(temporary)
		return "", err
	}
	return temporary, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func parseExitCode(err error) int {
	type exitCoder interface{ ExitCode() int }
	var coded exitCoder
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return 1
}

func pointerTo(value int) *int { return &value }

func parsePID(raw string) (int, error) { return strconv.Atoi(raw) }
