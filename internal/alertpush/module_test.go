package alertpush

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"go.uber.org/zap"
)

func TestModuleLifecycle_StartStopAndDegradedSignerKey(t *testing.T) {
	t.Run("normal start creates key and runs dispatcher", func(t *testing.T) {
		dataDir := t.TempDir()
		comp := NewComponents(Options{
			DataDir: dataDir,
			Logger:  zap.NewNop(),
		})
		ctx := context.Background()
		if err := comp.Lifecycle.Start(ctx); err != nil {
			t.Fatalf("Lifecycle.Start: %v", err)
		}
		if !comp.Dispatcher.running.Load() {
			t.Fatal("expected dispatcher running after Start")
		}
		if !comp.SignedMediaHandler.signer.IsConfigured() {
			t.Fatal("expected URLSigner configured after Start")
		}
		if err := comp.Lifecycle.Stop(ctx); err != nil {
			t.Fatalf("Lifecycle.Stop: %v", err)
		}
		if comp.Dispatcher.running.Load() {
			t.Fatal("expected dispatcher stopped after Stop")
		}
	})

	t.Run("insecure media_url.key returns degraded error while still starting dispatcher", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX permission bits check skipped on Windows")
		}
		dataDir := t.TempDir()
		keyPath := filepath.Join(dataDir, signingKeyFileName)
		if err := os.WriteFile(keyPath, bytes.Repeat([]byte{0x11}, 32), 0o644); err != nil {
			t.Fatalf("write 0644 key: %v", err)
		}

		comp := NewComponents(Options{
			DataDir: dataDir,
			Logger:  zap.NewNop(),
		})
		ctx := context.Background()
		startErr := comp.Lifecycle.Start(ctx)
		if !errors.Is(startErr, ErrInsecureSignerKeyPerm) {
			t.Fatalf("Lifecycle.Start err = %v, want ErrInsecureSignerKeyPerm", startErr)
		}
		// Dispatcher must still be started so multipart/base64/heartbeat delivery continues.
		if !comp.Dispatcher.running.Load() {
			t.Fatal("expected dispatcher running even when signer key init fails")
		}
		if comp.SignedMediaHandler.signer.IsConfigured() {
			t.Fatal("expected URLSigner to remain unconfigured on insecure key")
		}
		if err := comp.Lifecycle.Stop(ctx); err != nil {
			t.Fatalf("Lifecycle.Stop after degraded start: %v", err)
		}
	})

	t.Run("symlink media_url.key rejected while dispatcher starts", func(t *testing.T) {
		dataDir := t.TempDir()
		realKey := filepath.Join(dataDir, "real.key")
		if err := os.WriteFile(realKey, bytes.Repeat([]byte{0x22}, 32), 0o600); err != nil {
			t.Fatalf("write real key: %v", err)
		}
		keyPath := filepath.Join(dataDir, signingKeyFileName)
		if err := os.Symlink(realKey, keyPath); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}

		comp := NewComponents(Options{
			DataDir: dataDir,
			Logger:  zap.NewNop(),
		})
		ctx := context.Background()
		startErr := comp.Lifecycle.Start(ctx)
		if !errors.Is(startErr, ErrInsecureSignerKey) {
			t.Fatalf("Lifecycle.Start err = %v, want ErrInsecureSignerKey", startErr)
		}
		if !comp.Dispatcher.running.Load() {
			t.Fatal("expected dispatcher still running on symlink key failure")
		}
		if err := comp.Lifecycle.Stop(ctx); err != nil {
			t.Fatalf("Lifecycle.Stop: %v", err)
		}
	})
}
