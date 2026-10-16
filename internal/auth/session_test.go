package auth

import (
	"sync"
	"testing"
	"time"
)

func TestMemorySessionStore_Lifecycle(t *testing.T) {
	store := NewMemorySessionStore(1 * time.Hour)

	token, err := store.Create(1, "admin")
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}
	if len(token) != 64 {
		t.Fatalf("expected 64-char token, got len=%d", len(token))
	}

	item, ok := store.Get(token)
	if !ok {
		t.Fatal("expected session to be found")
	}
	if item.UserID != 1 || item.Username != "admin" {
		t.Fatalf("unexpected session item: %+v", item)
	}

	// Delete
	store.Delete(token)
	_, ok = store.Get(token)
	if ok {
		t.Fatal("expected session to be deleted")
	}
}

func TestMemorySessionStore_Expiration(t *testing.T) {
	fakeNow := time.Now().UTC()
	store := NewMemorySessionStore(10 * time.Minute)
	store.nowFunc = func() time.Time { return fakeNow }

	token, err := store.Create(1, "admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Still valid
	_, ok := store.Get(token)
	if !ok {
		t.Fatal("expected session to be valid")
	}

	// Fast forward time past TTL
	fakeNow = fakeNow.Add(15 * time.Minute)

	_, ok = store.Get(token)
	if ok {
		t.Fatal("expected session to be expired and removed")
	}
}

func TestMemorySessionStore_SlidingExpiration(t *testing.T) {
	fakeNow := time.Now().UTC()
	store := NewMemorySessionStore(10 * time.Minute)
	store.nowFunc = func() time.Time { return fakeNow }

	token, err := store.Create(1, "admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Advance time past half of TTL (e.g. +6m, initial expiration is at +10m)
	fakeNow = fakeNow.Add(6 * time.Minute)

	// Access session: this should refresh expiration to fakeNow + 10m (+16m from start)
	item, ok := store.Get(token)
	if !ok {
		t.Fatal("expected session to be valid and refreshed")
	}
	expectedExpiry := fakeNow.Add(10 * time.Minute)
	if !item.ExpiresAt.Equal(expectedExpiry) {
		t.Fatalf("expected ExpiresAt to be %v, got %v", expectedExpiry, item.ExpiresAt)
	}

	// Advance time to +12m from start (past the initial 10m TTL, but before new 16m expiration)
	fakeNow = fakeNow.Add(6 * time.Minute)

	// Should still be valid because of sliding expiration
	item, ok = store.Get(token)
	if !ok {
		t.Fatal("expected session to remain valid due to sliding expiration")
	}

	// Advance time past the new expiration (+11m, now at +23m from start)
	fakeNow = fakeNow.Add(11 * time.Minute)

	// Now it should be expired
	_, ok = store.Get(token)
	if ok {
		t.Fatal("expected session to be expired after inactivity")
	}
}

func TestMemorySessionStore_Concurrency(t *testing.T) {
	store := NewMemorySessionStore(1 * time.Hour)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			token, err := store.Create(int64(id), "admin")
			if err != nil {
				return
			}
			_, _ = store.Get(token)
			store.Delete(token)
		}(i)
	}
	wg.Wait()
}
