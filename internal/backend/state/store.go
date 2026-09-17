// Package state holds the backend's small runtime state: when the inventory was
// last refreshed and how the last publication ended.
//
// Nothing here is written to disk (§20). The container carries no state across
// restarts, so a replaced or duplicated backend is indistinguishable from the
// original, and what the last publication actually contained is recorded on the
// Confluence page itself (§19.4).
package state

import (
	"sync"
	"time"
)

// State is the process-local record of the last refresh and publication.
type State struct {
	LastConfluenceUpdate  *time.Time
	LastConfluenceStatus  string
	LastSuccessfulRefresh *time.Time
}

// Store guards the state for the lifetime of the process.
type Store struct {
	mu sync.Mutex
	st State
}

// NewStore creates an empty store.
func NewStore() *Store {
	return &Store{}
}

// Load returns a copy of the current state. The caller cannot reach the stored
// value through it, so a reader never sees a half-applied update.
func (s *Store) Load() *State {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.st
	return &state
}

// Update atomically replaces the state with the function's result. The function
// receives a copy for the same reason Load returns one.
func (s *Store) Update(fn func(*State) (*State, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	current := s.st
	updated, err := fn(&current)
	if err != nil {
		return err
	}
	if updated != nil {
		s.st = *updated
	}
	return nil
}
