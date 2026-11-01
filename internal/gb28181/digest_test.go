package gb28181

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
)

// testDigest is an independent oracle for the digest formula, so the production
// implementation is never compared against itself.
func testDigest(username, realm, password, method, uri, nonce, nc, cnonce, qop string) string {
	md5Hex := func(data string) string {
		sum := md5.Sum([]byte(data))
		return hex.EncodeToString(sum[:])
	}
	ha1 := md5Hex(username + ":" + realm + ":" + password)
	ha2 := md5Hex(method + ":" + uri)
	if qop == "" {
		return md5Hex(ha1 + ":" + nonce + ":" + ha2)
	}
	return md5Hex(ha1 + ":" + nonce + ":" + nc + ":" + cnonce + ":" + qop + ":" + ha2)
}

func TestNonceManagerIssueVerifyExpiry(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	manager := newNonceManager([]byte("secret"), time.Minute)
	manager.now = func() time.Time { return now }

	nonce := manager.Issue()
	if err := manager.Verify(nonce); err != nil {
		t.Fatalf("fresh nonce rejected: %v", err)
	}
	// Repeated verification must succeed: UDP retransmissions replay the same
	// Authorization, and answering them with 401 makes devices give up.
	if err := manager.Verify(nonce); err != nil {
		t.Fatalf("nonce must be reusable within its lifetime: %v", err)
	}

	now = now.Add(2 * time.Minute)
	if err := manager.Verify(nonce); !errors.Is(err, ErrDigestNonceStale) {
		t.Fatalf("expected a stale nonce, got %v", err)
	}
}

func TestNonceManagerRejectsForgedNonce(t *testing.T) {
	manager := newNonceManager([]byte("secret"), time.Minute)
	nonce := manager.Issue()

	other := newNonceManager([]byte("different-secret"), time.Minute)
	for _, candidate := range []string{
		"",
		"nodots",
		nonce + "0",
		strings.Replace(nonce, ".", ".0", 1),
		other.Issue(),
	} {
		if err := manager.Verify(candidate); !errors.Is(err, ErrDigestNonceInvalid) {
			t.Errorf("nonce %q: expected ErrDigestNonceInvalid, got %v", candidate, err)
		}
	}
}

func TestNonceManagerDefaultsTTL(t *testing.T) {
	manager := newNonceManager(nil, 0)
	if manager.ttl != defaultNonceTTL {
		t.Fatalf("expected the default TTL, got %s", manager.ttl)
	}
	if err := manager.Verify(manager.Issue()); err != nil {
		t.Fatalf("nonce issued without a configured secret must verify: %v", err)
	}
}

func TestParseDigestCredentials(t *testing.T) {
	header := `Digest username="34020000001320000001", realm="3402000000", nonce="abc.def", ` +
		`uri="sip:3402000000", response="deadbeef", algorithm=MD5, qop=auth, nc=00000001, cnonce="0a4f"`

	credentials, err := parseDigestCredentials(header)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if credentials.Username != "34020000001320000001" {
		t.Errorf("unexpected username %q", credentials.Username)
	}
	if credentials.Realm != "3402000000" || credentials.Nonce != "abc.def" {
		t.Errorf("unexpected realm/nonce: %+v", credentials)
	}
	if credentials.Algorithm != "MD5" || credentials.QOP != "auth" || credentials.Nc != "00000001" {
		t.Errorf("unexpected qop fields: %+v", credentials)
	}

	if _, err := parseDigestCredentials("Basic abc"); !errors.Is(err, ErrDigestMalformed) {
		t.Errorf("expected a malformed error for a non-Digest scheme, got %v", err)
	}
	if _, err := parseDigestCredentials(""); !errors.Is(err, ErrDigestMissing) {
		t.Errorf("expected a missing error for an empty header, got %v", err)
	}
	if _, err := parseDigestCredentials(`Digest username="a", realm="b"`); !errors.Is(err, ErrDigestMalformed) {
		t.Errorf("expected a malformed error for missing fields, got %v", err)
	}
}

func TestVerifyDigestResponse(t *testing.T) {
	const (
		username = "34020000001320000001"
		realm    = "3402000000"
		password = "digest-secret"
		method   = "REGISTER"
		uri      = "sip:3402000000"
		nonce    = "abc.def"
	)
	base := digestCredentials{
		Username: username,
		Realm:    realm,
		Nonce:    nonce,
		URI:      uri,
	}

	tests := []struct {
		name    string
		mutate  func(*digestCredentials)
		wantErr error
	}{
		{
			name: "rfc2069 without qop",
			mutate: func(c *digestCredentials) {
				c.Response = testDigest(username, realm, password, method, uri, nonce, "", "", "")
			},
		},
		{
			name: "qop auth",
			mutate: func(c *digestCredentials) {
				c.QOP, c.Nc, c.CNonce = "auth", "00000001", "0a4f"
				c.Response = testDigest(username, realm, password, method, uri, nonce, "00000001", "0a4f", "auth")
			},
		},
		{
			name: "wrong password",
			mutate: func(c *digestCredentials) {
				c.Response = testDigest(username, realm, "other", method, uri, nonce, "", "", "")
			},
			wantErr: ErrDigestMismatch,
		},
		{
			name: "wrong realm",
			mutate: func(c *digestCredentials) {
				c.Realm = "someone-else"
				c.Response = testDigest(username, realm, password, method, uri, nonce, "", "", "")
			},
			wantErr: ErrDigestRealmMismatch,
		},
		{
			name: "qop auth without nc",
			mutate: func(c *digestCredentials) {
				c.QOP = "auth"
			},
			wantErr: ErrDigestMalformed,
		},
		{
			name: "unsupported qop",
			mutate: func(c *digestCredentials) {
				c.QOP, c.Nc, c.CNonce = "auth-int", "00000001", "0a4f"
			},
			wantErr: ErrDigestUnsupported,
		},
		{
			name: "unsupported algorithm",
			mutate: func(c *digestCredentials) {
				c.Algorithm = "MD5-SESS"
			},
			wantErr: ErrDigestUnsupported,
		},
		{
			name: "uppercase response is accepted",
			mutate: func(c *digestCredentials) {
				c.Response = strings.ToUpper(testDigest(username, realm, password, method, uri, nonce, "", "", ""))
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			credentials := base
			test.mutate(&credentials)
			err := verifyDigestResponse(credentials, method, uri, realm, password)
			if test.wantErr == nil {
				if err != nil {
					t.Fatalf("verification failed: %v", err)
				}
				return
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("expected %v, got %v", test.wantErr, err)
			}
		})
	}
}

// A wall clock that jumps backwards must not turn the replay window into "until the
// clock catches up": a nonce stamped by this process cannot legitimately look like it
// was issued in the future.
func TestNonceManagerRejectsNonceFromTheFuture(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	manager := newNonceManager([]byte("secret"), time.Minute)
	manager.now = func() time.Time { return now }

	nonce := manager.Issue()
	now = now.Add(-maxNonceClockSkew - time.Second)
	if err := manager.Verify(nonce); !errors.Is(err, ErrDigestNonceInvalid) {
		t.Fatalf("expected a nonce from the future to be invalid, got %v", err)
	}

	// Within the skew budget the nonce stays usable: a small NTP correction is not an
	// attack, and locking the device out over it would be worse than the risk.
	now = now.Add(maxNonceClockSkew - time.Second)
	if err := manager.Verify(nonce); err != nil {
		t.Fatalf("expected a nonce inside the skew budget to verify, got %v", err)
	}
}

func TestVerifyDigestResponseBindsTheURIToTheRequestTarget(t *testing.T) {
	const (
		username   = "34020000001320000001"
		realm      = "3402000000"
		password   = "digest-secret"
		method     = "REGISTER"
		requestURI = "sip:34020000002000000001@3402000000"
		nonce      = "abc.def"
	)

	verify := func(uri string) error {
		credentials := digestCredentials{
			Username: username,
			Realm:    realm,
			Nonce:    nonce,
			URI:      uri,
			Response: testDigest(username, realm, password, method, uri, nonce, "", "", ""),
		}
		return verifyDigestResponse(credentials, method, requestURI, realm, password)
	}

	tests := []struct {
		name    string
		uri     string
		wantErr error
	}{
		{name: "exact request target", uri: requestURI},
		// Format variations are tolerated: devices rewrite the port, the host and
		// sometimes drop the user part, and a 401 over that would lock them out.
		{name: "port dropped", uri: "sip:34020000002000000001@3402000000:5060"},
		{name: "user dropped", uri: "sip:3402000000"},
		// A digest pointing at another target is a replay against a different identity.
		{name: "different user", uri: "sip:34020000009999999999@3402000000", wantErr: ErrDigestUserMismatch},
		{name: "unparsable", uri: "not a uri", wantErr: ErrDigestMalformed},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := verify(test.uri)
			if test.wantErr == nil {
				if err != nil {
					t.Fatalf("verification failed: %v", err)
				}
				return
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("expected %v, got %v", test.wantErr, err)
			}
		})
	}
}

func TestVerifyDigestResponseReportsAMissingUsername(t *testing.T) {
	credentials := digestCredentials{Realm: "3402000000", Nonce: "abc.def", URI: "sip:3402000000"}
	err := verifyDigestResponse(credentials, "REGISTER", "sip:3402000000", "3402000000", "secret")
	if !errors.Is(err, ErrDigestUserMismatch) {
		t.Fatalf("an empty username must be reported as a user mismatch, got %v", err)
	}
}
