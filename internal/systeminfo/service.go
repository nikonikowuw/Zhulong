package systeminfo

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Service coordinates system hardware telemetry sampling and historical aggregation.
type Service struct {
	collector  Collector
	ringBuffer *RingBuffer
	logger     *zap.Logger

	mu             sync.RWMutex
	prevSnapshot   *RawHardwareSnapshot
	latestResponse *SystemHardwareResponse

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	drained   chan struct{}
	doneWg    sync.WaitGroup
}

// NewService creates a new system hardware monitoring service.
func NewService(collector Collector, logger *zap.Logger) *Service {
	if collector == nil {
		collector = NewDefaultCollector()
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{
		collector:  collector,
		ringBuffer: NewRingBuffer(90), // 90 points * 2s = 3 minutes of history
		logger:     logger,
		stopCh:     make(chan struct{}),
		drained:    make(chan struct{}),
	}
}

// Start begins background sampling every 2 seconds.
func (s *Service) Start(ctx context.Context) error {
	var startErr error
	s.startOnce.Do(func() {
		// Initial sampling synchronously
		if err := s.sample(); err != nil {
			s.logger.Warn("initial hardware telemetry sample encountered error", zap.Error(err))
		}

		s.doneWg.Add(1)
		go s.samplingLoop()
	})
	return startErr
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
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			if err := s.sample(); err != nil {
				s.logger.Debug("hardware telemetry sample failed", zap.Error(err))
			}
		}
	}
}

// sample collects raw counters and computes rates against previous snapshot.
func (s *Service) sample() error {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	curr, err := s.collector.CollectSnapshot(ctx)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	prev := s.prevSnapshot
	s.prevSnapshot = curr

	var (
		cpuUsage     float64
		coreUsages   []float64
		netStatuses  []NetworkInterfaceStatus
		netRxRateSum uint64
		netTxRateSum uint64
		cpuTemp      float64
		durationSec  float64
	)

	if prev != nil {
		durationSec = curr.Timestamp.Sub(prev.Timestamp).Seconds()
		if durationSec <= 0 {
			durationSec = 2.0
		}

		// CPU total usage
		cpuUsage = CalculateUsage(prev.CPUTotal, curr.CPUTotal)

		// Per-core usages
		coreUsages = make([]float64, len(curr.CPUCores))
		for i, currCore := range curr.CPUCores {
			if i < len(prev.CPUCores) {
				coreUsages[i] = CalculateUsage(prev.CPUCores[i], currCore)
			}
		}

		// Network interfaces rates
		for iface, currNet := range curr.Network {
			var rxRate, txRate uint64
			if prevNet, ok := prev.Network[iface]; ok {
				if currNet.RxTotalBytes >= prevNet.RxTotalBytes {
					rxRate = uint64(float64(currNet.RxTotalBytes-prevNet.RxTotalBytes) / durationSec)
				}
				if currNet.TxTotalBytes >= prevNet.TxTotalBytes {
					txRate = uint64(float64(currNet.TxTotalBytes-prevNet.TxTotalBytes) / durationSec)
				}
			}
			netStatuses = append(netStatuses, NetworkInterfaceStatus{
				Interface:    currNet.Interface,
				RxBytesRate:  rxRate,
				TxBytesRate:  txRate,
				RxTotalBytes: currNet.RxTotalBytes,
				TxTotalBytes: currNet.TxTotalBytes,
				IsUp:         currNet.IsUp,
			})
			netRxRateSum += rxRate
			netTxRateSum += txRate
		}
	} else {
		// First sample: 0 rates, populate static interface structures
		coreUsages = make([]float64, len(curr.CPUCores))
		for _, currNet := range curr.Network {
			netStatuses = append(netStatuses, NetworkInterfaceStatus{
				Interface:    currNet.Interface,
				RxBytesRate:  0,
				TxBytesRate:  0,
				RxTotalBytes: currNet.RxTotalBytes,
				TxTotalBytes: currNet.TxTotalBytes,
				IsUp:         currNet.IsUp,
			})
		}
	}

	// Determine primary CPU temperature from thermals
	cpuTemp = findThermalByPattern(curr.Thermals, "cpu")
	if cpuTemp == 0 {
		cpuTemp = findThermalByPattern(curr.Thermals, "soc")
	}

	// Defensive null-safety
	if coreUsages == nil {
		coreUsages = []float64{}
	}
	if netStatuses == nil {
		netStatuses = []NetworkInterfaceStatus{}
	}
	if curr.Storage == nil {
		curr.Storage = []StorageMountStatus{}
	}
	if curr.Thermals == nil {
		curr.Thermals = []TemperatureInfo{}
	}
	if curr.NPU.CoreLoads == nil {
		curr.NPU.CoreLoads = []float64{}
	}

	cpuStatus := CPUStatus{
		ModelName:    curr.CPUModel,
		Cores:        len(curr.CPUCores),
		UsagePercent: cpuUsage,
		CoreUsages:   coreUsages,
		Temperature:  cpuTemp,
	}

	heterogeneous := HeterogeneousStatus{
		NPU:          curr.NPU,
		Temperatures: curr.Thermals,
	}

	// Push historical point
	maxTemp := cpuTemp
	if curr.NPU.Temperature > maxTemp {
		maxTemp = curr.NPU.Temperature
	}
	for _, th := range curr.Thermals {
		if th.Temperature > maxTemp {
			maxTemp = th.Temperature
		}
	}

	historyPoint := HardwareHistoryPoint{
		Timestamp:       curr.Timestamp,
		CPUUsagePercent: cpuUsage,
		MemUsagePercent: math.Round(curr.Memory.UsedPercent*10) / 10,
		NPULoadPercent:  math.Round(curr.NPU.LoadPercent*10) / 10,
		NetRxRate:       netRxRateSum,
		NetTxRate:       netTxRateSum,
		Temperature:     maxTemp,
	}
	s.ringBuffer.Push(historyPoint)

	// Deterministically sort network interfaces by name to prevent UI table jitter
	slices.SortFunc(netStatuses, func(a, b NetworkInterfaceStatus) int {
		return strings.Compare(a.Interface, b.Interface)
	})

	s.latestResponse = &SystemHardwareResponse{
		UpdatedAt:     curr.Timestamp,
		Host:          curr.Host,
		CPU:           cpuStatus,
		Memory:        curr.Memory,
		Storage:       curr.Storage,
		Network:       netStatuses,
		Heterogeneous: heterogeneous,
		History:       s.ringBuffer.Snapshot(),
	}

	return nil
}

// GetHardwareStatus returns the latest hardware telemetry snapshot along with history.
func (s *Service) GetHardwareStatus(ctx context.Context) (*SystemHardwareResponse, error) {
	s.mu.RLock()
	resp := s.latestResponse
	s.mu.RUnlock()

	if resp == nil {
		// Force an immediate sample if none exists yet
		if err := s.sample(); err != nil {
			return nil, err
		}
		s.mu.RLock()
		resp = s.latestResponse
		s.mu.RUnlock()
		if resp == nil {
			return nil, errors.New("system telemetry unavailable")
		}
	}

	result := *resp
	return &result, nil
}
