package audit

import (
	"context"
	"testing"
)

func TestActorContextRoundTrip(t *testing.T) {
	ctx := context.Background()

	if _, ok := FromContext(ctx); ok {
		t.Fatal("expected no actor in a bare context")
	}

	want := Actor{IP: "192.168.1.50", Username: "operator", Source: SourceWeb}
	ctx = WithActor(ctx, want)

	got, ok := FromContext(ctx)
	if !ok {
		t.Fatal("expected actor to be present")
	}
	if got != want {
		t.Fatalf("actor round trip mismatch: got %+v want %+v", got, want)
	}
}

func TestFromContextNilContext(t *testing.T) {
	//nolint:staticcheck // deliberately exercising the nil-context guard
	if _, ok := FromContext(nil); ok {
		t.Fatal("expected nil context to report no actor")
	}
}

func TestEventEntryWithoutActorIsUnknown(t *testing.T) {
	entry := eventEntry(context.Background(), ActionCameraCreate, "camera:1", "created", nil)

	if entry.Username != UsernameUnknown {
		t.Fatalf("expected username %q, got %q", UsernameUnknown, entry.Username)
	}
	if entry.IP != "" {
		t.Fatalf("expected empty IP for unattributed event, got %q", entry.IP)
	}
	if entry.Status != StatusSuccess {
		t.Fatalf("expected success status, got %q", entry.Status)
	}
}

func TestEventEntryKeepsActorIP(t *testing.T) {
	ctx := WithActor(context.Background(), Actor{IP: "10.0.0.9", Username: "operator", Source: SourceWeb})

	entry := eventEntry(ctx, ActionCameraUpdate, "camera:7", "updated", nil)

	if entry.IP != "10.0.0.9" {
		t.Fatalf("expected actor IP to be preserved, got %q", entry.IP)
	}
	if entry.Username != "operator" {
		t.Fatalf("expected actor username, got %q", entry.Username)
	}
}

func TestEventEntryWithEmptyUsernameActorIsUnknown(t *testing.T) {
	// An actor carrying only an IP must still not produce an empty username.
	ctx := WithActor(context.Background(), Actor{IP: "10.0.0.9", Source: SourceWeb})

	entry := eventEntry(ctx, ActionCameraDelete, "camera:7", "", nil)

	if entry.Username != UsernameUnknown {
		t.Fatalf("expected username %q, got %q", UsernameUnknown, entry.Username)
	}
	if entry.IP != "10.0.0.9" {
		t.Fatalf("expected IP to survive, got %q", entry.IP)
	}
}

func TestEventEntryFailureStatus(t *testing.T) {
	entry := eventEntry(context.Background(), ActionAuthLogin, "user:x", "", errSentinel)

	if entry.Status != StatusFailed {
		t.Fatalf("expected failed status, got %q", entry.Status)
	}
	if entry.ErrorMsg == "" {
		t.Fatal("expected error message to be recorded")
	}
}

func TestSystemEntryHasNoClientOrigin(t *testing.T) {
	// Action names are owned by their business modules (storage/systemtime);
	// the audit core must not import them, so the test uses the literal value.
	entry := systemEntry("system.storage.emergency_stop", "media_storage", "watermark breach", nil)

	if entry.Username != UsernameSystem {
		t.Fatalf("expected username %q, got %q", UsernameSystem, entry.Username)
	}
	if entry.IP != "" {
		t.Fatalf("expected empty IP for system event, got %q", entry.IP)
	}
	if entry.Status != StatusSuccess {
		t.Fatalf("expected success status, got %q", entry.Status)
	}
}

func TestSystemEntryFailureStatus(t *testing.T) {
	entry := systemEntry("system.time.ntp_sync", "system_time", "", errSentinel)

	if entry.Status != StatusFailed {
		t.Fatalf("expected failed status, got %q", entry.Status)
	}
	if entry.Username != UsernameSystem {
		t.Fatalf("expected system username even on failure, got %q", entry.Username)
	}
}

func TestEntryToLogNeverDefaultsToAdmin(t *testing.T) {
	log := entryToLog(Entry{Action: ActionAuthLogout})

	if log.Username != UsernameUnknown {
		t.Fatalf("expected %q instead of a hardcoded admin, got %q", UsernameUnknown, log.Username)
	}
	if log.Status != StatusSuccess {
		t.Fatalf("expected default success status, got %q", log.Status)
	}
	if log.CreatedAt.IsZero() {
		t.Fatal("expected CreatedAt to be filled")
	}
}
