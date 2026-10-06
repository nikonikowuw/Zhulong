package camera

import (
	"strings"
	"testing"
)

func TestNormalizeChallengeHeader(t *testing.T) {
	input := `Digest REALM="TestRealm", NONCE="dcd98b7102dd2f0e8b11d0f600bfb0c093", QOP="auth, auth-int", OPAQUE="", ALGORITHM=MD5`
	norm, hadEmptyOpaque, err := NormalizeChallengeHeader(input)
	if err != nil {
		t.Fatalf("NormalizeChallengeHeader failed: %v", err)
	}

	if !hadEmptyOpaque {
		t.Errorf("expected hadEmptyOpaque=true")
	}

	if !strings.Contains(norm, `qop="auth,auth-int"`) {
		t.Errorf("expected trimmed qop list, got: %s", norm)
	}
	if !strings.Contains(norm, `algorithm=MD5`) {
		t.Errorf("expected lowercase key algorithm, got: %s", norm)
	}
	if !strings.Contains(norm, `realm="TestRealm"`) {
		t.Errorf("expected lowercase key realm, got: %s", norm)
	}
}

func TestSelectBestChallenge(t *testing.T) {
	challenges := []string{
		`Basic realm="WLAN-AP"`,
		`Digest realm="IPCamera", nonce="12345", algorithm=MD5`,
		`Digest realm="IPCamera", nonce="67890", algorithm=SHA-256, qop="auth"`,
		`Digest realm="IPCamera", nonce="abcde", algorithm=MD5-sess`,
	}

	best, err := SelectBestChallenge(challenges)
	if err != nil {
		t.Fatalf("SelectBestChallenge failed: %v", err)
	}

	if best.Type != AuthTypeDigest || best.Algorithm != "SHA-256" {
		t.Fatalf("expected SHA-256 digest, got type=%v alg=%s", best.Type, best.Algorithm)
	}
}

func TestSelectFallbackToBasicWhenOnlyBasicAvailable(t *testing.T) {
	challenges := []string{
		`Basic realm="AccessControl"`,
	}

	best, err := SelectBestChallenge(challenges)
	if err != nil {
		t.Fatalf("SelectBestChallenge failed: %v", err)
	}

	if best.Type != AuthTypeBasic {
		t.Fatalf("expected Basic auth, got %v", best.Type)
	}
}

func TestBuildAuthorizationHeaderDigestWithQopAndEmptyOpaque(t *testing.T) {
	rawChal := `Digest realm="IPCamera", nonce="11223344", qop="auth", algorithm="MD5", opaque=""`
	chal, err := SelectBestChallenge([]string{rawChal})
	if err != nil {
		t.Fatalf("SelectBestChallenge failed: %v", err)
	}

	authHeader, err := BuildAuthorizationHeader(
		chal,
		"DESCRIBE",
		"rtsp://192.168.1.100/live/main",
		"admin",
		"secretPass",
		1,
		"0a1b2c3d4e5f6a7b",
	)
	if err != nil {
		t.Fatalf("BuildAuthorizationHeader failed: %v", err)
	}

	if !strings.HasPrefix(authHeader, "Digest ") {
		t.Fatalf("expected Digest header, got: %s", authHeader)
	}
	if !strings.Contains(authHeader, `username="admin"`) {
		t.Errorf("missing username in authHeader: %s", authHeader)
	}
	if !strings.Contains(authHeader, `uri="rtsp://192.168.1.100/live/main"`) {
		t.Errorf("missing exact uri in authHeader: %s", authHeader)
	}
	if !strings.Contains(authHeader, `qop="auth"`) && !strings.Contains(authHeader, `qop=auth`) {
		t.Errorf("missing qop in authHeader: %s", authHeader)
	}
	if !strings.Contains(authHeader, `nc=00000001`) {
		t.Errorf("missing nc in authHeader: %s", authHeader)
	}
	if !strings.Contains(authHeader, `cnonce="0a1b2c3d4e5f6a7b"`) {
		t.Errorf("missing cnonce in authHeader: %s", authHeader)
	}
	if !strings.Contains(authHeader, `opaque=""`) {
		t.Errorf("missing restored opaque=\"\" in authHeader: %s", authHeader)
	}
}
