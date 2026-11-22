package filestore

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
)

func TestMemoryDriver_LifecycleAndPresign(t *testing.T) {
	mem := NewMemoryDriver()
	mem.SetPresignBase("https://minio.local:9000/zhulong")

	ctx := context.Background()
	key := "snapshots/cam-01/2026-11-22/evt-1.jpg"
	putErr := mem.Put(
		ctx,
		key,
		strings.NewReader("abcdef0123456789"),
		PutOptions{
			Size:      16,
			Protected: true,
		},
	)
	if putErr != nil {
		t.Fatalf("Put: %v", putErr)
	}

	rc, info, err := mem.Open(ctx, key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if info.SizeBytes != 16 {
		rc.Close()
		t.Fatalf("SizeBytes = %d, want 16", info.SizeBytes)
	}
	if _, err := rc.Seek(6, io.SeekStart); err != nil {
		rc.Close()
		t.Fatalf("Seek: %v", err)
	}
	tail, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || string(tail) != "0123456789" {
		t.Fatalf("tail = %q, err = %v", string(tail), err)
	}

	presigned, err := mem.PresignGet(ctx, key, 10*time.Minute)
	if err != nil || !strings.Contains(presigned, key) {
		t.Fatalf("PresignGet = (%q, %v)", presigned, err)
	}

	mem.SetUnavailable(true)
	_, _, err = mem.Open(ctx, key)
	if apperr.CodeOf(err) != "MEDIA_ROOT_UNAVAILABLE" {
		t.Fatalf("expected MEDIA_ROOT_UNAVAILABLE, got %v", err)
	}

	mem.SetUnavailable(false)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = mem.Exists(ctx, key)
			_, _ = mem.Health(ctx)
		}()
	}
	wg.Wait()
}
