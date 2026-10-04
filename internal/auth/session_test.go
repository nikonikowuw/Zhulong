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
