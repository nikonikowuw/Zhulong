package alertpush

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/nikonikowuw/Zhulong/internal/record"
)

// blockingSender holds every Send until release is closed, then records deliveries.
type blockingSender struct {
	mu         sync.Mutex
	release    chan struct{}
	deliveries []Delivery
	heartbeats []uint64
}

func (b *blockingSender) Send(ctx context.Context, d Delivery) error {
	select {
	case <-b.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deliveries = append(b.deliveries, d)
	return nil
}

func (b *blockingSender) SendHeartbeat(_ context.Context, _ HeartbeatSnapshot, seq uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.heartbeats = append(b.heartbeats, seq)
	return nil
}

func (b *blockingSender) Close() error { return nil }

func (b *blockingSender) snapshot() ([]Delivery, []uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Delivery(nil), b.deliveries...), append([]uint64(nil), b.heartbeats...)
}

// slowResolver blocks until its context ends, then returns a camera name.
type slowResolver struct {
	calls atomic.Int32
}

func (r *slowResolver) ResolveSource(ctx context.Context, _, _ string) SourceInfo {
	r.calls.Add(1)
	<-ctx.Done()
	name := "late"
	return SourceInfo{Camera: &CameraSourceInfo{Name: &name}}
}

type staticResolver struct{}

func (staticResolver) ResolveSource(_ context.Context, cameraID, algorithmID string) SourceInfo {
	name := "name-" + cameraID
	version := "v-" + algorithmID
	return SourceInfo{
		Camera:    &CameraSourceInfo{ID: cameraID, Name: &name},
		Algorithm: &AlgorithmSourceInfo{ID: algorithmID, Version: &version},
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met before deadline")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func startDispatcherWithWorker(t *testing.T, w *destinationWorker) *Dispatcher {
	t.Helper()
	d := NewDispatcher(DispatcherOptions{Logger: zap.NewNop()})
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	d.mu.Lock()
	d.workers[w.id] = w
	d.mu.Unlock()
	return d
}

func TestDispatcherSeqGapOnOverflow(t *testing.T) {
	sender := &blockingSender{release: make(chan struct{})}
	w := newDestinationWorker(destinationWorkerOptions{
		dest:   &AlertDestination{ID: "seq_gap", ChannelType: ChannelTypeWebhook},
		sender: sender,
		logger: zap.NewNop(),
	})
	d := startDispatcherWithWorker(t, w)
	defer func() {
		if err := d.Stop(context.Background()); err != nil {
			t.Errorf("Stop: %v", err)
		}
	}()

	// Not started yet: fill past capacity (16) so the oldest events are dropped.
	const total = 20
	for range total {
		d.Dispatch(record.AlarmEvent{ID: "ev", CameraID: "cam"})
	}
	if w.dropped.Load() != total-16 {
		t.Fatalf("dropped = %d, want %d", w.dropped.Load(), total-16)
	}

	w.start()
	close(sender.release)
	waitFor(t, 2*time.Second, func() bool {
		got, _ := sender.snapshot()
		return len(got) == 16
	})

	got, _ := sender.snapshot()
	if got[0].Seq != total-16+1 {
		t.Fatalf("first delivered seq = %d, want %d (seq 1..4 must be gaps)", got[0].Seq, total-16+1)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Seq != got[i-1].Seq+1 {
			t.Fatalf("seq not strictly increasing at %d: %d after %d", i, got[i].Seq, got[i-1].Seq)
		}
	}
}

func TestDispatcherSeqSharedWithHeartbeatNoDuplicates(t *testing.T) {
	sender := &blockingSender{release: make(chan struct{})}
	close(sender.release)
	w := newDestinationWorker(destinationWorkerOptions{
		dest: &AlertDestination{
			ID: "seq_shared", ChannelType: ChannelTypeWebhook,
			EventsEnabled: true, HeartbeatEnabled: true,
		},
		sender:            sender,
		heartbeatInterval: 5 * time.Millisecond,
		noJitter:          true,
		logger:            zap.NewNop(),
	})
	d := startDispatcherWithWorker(t, w)
	w.start()

	var wg sync.WaitGroup
	for g := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 3 {
				d.Dispatch(record.AlarmEvent{ID: "ev", CameraID: "cam", AlgorithmID: string(rune('a' + g))})
				time.Sleep(time.Millisecond)
			}
		}()
	}
	wg.Wait()
	waitFor(t, 2*time.Second, func() bool {
		got, hbs := sender.snapshot()
		return len(got) == 12 && len(hbs) >= 3
	})
	if err := d.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	got, hbs := sender.snapshot()
	seen := map[uint64]bool{}
	for _, dlv := range got {
		if seen[dlv.Seq] {
			t.Fatalf("duplicate seq %d", dlv.Seq)
		}
		seen[dlv.Seq] = true
	}
	for _, seq := range hbs {
		if seen[seq] {
			t.Fatalf("heartbeat reused seq %d", seq)
		}
		seen[seq] = true
	}
}

func TestDispatcherResolvesSourceOffHotPath(t *testing.T) {
	sender := &blockingSender{release: make(chan struct{})}
	close(sender.release)
	w := newDestinationWorker(destinationWorkerOptions{
		dest:           &AlertDestination{ID: "src", ChannelType: ChannelTypeWebhook},
		sender:         sender,
		sourceResolver: staticResolver{},
		logger:         zap.NewNop(),
	})
	d := startDispatcherWithWorker(t, w)
	defer func() {
		if err := d.Stop(context.Background()); err != nil {
			t.Errorf("Stop: %v", err)
		}
	}()
	w.start()

	d.Dispatch(record.AlarmEvent{ID: "ev1", CameraID: "cam_9", AlgorithmID: "algo_x"})
	waitFor(t, time.Second, func() bool {
		got, _ := sender.snapshot()
		return len(got) == 1
	})
	got, _ := sender.snapshot()
	src := got[0].Source
	if src.Camera == nil || *src.Camera.Name != "name-cam_9" || *src.Algorithm.Version != "v-algo_x" {
		t.Fatalf("source = %+v", src)
	}
}

func TestDispatcherStopConvergesWithBlockingResolver(t *testing.T) {
	sender := &blockingSender{release: make(chan struct{})}
	close(sender.release)
	resolver := &slowResolver{}
	w := newDestinationWorker(destinationWorkerOptions{
		dest:           &AlertDestination{ID: "slow_src", ChannelType: ChannelTypeWebhook},
		sender:         sender,
		sourceResolver: resolver,
		logger:         zap.NewNop(),
	})
	d := startDispatcherWithWorker(t, w)
	w.start()

	for range 3 {
		d.Dispatch(record.AlarmEvent{ID: "ev", CameraID: "cam"})
	}
	waitFor(t, time.Second, func() bool { return resolver.calls.Load() >= 1 })

	start := time.Now()
	if err := d.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// One in-flight lookup (≤2s) plus the 3s retirement drain bound the shutdown.
	if elapsed := time.Since(start); elapsed > 6*time.Second {
		t.Fatalf("Stop took %v, want bounded by resolver and drain deadlines", elapsed)
	}
}
