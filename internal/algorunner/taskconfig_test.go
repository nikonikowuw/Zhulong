package algorunner

import (
	"context"
	"errors"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/algoipc"
	"go.uber.org/zap"
)

type recordedBroadcast struct {
	streamID uint64
	msgType  uint16
	payload  []byte
}

// fakeBroadcaster records every push so the test can assert stream routing and
// payload framing without a real FrameBroker.
type fakeBroadcaster struct {
	records []recordedBroadcast
	err     error
}

func (f *fakeBroadcaster) BroadcastAIMessage(_ context.Context, streamID uint64, msgType uint16, payload []byte) error {
	if f.err != nil {
		return f.err
	}
	f.records = append(f.records, recordedBroadcast{streamID: streamID, msgType: msgType, payload: payload})
	return nil
}

func TestTaskConfigBroadcasterRoutesPerStream(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	b := NewTaskConfigBroadcaster(broadcaster, zap.NewNop())

	configs := map[uint64]string{
		7: `{"threshold":0.55}`,
		3: `{"threshold":0.9,"top_k":5}`,
	}
	if err := b.BroadcastAlgorithm(context.Background(), "face_recognition", configs); err != nil {
		t.Fatalf("BroadcastAlgorithm: %v", err)
	}

	if len(broadcaster.records) != 2 {
		t.Fatalf("expected one push per stream, got %d", len(broadcaster.records))
	}
	// Deterministic order (insertion-sorted stream ids) is what makes the log sequence
	// and this assertion stable across runs.
	if broadcaster.records[0].streamID != 3 || broadcaster.records[1].streamID != 7 {
		t.Fatalf("streams pushed out of order: %d then %d",
			broadcaster.records[0].streamID, broadcaster.records[1].streamID)
	}
	for _, rec := range broadcaster.records {
		if rec.msgType != algoipc.MsgTaskConfig {
			t.Fatalf("message type = %d, want MsgTaskConfig", rec.msgType)
		}
		want := configs[rec.streamID]
		if got := string(rec.payload[algoipc.TaskConfigHeaderSize:]); got != want {
			t.Fatalf("stream %d config = %q, want %q", rec.streamID, got, want)
		}
		if got := string(rec.payload[4 : 4+len("face_recognition")]); got != "face_recognition" {
			t.Fatalf("stream %d algorithm id = %q", rec.streamID, got)
		}
	}
}

func TestTaskConfigBroadcasterSkipsInvalidJSON(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	b := NewTaskConfigBroadcaster(broadcaster, zap.NewNop())

	configs := map[uint64]string{
		1: `{"threshold":0.5}`,
		2: `threshold=0.5`, // malformed: must not reach the package
	}
	if err := b.BroadcastAlgorithm(context.Background(), "face_recognition", configs); err != nil {
		t.Fatalf("BroadcastAlgorithm: %v", err)
	}
	if len(broadcaster.records) != 1 || broadcaster.records[0].streamID != 1 {
		t.Fatalf("malformed config must be skipped, got %+v", broadcaster.records)
	}
}

func TestTaskConfigBroadcasterPropagatesFirstError(t *testing.T) {
	sentinel := errors.New("broker gone")
	broadcaster := &fakeBroadcaster{err: sentinel}
	b := NewTaskConfigBroadcaster(broadcaster, zap.NewNop())

	// A single stream failing must not stop the others, but the caller still learns
	// that something failed (it logs once per algorithm, not once per stream).
	err := b.BroadcastAlgorithm(context.Background(), "face_recognition", map[uint64]string{
		1: `{}`,
		2: `{}`,
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected the broadcast error to surface, got: %v", err)
	}
}

func TestTaskConfigBroadcasterNoopWithoutConfigs(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	b := NewTaskConfigBroadcaster(broadcaster, zap.NewNop())

	if err := b.BroadcastAlgorithm(context.Background(), "face_recognition", nil); err != nil {
		t.Fatalf("nil configs must be a no-op: %v", err)
	}
	if len(broadcaster.records) != 0 {
		t.Fatalf("no push expected, got %d", len(broadcaster.records))
	}
}

// staticTaskConfigProvider returns a fixed map, recording the algorithm it was
// asked about.
type staticTaskConfigProvider struct {
	configs map[uint64]string
	err     error
	asked   []string
}

func (p *staticTaskConfigProvider) TaskConfigsFor(_ context.Context, algorithmID string) (map[uint64]string, error) {
	p.asked = append(p.asked, algorithmID)
	return p.configs, p.err
}

// TestSupervisorPushTaskConfigs pins the pre-spawn seeding: without it a package
// that starts while no push is in flight would run on compiled defaults forever.
func TestSupervisorPushTaskConfigs(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	provider := &staticTaskConfigProvider{configs: map[uint64]string{5: `{"threshold":0.7}`}}
	s := &Supervisor{logger: zap.NewNop()}
	s.SetTaskConfig(provider, NewTaskConfigBroadcaster(broadcaster, zap.NewNop()))

	s.pushTaskConfigs("face_recognition")

	if len(provider.asked) != 1 || provider.asked[0] != "face_recognition" {
		t.Fatalf("provider called with %v", provider.asked)
	}
	if len(broadcaster.records) != 1 || broadcaster.records[0].streamID != 5 {
		t.Fatalf("expected one push to stream 5, got %+v", broadcaster.records)
	}
}

// TestSupervisorPushTaskConfigsWithoutWiringIsNoop keeps the pre-spawn hook safe
// for hosts (and tests) that never call SetTaskConfig.
func TestSupervisorPushTaskConfigsWithoutWiringIsNoop(t *testing.T) {
	s := &Supervisor{logger: zap.NewNop()}
	s.pushTaskConfigs("face_recognition") // must not panic or block
}

type staticTaskRegionProvider struct {
	staticTaskConfigProvider
	regions map[uint64]string
}

func (p *staticTaskRegionProvider) TaskRegionsFor(_ context.Context, _ string) (map[uint64]string, error) {
	return p.regions, nil
}

func TestSupervisorPushRegionsAndUnchangedParams(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	tripwires := `{"tripwires":[{"id":"entry","name":"入口",` +
		`"points":[[0,0.5],[1,0.5]],"direction":"both"}]}`
	provider := &staticTaskRegionProvider{
		staticTaskConfigProvider: staticTaskConfigProvider{
			configs: map[uint64]string{5: `{"threshold":0.7}`},
		},
		regions: map[uint64]string{5: tripwires},
	}
	s := &Supervisor{logger: zap.NewNop()}
	s.SetTaskConfig(provider, NewTaskConfigBroadcaster(broadcaster, zap.NewNop()))
	s.pushTaskConfigs("face")
	if len(broadcaster.records) != 2 {
		t.Fatalf("want region and params, got %+v", broadcaster.records)
	}
	for _, record := range broadcaster.records {
		want := provider.configs[5]
		if record.msgType == algoipc.MsgTaskRegions {
			want = provider.regions[5]
		} else if record.msgType != algoipc.MsgTaskConfig {
			t.Fatalf("unexpected type %d", record.msgType)
		}
		if got := string(record.payload[algoipc.TaskConfigHeaderSize:]); got != want {
			t.Fatalf("payload %q != %q", got, want)
		}
	}
	broadcaster.records = nil
	if err := s.taskConfigCast.BroadcastRegions(context.Background(), "face", map[uint64]string{5: `{}`}); err != nil {
		t.Fatal(err)
	}
	if string(broadcaster.records[0].payload[algoipc.TaskConfigHeaderSize:]) != "{}" {
		t.Fatal("clear snapshot missing")
	}
}
