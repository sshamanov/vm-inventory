package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// State represents the persistent application state (§20).
type State struct {
	SchemaVersion         int        `json:"schema_version"`
	LastConfluenceUpdate  *time.Time `json:"last_confluence_update"`
	LastConfluenceStatus  string     `json:"last_confluence_status"`
	LastSuccessfulRefresh *time.Time `json:"last_successful_prometheus_refresh"`
}

// Store provides atomic read/write access to persistent state.
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore creates a new state store at the given file path.
func NewStore(path string) *Store {
	return &Store{path: path}
}

// Load reads the state from disk. Returns an empty state if the file does not exist.
func (s *Store) Load() (*State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return &State{SchemaVersion: 1}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading state: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decoding state: %w", err)
	}

	if state.SchemaVersion == 0 {
		state.SchemaVersion = 1
	}

	return &state, nil
}

// Save writes the state atomically (§20: write temp, flush, rename).
func (s *Store) Save(state *State) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if state.SchemaVersion == 0 {
		state.SchemaVersion = 1
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding state: %w", err)
	}

	// Atomic write: temp file -> rename.
	dir := filepath.Dir(s.path)
	tmpFile, err := os.CreateTemp(dir, ".state-*.json")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("syncing temp file: %w", err)
	}
	tmpFile.Close()

	if err := os.Rename(tmpPath, s.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming temp file: %w", err)
	}

	return nil
}

// Update atomically reads, modifies, and writes the state.
// The function fn receives the current state and returns the modified version.
func (s *Store) Update(fn func(*State) (*State, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return err
	}

	updated, err := fn(state)
	if err != nil {
		return err
	}

	return s.saveLocked(updated)
}

func (s *Store) loadLocked() (*State, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return &State{SchemaVersion: 1}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading state: %w", err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decoding state: %w", err)
	}
	if state.SchemaVersion == 0 {
		state.SchemaVersion = 1
	}
	return &state, nil
}

func (s *Store) saveLocked(state *State) error {
	if state.SchemaVersion == 0 {
		state.SchemaVersion = 1
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding state: %w", err)
	}
	dir := filepath.Dir(s.path)
	tmpFile, err := os.CreateTemp(dir, ".state-*.json")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("syncing temp file: %w", err)
	}
	tmpFile.Close()
	if err := os.Rename(tmpPath, s.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}
