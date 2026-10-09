package state

import (
	"testing"
	"time"
)

func TestStoreStartsEmpty(t *testing.T) {
	st := NewStore().Load()
	if st.LastSuccessfulRefresh != nil || st.LastConfluenceUpdate != nil || st.LastConfluenceStatus != "" {
		t.Errorf("new store is not empty: %+v", st)
	}
}

func TestStoreUpdateIsVisibleToLaterLoads(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.Update(func(st *State) (*State, error) {
		st.LastSuccessfulRefresh = &now
		st.LastConfluenceStatus = "unchanged"
		return st, nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got := store.Load()
	if got.LastSuccessfulRefresh == nil || !got.LastSuccessfulRefresh.Equal(now) {
		t.Errorf("LastSuccessfulRefresh = %v, want %v", got.LastSuccessfulRefresh, now)
	}
	if got.LastConfluenceStatus != "unchanged" {
		t.Errorf("LastConfluenceStatus = %q, want %q", got.LastConfluenceStatus, "unchanged")
	}
}

// A reader holding a previously loaded copy must not observe later updates.
func TestStoreLoadReturnsACopy(t *testing.T) {
	store := NewStore()

	before := store.Load()
	before.LastConfluenceStatus = "tampered"

	if err := store.Update(func(st *State) (*State, error) {
		st.LastConfluenceStatus = "published"
		return st, nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if got := store.Load().LastConfluenceStatus; got != "published" {
		t.Errorf("stored status = %q, want %q", got, "published")
	}
}
