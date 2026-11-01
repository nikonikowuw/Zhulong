package gb28181

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// fakeCipher keeps the store tests independent of the camera package: the store
// only needs the Cipher contract, and the real AES implementation is covered by
// camera's own tests.
type fakeCipher struct{}

const fakeCipherPrefix = "aad:"

func (fakeCipher) Encrypt(plaintext []byte, aad string) ([]byte, error) {
	return append([]byte(fakeCipherPrefix+aad+"|"), plaintext...), nil
}

// Decrypt reverses Encrypt and enforces the AAD prefix, so a blob copied to another
// row fails here exactly like it would with the real AES-GCM cipher.
func (fakeCipher) Decrypt(ciphertext []byte, aad string) ([]byte, error) {
	prefix := []byte(fakeCipherPrefix + aad + "|")
	if !bytes.HasPrefix(ciphertext, prefix) {
		return nil, errors.New("fake cipher: AAD does not match the ciphertext")
	}
	return ciphertext[len(prefix):], nil
}

type failingCipher struct{}

func (failingCipher) Encrypt([]byte, string) ([]byte, error) {
	return nil, errors.New("encryption unavailable")
}

func (failingCipher) Decrypt([]byte, string) ([]byte, error) { return nil, nil }

// stringPtr builds the optional request fields the store distinguishes from
// "not provided".
func stringPtr(value string) *string { return &value }

func setupStore(t *testing.T) (*Store, database.DBProvider, func()) {
	t.Helper()
	dbStore := database.New(t.TempDir(), zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	return NewStore(dbStore, fakeCipher{}), dbStore, func() {
		_ = dbStore.Close()
	}
}

// readCiphertext reads the raw column so a test can prove the database never holds
// the plaintext password.
func readCiphertext(t *testing.T, store *Store, deviceID string) []byte {
	t.Helper()
	var row struct{ EncryptedPassword []byte }
	if err := store.dbProvider.DB().Raw(
		"SELECT encrypted_password FROM gb_devices WHERE device_id = ?",
		deviceID).Scan(&row).Error; err != nil {
		t.Fatalf("read ciphertext failed: %v", err)
	}
	return row.EncryptedPassword
}

func TestStoreGetConfigReturnsMigratedDefault(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()

	cfg, err := store.GetConfig(context.Background())
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}
	if cfg.Enabled {
		t.Error("expected GB28181 to be disabled by default")
	}
	if cfg.RTPPortMin != DefaultRTPPortMin || cfg.RTPPortMax != DefaultRTPPortMax {
		t.Errorf("expected default port range %d..%d, got %d..%d",
			DefaultRTPPortMin, DefaultRTPPortMax, cfg.RTPPortMin, cfg.RTPPortMax)
	}
	if cfg.SecurityProfile != SecurityProfileStandard {
		t.Errorf("expected only the standard profile by default, got %q", cfg.SecurityProfile)
	}
	if capacity := cfg.MediaCapacity(); capacity != 50 {
		t.Errorf("expected the default range to hold 50 pairs, got %d", capacity)
	}
}

func TestStoreSaveConfigRoundTrip(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()
	ctx := context.Background()

	target := DefaultConfig()
	target.Enabled = true
	target.SIPID = "34020000002000000001"
	target.SIPRealm = "3402000000"
	target.MediaAdvertisedIP = "192.168.1.10"
	target.RTPPortMin = 31000
	target.RTPPortMax = 31009

	saved, err := store.SaveConfig(ctx, target)
	if err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}
	if saved.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be stamped on save")
	}

	read, err := store.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}
	if !saved.Equal(*read) {
		t.Fatalf("round trip mismatch:\n saved %+v\n  read %+v", *saved, *read)
	}
	if read.MediaCapacity() != 5 {
		t.Errorf("expected a 10-port range to hold 5 pairs, got %d", read.MediaCapacity())
	}

	// Saving again must update the same singleton row, never insert a second one.
	target.RTPPortMax = 31019
	if _, err := store.SaveConfig(ctx, target); err != nil {
		t.Fatalf("second SaveConfig failed: %v", err)
	}
	var rows int64
	if err := store.dbProvider.DB().Model(&Config{}).Count(&rows).Error; err != nil {
		t.Fatalf("count configs failed: %v", err)
	}
	if rows != 1 {
		t.Fatalf("expected exactly one configuration row, got %d", rows)
	}
}

func TestStoreUpsertDeviceRequiresPasswordOnCreate(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()

	_, err := store.UpsertDevice(context.Background(), DeviceUpsert{DeviceID: "34020000001320000001"})
	if err == nil {
		t.Fatal("expected a missing password to be rejected")
	}
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != CodePasswordRequired {
		t.Fatalf("expected %s, got %v", CodePasswordRequired, err)
	}
}

func TestStoreUpsertDeviceRejectsNonGBDeviceIDs(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()

	password := "secret"
	for _, deviceID := range []string{
		"3402000000132000001",
		"340200000013200000010",
		"3402000000132000000X",
	} {
		t.Run(deviceID, func(t *testing.T) {
			_, err := store.UpsertDevice(context.Background(), DeviceUpsert{
				DeviceID: deviceID,
				Password: &password,
			})
			var appErr *apperr.Error
			if !errors.As(err, &appErr) || appErr.Code != CodeDeviceIDInvalid {
				t.Fatalf("expected %s for %q, got %v", CodeDeviceIDInvalid, deviceID, err)
			}
		})
	}
}

func TestStoreUpsertDeviceEncryptsAndRedacts(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()
	ctx := context.Background()

	password := "s3cret-digest"
	device, err := store.UpsertDevice(ctx, DeviceUpsert{
		DeviceID: "34020000001320000001",
		Name:     stringPtr("一楼大厅"),
		Password: &password,
	})
	if err != nil {
		t.Fatalf("UpsertDevice failed: %v", err)
	}
	// The pre-REGISTER state must be offline and the remote address unknown.
	if device.State != DeviceStateOffline {
		t.Errorf("expected offline after provisioning, got %q", device.State)
	}
	if device.RemoteAddr != nil {
		t.Errorf("expected an empty remote address before the first REGISTER, got %v", *device.RemoteAddr)
	}
	if device.Transport != TransportUDP || device.ExpiresSeconds != DefaultExpiresSeconds {
		t.Errorf("expected migration defaults to be applied, got transport=%q expires=%d",
			device.Transport, device.ExpiresSeconds)
	}

	stored := readCiphertext(t, store, device.DeviceID)
	if len(stored) == 0 {
		t.Fatal("expected ciphertext to be persisted")
	}
	if string(stored) == password {
		t.Fatal("password was persisted in plaintext")
	}
	if got, want := string(stored), fakeCipherPrefix+deviceCredentialsAAD(device.DeviceID)+"|"+password; got != want {
		t.Fatalf("expected ciphertext bound to the device AAD, got %q", got)
	}

	listed, err := store.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices failed: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected one device, got %d", len(listed))
	}
	if listed[0].EncryptedPass != nil {
		t.Fatal("list results must not carry ciphertext")
	}
}

func TestStoreUpsertDeviceKeepsPasswordWhenOmitted(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()
	ctx := context.Background()

	first := "first-password"
	device, err := store.UpsertDevice(ctx, DeviceUpsert{
		DeviceID: "34020000001320000002",
		Name:     stringPtr("原名"),
		Password: &first,
	})
	if err != nil {
		t.Fatalf("initial UpsertDevice failed: %v", err)
	}

	before := readCiphertext(t, store, device.DeviceID)

	// Renaming without a password must not wipe the credential: otherwise every
	// metadata edit would lock the device out of REGISTER.
	updated, err := store.UpsertDevice(ctx, DeviceUpsert{
		DeviceID: device.DeviceID,
		Name:     stringPtr("新名字"),
	})
	if err != nil {
		t.Fatalf("UpsertDevice without password failed: %v", err)
	}
	if updated.Name != "新名字" {
		t.Errorf("expected the name to be updated, got %q", updated.Name)
	}

	after := readCiphertext(t, store, device.DeviceID)
	if string(before) != string(after) {
		t.Fatal("omitting the password must keep the stored ciphertext")
	}
}

func TestStoreUpsertDeviceKeepsNameWhenPasswordRotated(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()
	ctx := context.Background()

	first := "first-password"
	device, err := store.UpsertDevice(ctx, DeviceUpsert{
		DeviceID: "34020000001320000006",
		Name:     stringPtr("大厅"),
		Password: &first,
	})
	if err != nil {
		t.Fatalf("initial UpsertDevice failed: %v", err)
	}
	before := readCiphertext(t, store, device.DeviceID)

	// Rotating only the secret must not erase the label: Name is optional exactly
	// like Password, and a nil name means "keep the stored value".
	second := "second-password"
	updated, err := store.UpsertDevice(ctx, DeviceUpsert{
		DeviceID: device.DeviceID,
		Password: &second,
	})
	if err != nil {
		t.Fatalf("password-only UpsertDevice failed: %v", err)
	}
	if updated.Name != "大厅" {
		t.Fatalf("rotating the password must keep the name, got %q", updated.Name)
	}
	if after := readCiphertext(t, store, device.DeviceID); string(after) == string(before) {
		t.Fatal("expected the ciphertext to change when the password is rotated")
	}
}

func TestStoreUpsertDeviceCountsPasswordCharactersNotBytes(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()
	ctx := context.Background()

	// 128 characters is the documented limit. The API binding counts runes, so a
	// multi-byte password must not be rejected here for exceeding a byte budget.
	withinLimit := strings.Repeat("密", maxPasswordLength)
	if _, err := store.UpsertDevice(ctx, DeviceUpsert{
		DeviceID: "34020000001320000007",
		Password: &withinLimit,
	}); err != nil {
		t.Fatalf("expected a %d-character password to be accepted: %v", maxPasswordLength, err)
	}

	tooLong := withinLimit + "密"
	_, err := store.UpsertDevice(ctx, DeviceUpsert{
		DeviceID: "34020000001320000008",
		Password: &tooLong,
	})
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != CodePasswordRequired {
		t.Fatalf("expected %s for an oversized password, got %v", CodePasswordRequired, err)
	}
}

func TestStoreEncryptionFailureAbortsProvisioning(t *testing.T) {
	dbStore := database.New(t.TempDir(), zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	defer func() { _ = dbStore.Close() }()

	store := NewStore(dbStore, failingCipher{})
	password := "secret"
	if _, err := store.UpsertDevice(context.Background(), DeviceUpsert{
		DeviceID: "34020000001320000003",
		Password: &password,
	}); err == nil {
		t.Fatal("expected an encryption failure to abort the write")
	}

	// A failed encryption must not leave a device row behind.
	devices, err := store.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices failed: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("expected no device rows after a failed write, got %d", len(devices))
	}
}

func TestStoreUpsertDeviceRejectsInvalidTransport(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()

	password := "secret"
	bad := "sctp"
	_, err := store.UpsertDevice(context.Background(), DeviceUpsert{
		DeviceID:  "34020000001320000004",
		Password:  &password,
		Transport: &bad,
	})
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != CodeTransportInvalid {
		t.Fatalf("expected %s, got %v", CodeTransportInvalid, err)
	}
}

func TestStoreHasExistingEncryptedData(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()
	ctx := context.Background()

	has, err := store.HasExistingEncryptedData(ctx)
	if err != nil {
		t.Fatalf("HasExistingEncryptedData failed: %v", err)
	}
	if has {
		t.Fatal("expected no encrypted device credentials on a fresh database")
	}

	password := "secret"
	if _, err := store.UpsertDevice(ctx, DeviceUpsert{
		DeviceID: "34020000001320000005",
		Password: &password,
	}); err != nil {
		t.Fatalf("UpsertDevice failed: %v", err)
	}

	// This is the bit that protects camera.key: a GB-only database must still block
	// key regeneration, otherwise every device credential is silently destroyed.
	has, err = store.HasExistingEncryptedData(ctx)
	if err != nil {
		t.Fatalf("HasExistingEncryptedData failed: %v", err)
	}
	if !has {
		t.Fatal("expected encrypted device credentials to be detected")
	}
}

func TestStoreGetDeviceNotFound(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()

	_, err := store.GetDevice(context.Background(), "34020000001320009999")
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != CodeDeviceNotFound {
		t.Fatalf("expected %s, got %v", CodeDeviceNotFound, err)
	}
}

func TestStoreOperationsFailBeforeDatabaseReady(t *testing.T) {
	store := NewStore(nil, fakeCipher{})
	if _, err := store.GetConfig(context.Background()); err == nil {
		t.Fatal("expected an error when the database provider is nil")
	}
	if _, err := store.ListDevices(context.Background()); err == nil {
		t.Fatal("expected an error when the database provider is nil")
	}
}

func TestChannelStoreOperations(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()

	ctx := context.Background()
	devID1 := "34020000001180000001"
	devID2 := "34020000001180000002"
	pwd := "testpass"

	// Provision 2 devices
	if _, err := store.UpsertDevice(ctx, DeviceUpsert{DeviceID: devID1, Password: &pwd}); err != nil {
		t.Fatalf("upsert dev1: %v", err)
	}
	if _, err := store.UpsertDevice(ctx, DeviceUpsert{DeviceID: devID2, Password: &pwd}); err != nil {
		t.Fatalf("upsert dev2: %v", err)
	}

	// 1. List channels for unknown device returns 404
	if _, err := store.ListChannels(ctx, "34020000001180009999"); err == nil {
		t.Fatalf("expected 404 for unknown device")
	}

	// 2. Insert channels for dev1
	w, h := 1920, 1080
	channels := []Channel{
		{
			DeviceID:         devID1,
			ChannelID:        "34020000001310000002",
			Name:             "Channel 2",
			ResolutionWidth:  &w,
			ResolutionHeight: &h,
		},
		{
			DeviceID:         devID1,
			ChannelID:        "34020000001310000001",
			Name:             "Channel 1",
			ResolutionWidth:  &w,
			ResolutionHeight: &h,
		},
	}
	if err := store.SyncChannels(ctx, devID1, channels); err != nil {
		t.Fatalf("sync channels: %v", err)
	}

	// Verify order is channel_id ASC
	listed, err := store.ListChannels(ctx, devID1)
	if err != nil {
		t.Fatalf("list channels: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(listed))
	}
	if listed[0].ChannelID != "34020000001310000001" || listed[1].ChannelID != "34020000001310000002" {
		t.Errorf("expected channel order ASC: %+v", listed)
	}

	// 3. GetDevice is the SIP-path read: it must not pay for the channel count.
	dev1, err := store.GetDevice(ctx, devID1)
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if dev1.ChannelCount != 0 {
		t.Errorf("GetDevice must not count channels, got ChannelCount %d", dev1.ChannelCount)
	}

	allDevs, err := store.ListDevices(ctx)
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	for _, d := range allDevs {
		if d.DeviceID == devID1 && d.ChannelCount != 2 {
			t.Errorf("expected dev1 ChannelCount 2, got %d", d.ChannelCount)
		}
		if d.DeviceID == devID2 && d.ChannelCount != 0 {
			t.Errorf("expected dev2 ChannelCount 0, got %d", d.ChannelCount)
		}
	}

	// 4. Bind Channel to Camera (insert into cameras table first to satisfy foreign key)
	camID := "cam_01JTEST0000000000000000"
	db := store.dbProvider.DB()
	if err := db.Exec("INSERT INTO cameras (id, name, revision, created_at, updated_at) VALUES (?, 'Test Cam', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)", camID).Error; err != nil {
		t.Fatalf("insert test camera: %v", err)
	}

	if err := store.BindChannelCamera(ctx, devID1, "34020000001310000001", camID); err != nil {
		t.Fatalf("bind channel camera: %v", err)
	}

	ch, err := store.GetChannel(ctx, devID1, "34020000001310000001")
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	if ch.CameraID == nil || *ch.CameraID != camID {
		t.Fatalf("expected cameraId %q, got %v", camID, ch.CameraID)
	}

	// 5. Sync again with a renamed channel and one channel dropped by the device:
	// the surviving row keeps its binding and its update, the dropped unbound row goes.
	channels[1].Name = "Channel 1 Renamed"
	if err := store.SyncChannels(ctx, devID1, channels[1:]); err != nil {
		t.Fatalf("re-sync channels: %v", err)
	}
	ch, err = store.GetChannel(ctx, devID1, "34020000001310000001")
	if err != nil {
		t.Fatalf("get channel after re-sync: %v", err)
	}
	if ch.Name != "Channel 1 Renamed" {
		t.Errorf("expected updated name, got %q", ch.Name)
	}
	if ch.CameraID == nil || *ch.CameraID != camID {
		t.Fatalf("expected cameraId %q preserved across sync, got %v", camID, ch.CameraID)
	}
	if _, err := store.GetChannel(ctx, devID1, "34020000001310000002"); err == nil {
		t.Fatalf("expected the channel dropped by the device to be pruned")
	}

	// A pruned report must never remove a bound channel even when the device stops
	// listing it: the operator still has that camera.
	if err := store.SyncChannels(ctx, devID1, nil); err != nil {
		t.Fatalf("sync empty catalog: %v", err)
	}
	if _, err := store.GetChannel(ctx, devID1, "34020000001310000001"); err != nil {
		t.Fatalf("bound channel must survive an empty catalog: %v", err)
	}

	// 6. DeleteDevice is refused while a camera is bound
	err = store.DeleteDevice(ctx, devID1)
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != CodeCameraBound {
		t.Fatalf("expected %s when deleting device with bound camera, got %v", CodeCameraBound, err)
	}

	// Deleting the camera clears the binding through ON DELETE SET NULL, after which
	// the device delete is allowed and takes the catalog with it (ON DELETE CASCADE).
	if err := db.Exec("DELETE FROM cameras WHERE id = ?", camID).Error; err != nil {
		t.Fatalf("delete camera: %v", err)
	}
	ch, err = store.GetChannel(ctx, devID1, "34020000001310000001")
	if err != nil {
		t.Fatalf("get channel after camera delete: %v", err)
	}
	if ch.CameraID != nil {
		t.Fatalf("expected ON DELETE SET NULL to clear camera_id, got %v", *ch.CameraID)
	}
	if err := store.DeleteDevice(ctx, devID1); err != nil {
		t.Fatalf("delete device after unbind: %v", err)
	}
	var orphan int64
	if err := db.Model(&Channel{}).Where("device_id = ?", devID1).Count(&orphan).Error; err != nil {
		t.Fatalf("count orphan channels: %v", err)
	}
	if orphan != 0 {
		t.Fatalf("expected device channels to cascade, found %d orphans", orphan)
	}

	// 7. 1600 channels (100 devices x 16) through the batch path. The budget is recorded,
	// not asserted: a wall-clock threshold in a unit test turns a slow CI disk into a red
	// run without saying anything about the code.
	for d := 0; d < 100; d++ {
		curDev := fmt.Sprintf("3402000000118%07d", d)
		if err := db.Exec("INSERT OR IGNORE INTO gb_devices (device_id, name, transport, expires_seconds, state, created_at, updated_at) VALUES (?, 'Dev', 'udp', 3600, 'online', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)", curDev).Error; err != nil {
			t.Fatalf("insert test dev %s: %v", curDev, err)
		}
	}

	bulkChannels := make([]Channel, 0, 1600)
	for d := 0; d < 100; d++ {
		curDev := fmt.Sprintf("3402000000118%07d", d)
		for c := 0; c < 16; c++ {
			bulkChannels = append(bulkChannels, Channel{
				DeviceID:         curDev,
				ChannelID:        fmt.Sprintf("3402000000131%04d%03d", d, c),
				Name:             fmt.Sprintf("Channel %d-%d", d, c),
				ResolutionWidth:  &w,
				ResolutionHeight: &h,
			})
		}
	}

	start := time.Now()
	for d := 0; d < 100; d++ {
		curDev := fmt.Sprintf("3402000000118%07d", d)
		devChannels := bulkChannels[d*16 : (d+1)*16]
		if err := store.SyncChannels(ctx, curDev, devChannels); err != nil {
			t.Fatalf("sync bulk channels for dev %s: %v", curDev, err)
		}
	}
	t.Logf("synced 1600 channels in %v", time.Since(start))

	var total int64
	if err := db.Model(&Channel{}).Count(&total).Error; err != nil {
		t.Fatalf("count channels: %v", err)
	}
	if total != 1600 {
		t.Fatalf("expected 1600 channels, got %d", total)
	}
}

func TestStoreDeleteDeviceSerializesWithChannelBinding(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()
	ctx := context.Background()

	const (
		deviceID  = "34020000001320000003"
		channelID = "34020000001310000003"
		cameraID  = "cam_01JTEST0000000000000001"
	)
	password := "secret"
	if _, err := store.UpsertDevice(ctx, DeviceUpsert{DeviceID: deviceID, Password: &password}); err != nil {
		t.Fatalf("provision device: %v", err)
	}
	if err := store.SyncChannels(ctx, deviceID, []Channel{{DeviceID: deviceID, ChannelID: channelID}}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := store.dbProvider.DB().Exec(
		"INSERT INTO cameras (id, name, revision, created_at, updated_at) VALUES (?, 'Test Cam', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)",
		cameraID,
	).Error; err != nil {
		t.Fatalf("insert camera: %v", err)
	}

	db := store.dbProvider.DB()
	countReached := make(chan struct{})
	resumeCount := make(chan struct{})
	var countOnce sync.Once
	if err := db.Callback().Query().Before("gorm:query").Register("test:block_delete_device_count", func(tx *gorm.DB) {
		if tx.Statement.Table == "gb_channels" {
			countOnce.Do(func() {
				close(countReached)
				<-resumeCount
			})
		}
	}); err != nil {
		t.Fatalf("register query callback: %v", err)
	}

	deleteDone := make(chan error, 1)
	go func() { deleteDone <- store.DeleteDevice(ctx, deviceID) }()

	var deleteFinished bool
	var bindDone chan error
	var bindFinished bool
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(resumeCount) }) }
	defer func() {
		release()
		if !deleteFinished {
			<-deleteDone
		}
		if bindDone != nil && !bindFinished {
			<-bindDone
		}
	}()

	select {
	case <-countReached:
	case <-time.After(time.Second):
		t.Fatal("device deletion did not reach its bound-channel check")
	}

	bindDone = make(chan error, 1)
	bindStarted := make(chan struct{})
	go func() {
		close(bindStarted)
		bindDone <- store.BindChannelCamera(ctx, deviceID, channelID, cameraID)
	}()
	<-bindStarted
	select {
	case err := <-bindDone:
		bindFinished = true
		t.Fatalf("channel binding passed an active device deletion check: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	release()
	if err := <-deleteDone; err != nil {
		deleteFinished = true
		t.Fatalf("delete device: %v", err)
	}
	deleteFinished = true
	if err := <-bindDone; err == nil {
		bindFinished = true
		t.Fatal("binding should fail after the device and its catalog are deleted")
	} else {
		bindFinished = true
		var appErr *apperr.Error
		if !errors.As(err, &appErr) || appErr.Code != CodeChannelNotFound {
			t.Fatalf("expected channel-not-found after serialized delete, got %v", err)
		}
	}

	var channelCount int64
	if err := db.Model(&Channel{}).Where("device_id = ?", deviceID).Count(&channelCount).Error; err != nil {
		t.Fatalf("count remaining channels: %v", err)
	}
	if channelCount != 0 {
		t.Fatalf("device deletion left %d catalog rows", channelCount)
	}
}
