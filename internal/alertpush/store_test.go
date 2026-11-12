package alertpush

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

type fakeCipher struct{}

const fakeCipherPrefix = "aad:"

func (fakeCipher) Encrypt(plaintext []byte, aad string) ([]byte, error) {
	return append([]byte(fakeCipherPrefix+aad+"|"), plaintext...), nil
}

func (fakeCipher) Decrypt(ciphertext []byte, aad string) ([]byte, error) {
	prefix := []byte(fakeCipherPrefix + aad + "|")
	if !bytes.HasPrefix(ciphertext, prefix) {
		return nil, errors.New("fake cipher: AAD mismatch")
	}
	return ciphertext[len(prefix):], nil
}

func setupTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dbStore := database.New(t.TempDir(), zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	store := NewStore(dbStore, fakeCipher{})
	return store, func() {
		if err := dbStore.Close(); err != nil {
			t.Errorf("dbStore.Close failed: %v", err)
		}
	}
}

func TestStoreCRUD(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// 1. Initial encrypted probe is false
	has, err := store.HasExistingEncryptedData(ctx)
	if err != nil {
		t.Fatalf("HasExistingEncryptedData failed: %v", err)
	}
	if has {
		t.Fatal("expected no existing encrypted data initially")
	}

	// 2. Create destination
	webhookID := "01J8Z000000000000000000001"
	secData, err := store.EncryptSecret(webhookID, &webhookSecret{URL: "https://example.com/hook"})
	if err != nil {
		t.Fatalf("EncryptSecret failed: %v", err)
	}

	dest := &AlertDestination{
		ID:               webhookID,
		Name:             "Default Webhook",
		ChannelType:      ChannelTypeWebhook,
		Enabled:          true,
		ImageMode:        ImageModeURL,
		ConfigJSON:       `{"auth_type":"none","timeout_seconds":5}`,
		SecretCiphertext: secData,
		FilterJSON:       `{}`,
	}
	if err := store.Create(ctx, dest); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// 3. Probe now returns true
	has, err = store.HasExistingEncryptedData(ctx)
	if err != nil || !has {
		t.Fatalf("expected HasExistingEncryptedData true, got %v (err: %v)", has, err)
	}

	// 4. Duplicate name fails
	dup := &AlertDestination{
		ID:          "01J8Z000000000000000000002",
		Name:        "Default Webhook",
		ChannelType: ChannelTypeWebhook,
		Enabled:     true,
		ImageMode:   ImageModeURL,
	}
	if err := store.Create(ctx, dup); !errors.Is(err, ErrDestinationNameConflict) {
		t.Fatalf("expected ErrDestinationNameConflict, got %v", err)
	}

	// 5. GetByID and Decrypt
	got, err := store.GetByID(ctx, webhookID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if got.Name != "Default Webhook" || !got.Enabled {
		t.Fatalf("unexpected destination properties: %+v", got)
	}
	sec, err := store.DecryptWebhookSecret(webhookID, got.SecretCiphertext)
	if err != nil {
		t.Fatalf("DecryptWebhookSecret failed: %v", err)
	}
	if sec.URL != "https://example.com/hook" {
		t.Fatalf("expected URL https://example.com/hook, got %q", sec.URL)
	}

	// 6. Decrypt with wrong ID fails
	if _, err := store.DecryptWebhookSecret("wrong-id", got.SecretCiphertext); err == nil {
		t.Fatal("expected AAD mismatch error on wrong ID")
	}

	// 7. Update with enabled=false & capability toggle (verifying zero values are NOT ignored)
	if !got.EventsEnabled || got.HeartbeatEnabled {
		t.Fatalf("expected default capabilities events=true heartbeat=false, got events=%v heartbeat=%v",
			got.EventsEnabled, got.HeartbeatEnabled)
	}
	got.Enabled = false
	got.EventsEnabled = false
	got.HeartbeatEnabled = true
	got.Name = "Renamed Webhook"
	if err := store.Update(ctx, got); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	reloaded, err := store.GetByID(ctx, webhookID)
	if err != nil {
		t.Fatalf("reload after update failed: %v", err)
	}
	if reloaded.Enabled {
		t.Fatal("expected Enabled to be false after update, but zero value was ignored!")
	}
	if reloaded.EventsEnabled || !reloaded.HeartbeatEnabled {
		t.Fatalf("expected EventsEnabled=false HeartbeatEnabled=true after update, got events=%v heartbeat=%v",
			reloaded.EventsEnabled, reloaded.HeartbeatEnabled)
	}
	if reloaded.Name != "Renamed Webhook" {
		t.Fatalf("expected Renamed Webhook, got %s", reloaded.Name)
	}

	// 7b. Create explicit heartbeat-only destination (EventsEnabled=false, HeartbeatEnabled=true)
	hbOnlyID := "01J8Z000000000000000000009"
	hbOnly := &AlertDestination{
		ID:               hbOnlyID,
		Name:             "Heartbeat Only",
		ChannelType:      ChannelTypeWebhook,
		Enabled:          true,
		EventsEnabled:    false,
		HeartbeatEnabled: true,
		ImageMode:        ImageModeURL,
		ConfigJSON:       `{"auth_type":"none","timeout_seconds":5}`,
		SecretCiphertext: secData,
		FilterJSON:       `{}`,
	}
	if err := store.Create(ctx, hbOnly); err != nil {
		t.Fatalf("Create heartbeat-only failed: %v", err)
	}
	gotHB, err := store.GetByID(ctx, hbOnlyID)
	if err != nil {
		t.Fatalf("GetByID heartbeat-only failed: %v", err)
	}
	if gotHB.EventsEnabled || !gotHB.HeartbeatEnabled {
		t.Fatalf("expected EventsEnabled=false HeartbeatEnabled=true on create, got events=%v heartbeat=%v",
			gotHB.EventsEnabled, gotHB.HeartbeatEnabled)
	}
	if err := store.Delete(ctx, hbOnlyID); err != nil {
		t.Fatalf("Delete heartbeat-only failed: %v", err)
	}

	// 8. SetEnabled
	if err := store.SetEnabled(ctx, webhookID, true); err != nil {
		t.Fatalf("SetEnabled failed: %v", err)
	}
	reloaded, err = store.GetByID(ctx, webhookID)
	if err != nil {
		t.Fatalf("GetByID after SetEnabled failed: %v", err)
	}
	if !reloaded.Enabled {
		t.Fatal("expected Enabled to be true after SetEnabled")
	}

	// 9. List and ListEnabled
	list, err := store.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List failed: %v, len=%d", err, len(list))
	}
	enabledList, err := store.ListEnabled(ctx)
	if err != nil || len(enabledList) != 1 {
		t.Fatalf("ListEnabled failed: %v, len=%d", err, len(enabledList))
	}

	// 10. Delete
	if err := store.Delete(ctx, webhookID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if _, err := store.GetByID(ctx, webhookID); !errors.Is(err, ErrDestinationNotFound) {
		t.Fatalf("expected ErrDestinationNotFound, got %v", err)
	}
}

func TestStoreMQTTClientIDUnique(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	clientID := "z01J8Z00000000000000000"
	cID := clientID
	dest1 := &AlertDestination{
		ID:           "01J8Z000000000000000000010",
		Name:         "MQTT 1",
		ChannelType:  ChannelTypeMQTT,
		Enabled:      true,
		ImageMode:    ImageModeBase64,
		MQTTClientID: &cID,
	}
	if err := store.Create(ctx, dest1); err != nil {
		t.Fatalf("Create dest1 failed: %v", err)
	}

	// Conflict check
	conflict, err := store.CheckMQTTClientIDConflict(ctx, "", clientID)
	if err != nil || !conflict {
		t.Fatalf("expected conflict true, got %v (err: %v)", conflict, err)
	}
	// Exclude self is not conflict
	conflict, err = store.CheckMQTTClientIDConflict(ctx, dest1.ID, clientID)
	if err != nil || conflict {
		t.Fatalf("expected conflict false when excluding self, got %v", conflict)
	}

	// Creating another with same MQTT client ID fails
	dest2 := &AlertDestination{
		ID:           "01J8Z000000000000000000011",
		Name:         "MQTT 2",
		ChannelType:  ChannelTypeMQTT,
		Enabled:      true,
		ImageMode:    ImageModeBase64,
		MQTTClientID: &cID,
	}
	if err := store.Create(ctx, dest2); !errors.Is(err, ErrClientIDConflict) {
		t.Fatalf("expected ErrClientIDConflict, got %v", err)
	}
}
