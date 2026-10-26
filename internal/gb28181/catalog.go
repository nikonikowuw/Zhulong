package gb28181

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/camera"
	"go.uber.org/zap"
)

// DefaultCatalogTimeout bounds one catalog exchange end to end.
//
// It cannot come from the caller: the trigger is an HTTP request whose context has no
// server-side deadline (the host sets only ReadHeaderTimeout), so waiting on it alone
// means a silent device holds a goroutine, a session entry and an open HTTP response
// forever. The value covers the device's fragment bursts plus one retransmit.
const DefaultCatalogTimeout = 20 * time.Second

// sipSender is the transport capability required to dispatch outbound SIP MESSAGE
// requests to devices.
type sipSender interface {
	SendMessage(ctx context.Context, device Device, body []byte) error
}

type catalogSession struct {
	deviceID string
	sn       int
	sumNum   int
	items    map[string]Channel
	// rejected counts items dropped for failing the 20-digit code check, so a device
	// that reports garbage is visible in the log instead of looking like an empty catalog.
	rejected int
	mu       sync.Mutex
	done     chan struct{}
}

// CatalogManager manages outbound Catalog queries, SN transaction correlation,
// fragment reassembly, deduplication, and channel persistence.
type CatalogManager struct {
	sender   sipSender
	store    ChannelStore
	logger   *zap.Logger
	timeout  time.Duration
	mu       sync.Mutex
	sessions map[string]*catalogSession
	nextSN   atomic.Int64
}

// NewCatalogManager constructs a CatalogManager.
func NewCatalogManager(sender sipSender, store ChannelStore, logger *zap.Logger) *CatalogManager {
	if logger == nil {
		logger = zap.NewNop()
	}
	cm := &CatalogManager{
		sender:   sender,
		store:    store,
		logger:   logger,
		timeout:  DefaultCatalogTimeout,
		sessions: make(map[string]*catalogSession),
	}
	// Seed with pseudo-random offset based on current timestamp
	cm.nextSN.Store(time.Now().UnixNano() % 10000000)
	return cm
}

func sessionKey(deviceID string, sn int) string {
	return fmt.Sprintf("%s:%d", deviceID, sn)
}

func (cm *CatalogManager) allocateSN() int {
	return int(cm.nextSN.Add(1)%90000000 + 10000000)
}

// Sync sends a Catalog query to the target device, collects multi-fragment responses
// within the catalog timeout, and reconciles the discovered channels.
func (cm *CatalogManager) Sync(ctx context.Context, device Device) ([]Channel, error) {
	if cm.sender == nil {
		return nil, apperr.Internal(CodeRuntimeNotReady, "SIP sender is not ready", nil)
	}

	if cm.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cm.timeout)
		defer cancel()
	}

	sn := cm.allocateSN()
	sess := &catalogSession{
		deviceID: device.DeviceID,
		sn:       sn,
		sumNum:   -1,
		items:    make(map[string]Channel),
		done:     make(chan struct{}),
	}

	key := sessionKey(device.DeviceID, sn)
	cm.mu.Lock()
	cm.sessions[key] = sess
	cm.mu.Unlock()

	defer func() {
		cm.mu.Lock()
		delete(cm.sessions, key)
		cm.mu.Unlock()
	}()

	queryXML := buildCatalogQuery(sn, device.DeviceID)

	// Send with retry (1 immediate retransmit on failure)
	var sendErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, catalogTimeoutError(sess, ctx.Err())
			case <-time.After(200 * time.Millisecond):
			}
		}
		sendErr = cm.sender.SendMessage(ctx, device, queryXML)
		if sendErr == nil {
			break
		}
		var appErr *apperr.Error
		if errors.As(sendErr, &appErr) {
			// Domain failures (flow closed, device offline) carry their own code and status;
			// relabelling them as a catalog failure would hide the real cause from the UI.
			return nil, sendErr
		}
	}
	if sendErr != nil {
		cm.logger.Warn("failed to send gb28181 catalog query",
			zap.String("device_id", device.DeviceID),
			zap.Int("sn", sn),
			zap.Error(sendErr),
		)
		return nil, apperr.New(apperr.KindInvalid, CodeCatalogSyncFailed, "failed to send catalog query", sendErr)
	}

	select {
	case <-sess.done:
		channels := sess.snapshot()
		if cm.store != nil {
			if err := cm.store.SyncChannels(ctx, device.DeviceID, channels); err != nil {
				return nil, storeFailure(err, "Failed to persist GB28181 catalog")
			}
		}
		return channels, nil

	case <-ctx.Done():
		return nil, catalogTimeoutError(sess, ctx.Err())
	}
}

// snapshot copies the accumulated channels out of the session under its lock.
func (s *catalogSession) snapshot() []Channel {
	s.mu.Lock()
	defer s.mu.Unlock()
	channels := make([]Channel, 0, len(s.items))
	for _, ch := range s.items {
		channels = append(channels, ch)
	}
	return channels
}

// catalogTimeoutError reports a catalog exchange that never completed. Fragment loss
// and a device that ignores the query look identical from here, so the log carries the
// partial progress and the API carries a stable code.
func catalogTimeoutError(sess *catalogSession, cause error) error {
	received, expected := 0, -1
	if sess != nil {
		sess.mu.Lock()
		received, expected = len(sess.items), sess.sumNum
		sess.mu.Unlock()
	}
	progress := fmt.Sprintf("%d channels received", received)
	if expected >= 0 {
		progress = fmt.Sprintf("%d/%d channels received", received, expected)
	}
	return apperr.New(apperr.KindInvalid, CodeCatalogTimeout,
		"catalog synchronization timed out with "+progress, cause)
}

// HandleResponse processes an inbound Catalog response from the validated SIP sender.
func (cm *CatalogManager) HandleResponse(ctx context.Context, senderDeviceID string, resp gbCatalogResponse) error {
	if resp.DeviceID != senderDeviceID {
		return fmt.Errorf("catalog response device id %q does not match SIP sender %q", resp.DeviceID, senderDeviceID)
	}

	key := sessionKey(resp.DeviceID, resp.SN)
	cm.mu.Lock()
	sess, ok := cm.sessions[key]
	cm.mu.Unlock()

	if !ok {
		cm.logger.Debug("unmatched or timed out gb28181 catalog response",
			zap.String("device_id", resp.DeviceID),
			zap.Int("sn", resp.SN),
		)
		return nil
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()

	if sess.sumNum < 0 {
		sess.sumNum = resp.SumNum
	}

	now := time.Now().UTC()
	for _, item := range resp.DeviceList {
		ch := item.toChannel(resp.DeviceID, now)
		// The catalog is device-reported, so every code is untrusted input: a channel
		// that is not a 20-digit code can never become a camera, and letting it into the
		// table would only surface as CAMERA_INVALID_PARAM when an operator tries to add it.
		if !camera.IsGB28181ID(ch.ChannelID) {
			sess.rejected++
			continue
		}
		sess.items[ch.ChannelID] = ch
	}
	if sess.rejected > 0 {
		cm.logger.Warn("ignored malformed gb28181 catalog items",
			zap.String("device_id", resp.DeviceID),
			zap.Int("sn", resp.SN),
			zap.Int("rejected", sess.rejected),
		)
	}

	// Terminate if:
	// - SumNum is 0 (device has no channels)
	// - We have accumulated at least SumNum unique items
	// - SumNum <= 0 and empty device list
	if sess.sumNum == 0 || (sess.sumNum > 0 && len(sess.items) >= sess.sumNum) || (sess.sumNum <= 0 && len(resp.DeviceList) == 0) {
		select {
		case <-sess.done:
		default:
			close(sess.done)
		}
	}
	return nil
}
