package alertpush

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
)

// AlertDestinationStatus reports the operational health of a destination.
type AlertDestinationStatus struct {
	State         string     `json:"state"`
	LastError     string     `json:"lastError"`
	LastSuccessAt *time.Time `json:"lastSuccessAt"`
	Dropped       int64      `json:"dropped"`
	Queued        int64      `json:"queued"`
}

// Config represents alertpush configuration injected into Dispatcher.
type Config struct {
	PublicBaseURL string
	SignedURLTTL  time.Duration
	// DeviceID supplies the host identity stamped onto every dispatched payload.
	// It is read per send (not cached at construction) so editing it under
	// settings → system takes effect without rebuilding destinations.
	DeviceID DeviceIDProvider
}

// DispatcherOptions holds parameters for constructing a Dispatcher.
type DispatcherOptions struct {
	Store  *Store
	Signer *URLSigner
	Media  MediaReader
	Config Config
	Logger *zap.Logger
}

// Dispatcher coordinates multi-target event filtering and asynchronous fan-out.
type Dispatcher struct {
	store    *Store
	signer   *URLSigner
	media    MediaReader
	config   Config
	logger   *zap.Logger
	workers  map[string]*destinationWorker
	mu       sync.RWMutex
	statsMu  sync.RWMutex
	stats    map[string]AlertDestinationStatus
	reloadCh chan struct{}
	stopCh   chan struct{}
	running  atomic.Bool
	wg       sync.WaitGroup
}

// NewDispatcher creates a new Dispatcher instance.
func NewDispatcher(opts DispatcherOptions) *Dispatcher {
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Dispatcher{
		store:    opts.Store,
		signer:   opts.Signer,
		media:    opts.Media,
		config:   opts.Config,
		logger:   logger.Named("alertpush.dispatcher"),
		workers:  make(map[string]*destinationWorker),
		stats:    make(map[string]AlertDestinationStatus),
		reloadCh: make(chan struct{}, 1),
		stopCh:   make(chan struct{}),
	}
}

// Start begins background reconciliation loop and loads initial destinations.
func (d *Dispatcher) Start(ctx context.Context) error {
	d.running.Store(true)
	d.reconcile()

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		for {
			select {
			case <-d.stopCh:
				return
			case <-d.reloadCh:
				d.reconcile()
			}
		}
	}()
	return nil
}

// Stop cleanly drains workers and terminates dispatcher.
func (d *Dispatcher) Stop(ctx context.Context) error {
	if !d.running.CompareAndSwap(true, false) {
		return nil
	}
	close(d.stopCh)
	d.wg.Wait()

	d.mu.Lock()
	workers := d.workers
	d.workers = make(map[string]*destinationWorker)
	d.mu.Unlock()

	for _, w := range workers {
		w.stop()
	}
	return nil
}

// OnAlarm implements record.AlarmListener.
func (d *Dispatcher) OnAlarm(ctx context.Context, ev record.AlarmEvent) {
	d.Dispatch(ev)
}

// Dispatch matches destinations and enqueues events without blocking.
func (d *Dispatcher) Dispatch(ev record.AlarmEvent) {
	if !d.running.Load() {
		return
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	for _, w := range d.workers {
		if !w.filter.Matches(string(ev.EventType), ev.AlgorithmID, ev.CameraID) {
			continue
		}

		select {
		case w.queue <- ev:
		default:
			// Queue full: drop oldest event and enqueue new event
			select {
			case <-w.queue:
				w.dropped.Add(1)
			default:
			}
			select {
			case w.queue <- ev:
			default:
				w.dropped.Add(1)
			}
		}
	}
}

// RequestReload signals the reconciler to reload active destinations from database.
func (d *Dispatcher) RequestReload() {
	select {
	case d.reloadCh <- struct{}{}:
	default:
	}
}

// GetStatus returns the operational status of a destination.
func (d *Dispatcher) GetStatus(id string, enabled bool) AlertDestinationStatus {
	if !enabled {
		return AlertDestinationStatus{
			State: StateDisabled,
		}
	}

	d.mu.RLock()
	w, ok := d.workers[id]
	d.mu.RUnlock()

	if ok {
		return w.getStatus()
	}

	d.statsMu.RLock()
	st, found := d.stats[id]
	d.statsMu.RUnlock()
	if found {
		return st
	}

	return AlertDestinationStatus{
		State: StateUnverified,
	}
}

// UpdateTestResult updates destination status after an explicit test execution.
func (d *Dispatcher) UpdateTestResult(id string, success bool, reason string) {
	d.mu.RLock()
	w, ok := d.workers[id]
	d.mu.RUnlock()

	if ok {
		if success {
			w.state.Store(StateConnected)
			w.lastError.Store("")
			now := time.Now().UTC()
			w.lastSuccessAt.Store(&now)
		} else {
			w.state.Store(StateFailed)
			w.lastError.Store(reason)
		}
	}

	d.statsMu.Lock()
	st := d.stats[id]
	if success {
		st.State = StateConnected
		st.LastError = ""
		now := time.Now().UTC()
		st.LastSuccessAt = &now
	} else {
		st.State = StateFailed
		st.LastError = reason
	}
	d.stats[id] = st
	d.statsMu.Unlock()
}

func (d *Dispatcher) reconcile() {
	if d.store == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	enabledDestinations, err := d.store.ListEnabled(ctx)
	if err != nil {
		d.logger.Error("reconcile list enabled destinations failed", zap.Error(err))
		return
	}

	activeIDs := make(map[string]struct{}, len(enabledDestinations))
	toRetire := []*destinationWorker{}
	toStart := []*destinationWorker{}

	d.mu.Lock()
	for _, dest := range enabledDestinations {
		activeIDs[dest.ID] = struct{}{}
		fp := computeFingerprint(dest)

		if currentWorker, exists := d.workers[dest.ID]; exists {
			if currentWorker.fingerprint == fp {
				// Destination config has not changed, keep running worker intact
				continue
			}
			// Config changed: retire old worker
			toRetire = append(toRetire, currentWorker)
			delete(d.workers, dest.ID)
			d.statsMu.Lock()
			d.stats[dest.ID] = currentWorker.getStatus()
			d.statsMu.Unlock()
		}

		// Destination is new or updated, build sender
		sender, filter, err := d.buildSender(dest)
		if err != nil {
			d.logger.Warn("failed to build sender for destination, skipping",
				zap.String("id", dest.ID),
				zap.Error(err),
			)
			continue
		}

		w := newDestinationWorker(destinationWorkerOptions{
			dest:        dest,
			sender:      sender,
			filter:      filter,
			fingerprint: fp,
			logger:      d.logger,
		})
		d.workers[dest.ID] = w
		toStart = append(toStart, w)
	}

	// Retire destinations that are no longer enabled or removed
	for id, w := range d.workers {
		if _, active := activeIDs[id]; !active {
			toRetire = append(toRetire, w)
			delete(d.workers, id)
			d.statsMu.Lock()
			d.stats[id] = w.getStatus()
			d.statsMu.Unlock()
		}
	}
	d.mu.Unlock()

	for _, w := range toStart {
		w.start()
	}
	for _, w := range toRetire {
		w.stop()
	}
}

func (d *Dispatcher) buildSender(dest *AlertDestination) (Sender, destinationFilter, error) {
	var filter destinationFilter
	if dest.FilterJSON != "" {
		if err := json.Unmarshal([]byte(dest.FilterJSON), &filter); err != nil {
			return nil, filter, fmt.Errorf("unmarshal destination filter: %w", err)
		}
	}

	switch dest.ChannelType {
	case ChannelTypeWebhook:
		sender, err := d.buildWebhookSender(dest)
		return sender, filter, err
	case ChannelTypeMQTT:
		sender, err := d.buildMQTTSender(dest)
		return sender, filter, err
	default:
		return nil, filter, errors.New("unsupported channel type")
	}
}

func (d *Dispatcher) buildWebhookSender(dest *AlertDestination) (Sender, error) {
	var cfg webhookConfig
	if dest.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(dest.ConfigJSON), &cfg); err != nil {
			return nil, fmt.Errorf("unmarshal webhook config: %w", err)
		}
	}
	secret, err := d.store.DecryptWebhookSecret(dest.ID, dest.SecretCiphertext)
	if err != nil {
		return nil, err
	}

	return NewWebhookSender(WebhookSenderOptions{
		ID:            dest.ID,
		Name:          dest.Name,
		ImageMode:     dest.ImageMode,
		PublicBaseURL: d.config.PublicBaseURL,
		SignedURLTTL:  d.config.SignedURLTTL,
		Signer:        d.signer,
		Media:         d.media,
		URL:           secret.URL,
		AuthType:      cfg.AuthType,
		Token:         secret.Token,
		Username:      secret.Username,
		Password:      secret.Password,
		Headers:       secret.Headers,
		Timeout:       time.Duration(cfg.TimeoutSeconds) * time.Second,
		Identity:      d.config.DeviceID,
		Logger:        d.logger,
	})
}

func (d *Dispatcher) buildMQTTSender(dest *AlertDestination) (Sender, error) {
	var cfg mqttConfig
	if dest.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(dest.ConfigJSON), &cfg); err != nil {
			return nil, fmt.Errorf("unmarshal mqtt config: %w", err)
		}
	}
	secret, err := d.store.DecryptMQTTSecret(dest.ID, dest.SecretCiphertext)
	if err != nil {
		return nil, err
	}

	var clientID string
	if dest.MQTTClientID != nil {
		clientID = *dest.MQTTClientID
	}

	s, err := NewMQTTSender(MQTTSenderOptions{
		ID:            dest.ID,
		Name:          dest.Name,
		ImageMode:     dest.ImageMode,
		PublicBaseURL: d.config.PublicBaseURL,
		SignedURLTTL:  d.config.SignedURLTTL,
		Signer:        d.signer,
		Media:         d.media,
		Broker:        cfg.Broker,
		ClientID:      clientID,
		Username:      cfg.Username,
		Password:      secret.Password,
		Topic:         cfg.Topic,
		QoS:           cfg.QoS,
		KeepAlive:     time.Duration(cfg.KeepAliveSeconds) * time.Second,
		TLSSkipVerify: cfg.TLSSkipVerify,
		Identity:      d.config.DeviceID,
		Logger:        d.logger,
	})
	if err != nil {
		return nil, err
	}

	// Attempt initial connection asynchronously
	go func() {
		if connectErr := s.Connect(); connectErr != nil {
			d.logger.Debug("initial mqtt connect attempt failed", zap.String("id", dest.ID), zap.Error(connectErr))
		}
	}()
	return s, nil
}

type destinationWorkerOptions struct {
	dest        *AlertDestination
	sender      Sender
	filter      destinationFilter
	fingerprint string
	logger      *zap.Logger
}

type destinationWorker struct {
	id            string
	name          string
	channelType   string
	filter        destinationFilter
	sender        Sender
	queue         chan record.AlarmEvent
	dropped       atomic.Int64
	state         atomic.Value // string
	lastError     atomic.Value // string
	lastSuccessAt atomic.Pointer[time.Time]
	fingerprint   string
	started       atomic.Bool
	stopOnce      sync.Once
	stopCh        chan struct{}
	doneCh        chan struct{}
	logger        *zap.Logger
}

func newDestinationWorker(opts destinationWorkerOptions) *destinationWorker {
	w := &destinationWorker{
		id:          opts.dest.ID,
		name:        opts.dest.Name,
		channelType: opts.dest.ChannelType,
		filter:      opts.filter,
		sender:      opts.sender,
		queue:       make(chan record.AlarmEvent, 16),
		fingerprint: opts.fingerprint,
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
		logger:      opts.logger.With(zap.String("destination_id", opts.dest.ID)),
	}

	initialState := StateUnverified
	if opts.dest.ChannelType == ChannelTypeMQTT {
		initialState = StateConnecting
	}
	w.state.Store(initialState)
	w.lastError.Store("")
	return w
}

func (w *destinationWorker) start() {
	if w.started.CompareAndSwap(false, true) {
		go w.run()
	}
}

func (w *destinationWorker) run() {
	defer func() {
		if w.sender != nil {
			if closeErr := w.sender.Close(); closeErr != nil {
				w.logger.Debug("close destination sender", zap.Error(closeErr))
			}
		}
		close(w.doneCh)
	}()

	// If MQTT sender, update connected state initially
	if mqttSender, ok := w.sender.(*MQTTSender); ok && mqttSender.IsConnected() {
		w.state.Store(StateConnected)
	}

	for {
		select {
		case <-w.stopCh:
			// 目标退役时，在 3s 独立 deadline 内排空队列已排队事件
			drainCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			for {
				select {
				case ev := <-w.queue:
					if err := w.sender.Send(drainCtx, ev); err != nil {
						w.logger.Debug("failed to send drained event on retirement", zap.Error(err))
					}
				case <-drainCtx.Done():
					return
				default:
					return
				}
			}
		case ev := <-w.queue:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := w.sender.Send(ctx, ev)
			cancel()

			if err == nil {
				w.state.Store(StateConnected)
				w.lastError.Store("")
				now := time.Now().UTC()
				w.lastSuccessAt.Store(&now)
			} else {
				w.state.Store(StateFailed)
				w.lastError.Store(classifyError(err))
				w.logger.Warn("destination dispatch failed", zap.Error(err))
			}
		}
	}
}

func (w *destinationWorker) stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})

	if w.started.Load() {
		<-w.doneCh
		return
	}
	if w.sender != nil {
		if closeErr := w.sender.Close(); closeErr != nil {
			w.logger.Debug("close unstarted destination sender", zap.Error(closeErr))
		}
	}
}

func (w *destinationWorker) getStatus() AlertDestinationStatus {
	st, ok := w.state.Load().(string)
	if !ok || st == "" {
		st = StateUnverified
	}
	lastErr, ok := w.lastError.Load().(string)
	if !ok {
		lastErr = ""
	}

	return AlertDestinationStatus{
		State:         st,
		LastError:     lastErr,
		LastSuccessAt: w.lastSuccessAt.Load(),
		Dropped:       w.dropped.Load(),
		Queued:        int64(len(w.queue)),
	}
}

func computeFingerprint(dest *AlertDestination) string {
	var clientID string
	if dest.MQTTClientID != nil {
		clientID = *dest.MQTTClientID
	}

	h := sha256.New()
	for _, part := range []string{
		dest.ChannelType,
		dest.ImageMode,
		clientID,
		dest.ConfigJSON,
		dest.FilterJSON,
	} {
		h.Write([]byte(part))
		h.Write([]byte("|"))
	}
	h.Write(dest.SecretCiphertext)
	return hex.EncodeToString(h.Sum(nil))
}

// classifyError maps Go errors into stable reason codes for status and test response.
func classifyError(err error) string {
	if err == nil {
		return ""
	}
	errStr := strings.ToLower(err.Error())

	var dnsErr *net.DNSError
	isDNS := errors.As(err, &dnsErr) || strings.Contains(errStr, "no such host")
	isTimeout := strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline exceeded")
	isTLS := strings.Contains(errStr, "certificate") ||
		strings.Contains(errStr, "tls") ||
		strings.Contains(errStr, "x509")
	isAuth := strings.Contains(errStr, "401") ||
		strings.Contains(errStr, "403") ||
		strings.Contains(errStr, "not authorized")
	isNotConnected := errors.Is(err, ErrMQTTNotConnected) || strings.Contains(errStr, "not connected")
	isPublishFailed := errors.Is(err, ErrMQTTPublishFailed) || strings.Contains(errStr, "publish")

	switch {
	case isDNS:
		return "dns"
	case strings.Contains(errStr, "connection refused"):
		return "connection_refused"
	case isTimeout:
		return "timeout"
	case isTLS:
		return "tls"
	case isAuth:
		return "auth"
	case strings.Contains(errStr, "status"):
		return "http_status"
	case isNotConnected:
		return "not_connected"
	case isPublishFailed:
		return "publish_failed"
	default:
		return "unknown"
	}
}
