package gb28181

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"go.uber.org/zap"
)

// testClock is a settable clock. It is a pointer type because the state machine
// captures the accessor: a captured time.Time would freeze at construction, and
// "advance the clock" is the whole point of the expiry tests.
type testClock struct {
	at time.Time
}

func newTestClock(at time.Time) *testClock { return &testClock{at: at} }

func (c *testClock) now() time.Time { return c.at }

func (c *testClock) advance(d time.Duration) { c.at = c.at.Add(d) }

// newTestStateMachine wires the state machine on a migrated database with a settable
// clock, so expiry is asserted without sleeping.
func newTestStateMachine(t *testing.T, start time.Time) (*deviceStateMachine, *Store, *Runtime, *testClock, func()) {
	t.Helper()
	store, _, cleanup := setupStore(t)
	runtime := NewRuntime()
	clock := newTestClock(start)
	machine := newDeviceStateMachine(deviceStateOptions{
		Store:             store,
		Runtime:           runtime,
		KeepaliveTimeout:  3 * time.Minute,
		RegistrationGrace: 10 * time.Second,
		Now:               clock.now,
	})
	return machine, store, runtime, clock, cleanup
}

func provisionDevice(t *testing.T, store *Store, deviceID, password string) {
	t.Helper()
	if _, err := store.UpsertDevice(context.Background(), DeviceUpsert{
		DeviceID: deviceID,
		Name:     stringPtr("测试设备"),
		Password: &password,
	}); err != nil {
		t.Fatalf("provision device %s: %v", deviceID, err)
	}
}

func deviceState(t *testing.T, store *Store, deviceID string) *Device {
	t.Helper()
	device, err := store.GetDevice(context.Background(), deviceID)
	if err != nil {
		t.Fatalf("read device %s: %v", deviceID, err)
	}
	return device
}

func TestDeviceStateMachineRegisterAndUnregister(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	machine, store, runtime, clock, cleanup := newTestStateMachine(t, now)
	defer cleanup()
	ctx := context.Background()

	const deviceID = "34020000001320000001"
	provisionDevice(t, store, deviceID, "secret")

	if err := machine.Registered(ctx, Registration{
		DeviceID:       deviceID,
		Transport:      TransportUDP,
		RemoteAddr:     "192.168.1.64:5060",
		ExpiresSeconds: 3600,
		RegisteredAt:   clock.now(),
	}); err != nil {
		t.Fatalf("Registered failed: %v", err)
	}

	device := deviceState(t, store, deviceID)
	if device.State != DeviceStateOnline {
		t.Fatalf("expected online, got %q", device.State)
	}
	if device.RemoteAddr == nil || *device.RemoteAddr != "192.168.1.64:5060" {
		t.Fatalf("expected the source address to be recorded, got %v", device.RemoteAddr)
	}
	if device.RegisteredAt == nil || !device.RegisteredAt.Equal(clock.now()) {
		t.Fatalf("expected registered_at to be %s, got %v", clock.now(), device.RegisteredAt)
	}
	if snapshot := runtime.Snapshot(); snapshot.Devices.Online != 1 || snapshot.Devices.Total != 1 {
		t.Fatalf("expected 1/1 devices, got %+v", snapshot.Devices)
	}

	if err := machine.Unregistered(ctx, deviceID); err != nil {
		t.Fatalf("Unregistered failed: %v", err)
	}
	device = deviceState(t, store, deviceID)
	if device.State != DeviceStateOffline {
		t.Fatalf("expected offline after EXPIRES=0, got %q", device.State)
	}
	if device.RemoteAddr != nil {
		t.Fatalf("unregistering must clear the remote address, got %v", *device.RemoteAddr)
	}
	if snapshot := runtime.Snapshot(); snapshot.Devices.Online != 0 {
		t.Fatalf("expected zero online devices, got %+v", snapshot.Devices)
	}
}

func TestDeviceStateMachineIgnoresKeepaliveFromOfflineDevice(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	machine, store, _, clock, cleanup := newTestStateMachine(t, now)
	defer cleanup()
	ctx := context.Background()

	const deviceID = "34020000001320000002"
	provisionDevice(t, store, deviceID, "secret")

	// A keepalive alone never proves a registration: only REGISTER does.
	if err := machine.Keepalive(ctx, deviceID); err != nil {
		t.Fatalf("Keepalive returned an error: %v", err)
	}
	if device := deviceState(t, store, deviceID); device.State != DeviceStateOffline {
		t.Fatalf("a keepalive must not bring a device online, got %q", device.State)
	}
	if device := deviceState(t, store, deviceID); device.LastKeepaliveAt != nil {
		t.Fatalf("an offline device's keepalive must not be recorded, got %v", device.LastKeepaliveAt)
	}

	if err := machine.Registered(ctx, Registration{
		DeviceID:       deviceID,
		Transport:      TransportUDP,
		RemoteAddr:     "192.168.1.65:5060",
		ExpiresSeconds: 3600,
		RegisteredAt:   clock.now(),
	}); err != nil {
		t.Fatalf("Registered failed: %v", err)
	}
	if err := machine.Keepalive(ctx, deviceID); err != nil {
		t.Fatalf("Keepalive failed: %v", err)
	}
	if device := deviceState(t, store, deviceID); device.LastKeepaliveAt == nil {
		t.Fatal("expected the keepalive to be recorded for an online device")
	}
}

func TestDeviceStateMachineSweepsExpiredAndSilentDevices(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	machine, store, _, clock, cleanup := newTestStateMachine(t, now)
	defer cleanup()
	ctx := context.Background()

	const silent = "34020000001320000003"
	const shortLease = "34020000001320000004"
	const healthy = "34020000001320000005"
	for _, deviceID := range []string{silent, shortLease, healthy} {
		provisionDevice(t, store, deviceID, "secret")
	}
	register := func(deviceID string, expires int) {
		t.Helper()
		if err := machine.Registered(ctx, Registration{
			DeviceID:       deviceID,
			Transport:      TransportUDP,
			RemoteAddr:     "192.168.1.66:5060",
			ExpiresSeconds: expires,
			RegisteredAt:   clock.now(),
		}); err != nil {
			t.Fatalf("Registered(%s) failed: %v", deviceID, err)
		}
	}
	for _, deviceID := range []string{silent, shortLease, healthy} {
		register(deviceID, 3600)
	}

	// Past the keepalive window but well inside every lease: only the device that
	// never sent a keepalive may be swept.
	clock.advance(4 * time.Minute)
	if err := machine.Keepalive(ctx, healthy); err != nil {
		t.Fatalf("Keepalive failed: %v", err)
	}
	register(shortLease, minRegistrationSeconds)

	swept, err := machine.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep failed: %v", err)
	}
	if swept != 1 {
		t.Fatalf("expected only the silent device to be swept, got %d", swept)
	}
	if device := deviceState(t, store, silent); device.State != DeviceStateOffline {
		t.Errorf("expected the silent device offline, got %q", device.State)
	}
	if device := deviceState(t, store, healthy); device.State != DeviceStateOnline {
		t.Errorf("expected the keepalive device online, got %q", device.State)
	}

	// The short lease expires next, while the healthy device keeps proving liveness.
	clock.advance(2 * time.Minute)
	if _, err := machine.Sweep(ctx); err != nil {
		t.Fatalf("Sweep failed: %v", err)
	}
	if device := deviceState(t, store, shortLease); device.State != DeviceStateOffline {
		t.Errorf("expected the short lease to expire, got %q", device.State)
	}
	if device := deviceState(t, store, healthy); device.State != DeviceStateOnline {
		t.Errorf("expected the healthy device to stay online, got %q", device.State)
	}

	// A live keepalive must not keep a device online past its lease: the REGISTER is
	// what grants the lease, keepalives only prove liveness inside it.
	clock.at = time.Date(2026, 10, 25, 13, 1, 0, 0, time.UTC)
	if err := machine.Keepalive(ctx, healthy); err != nil {
		t.Fatalf("Keepalive failed: %v", err)
	}
	swept, err = machine.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep failed: %v", err)
	}
	if swept != 1 {
		t.Fatalf("expected the expired lease to be swept, got %d", swept)
	}
	if device := deviceState(t, store, healthy); device.State != DeviceStateOffline {
		t.Errorf("expected the device offline once its lease expired, got %q", device.State)
	}
}

// Stale registrations are cleared by the lifecycle, not by the state machine, because
// they must be cleared even when GB28181 is disabled (see
// TestLifecycleClearsStaleRegistrationsEvenWhenDisabled). This test covers the store
// side of that path.
func TestStoreResetOnlineStatesClearsStaleRegistrations(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	machine, store, runtime, clock, cleanup := newTestStateMachine(t, now)
	defer cleanup()
	ctx := context.Background()

	const deviceID = "34020000001320000006"
	provisionDevice(t, store, deviceID, "secret")
	if err := machine.Registered(ctx, Registration{
		DeviceID:       deviceID,
		Transport:      TransportTCP,
		RemoteAddr:     "192.168.1.68:5060",
		ExpiresSeconds: 3600,
		RegisteredAt:   clock.now(),
	}); err != nil {
		t.Fatalf("Registered failed: %v", err)
	}

	cleared, err := store.ResetOnlineStates(ctx, clock.now())
	if err != nil {
		t.Fatalf("ResetOnlineStates failed: %v", err)
	}
	if cleared != 1 {
		t.Fatalf("expected one stale registration cleared, got %d", cleared)
	}
	device := deviceState(t, store, deviceID)
	if device.State != DeviceStateOffline || device.RemoteAddr != nil {
		t.Fatalf("expected a clean offline row, got state=%q remote=%v", device.State, device.RemoteAddr)
	}
	// The runtime counters are republished by the lifecycle after the reset.
	publishDeviceCounters(ctx, store, runtime, zap.NewNop())
	if snapshot := runtime.Snapshot(); snapshot.Devices.Online != 0 {
		t.Fatalf("expected the runtime to report zero online devices, got %+v", snapshot.Devices)
	}
}

func TestStoreDevicePasswordRoundTrip(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()
	ctx := context.Background()

	const deviceID = "34020000001320000007"
	provisionDevice(t, store, deviceID, "分层密码")

	device, password, err := store.deviceForAuthentication(ctx, deviceID)
	if err != nil {
		t.Fatalf("deviceForAuthentication failed: %v", err)
	}
	if password != "分层密码" {
		t.Fatalf("expected the decrypted password, got %q", password)
	}
	if device.ExpiresSeconds != DefaultExpiresSeconds {
		t.Fatalf("expected the provisioned lease to come back, got %d", device.ExpiresSeconds)
	}
	if device.EncryptedPass != nil {
		t.Fatal("expected the ciphertext to be cleared before leaving the store")
	}

	_, _, err = store.deviceForAuthentication(ctx, "34020000001320000099")
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != CodeDeviceNotFound {
		t.Fatalf("expected %s for an unknown device, got %v", CodeDeviceNotFound, err)
	}
}

func TestDeviceStateMachineRequiresProvisionedDevice(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	machine, _, _, clock, cleanup := newTestStateMachine(t, now)
	defer cleanup()

	err := machine.Registered(context.Background(), Registration{
		DeviceID:       "34020000001320000099",
		Transport:      TransportUDP,
		RemoteAddr:     "192.168.1.69:5060",
		ExpiresSeconds: 3600,
		RegisteredAt:   clock.now(),
	})
	if !isNotFound(err) {
		t.Fatalf("expected a not-found error for an unprovisioned device, got %v", err)
	}
}
