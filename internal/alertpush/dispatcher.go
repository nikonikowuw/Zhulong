package alertpush

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/nikonikowuw/Zhulong/internal/record"
)

// AlertDestinationStatus reports the operational health of a destination.
type AlertDestinationStatus struct {
	State               string     `json:"state"`
	LastError           string     `json:"lastError"`
	LastAttemptAt       *time.Time `json:"lastAttemptAt"`
	LastSuccessAt       *time.Time `json:"lastSuccessAt"`
	LastFailureAt       *time.Time `json:"lastFailureAt"`
	ConsecutiveFailures int64      `json:"consecutiveFailures"`
	TotalDispatched     int64      `json:"totalDispatched"`
	Dropped             int64      `json:"dropped"`
	Queued              int64      `json:"queued"`
	QueueCapacity       int64      `json:"queueCapacity"`
}

// Config represents alertpush configuration injected into Dispatcher.
type Config struct {
	PublicBaseURL string
	SignedURLTTL  time.Duration
	// SignedMediaRateLimitPerMin / SignedMediaRateLimitBurst tune per-IP rate limiting on
	// /api/v1/media/signed/*path. Zero values fall back to 120 req/min and burst 8.
	SignedMediaRateLimitPerMin int
	SignedMediaRateLimitBurst  int
	// DeviceID supplies the host identity stamped onto every dispatched payload.
	// It is read per send (not cached at construction) so editing it under
	// settings → system takes effect without rebuilding destinations.
	DeviceID DeviceIDProvider
	// HeartbeatSnapshot supplies sanitized point-in-time host and workload summaries.
	HeartbeatSnapshot HeartbeatSnapshotProvider
	// Source resolves camera / algorithm display metadata per delivery. Nil leaves the
	// names null; the payload still carries the raw IDs.
	Source SourceResolver
	// AllowLoopback permits outbound Webhook/MQTT connections to loopback hosts (127.0.0.1/::1/localhost).
	// Production defaults to false; unit tests opt in explicitly.
	AllowLoopback bool
}

const (
	// sourceResolveTimeout bounds metadata lookups so a slow store never stalls a worker
	// (or its retirement drain).
	sourceResolveTimeout   = 2 * time.Second
	workerQueueCapacity    = 16
	retirementDrainTimeout = 3 * time.Second

	defaultFailureBackoffThreshold = 3
	defaultFailureBackoffBase      = 1 * time.Second
	defaultFailureBackoffMax       = 30 * time.Second
)

// queuedEvent pairs an event with the sequence number assigned at enqueue time.
type queuedEvent struct {
	ev  record.AlarmEvent
	seq uint64
}

// DispatcherOptions holds parameters for constructing a Dispatcher.
type DispatcherOptions struct {
	Store        *Store
	Signer       *URLSigner
	Media        MediaReader
	GalleryMedia MediaReader
	Config       Config
	Logger       *zap.Logger
}

// Dispatcher coordinates multi-target event filtering, asynchronous fan-out, and
// dual-rail periodic heartbeat reporting.
type Dispatcher struct {
	store        *Store
	signer       *URLSigner
	media        MediaReader
	galleryMedia MediaReader
	config       Config
	logger       *zap.Logger
	workers      map[string]*destinationWorker
	mu           sync.RWMutex
	statsMu      sync.RWMutex
	stats        map[string]AlertDestinationStatus
	seqMu        sync.Mutex
	seqMap       map[string]*atomic.Uint64
	reloadCh     chan struct{}
	stopCh       chan struct{}
	running      atomic.Bool
	wg           sync.WaitGroup
}

// NewDispatcher creates a new Dispatcher instance.
func NewDispatcher(opts DispatcherOptions) *Dispatcher {
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Dispatcher{
		store:        opts.Store,
		signer:       opts.Signer,
		media:        opts.Media,
		galleryMedia: opts.GalleryMedia,
		config:       opts.Config,
		logger:       logger.Named("alertpush.dispatcher"),
		workers:      make(map[string]*destinationWorker),
		stats:        make(map[string]AlertDestinationStatus),
		seqMap:       make(map[string]*atomic.Uint64),
		reloadCh:     make(chan struct{}, 1),
		stopCh:       make(chan struct{}),
	}
}

func (d *Dispatcher) counterFor(destID string) *atomic.Uint64 {
	d.seqMu.Lock()
	defer d.seqMu.Unlock()

	if c, ok := d.seqMap[destID]; ok {
		return c
	}
	c := &atomic.Uint64{}
	d.seqMap[destID] = c
	return c
}

// Start begins background reconciliation loop and loads initial destinations.
func (d *Dispatcher) Start(ctx context.Context) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	d.running.Store(true)
	d.reconcileWithContext(ctx)

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

// Stop cleanly drains workers concurrently and terminates dispatcher, respecting ctx cancellation.
func (d *Dispatcher) Stop(ctx context.Context) error {
	if !d.running.CompareAndSwap(true, false) {
		return nil
	}
	close(d.stopCh)

	reconcileDone := make(chan struct{})
	go func() {
		defer close(reconcileDone)
		d.wg.Wait()
	}()
	if err := waitWithContext(ctx, reconcileDone); err != nil {
		return err
	}

	d.mu.Lock()
	workers := d.workers
	d.workers = make(map[string]*destinationWorker)
	d.mu.Unlock()

	workersDone := make(chan struct{})
	go func() {
		defer close(workersDone)
		var stopWg sync.WaitGroup
		for _, w := range workers {
			stopWg.Add(1)
			go func(worker *destinationWorker) {
				defer stopWg.Done()
				worker.stop()
			}(w)
		}
		stopWg.Wait()
	}()

	return waitWithContext(ctx, workersDone)
}

func waitWithContext(ctx context.Context, done <-chan struct{}) error {
	if ctx == nil {
		<-done
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RemoveDestinationState drops cached status and sequence counter entries for a deleted destination.
func (d *Dispatcher) RemoveDestinationState(id string) {
	if id == "" {
		return
	}
	d.statsMu.Lock()
	delete(d.stats, id)
	d.statsMu.Unlock()

	d.seqMu.Lock()
	delete(d.seqMap, id)
	d.seqMu.Unlock()
}

// OnAlarm implements record.AlarmListener.
func (d *Dispatcher) OnAlarm(ctx context.Context, ev record.AlarmEvent) {
	d.Dispatch(ev)
}

// Dispatch matches destinations with events enabled and enqueues events without blocking.
func (d *Dispatcher) Dispatch(ev record.AlarmEvent) {
	if !d.running.Load() {
		return
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	for _, w := range d.workers {
		if !w.eventsEnabled {
			continue
		}
		if !w.filter.Matches(externalEventType(ev.EventType), ev.AlgorithmID, ev.CameraID) {
			continue
		}
		w.enqueue(ev)
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
			State:         StateDisabled,
			QueueCapacity: workerQueueCapacity,
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
		if st.QueueCapacity <= 0 {
			st.QueueCapacity = workerQueueCapacity
		}
		return st
	}

	return AlertDestinationStatus{
		State:         StateUnverified,
		QueueCapacity: workerQueueCapacity,
	}
}

// UpdateTestResult updates destination status after an explicit test execution.
func (d *Dispatcher) UpdateTestResult(id string, success bool, reason string) {
	d.mu.RLock()
	w, ok := d.workers[id]
	d.mu.RUnlock()

	now := time.Now().UTC()
	if ok {
		if success {
			w.recordSuccess()
		} else {
			w.recordFailure(reason)
		}
	}

	d.statsMu.Lock()
	st := d.stats[id]
	st.LastAttemptAt = &now
	if success {
		st.State = StateConnected
		st.LastError = ""
		st.LastSuccessAt = &now
		st.ConsecutiveFailures = 0
	} else {
		st.State = StateFailed
		st.LastError = reason
		st.LastFailureAt = &now
		st.ConsecutiveFailures++
	}
	d.stats[id] = st
	d.statsMu.Unlock()
}

func (d *Dispatcher) reconcile() {
	d.reconcileWithContext(context.Background())
}

func (d *Dispatcher) reconcileWithContext(parent context.Context) {
	if d.store == nil {
		return
	}
	if parent == nil {
		parent = context.Background()
	}

	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()

	allDestinations, err := d.store.List(ctx)
	if err != nil {
		d.logger.Error("reconcile list destinations failed", zap.Error(err))
		return
	}

	existingIDs := make(map[string]struct{}, len(allDestinations))
	enabledDestinations := make([]*AlertDestination, 0, len(allDestinations))
	for _, dest := range allDestinations {
		existingIDs[dest.ID] = struct{}{}
		if dest.Enabled {
			enabledDestinations = append(enabledDestinations, dest)
		}
	}

	currentDeviceID := resolveDeviceID(d.config.DeviceID)
	activeIDs := make(map[string]struct{}, len(enabledDestinations))
	toRetireReload := []*destinationWorker{}
	toRetireOffline := []*destinationWorker{}
	retiredWorkers := []*destinationWorker{}
	replacedBy := make(map[string]*destinationWorker)
	buildFailedIDs := make(map[string]struct{})
	toStart := []*destinationWorker{}

	d.mu.Lock()
	for _, dest := range enabledDestinations {
		activeIDs[dest.ID] = struct{}{}
		fp := computeFingerprint(dest, currentDeviceID)

		currentWorker, exists := d.workers[dest.ID]
		if exists && currentWorker.fingerprint == fp {
			// Destination config has not changed, keep running worker intact
			continue
		}

		// Destination is new or updated, build replacement sender before retiring
		sender, filter, hbInterval, err := d.buildSender(dest)
		if err != nil {
			buildFailedIDs[dest.ID] = struct{}{}
			reason := classifyError(err)
			d.logger.Warn("failed to build sender for destination, marking failed",
				zap.String("id", dest.ID),
				zap.String("reason", reason),
				zap.Error(err),
			)
			now := time.Now().UTC()
			var st AlertDestinationStatus
			if exists {
				toRetireOffline = append(toRetireOffline, currentWorker)
				retiredWorkers = append(retiredWorkers, currentWorker)
				delete(d.workers, dest.ID)
				st = currentWorker.getStatus()
			} else {
				d.statsMu.RLock()
				st = d.stats[dest.ID]
				d.statsMu.RUnlock()
			}
			st.State = StateFailed
			st.LastError = reason
			st.LastAttemptAt = &now
			st.LastFailureAt = &now
			st.ConsecutiveFailures++
			st.Queued = 0
			st.QueueCapacity = workerQueueCapacity
			d.statsMu.Lock()
			d.stats[dest.ID] = st
			d.statsMu.Unlock()
			continue
		}

		var prevStatus *AlertDestinationStatus
		if exists {
			if dest.HeartbeatEnabled {
				toRetireReload = append(toRetireReload, currentWorker)
			} else {
				toRetireOffline = append(toRetireOffline, currentWorker)
			}
			retiredWorkers = append(retiredWorkers, currentWorker)
			delete(d.workers, dest.ID)
		} else {
			d.statsMu.RLock()
			if snap, found := d.stats[dest.ID]; found {
				snapCopy := snap
				prevStatus = &snapCopy
			}
			d.statsMu.RUnlock()
		}

		w := newDestinationWorker(destinationWorkerOptions{
			dest:              dest,
			sender:            sender,
			filter:            filter,
			fingerprint:       fp,
			heartbeatInterval: hbInterval,
			snapshotProvider:  d.config.HeartbeatSnapshot,
			publicBaseURL:     d.config.PublicBaseURL,
			seqCounter:        d.counterFor(dest.ID),
			sourceResolver:    d.config.Source,
			onWillStale:       d.RequestReload,
			initialStatus:     prevStatus,
			logger:            d.logger,
		})
		d.workers[dest.ID] = w
		if exists {
			replacedBy[dest.ID] = w
		}
		toStart = append(toStart, w)
	}

	// Retire destinations that are no longer enabled or removed
	for id, w := range d.workers {
		if _, active := activeIDs[id]; !active {
			toRetireOffline = append(toRetireOffline, w)
			retiredWorkers = append(retiredWorkers, w)
			delete(d.workers, id)
		}
	}
	d.mu.Unlock()

	// Stop retiring workers concurrently BEFORE starting replacements so shared MQTT ClientIDs
	// disconnect cleanly without broker session-takeover kicks or false LWT fires, while
	// keeping shutdown latency bounded even when multiple destinations change at once.
	var retireWg sync.WaitGroup
	for _, w := range toRetireReload {
		retireWg.Add(1)
		go func(worker *destinationWorker) {
			defer retireWg.Done()
			worker.stopForReload()
		}(w)
	}
	for _, w := range toRetireOffline {
		retireWg.Add(1)
		go func(worker *destinationWorker) {
			defer retireWg.Done()
			worker.stop()
		}(w)
	}
	retireWg.Wait()

	// Snapshot post-drain cumulative telemetry after retirement finishes so drained
	// deliveries are preserved across reloads, and prune deleted destinations permanently.
	d.statsMu.Lock()
	for _, oldW := range retiredWorkers {
		if _, stillExists := existingIDs[oldW.id]; !stillExists {
			delete(d.stats, oldW.id)
			continue
		}
		snap := oldW.getStatus()
		if newW, replaced := replacedBy[oldW.id]; replaced {
			newW.inheritStatus(snap)
			d.stats[oldW.id] = snap
		} else if _, buildFailed := buildFailedIDs[oldW.id]; buildFailed {
			// Preserve build-failure status recorded earlier in this reconcile pass (regardless of
			// reason: config_invalid, ssrf_blocked, unsupported_channel, etc.) while merging drained counts.
			existing := d.stats[oldW.id]
			existing.TotalDispatched = snap.TotalDispatched
			existing.Dropped = snap.Dropped
			if snap.LastSuccessAt != nil && (existing.LastSuccessAt == nil || snap.LastSuccessAt.After(*existing.LastSuccessAt)) {
				t := *snap.LastSuccessAt
				existing.LastSuccessAt = &t
			}
			d.stats[oldW.id] = existing
		} else {
			d.stats[oldW.id] = snap
		}
	}
	for id := range d.stats {
		if _, stillExists := existingIDs[id]; !stillExists {
			delete(d.stats, id)
		}
	}
	d.statsMu.Unlock()

	d.seqMu.Lock()
	for id := range d.seqMap {
		if _, stillExists := existingIDs[id]; !stillExists {
			delete(d.seqMap, id)
		}
	}
	d.seqMu.Unlock()

	for _, w := range toStart {
		w.start()
	}
}

func (d *Dispatcher) buildSender(
	dest *AlertDestination,
) (Sender, destinationFilter, time.Duration, error) {
	var filter destinationFilter
	if dest.FilterJSON != "" {
		if err := json.Unmarshal([]byte(dest.FilterJSON), &filter); err != nil {
			return nil, filter, 0, fmt.Errorf("unmarshal destination filter: %w", err)
		}
	}
	if mappedLegacy, unknown := filter.normalize(); mappedLegacy || len(unknown) > 0 {
		d.logger.Warn("normalized persisted destination filter",
			zap.String("destination_id", dest.ID),
			zap.Bool("mapped_legacy_event_types", mappedLegacy),
			zap.Strings("unknown_event_types", unknown),
		)
	}

	switch dest.ChannelType {
	case ChannelTypeWebhook:
		sender, interval, err := d.buildWebhookSender(dest)
		return sender, filter, interval, err
	case ChannelTypeMQTT:
		sender, interval, err := d.buildMQTTSender(dest)
		return sender, filter, interval, err
	default:
		return nil, filter, 0, errors.New("unsupported channel type")
	}
}

func (d *Dispatcher) buildWebhookSender(dest *AlertDestination) (Sender, time.Duration, error) {
	var cfg webhookConfig
	if dest.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(dest.ConfigJSON), &cfg); err != nil {
			return nil, 0, fmt.Errorf("unmarshal webhook config: %w", err)
		}
	}
	secret, err := d.store.DecryptWebhookSecret(dest.ID, dest.SecretCiphertext)
	if err != nil {
		return nil, 0, err
	}

	interval := time.Duration(normalizeHeartbeatInterval(cfg.HeartbeatIntervalSeconds)) * time.Second
	sender, err := NewWebhookSender(WebhookSenderOptions{
		ID:                dest.ID,
		Name:              dest.Name,
		ImageMode:         dest.ImageMode,
		SkipImageURLCheck: !dest.EventsEnabled,
		PublicBaseURL:     d.config.PublicBaseURL,
		SignedURLTTL:      d.config.SignedURLTTL,
		Signer:            d.signer,
		Media:             d.media,
		GalleryMedia:      d.galleryMedia,
		URL:               secret.URL,
		HeartbeatURL:      secret.HeartbeatURL,
		AuthType:          cfg.AuthType,
		Token:             secret.Token,
		Username:          secret.Username,
		Password:          secret.Password,
		SigningSecret:     secret.SigningSecret,
		Headers:           secret.Headers,
		Timeout:           time.Duration(cfg.TimeoutSeconds) * time.Second,
		TLSSkipVerify:     cfg.TLSSkipVerify,
		AllowLoopback:     d.config.AllowLoopback,
		Identity:          d.config.DeviceID,
		Logger:            d.logger,
	})
	return sender, interval, err
}

func (d *Dispatcher) buildMQTTSender(dest *AlertDestination) (Sender, time.Duration, error) {
	var cfg mqttConfig
	if dest.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(dest.ConfigJSON), &cfg); err != nil {
			return nil, 0, fmt.Errorf("unmarshal mqtt config: %w", err)
		}
	}
	secret, err := d.store.DecryptMQTTSecret(dest.ID, dest.SecretCiphertext)
	if err != nil {
		return nil, 0, err
	}

	var clientID string
	if dest.MQTTClientID != nil {
		clientID = *dest.MQTTClientID
	}

	interval := time.Duration(normalizeHeartbeatInterval(cfg.HeartbeatIntervalSeconds)) * time.Second
	counter := d.counterFor(dest.ID)
	s, err := NewMQTTSender(MQTTSenderOptions{
		ID:                dest.ID,
		Name:              dest.Name,
		ImageMode:         dest.ImageMode,
		SkipImageURLCheck: !dest.EventsEnabled,
		PublicBaseURL:     d.config.PublicBaseURL,
		SignedURLTTL:      d.config.SignedURLTTL,
		Signer:            d.signer,
		Media:             d.media,
		GalleryMedia:      d.galleryMedia,
		Broker:            cfg.Broker,
		ClientID:          clientID,
		Username:          cfg.Username,
		Password:          secret.Password,
		Topic:             cfg.Topic,
		HeartbeatEnabled:  dest.HeartbeatEnabled,
		HeartbeatTopic:    cfg.HeartbeatTopic,
		LWTEnabled:        cfg.effectiveLWTEnabled(),
		QoS:               cfg.QoS,
		KeepAlive:         time.Duration(cfg.KeepAliveSeconds) * time.Second,
		TLSSkipVerify:     cfg.TLSSkipVerify,
		AllowLoopback:     d.config.AllowLoopback,
		InlineMaxBytes:    int64(normalizeInlineMaxKB(cfg.InlineMaxKB)) * 1024,
		NextSeq:           func() uint64 { return counter.Add(1) },
		Identity:          d.config.DeviceID,
		Logger:            d.logger,
	})
	if err != nil {
		return nil, 0, err
	}
	return s, interval, nil
}

type destinationWorkerOptions struct {
	dest                    *AlertDestination
	sender                  Sender
	filter                  destinationFilter
	fingerprint             string
	heartbeatInterval       time.Duration
	noJitter                bool
	failureBackoffThreshold int64
	failureBackoffBase      time.Duration
	failureBackoffMax       time.Duration
	snapshotProvider        HeartbeatSnapshotProvider
	publicBaseURL           string
	seqCounter              *atomic.Uint64
	sourceResolver          SourceResolver
	onWillStale             func()
	initialStatus           *AlertDestinationStatus
	logger                  *zap.Logger
}

type destinationWorker struct {
	id                      string
	name                    string
	channelType             string
	eventsEnabled           bool
	heartbeatEnabled        bool
	heartbeatInterval       time.Duration
	noJitter                bool
	failureBackoffThreshold int64
	failureBackoffBase      time.Duration
	failureBackoffMax       time.Duration
	snapshotProvider        HeartbeatSnapshotProvider
	publicBaseURL           string
	seqCounter              *atomic.Uint64
	sourceResolver          SourceResolver
	onWillStale             func()
	filter                  destinationFilter
	sender                  Sender
	queue                   chan queuedEvent
	backoffResetCh          chan struct{}
	// enqueueMu makes seq assignment and enqueue one step, so concurrent Dispatch calls
	// cannot reorder sequence numbers inside the queue.
	enqueueMu           sync.Mutex
	totalDispatched     atomic.Int64
	dropped             atomic.Int64
	state               atomic.Value // string
	lastError           atomic.Value // string
	lastAttemptAt       atomic.Pointer[time.Time]
	lastSuccessAt       atomic.Pointer[time.Time]
	lastFailureAt       atomic.Pointer[time.Time]
	consecutiveFailures atomic.Int64
	fingerprint         string
	started             atomic.Bool
	suppressOffline     atomic.Bool
	stopOnce            sync.Once
	stopCh              chan struct{}
	wg                  sync.WaitGroup
	doneCh              chan struct{}
	logger              *zap.Logger
}

func newDestinationWorker(opts destinationWorkerOptions) *destinationWorker {
	eventsEnabled := opts.dest.EventsEnabled
	heartbeatEnabled := opts.dest.HeartbeatEnabled
	if !eventsEnabled && !heartbeatEnabled {
		// Legacy unit test struct literals omit both capability flags; treat as events-only.
		eventsEnabled = true
	}

	hbInterval := opts.heartbeatInterval
	if hbInterval <= 0 {
		hbInterval = time.Duration(DefaultHeartbeatIntervalSeconds) * time.Second
	}

	seqCounter := opts.seqCounter
	if seqCounter == nil {
		seqCounter = &atomic.Uint64{}
	}

	backoffThreshold := opts.failureBackoffThreshold
	if backoffThreshold <= 0 {
		backoffThreshold = defaultFailureBackoffThreshold
	}
	backoffBase := opts.failureBackoffBase
	if backoffBase <= 0 {
		backoffBase = defaultFailureBackoffBase
	}
	backoffMax := opts.failureBackoffMax
	if backoffMax <= 0 {
		backoffMax = defaultFailureBackoffMax
	}

	w := &destinationWorker{
		id:                      opts.dest.ID,
		name:                    opts.dest.Name,
		channelType:             opts.dest.ChannelType,
		eventsEnabled:           eventsEnabled,
		heartbeatEnabled:        heartbeatEnabled,
		heartbeatInterval:       hbInterval,
		noJitter:                opts.noJitter,
		failureBackoffThreshold: backoffThreshold,
		failureBackoffBase:      backoffBase,
		failureBackoffMax:       backoffMax,
		snapshotProvider:        opts.snapshotProvider,
		publicBaseURL:           opts.publicBaseURL,
		seqCounter:              seqCounter,
		sourceResolver:          opts.sourceResolver,
		onWillStale:             opts.onWillStale,
		filter:                  opts.filter,
		sender:                  opts.sender,
		queue:                   make(chan queuedEvent, workerQueueCapacity),
		backoffResetCh:          make(chan struct{}, 1),
		fingerprint:             opts.fingerprint,
		stopCh:                  make(chan struct{}),
		doneCh:                  make(chan struct{}),
		logger:                  opts.logger.With(zap.String("destination_id", opts.dest.ID)),
	}

	initialState := StateUnverified
	if opts.dest.ChannelType == ChannelTypeMQTT {
		initialState = StateConnecting
	}
	w.state.Store(initialState)
	w.lastError.Store("")

	// Carry forward cumulative counters and historical timestamps across hot-reloads
	// while resetting health state so updated config is evaluated cleanly.
	if st := opts.initialStatus; st != nil {
		w.inheritStatus(*st)
	}
	return w
}

// inheritStatus merges cumulative counters and historical timestamps from a retired
// worker (after its drain completes) without clobbering any drops already accrued while
// the replacement worker waited to start.
func (w *destinationWorker) inheritStatus(st AlertDestinationStatus) {
	if st.TotalDispatched > 0 {
		w.totalDispatched.Add(st.TotalDispatched)
	}
	if st.Dropped > 0 {
		w.dropped.Add(st.Dropped)
	}
	if st.LastAttemptAt != nil && w.lastAttemptAt.Load() == nil {
		t := *st.LastAttemptAt
		w.lastAttemptAt.Store(&t)
	}
	if st.LastSuccessAt != nil && w.lastSuccessAt.Load() == nil {
		t := *st.LastSuccessAt
		w.lastSuccessAt.Store(&t)
	}
	if st.LastFailureAt != nil && w.lastFailureAt.Load() == nil {
		t := *st.LastFailureAt
		w.lastFailureAt.Store(&t)
	}
}

func (w *destinationWorker) start() {
	if !w.started.CompareAndSwap(false, true) {
		return
	}

	hbCtx, hbCancel := context.WithCancel(context.Background())
	hbStopDone := make(chan struct{})
	go func() {
		defer close(hbStopDone)
		select {
		case <-w.stopCh:
			hbCancel()
		case <-hbCtx.Done():
		}
	}()

	if mqttSender, ok := w.sender.(*MQTTSender); ok {
		w.initMQTTConnection(hbCtx, mqttSender)
	}

	w.wg.Add(1)
	go w.runEvents()

	if w.heartbeatEnabled {
		w.wg.Add(1)
		go w.runHeartbeats(hbCtx)
	}

	go func() {
		w.wg.Wait()
		hbCancel()
		<-hbStopDone
		w.closeSender()
		close(w.doneCh)
	}()
}

func (w *destinationWorker) initMQTTConnection(stopCtx context.Context, mqttSender *MQTTSender) {
	mqttSender.SetConnectionHooks(
		func() {
			w.recordSuccess()
		},
		func(lostErr error) {
			reason := classifyError(lostErr)
			if reason == "" {
				reason = "not_connected"
			}
			w.recordFailure(reason)
			w.logger.Warn("mqtt broker connection lost",
				zap.String("reason", reason),
				zap.Error(lostErr),
			)
		},
	)

	if mqttSender.IsConnected() {
		w.recordSuccess()
		return
	}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		if connectErr := mqttSender.ConnectContext(stopCtx); connectErr != nil {
			if stopCtx.Err() != nil {
				return
			}
			reason := classifyError(connectErr)
			w.recordFailure(reason)
			w.logger.Debug("initial mqtt connect attempt failed", zap.Error(connectErr))
		}
	}()
}

func (w *destinationWorker) runEvents() {
	defer w.wg.Done()

	// Bind in-flight deliveries and retirement drain to a single bounded grace window
	// after stopCh closes so slow Webhook retries (up to 92s) can never breach the
	// host's 60s shutdown timeout.
	eventCtx, eventCancel := context.WithCancel(context.Background())
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-w.stopCh:
			timer := time.NewTimer(retirementDrainTimeout)
			defer timer.Stop()
			select {
			case <-timer.C:
				eventCancel()
			case <-eventCtx.Done():
			}
		case <-eventCtx.Done():
		}
	}()
	defer func() {
		eventCancel()
		<-watcherDone
	}()

	for {
		if cooldown := w.failureCooldownDuration(); cooldown > 0 {
			timer := time.NewTimer(cooldown)
			select {
			case <-w.stopCh:
				timer.Stop()
				w.drainQueuedEvents(eventCtx)
				return
			case <-w.backoffResetCh:
				timer.Stop()
			case <-timer.C:
			}
		}

		select {
		case <-w.stopCh:
			w.drainQueuedEvents(eventCtx)
			return
		case qe := <-w.queue:
			ctx, cancel := context.WithTimeout(eventCtx, w.eventSendBudget())
			err := w.sender.Send(ctx, w.delivery(ctx, qe))
			cancel()

			switch {
			case err == nil:
				w.totalDispatched.Add(1)
				w.recordSuccess()
			case eventCtx.Err() != nil:
				w.logger.Debug("in-flight dispatch canceled on worker shutdown", zap.Error(err))
			default:
				reason := classifyError(err)
				w.recordFailure(reason)
				w.logger.Warn("destination dispatch failed", zap.Error(err))
			}
		}
	}
}

func (w *destinationWorker) failureCooldownDuration() time.Duration {
	failures := w.consecutiveFailures.Load()
	if failures < w.failureBackoffThreshold {
		return 0
	}
	shift := failures - w.failureBackoffThreshold
	if shift > 10 {
		shift = 10
	}
	delay := w.failureBackoffBase << shift
	if delay <= 0 || delay > w.failureBackoffMax {
		return w.failureBackoffMax
	}
	return delay
}

func (w *destinationWorker) drainQueuedEvents(eventCtx context.Context) {
	drainCtx, cancel := context.WithTimeout(eventCtx, retirementDrainTimeout)
	defer cancel()
	for {
		if drainCtx.Err() != nil {
			w.discardRemainingQueueOnTimeout()
			return
		}
		select {
		case qe := <-w.queue:
			if err := w.sender.Send(drainCtx, w.delivery(drainCtx, qe)); err != nil {
				w.logger.Debug("failed to send drained event on retirement", zap.Error(err))
			} else {
				w.totalDispatched.Add(1)
			}
		case <-drainCtx.Done():
			w.discardRemainingQueueOnTimeout()
			return
		default:
			return
		}
	}
}

func (w *destinationWorker) discardRemainingQueueOnTimeout() {
	var discarded int64
	for {
		select {
		case <-w.queue:
			discarded++
		default:
			if discarded > 0 {
				w.dropped.Add(discarded)
				w.logger.Warn("dropped queued events after retirement drain timeout",
					zap.Int64("dropped_count", discarded),
				)
			}
			return
		}
	}
}

// enqueue assigns the next sequence number and queues the event without blocking. When
// the queue is full the oldest event is dropped; its sequence number becomes a gap the
// receiver can observe.
func (w *destinationWorker) enqueue(ev record.AlarmEvent) {
	w.enqueueMu.Lock()
	defer w.enqueueMu.Unlock()

	qe := queuedEvent{ev: ev, seq: w.seqCounter.Add(1)}
	select {
	case w.queue <- qe:
		return
	default:
	}
	select {
	case <-w.queue:
		w.dropped.Add(1)
	default:
	}
	select {
	case w.queue <- qe:
	default:
		w.dropped.Add(1)
	}
}

// delivery resolves source metadata off the Dispatch hot path. The lookup gets its own
// short deadline derived from parent so it can neither outlive the send nor stall it.
func (w *destinationWorker) delivery(parent context.Context, qe queuedEvent) Delivery {
	d := Delivery{Event: qe.ev, Seq: qe.seq}
	if w.sourceResolver == nil {
		return d
	}
	ctx, cancel := context.WithTimeout(parent, sourceResolveTimeout)
	defer cancel()
	d.Source = w.sourceResolver.ResolveSource(ctx, qe.ev.CameraID, qe.ev.AlgorithmID)
	return d
}

func (w *destinationWorker) runHeartbeats(stopCtx context.Context) {
	defer w.wg.Done()

	var prevErrReason string
	timer := time.NewTimer(w.initialHeartbeatDelay())
	defer timer.Stop()

	for {
		select {
		case <-w.stopCh:
			return
		case <-timer.C:
			if mqttSender, ok := w.sender.(*MQTTSender); ok && mqttSender.hasStaleWillDeviceID() {
				if w.onWillStale != nil {
					w.onWillStale()
					timer.Reset(min(5*time.Second, w.nextHeartbeatDelay()))
					continue
				}
			}

			sendCtx, cancel := context.WithTimeout(stopCtx, w.heartbeatSendBudget())
			snap := resolveHeartbeatSnapshot(sendCtx, w.snapshotProvider, w.publicBaseURL)
			seq := w.seqCounter.Add(1)
			err := w.sender.SendHeartbeat(sendCtx, snap, seq)
			cancel()

			if stopCtx.Err() != nil {
				return
			}

			prevErrReason = w.recordHeartbeatOutcome(err, prevErrReason)
			timer.Reset(w.nextHeartbeatDelay())
		}
	}
}

func (w *destinationWorker) recordHeartbeatOutcome(err error, prevReason string) string {
	if err == nil {
		w.recordSuccess()
		if prevReason != "" {
			w.logger.Info("destination heartbeat recovered")
		}
		return ""
	}

	reason := classifyError(err)
	w.recordFailure(reason)
	if prevReason != reason {
		w.logger.Warn("destination heartbeat failed",
			zap.String("reason", reason),
			zap.Error(err),
		)
		return reason
	}
	w.logger.Debug("destination heartbeat still failing",
		zap.String("reason", reason),
		zap.Error(err),
	)
	return reason
}

func (w *destinationWorker) recordSuccess() {
	now := time.Now().UTC()
	w.state.Store(StateConnected)
	w.lastError.Store("")
	w.consecutiveFailures.Store(0)
	w.lastAttemptAt.Store(&now)
	w.lastSuccessAt.Store(&now)
	select {
	case w.backoffResetCh <- struct{}{}:
	default:
	}
}

func (w *destinationWorker) recordFailure(reason string) {
	now := time.Now().UTC()
	w.state.Store(StateFailed)
	w.lastError.Store(reason)
	w.consecutiveFailures.Add(1)
	w.lastAttemptAt.Store(&now)
	w.lastFailureAt.Store(&now)
}

func (w *destinationWorker) eventSendBudget() time.Duration {
	if b, ok := w.sender.(timeoutBudgets); ok {
		if d := b.EventSendBudget(); d > 0 {
			return d
		}
	}
	return defaultSendBudget
}

func (w *destinationWorker) heartbeatSendBudget() time.Duration {
	if b, ok := w.sender.(timeoutBudgets); ok {
		if d := b.HeartbeatSendBudget(); d > 0 {
			return d
		}
	}
	return defaultSendBudget
}

func (w *destinationWorker) initialHeartbeatDelay() time.Duration {
	if w.noJitter {
		return 0
	}
	maxJitter := min(2*time.Second, w.heartbeatInterval/10)
	if maxJitter <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(maxJitter)))
}

func (w *destinationWorker) nextHeartbeatDelay() time.Duration {
	if w.noJitter {
		return w.heartbeatInterval
	}
	window := w.heartbeatInterval / 20 // ±5%
	if window <= 0 {
		return w.heartbeatInterval
	}
	delta := rand.Int64N(int64(window)*2+1) - int64(window)
	return w.heartbeatInterval + time.Duration(delta)
}

func (w *destinationWorker) stopForReload() {
	w.suppressOffline.Store(true)
	w.stop()
}

func (w *destinationWorker) stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})

	if w.started.Load() {
		<-w.doneCh
		return
	}
	w.closeSender()
}

func (w *destinationWorker) closeSender() {
	if w.sender == nil {
		return
	}
	if w.suppressOffline.Load() {
		if mqttSender, ok := w.sender.(*MQTTSender); ok {
			if closeErr := mqttSender.closeWithoutOffline(); closeErr != nil {
				w.logger.Debug("close destination mqtt sender on reload", zap.Error(closeErr))
			}
			return
		}
	}
	if closeErr := w.sender.Close(); closeErr != nil {
		w.logger.Debug("close destination sender", zap.Error(closeErr))
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
		State:               st,
		LastError:           lastErr,
		LastAttemptAt:       w.lastAttemptAt.Load(),
		LastSuccessAt:       w.lastSuccessAt.Load(),
		LastFailureAt:       w.lastFailureAt.Load(),
		ConsecutiveFailures: w.consecutiveFailures.Load(),
		TotalDispatched:     w.totalDispatched.Load(),
		Dropped:             w.dropped.Load(),
		Queued:              int64(len(w.queue)),
		QueueCapacity:       workerQueueCapacity,
	}
}

func computeFingerprint(dest *AlertDestination, currentDeviceID string) string {
	var clientID string
	if dest.MQTTClientID != nil {
		clientID = *dest.MQTTClientID
	}

	// Include device_id when MQTT heartbeat is active so editing host identity
	// triggers a clean reconnect that refreshes the broker's LWT will payload.
	var lwtIdentity string
	if dest.ChannelType == ChannelTypeMQTT && dest.HeartbeatEnabled {
		lwtIdentity = currentDeviceID
	}

	h := sha256.New()
	for _, part := range []string{
		dest.ChannelType,
		strconv.FormatBool(dest.EventsEnabled),
		strconv.FormatBool(dest.HeartbeatEnabled),
		dest.ImageMode,
		clientID,
		lwtIdentity,
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
	isTimeout := errors.Is(err, errMQTTConnectTimeout) ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "timed out") ||
		strings.Contains(errStr, "deadline exceeded")
	isTLS := strings.Contains(errStr, "certificate") ||
		strings.Contains(errStr, "tls") ||
		strings.Contains(errStr, "x509")
	isAuth := strings.Contains(errStr, "status 401") ||
		strings.Contains(errStr, "status 403") ||
		strings.Contains(errStr, "not authorized") ||
		strings.Contains(errStr, "bad user name or password")
	isNotConnected := errors.Is(err, ErrMQTTNotConnected) || strings.Contains(errStr, "not connected")
	isPublishFailed := errors.Is(err, ErrMQTTPublishFailed) || strings.Contains(errStr, "publish")
	isConfigInvalid := errors.Is(err, ErrPublicBaseURLMissing) ||
		errors.Is(err, ErrPublicBaseURLNotConfigured) ||
		errors.Is(err, ErrWebhookURLMissing) ||
		errors.Is(err, ErrMQTTBrokerMissing) ||
		errors.Is(err, ErrMQTTTopicMissing) ||
		errors.Is(err, ErrMQTTWildcardTopic) ||
		errors.Is(err, ErrInvalidMQTTScheme) ||
		errors.Is(err, ErrInvalidConfig) ||
		errors.Is(err, ErrInvalidImageMode)

	switch {
	case errors.Is(err, ErrInvalidChannelType):
		return "unsupported_channel"
	case errors.Is(err, ErrSSRFBlocked):
		return "ssrf_blocked"
	case isConfigInvalid:
		return "config_invalid"
	case errors.Is(err, ErrCrossOriginRedirect):
		return "cross_origin_redirect"
	case errors.Is(err, ErrWebhookRedirectMethodChanged),
		errors.Is(err, ErrWebhookTooManyRedirects),
		errors.Is(err, ErrWebhookHTTPSDowngrade):
		return "redirect_blocked"
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
