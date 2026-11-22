package filestore

import (
	"errors"
	"strings"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
)

func TestValidateKey_AcceptsAndRejectsExpectedShapes(t *testing.T) {
	valid := []string{
		"recordings/cam-01/2026-11-22/evt-1.mp4",
		"snapshots/cam-01/2026-11-22/01J8Z.jpg",
		"exports/task-1.mp4",
		"faces/person_1_face_1.jpg",
		"maps/01J8Z.svg",
		"recordings",
	}
	for _, key := range valid {
		got, err := ValidateKey(key)
		if err != nil {
			t.Fatalf("ValidateKey(%q) unexpected error: %v", key, err)
		}
		if got != key {
			t.Fatalf("ValidateKey(%q) = %q", key, got)
		}
	}

	if _, err := ValidateObjectKeyForWrite("recordings"); err == nil {
		t.Fatal("expected ValidateObjectKeyForWrite to reject bare namespace")
	}

	cases := []struct {
		name string
		key  string
		code string
	}{
		{name: "empty", key: "", code: "MEDIA_PATH_INVALID"},
		{name: "NUL", key: "snapshots/a\x00.jpg", code: "MEDIA_PATH_INVALID"},
		{name: "absolute", key: "/etc/passwd", code: "MEDIA_PATH_INVALID"},
		{name: "double slash prefix", key: "//etc/passwd", code: "MEDIA_PATH_INVALID"},
		{name: "backslash", key: `snapshots\a.jpg`, code: "MEDIA_PATH_INVALID"},
		{name: "dot", key: ".", code: "MEDIA_PATH_INVALID"},
		{name: "dotdot", key: "..", code: "MEDIA_PATH_INVALID"},
		{name: "dot inside", key: "snapshots/./a.jpg", code: "MEDIA_PATH_INVALID"},
		{name: "traversal", key: "snapshots/../../etc/passwd", code: "MEDIA_PATH_INVALID"},
		{name: "double slash inside", key: "snapshots//a.jpg", code: "MEDIA_PATH_INVALID"},
		{name: "trailing slash", key: "snapshots/a.jpg/", code: "MEDIA_PATH_INVALID"},
		{name: "space", key: "snapshots/cam 01/a.jpg", code: "MEDIA_PATH_INVALID"},
		{name: "too long", key: "snapshots/" + strings.Repeat("a", MaxKeyLength), code: "MEDIA_PATH_INVALID"},
		{name: "unknown namespace", key: "secrets/a.txt", code: "MEDIA_KIND_INVALID"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateKey(tc.key)
			if err == nil {
				t.Fatalf("expected %s for %q", tc.code, tc.key)
			}
			var appErr *apperr.Error
			if !errors.As(err, &appErr) {
				t.Fatalf("expected *apperr.Error, got %T: %v", err, err)
			}
			if appErr.Code != tc.code {
				t.Fatalf(
					"expected code %s, got %s (%v)",
					tc.code,
					appErr.Code,
					err,
				)
			}
		})
	}
}

func TestValidateMediaKey_RejectsFacesAndMaps(t *testing.T) {
	for _, key := range []string{"faces/p1_f1.jpg", "maps/m1.png"} {
		_, err := ValidateMediaKey(key)
		var appErr *apperr.Error
		if !errors.As(err, &appErr) || appErr.Code != "MEDIA_KIND_INVALID" {
			t.Fatalf("expected MEDIA_KIND_INVALID for %q, got %v", key, err)
		}
	}
}
