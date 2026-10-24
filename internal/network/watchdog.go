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

// rollbackTimeout bounds the rollback attempt triggered by the in-memory timer.
const rollbackTimeout = 10 * time.Second

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

// disarmTimerLocked stops and clears the pending rollback timer. Callers must hold w.mu.
func (w *WatchdogManager) disarmTimerLocked() {
	if w.timer == nil {
		return
	}
	w.timer.Stop()
	w.timer = nil
}

// loadActiveStateLocked loads the persisted transaction into memory when absent.
// Callers must hold w.mu.
func (w *WatchdogManager) loadActiveStateLocked() error {
	if w.activeState != nil {
		return nil
	}

	diskState, err := w.readDiskStateLocked()
	if err != nil || diskState == nil {
		return noPendingTransactionError()
	}
	w.activeState = diskState
	return nil
}

// clearDiskStateLocked removes the persisted transaction file. Callers must hold w.mu.
func (w *WatchdogManager) clearDiskStateLocked() {
	filePath := w.transactionFilePath()
	if err := os.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		// A leftover pending_confirm file would trigger an unwanted boot rollback, so it must be visible.
		w.logger.Warn("Failed to remove network transaction file",
			zap.String("path", filePath),
			zap.Error(err))
	}
}

func noPendingTransactionError() error {
	return apperr.New(
		apperr.KindNotFound,
		"NO_PENDING_TRANSACTION",
		"No pending network transaction",
		nil,
	)
}

// BeginTransaction initiates a two-phase transaction, writes state to disk with fsync, and arms rollback timer.
func (w *WatchdogManager) BeginTransaction(state TransactionState) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.disarmTimerLocked()

	state.Status = "pending_confirm"
	if state.TimeoutSec <= 0 {
		state.TimeoutSec = DefaultTimeoutSec
	}
	state.ExpiresAt = time.Now().Add(time.Duration(state.TimeoutSec) * time.Second)
	if state.RollbackCfg.DNS == nil {
		state.RollbackCfg.DNS = make([]string, 0)
	}

	if err := w.persistStateLocked(state); err != nil {
		return fmt.Errorf("persist network transaction: %w", err)
	}

	w.activeState = &state

	timeout := time.Duration(state.TimeoutSec) * time.Second
	w.timer = time.AfterFunc(timeout, w.handleTimeout)

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
	w.disarmTimerLocked()
}

// ConfirmTransaction commits the transaction, stops timer, and removes disk state.
func (w *WatchdogManager) ConfirmTransaction(token string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.loadActiveStateLocked(); err != nil {
		return err
	}

	if token != "" && w.activeState.ConfirmToken != token {
		return apperr.New(
			apperr.KindPermissionDenied,
			"INVALID_CONFIRM_TOKEN",
			"Confirmation token is invalid",
			nil,
		)
	}

	w.disarmTimerLocked()
	w.clearDiskStateLocked()

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

	w.disarmTimerLocked()

	if err := w.loadActiveStateLocked(); err != nil {
		return err
	}

	state := w.activeState
	w.logger.Warn("Triggering network rollback to previous safe configuration",
		zap.String("tx_id", state.TransactionID),
		zap.String("interface", state.InterfaceName),
		zap.String("mode", state.RollbackCfg.Mode))

	applyErr := w.provider.ApplyInterfaceConfig(ctx, state.InterfaceName, state.RollbackCfg)

	w.clearDiskStateLocked()
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
		if !errors.Is(err, os.ErrNotExist) {
			w.logger.Warn("Failed to read network transaction file on boot", zap.Error(err))
		}
		return nil
	}

	if state == nil || state.Status != "pending_confirm" {
		return nil
	}

	w.logger.Warn("Discovered unconfirmed network transaction on boot, auto-rolling back to the safe configuration",
		zap.String("tx_id", state.TransactionID),
		zap.String("interface", state.InterfaceName),
		zap.Time("expires_at", state.ExpiresAt))

	if err := w.provider.ApplyInterfaceConfig(ctx, state.InterfaceName, state.RollbackCfg); err != nil {
		w.logger.Error("Failed to auto-rollback on boot", zap.Error(err))
		return fmt.Errorf("boot auto-rollback: %w", err)
	}

	w.clearDiskStateLocked()
	w.logger.Info("Boot auto-rollback completed successfully; network transaction file cleared")
	return nil
}

// GetActiveTransaction returns a deep copy of the current transaction status.
func (w *WatchdogManager) GetActiveTransaction() *TransactionState {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.loadActiveStateLocked(); err != nil || w.activeState == nil {
		return nil
	}

	copied := *w.activeState
	currentDNS := w.activeState.RollbackCfg.DNS
	copied.RollbackCfg.DNS = append(make([]string, 0, len(currentDNS)), currentDNS...)
	return &copied
}

func (w *WatchdogManager) handleTimeout() {
	w.logger.Warn("Network trial timeout expired without confirmation, rolling back now...")

	ctx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
	defer cancel()

	if err := w.RollbackTransaction(ctx); err != nil {
		w.logger.Error("Network watchdog rollback failed", zap.Error(err))
	}
}

func (w *WatchdogManager) persistStateLocked(state TransactionState) error {
	if err := os.MkdirAll(w.dataDir, 0o755); err != nil {
		return fmt.Errorf("ensure data directory %s: %w", w.dataDir, err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal transaction state: %w", err)
	}

	targetPath := w.transactionFilePath()
	tmpPath := targetPath + ".tmp"

	if err := writeFileSynced(tmpPath, data, 0o600); err != nil {
		discardTempFile(w.logger, tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, targetPath); err != nil {
		discardTempFile(w.logger, tmpPath)
		return fmt.Errorf("rename transaction file: %w", err)
	}

	return nil
}

func (w *WatchdogManager) readDiskStateLocked() (*TransactionState, error) {
	data, err := os.ReadFile(w.transactionFilePath())
	if err != nil {
		return nil, err
	}

	var state TransactionState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshal transaction state: %w", err)
	}
	if state.RollbackCfg.DNS == nil {
		state.RollbackCfg.DNS = make([]string, 0)
	}
	return &state, nil
}

// GenerateSecureToken returns a cryptographically secure random hex string.
func GenerateSecureToken(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
