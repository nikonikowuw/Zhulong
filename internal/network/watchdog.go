package network

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"go.uber.org/zap"
)

const (
	TransactionFileName = "network_transaction.json"
	DefaultTimeoutSec   = 60
)

// WatchdogManager coordinates two-phase network configuration rollbacks and disk persistence.
type WatchdogManager struct {
	mu          sync.Mutex
	dataDir     string
	provider    NetworkProvider
	activeState *TransactionState
	timer       *time.Timer
	logger      *zap.Logger
}

// NewWatchdogManager creates a new WatchdogManager.
func NewWatchdogManager(dataDir string, provider NetworkProvider, logger *zap.Logger) *WatchdogManager {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &WatchdogManager{
		dataDir:  dataDir,
		provider: provider,
		logger:   logger,
	}
}

func (w *WatchdogManager) transactionFilePath() string {
	return filepath.Join(w.dataDir, TransactionFileName)
}

// BeginTransaction initiates a two-phase transaction, writes state to disk with fsync, and arms rollback timer.
func (w *WatchdogManager) BeginTransaction(state TransactionState) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// If a previous timer is ticking, stop it
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}

	state.Status = "pending_confirm"
	if state.TimeoutSec <= 0 {
		state.TimeoutSec = DefaultTimeoutSec
	}
	state.ExpiresAt = time.Now().Add(time.Duration(state.TimeoutSec) * time.Second)

	// Atomic write with fsync
	if err := w.persistStateLocked(state); err != nil {
		return fmt.Errorf("persist network transaction: %w", err)
	}

	w.activeState = &state

	// Arm in-memory rollback timer
	timeout := time.Duration(state.TimeoutSec) * time.Second
	w.timer = time.AfterFunc(timeout, func() {
		w.handleTimeout()
	})

	w.logger.Info("Network watchdog transaction armed",
		zap.String("tx_id", state.TransactionID),
		zap.String("interface", state.InterfaceName),
		zap.Int("timeout_sec", state.TimeoutSec))
	return nil
}

// Stop disarms the timer and halts watchdog background activities during graceful shutdown.
func (w *WatchdogManager) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
}

// ConfirmTransaction commits the transaction, stops timer, and removes disk state.
func (w *WatchdogManager) ConfirmTransaction(token string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// If memory state is empty, attempt to read from disk
	if w.activeState == nil {
		diskState, err := w.readDiskStateLocked()
		if err != nil || diskState == nil {
			return apperr.New(apperr.KindNotFound, "NO_PENDING_TRANSACTION", "No pending network transaction to confirm", nil)
		}
		w.activeState = diskState
	}

	if token != "" && w.activeState.ConfirmToken != token {
		return apperr.New(apperr.KindPermissionDenied, "INVALID_CONFIRM_TOKEN", "Confirmation token is invalid", nil)
	}

	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}

	// Remove transaction file from disk
	filePath := w.transactionFilePath()
	_ = os.Remove(filePath)

	txID := w.activeState.TransactionID
	iface := w.activeState.InterfaceName
	w.activeState = nil

	w.logger.Info("Network configuration trial confirmed and solidified",
		zap.String("tx_id", txID),
		zap.String("interface", iface))
	return nil
}

// RollbackTransaction immediately cancels the trial and rolls back to RollbackCfg.
func (w *WatchdogManager) RollbackTransaction(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}

	state := w.activeState
	if state == nil {
		var err error
		state, err = w.readDiskStateLocked()
		if err != nil || state == nil {
			return apperr.New(apperr.KindNotFound, "NO_PENDING_TRANSACTION", "No pending network transaction to rollback", nil)
		}
	}

	w.logger.Warn("Triggering network rollback to previous safe configuration",
		zap.String("tx_id", state.TransactionID),
		zap.String("interface", state.InterfaceName),
		zap.String("mode", state.RollbackCfg.Mode))

	applyErr := w.provider.ApplyInterfaceConfig(ctx, state.InterfaceName, state.RollbackCfg)

	// Clean up disk and memory
	_ = os.Remove(w.transactionFilePath())
	w.activeState = nil

	if applyErr != nil {
		return fmt.Errorf("apply rollback configuration: %w", applyErr)
	}
	return nil
}

// CheckPendingBootTransaction checks on host startup whether an unconfirmed transaction was left from abnormal reboot.
func (w *WatchdogManager) CheckPendingBootTransaction(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	state, err := w.readDiskStateLocked()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		w.logger.Warn("Failed to read network transaction file on boot", zap.Error(err))
		return nil
	}

	if state == nil || state.Status != "pending_confirm" {
		return nil
	}

	w.logger.Warn("⚠️ DISCOVERED UNCONFIRMED NETWORK TRANSACTION ON BOOT. Initiating emergency auto-rollback to restore safe network configuration",
		zap.String("tx_id", state.TransactionID),
		zap.String("interface", state.InterfaceName),
		zap.Time("expires_at", state.ExpiresAt))

	if err := w.provider.ApplyInterfaceConfig(ctx, state.InterfaceName, state.RollbackCfg); err != nil {
		w.logger.Error("Failed to auto-rollback on boot", zap.Error(err))
		return fmt.Errorf("boot auto-rollback: %w", err)
	}

	_ = os.Remove(w.transactionFilePath())
	w.logger.Info("Boot auto-rollback completed successfully; network transaction file cleared")
	return nil
}

// GetActiveTransaction returns current in-memory transaction status.
func (w *WatchdogManager) GetActiveTransaction() *TransactionState {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.activeState == nil {
		diskState, err := w.readDiskStateLocked()
		if err == nil && diskState != nil {
			w.activeState = diskState
		}
	}

	if w.activeState == nil {
		return nil
	}

	copied := *w.activeState
	return &copied
}

func (w *WatchdogManager) handleTimeout() {
	w.logger.Warn("Network trial timeout expired without confirmation, rolling back now...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := w.RollbackTransaction(ctx); err != nil {
		w.logger.Error("Network watchdog rollback failed", zap.Error(err))
	}
}

func (w *WatchdogManager) persistStateLocked(state TransactionState) error {
	if err := os.MkdirAll(w.dataDir, 0755); err != nil {
		return fmt.Errorf("ensure data directory %s: %w", w.dataDir, err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal transaction state: %w", err)
	}

	targetPath := w.transactionFilePath()
	tmpPath := targetPath + ".tmp"

	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("create temporary transaction file: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write transaction data: %w", err)
	}

	// Critical fsync to ensure data hit non-volatile media before network switch
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("fsync transaction data: %w", err)
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close transaction file: %w", err)
	}

	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename transaction file: %w", err)
	}

	return nil
}

func (w *WatchdogManager) readDiskStateLocked() (*TransactionState, error) {
	filePath := w.transactionFilePath()
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var state TransactionState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshal transaction state: %w", err)
	}
	return &state, nil
}

// GenerateSecureToken returns a cryptographically secure random hex string.
func GenerateSecureToken(bytesLen int) string {
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("fallback_token_%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
