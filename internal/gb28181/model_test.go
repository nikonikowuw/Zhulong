package gb28181

import (
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
)

func TestConfigNormalizeAppliesDocumentedFallbacks(t *testing.T) {
	tests := []struct {
		name string
		in   Config
		want Config
	}{
		{
			name: "empty realm falls back to sip identity",
			in:   Config{SIPID: " 34020000002000000001 "},
			want: Config{
				ID:               ConfigSingletonID,
				SIPID:            "34020000002000000001",
				SIPRealm:         "34020000002000000001",
				SIPListen:        DefaultSIPListen,
				SIPTransport:     TransportBoth,
				SecurityProfile:  SecurityProfileStandard,
				MediaBindAddress: DefaultMediaBindAddress,
				RTPPortMin:       DefaultRTPPortMin,
				RTPPortMax:       DefaultRTPPortMax,
			},
		},
		{
			name: "explicit transport and profile are lowercased",
			in:   Config{SIPTransport: " TCP ", SecurityProfile: "STANDARD"},
			want: Config{
				ID:               ConfigSingletonID,
				SIPTransport:     TransportTCP,
				SecurityProfile:  SecurityProfileStandard,
				SIPListen:        DefaultSIPListen,
				MediaBindAddress: DefaultMediaBindAddress,
				RTPPortMin:       DefaultRTPPortMin,
				RTPPortMax:       DefaultRTPPortMax,
			},
		},
		{
			name: "zero ports fall back to the migrated defaults",
			in:   Config{RTPPortMin: 0, RTPPortMax: 0},
			want: Config{
				ID:               ConfigSingletonID,
				SIPListen:        DefaultSIPListen,
				SIPTransport:     TransportBoth,
				SecurityProfile:  SecurityProfileStandard,
				MediaBindAddress: DefaultMediaBindAddress,
				RTPPortMin:       DefaultRTPPortMin,
				RTPPortMax:       DefaultRTPPortMax,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in.Normalize()
			if !got.Equal(tc.want) {
				t.Fatalf("normalize mismatch:\n got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	enabled := func(mutate func(*Config)) Config {
		cfg := DefaultConfig()
		cfg.Enabled = true
		cfg.SIPID = "34020000002000000001"
		cfg.SIPRealm = "34020000002000000001"
		if mutate != nil {
			mutate(&cfg)
		}
		return cfg.Normalize()
	}

	tests := []struct {
		name     string
		cfg      Config
		wantCode string
	}{
		{
			name: "disabled config only needs a valid port range",
			cfg:  DefaultConfig(),
		},
		{
			name: "disabled permits a half-filled draft",
			cfg:  Config{RequireRegisteredDevice: true, SIPID: "", RTPPortMin: 30000, RTPPortMax: 30100}.Normalize(),
		},
		{
			name:     "registered-device enforcement cannot be disabled",
			cfg:      func() Config { cfg := DefaultConfig(); cfg.RequireRegisteredDevice = false; return cfg }(),
			wantCode: CodeRegisteredDeviceRequired,
		},
		{name: "enabled minimal config is valid", cfg: enabled(nil)},
		{
			name:     "enabled requires a sip identity",
			cfg:      enabled(func(c *Config) { c.SIPID = ""; c.SIPRealm = "realm" }),
			wantCode: CodeConfigInvalid,
		},
		{
			name:     "enabled identity rejects whitespace and punctuation",
			cfg:      enabled(func(c *Config) { c.SIPID = "3402 0000002000000001" }),
			wantCode: CodeConfigInvalid,
		},
		{
			name:     "enabled requires a numeric sip listen port",
			cfg:      enabled(func(c *Config) { c.SIPListen = "0.0.0.0:sip" }),
			wantCode: CodeConfigInvalid,
		},
		{
			name:     "enabled requires host:port format",
			cfg:      enabled(func(c *Config) { c.SIPListen = "0.0.0.0" }),
			wantCode: CodeConfigInvalid,
		},
		{
			name:     "unsupported security profile is rejected",
			cfg:      enabled(func(c *Config) { c.SecurityProfile = "high" }),
			wantCode: CodeProfileUnsupported,
		},
		{
			name:     "invalid transport is rejected",
			cfg:      enabled(func(c *Config) { c.SIPTransport = "sctp" }),
			wantCode: CodeTransportInvalid,
		},
		{
			name:     "port range below 1 is rejected",
			cfg:      enabled(func(c *Config) { c.RTPPortMin = 0; c.RTPPortMax = 30100 }),
			wantCode: CodePortRangeInvalid,
		},
		{
			name:     "reversed port range is rejected",
			cfg:      enabled(func(c *Config) { c.RTPPortMin = 30100; c.RTPPortMax = 30000 }),
			wantCode: CodePortRangeInvalid,
		},
		{
			name:     "single port cannot host an RTP/RTCP pair",
			cfg:      enabled(func(c *Config) { c.RTPPortMin = 30000; c.RTPPortMax = 30000 }),
			wantCode: CodePortRangeInvalid,
		},
		{
			name:     "advertised ip must be IPv4",
			cfg:      enabled(func(c *Config) { c.MediaAdvertisedIP = "::1" }),
			wantCode: CodeAddressInvalid,
		},
		{
			name:     "bind address must be IPv4",
			cfg:      enabled(func(c *Config) { c.MediaBindAddress = "box.local" }),
			wantCode: CodeAddressInvalid,
		},
		{
			name: "two ports are exactly one pair",
			cfg:  enabled(func(c *Config) { c.RTPPortMin = 30000; c.RTPPortMax = 30001 }),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("expected valid config, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error code %s, got nil", tc.wantCode)
			}
			var appErr *apperr.Error
			if !errors.As(err, &appErr) {
				t.Fatalf("expected *apperr.Error, got %T", err)
			}
			if appErr.Code != tc.wantCode {
				t.Fatalf("expected code %s, got %s (%v)", tc.wantCode, appErr.Code, err)
			}
		})
	}
}

func TestConfigMediaCapacity(t *testing.T) {
	tests := []struct {
		name     string
		min, max int
		want     int
	}{
		{name: "default range holds fifty pairs", min: 30000, max: 30100, want: 50},
		{name: "exactly one pair", min: 30000, max: 30001, want: 1},
		{name: "odd span drops the trailing port", min: 30000, max: 30002, want: 1},
		{name: "quarter span still floors", min: 30000, max: 30105, want: 53},
		{name: "inverted range reports nothing", min: 30100, max: 30000, want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{RTPPortMin: tc.min, RTPPortMax: tc.max}
			if got := cfg.MediaCapacity(); got != tc.want {
				t.Fatalf("expected %d pairs, got %d", tc.want, got)
			}
		})
	}
}

func TestResolveAdvertisedIP(t *testing.T) {
	candidates := func(values ...string) []net.IP {
		ips := make([]net.IP, 0, len(values))
		for _, value := range values {
			ips = append(ips, net.ParseIP(value))
		}
		return ips
	}

	tests := []struct {
		name       string
		bind       string
		advertised string
		candidates []net.IP
		want       string
		wantCode   string
	}{
		{
			name:       "explicit value wins over enumeration",
			bind:       "0.0.0.0",
			advertised: "203.0.113.9",
			candidates: candidates("192.168.1.10"),
			want:       "203.0.113.9",
		},
		{
			name:       "concrete bind must match the only host address",
			bind:       "192.168.1.10",
			candidates: candidates("192.168.1.10"),
			want:       "192.168.1.10",
		},
		{
			name:       "concrete bind does not bypass ambiguous addresses",
			bind:       "192.168.1.10",
			candidates: candidates("192.168.1.10", "10.0.0.5"),
			wantCode:   CodeAddressUnresolvable,
		},
		{
			name:       "concrete bind must match a unique host address",
			bind:       "192.168.1.10",
			candidates: candidates("192.168.1.11"),
			wantCode:   CodeAddressUnresolvable,
		},
		{
			name:       "single non-loopback IPv4 is inferred",
			bind:       "0.0.0.0",
			candidates: candidates("127.0.0.1", "192.168.1.10"),
			want:       "192.168.1.10",
		},
		{
			name:       "duplicate candidates count once",
			bind:       "0.0.0.0",
			candidates: candidates("192.168.1.10", "192.168.1.10"),
			want:       "192.168.1.10",
		},
		{
			name:       "multiple IPv4 addresses refuse to guess",
			bind:       "0.0.0.0",
			candidates: candidates("192.168.1.10", "10.0.0.5"),
			wantCode:   CodeAddressUnresolvable,
		},
		{
			name:       "no usable address fails loudly",
			bind:       "0.0.0.0",
			candidates: candidates("127.0.0.1"),
			wantCode:   CodeAddressUnresolvable,
		},
		{
			name:       "explicit invalid value is rejected before enumeration",
			bind:       "0.0.0.0",
			advertised: "not-an-ip",
			wantCode:   CodeAddressInvalid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveAdvertisedIP(tc.bind, tc.advertised, tc.candidates)
			if tc.wantCode != "" {
				if err == nil {
					t.Fatalf("expected error code %s, got nil", tc.wantCode)
				}
				var appErr *apperr.Error
				if !errors.As(err, &appErr) {
					t.Fatalf("expected *apperr.Error, got %T", err)
				}
				if appErr.Code != tc.wantCode {
					t.Fatalf("expected code %s, got %s", tc.wantCode, appErr.Code)
				}
				if appErr.Kind != apperr.KindInvalid {
					t.Fatalf("expected HTTP 422 invalid kind, got %q", appErr.Kind)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %s, got %s", tc.want, got)
			}
		})
	}
}

func TestConfigEqualIgnoresAuditTimestamp(t *testing.T) {
	base := DefaultConfig()
	other := base
	other.UpdatedAt = base.UpdatedAt.AddDate(0, 0, 1)
	if !base.Equal(other) {
		t.Fatal("expected UpdatedAt to be ignored by Equal")
	}
	other.MediaAdvertisedIP = "192.168.1.10"
	if base.Equal(other) {
		t.Fatal("expected a changed advertised address to break equality")
	}
}

func TestDeviceCredentialsAADIsDeviceScoped(t *testing.T) {
	first := deviceCredentialsAAD("34020000001320000001")
	second := deviceCredentialsAAD("34020000001320000002")
	if first == second {
		t.Fatal("expected distinct AAD per device")
	}
	if !strings.Contains(first, "34020000001320000001") {
		t.Fatalf("expected the device identity inside the AAD, got %q", first)
	}
}
