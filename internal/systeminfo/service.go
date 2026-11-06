package systeminfo

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

const (
	// sampleInterval is the telemetry cadence. It doubles as the nominal window used
	// to turn monotonic byte/tick counters into per-second rates.
	sampleInterval = 2 * time.Second

	// sampleTimeout bounds one collector sweep so a wedged procfs read cannot pin a
	// sampling goroutine indefinitely.
	sampleTimeout = 1500 * time.Millisecond

	// historyPoints is how many samples the trend chart keeps:
	// historyPoints * sampleInterval = 3 minutes.
	historyPoints = 90
)

// errTelemetryUnavailable reports that the collector produced no sample at all.
var errTelemetryUnavailable = apperr.New(
	apperr.KindUnavailable,
	CodeTelemetryUnavailable,
	"system telemetry is unavailable",
	nil,
)

// Service coordinates system hardware telemetry sampling and historical aggregation.
//
// Immutability contract: a *rawHardwareSnapshot published to latestResponse or
// prevSnapshot is never mutated again. Every projection is built from scalar copies
// plus read-only reuse of the snapshot's slices, which is what makes handing the
// cached response to HTTP handlers without a deep copy safe.
type Service struct {
	collector  collector
	ringBuffer *ringBuffer
	logger     *zap.Logger

	mu             sync.RWMutex
	prevSnapshot   *rawHardwareSnapshot
	latestResponse *SystemHardwareResponse

	// sampleGroup collapses concurrent demand-driven samples (every GET that lands
	// before the first scheduled tick) into one collector sweep instead of one per
	// request.
	sampleGroup singleflight.Group

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	drained   chan struct{}
	doneWg    sync.WaitGroup
}

// NewService creates a new system hardware monitoring service.
func NewService(c collector, logger *zap.Logger) *Service {
	if c == nil {
		c = newDefaultCollector()
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{
		collector:  c,
		ringBuffer: newRingBuffer(historyPoints),
		logger:     logger,
		stopCh:     make(chan struct{}),
		drained:    make(chan struct{}),
	}
}

// Start begins background sampling.
//
// The initial sample is best-effort: a host whose collector fails once must still
// boot (it has cameras to run), and GetHardwareStatus retries on demand.
func (s *Service) Start(ctx context.Context) error {
	s.startOnce.Do(func() {
		sampleCtx, cancel := context.WithTimeout(ctx, sampleTimeout)
		defer cancel()

		if err := s.sample(sampleCtx); err != nil {
			s.logger.Warn("initial hardware telemetry sample failed", zap.Error(err))
		}

		s.doneWg.Add(1)
		go s.samplingLoop()
	})
	return nil
}

// Stop terminates the background sampling loop cleanly and idempotently.
func (s *Service) Stop(ctx context.Context) error {
	s.stopOnce.Do(func() {
		close(s.stopCh)
		go func() {
			s.doneWg.Wait()
			close(s.drained)
		}()
	})

	select {
	case <-s.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) samplingLoop() {
	defer s.doneWg.Done()

	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			sampleCtx, cancel := context.WithTimeout(context.Background(), sampleTimeout)
			err := s.sample(sampleCtx)
			cancel()
			if err != nil {
				s.logger.Debug("hardware telemetry sample failed", zap.Error(err))
			}
		}
	}
}

// sample collects raw counters and publishes the derived response.
func (s *Service) sample(ctx context.Context) error {
	curr, err := s.collector.CollectSnapshot(ctx)
	if err != nil {
		return err
	}

	s.mu.RLock()
	prev := s.prevSnapshot
	s.mu.RUnlock()

	// All rate arithmetic and history assembly happens outside the write lock; only
	// the two pointer swaps below need it, so readers never queue behind a full
	// response build.
	resp, point := buildHardwareResponse(prev, curr)
	s.ringBuffer.push(point)
	resp.History = s.ringBuffer.snapshot()

	s.mu.Lock()
	s.prevSnapshot = curr
	s.latestResponse = resp
	s.mu.Unlock()

	return nil
}

// buildHardwareResponse derives the API projection from two consecutive raw
// snapshots, plus the history point the caller appends to the ring buffer.
//
// prev may be nil: the first sample of a process then reports zero rates instead of
// omitting the network table.
func buildHardwareResponse(prev, curr *rawHardwareSnapshot) (*SystemHardwareResponse, HardwareHistoryPoint) {
	durationSec := sampleInterval.Seconds()
	if prev != nil && curr.Timestamp.After(prev.Timestamp) {
		durationSec = curr.Timestamp.Sub(prev.Timestamp).Seconds()
	}

	coreUsages := make([]float64, len(curr.CPUCores))
	for i, core := range curr.CPUCores {
		if prev != nil && i < len(prev.CPUCores) {
			coreUsages[i] = calculateUsage(prev.CPUCores[i], core)
		}
	}

	var cpuUsage float64
	if prev != nil {
		cpuUsage = calculateUsage(prev.CPUTotal, curr.CPUTotal)
	}

	network, rxRateSum, txRateSum := buildNetworkStatuses(prev, curr, durationSec)

	// Both projections already carry the NPU reading; only the CPU probe needs the
	// SoC fallback because some SoCs name their sole die sensor "soc-thermal".
	cpuTemperature := firstTemperature(curr.Thermals, "cpu", "soc")

	point := HardwareHistoryPoint{
		Timestamp:       curr.Timestamp,
		CPUUsagePercent: cpuUsage,
		MemUsagePercent: roundPercent(curr.Memory.UsedPercent),
		NPULoadPercent:  roundPercent(curr.NPU.LoadPercent),
		NetRxRate:       rxRateSum,
		NetTxRate:       txRateSum,
		Temperature:     maxTemperature(curr.Thermals, curr.NPU.Temperature),
	}

	return &SystemHardwareResponse{
		UpdatedAt: curr.Timestamp,
		Host:      curr.Host,
		CPU: CPUStatus{
			ModelName:    curr.CPUModel,
			Cores:        len(curr.CPUCores),
			UsagePercent: cpuUsage,
			CoreUsages:   coreUsages,
			Temperature:  cpuTemperature,
		},
		Memory:  curr.Memory,
		Storage: curr.Storage,
		Network: network,
		Heterogeneous: HeterogeneousStatus{
			NPU:          curr.NPU,
			Temperatures: curr.Thermals,
		},
	}, point
}

// buildNetworkStatuses projects interface counters into rates, sorted by name so the
// UI table does not reshuffle when the kernel reorders /proc/net/dev.
func buildNetworkStatuses(
	prev, curr *rawHardwareSnapshot,
	durationSec float64,
) ([]NetworkInterfaceStatus, uint64, uint64) {
	statuses := make([]NetworkInterfaceStatus, 0, len(curr.Network))
	var rxRateSum, txRateSum uint64

	for name, currNet := range curr.Network {
		var rxRate, txRate uint64
		if prev != nil {
			// Reading a nil map is legal in Go, so the first sample needs no branch.
			if prevNet, ok := prev.Network[name]; ok {
				rxRate = byteRate(prevNet.RxTotalBytes, currNet.RxTotalBytes, durationSec)
				txRate = byteRate(prevNet.TxTotalBytes, currNet.TxTotalBytes, durationSec)
			}
		}

		statuses = append(statuses, NetworkInterfaceStatus{
			Interface:    currNet.Interface,
			RxBytesRate:  rxRate,
			TxBytesRate:  txRate,
			RxTotalBytes: currNet.RxTotalBytes,
			TxTotalBytes: currNet.TxTotalBytes,
			IsUp:         currNet.IsUp,
		})
		rxRateSum += rxRate
		txRateSum += txRate
	}

	slices.SortFunc(statuses, func(a, b NetworkInterfaceStatus) int {
		return strings.Compare(a.Interface, b.Interface)
	})

	return statuses, rxRateSum, txRateSum
}

// byteRate converts two cumulative counter readings into a per-second rate.
//
// A counter that went backwards means the interface (or the container wrapping it)
// was recreated between samples. That sample reports 0 rather than letting uint64
// underflow into a rate of 18 exabytes per second.
func byteRate(prev, curr uint64, durationSec float64) uint64 {
	if curr < prev || durationSec <= 0 {
		return 0
	}
	return uint64(float64(curr-prev) / durationSec)
}

// firstTemperature returns the reading of the first sensor matching any keyword, in
// keyword order.
func firstTemperature(thermals []TemperatureInfo, keywords ...string) float64 {
	for _, keyword := range keywords {
		if temperature, ok := findThermalByPattern(thermals, keyword); ok {
			return temperature
		}
	}
	return 0
}

// maxTemperature reports the hottest reading across thermal zones and the NPU probe.
func maxTemperature(thermals []TemperatureInfo, npuTemperature float64) float64 {
	hottest := npuTemperature
	for _, thermal := range thermals {
		hottest = max(hottest, thermal.Temperature)
	}
	return hottest
}

// GetHardwareStatus returns the latest hardware telemetry snapshot along with history.
func (s *Service) GetHardwareStatus(ctx context.Context) (*SystemHardwareResponse, error) {
	if resp := s.cachedResponse(); resp != nil {
		return resp, nil
	}

	// Nothing published yet: the request landed before the first scheduled tick, or
	// that tick failed. Take one on demand, sharing the sweep with concurrent callers.
	//
	// The sweep runs on a context detached from this caller's cancellation: the work
	// is shared, so one client hanging up must not fail the requests that joined it.
	// sampleTimeout still bounds it.
	_, err, _ := s.sampleGroup.Do("snapshot", func() (any, error) {
		if s.cachedResponse() != nil {
			return nil, nil
		}
		sampleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sampleTimeout)
		defer cancel()
		return nil, s.sample(sampleCtx)
	})
	if err != nil {
		return nil, err
	}

	if resp := s.cachedResponse(); resp != nil {
		return resp, nil
	}
	return nil, errTelemetryUnavailable
}

// cachedResponse hands out a shallow copy so a caller cannot swap fields on the
// cached response. The nested slices are shared: they are only written while a fresh
// response is built (see the immutability contract on Service).
func (s *Service) cachedResponse() *SystemHardwareResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.latestResponse == nil {
		return nil
	}
	resp := *s.latestResponse
	return &resp
}
