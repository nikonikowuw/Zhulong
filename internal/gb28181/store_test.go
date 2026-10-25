package gb28181

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
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
