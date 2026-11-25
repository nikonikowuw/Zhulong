package alertpush

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/record"
)

func TestIsAllowedMQTTScheme(t *testing.T) {
	tests := []struct {
		scheme string
		want   bool
	}{
		{"tcp", true},
		{"TCP", true},
		{"ssl", true},
		{"tls", true},
		{"tcps", true},
		{"ws", true},
		{"wss", true},
		{"mqtt", true},
		{"mqtts", true},
		{"http", false},
		{"https", false},
		{"unix", false},
		{"file", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := isAllowedMQTTScheme(tc.scheme); got != tc.want {
			t.Errorf("isAllowedMQTTScheme(%q) = %v, want %v", tc.scheme, got, tc.want)
		}
	}
}

func TestValidateDestinationHostIP(t *testing.T) {
	tests := []struct {
		name          string
		ip            string
		allowLoopback bool
		wantErr       bool
	}{
		{"loopback ipv4 blocked by default", "127.0.0.1", false, true},
		{"loopback ipv4 subnet blocked", "127.10.20.30", false, true},
		{"loopback ipv4 allowed in test mode", "127.0.0.1", true, false},
		{"loopback ipv6 blocked by default", "::1", false, true},
		{"loopback ipv6 allowed in test mode", "::1", true, false},
		{"ipv4-mapped loopback blocked", "::ffff:127.0.0.1", false, true},
		{"cloud metadata link-local blocked even if allowLoopback", "169.254.169.254", true, true},
		{"ipv4-mapped metadata blocked", "::ffff:169.254.169.254", true, true},
		{"ipv6 link-local blocked", "fe80::1", true, true},
		{"unspecified ipv4 blocked", "0.0.0.0", true, true},
		{"this-network 0/8 blocked", "0.1.2.3", true, true},
		{"unspecified ipv6 blocked", "::", true, true},
		{"multicast ipv4 blocked", "224.0.0.1", true, true},
		{"multicast ipv6 blocked", "ff02::1", true, true},
		{"rfc1918 10/8 allowed", "10.0.0.12", false, false},
		{"rfc1918 172.16/12 allowed", "172.16.10.5", false, false},
		{"rfc1918 192.168/16 allowed", "192.168.1.50", false, false},
		{"public ipv4 allowed", "93.184.216.34", false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("net.ParseIP(%q) returned nil", tc.ip)
			}
			err := validateDestinationHostIP(ip, tc.allowLoopback)
			if tc.wantErr {
				if !errors.Is(err, ErrSSRFBlocked) {
					t.Fatalf("validateDestinationHostIP(%s, %v) = %v, want ErrSSRFBlocked", tc.ip, tc.allowLoopback, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateDestinationHostIP(%s, %v) unexpected err: %v", tc.ip, tc.allowLoopback, err)
			}
		})
	}
}

func TestValidateWebhookTargetHost(t *testing.T) {
	if err := validateWebhookTargetHost("localhost:8000", false); !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("localhost blocked: got %v, want ErrSSRFBlocked", err)
	}
	if err := validateWebhookTargetHost("api.localhost:8000", false); !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("sub.localhost blocked: got %v, want ErrSSRFBlocked", err)
	}
	if err := validateWebhookTargetHost("localhost:8000", true); err != nil {
		t.Fatalf("localhost with allowLoopback: unexpected %v", err)
	}
	if err := validateWebhookTargetHost("169.254.169.254:80", true); !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("metadata host: got %v, want ErrSSRFBlocked", err)
	}
	if err := validateWebhookTargetHost("192.168.1.20:8080", false); err != nil {
		t.Fatalf("LAN host should be allowed: %v", err)
	}
}

func TestWebhookSenderSSRFBlocksLoopbackMetadataAndHTTPSDowngrade(t *testing.T) {
	// 1. Default AllowLoopback=false blocks loopback and metadata URLs at construction.
	for _, blockedURL := range []string{
		"http://127.0.0.1:8000/api/v1/health",
		"http://[::1]:8000/hook",
		"http://localhost:8000/hook",
		"http://169.254.169.254/latest/meta-data/",
	} {
		_, err := NewWebhookSender(WebhookSenderOptions{
			ID:        "ssrf-blocked",
			ImageMode: ImageModeMultipart,
			URL:       blockedURL,
		})
		if !errors.Is(err, ErrSSRFBlocked) {
			t.Fatalf("NewWebhookSender(%q) err = %v, want ErrSSRFBlocked", blockedURL, err)
		}
		if reason := classifyError(err); reason != "ssrf_blocked" {
			t.Fatalf("classifyError(%v) = %q, want ssrf_blocked", err, reason)
		}
	}

	// 2. HeartbeatURL is also validated against SSRF rules.
	_, err := NewWebhookSender(WebhookSenderOptions{
		ID:           "ssrf-hb",
		ImageMode:    ImageModeMultipart,
		URL:          "https://10.0.0.20/events",
		HeartbeatURL: "http://169.254.169.254/latest/meta-data/",
	})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("HeartbeatURL metadata err = %v, want ErrSSRFBlocked", err)
	}

	// 3. HTTPS -> HTTP downgrade redirect is refused even when AuthType=none and no headers/secret.
	var httpsHits atomic.Int32
	var plainHits atomic.Int32
	plainServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		plainHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer plainServer.Close()

	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpsHits.Add(1)
		http.Redirect(w, r, plainServer.URL+"/downgraded", http.StatusTemporaryRedirect)
	}))
	defer tlsServer.Close()

	sender, err := NewWebhookSender(WebhookSenderOptions{
		ID:            "https-downgrade",
		ImageMode:     ImageModeMultipart,
		URL:           tlsServer.URL + "/events",
		AuthType:      AuthTypeNone,
		TLSSkipVerify: true,
		AllowLoopback: true,
	})
	if err != nil {
		t.Fatalf("NewWebhookSender: %v", err)
	}
	defer func() {
		if closeErr := sender.Close(); closeErr != nil {
			t.Errorf("Close: %v", closeErr)
		}
	}()

	sendErr := sender.Send(context.Background(), Delivery{
		Event: record.AlarmEvent{ID: "01DOWNGRADE", CapturedAt: time.Now().UTC()},
	})
	if !errors.Is(sendErr, ErrWebhookHTTPSDowngrade) {
		t.Fatalf("expected ErrWebhookHTTPSDowngrade, got %v", sendErr)
	}
	if reason := classifyError(sendErr); reason != "redirect_blocked" {
		t.Fatalf("classifyError(%v) = %q, want redirect_blocked", sendErr, reason)
	}
	if httpsHits.Load() != 1 || plainHits.Load() != 0 {
		t.Fatalf("expected 1 TLS hit and 0 plain hits, got tls=%d plain=%d", httpsHits.Load(), plainHits.Load())
	}

	// 4. Redirect to link-local metadata IP is blocked and non-retryable.
	var redirMetadataHits atomic.Int32
	redirServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirMetadataHits.Add(1)
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusTemporaryRedirect)
	}))
	defer redirServer.Close()

	metaSender, err := NewWebhookSender(WebhookSenderOptions{
		ID:            "redir-meta",
		ImageMode:     ImageModeMultipart,
		URL:           redirServer.URL + "/events",
		AllowLoopback: true,
	})
	if err != nil {
		t.Fatalf("NewWebhookSender meta: %v", err)
	}
	defer func() {
		if closeErr := metaSender.Close(); closeErr != nil {
			t.Errorf("Close: %v", closeErr)
		}
	}()

	metaErr := metaSender.Send(context.Background(), Delivery{
		Event: record.AlarmEvent{ID: "01META", CapturedAt: time.Now().UTC()},
	})
	if !errors.Is(metaErr, ErrSSRFBlocked) {
		t.Fatalf("expected ErrSSRFBlocked on metadata redirect, got %v", metaErr)
	}
	if redirMetadataHits.Load() != 1 {
		t.Fatalf("metadata redirect must not retry: hits=%d, want 1", redirMetadataHits.Load())
	}
}

func TestMQTTSenderSSRFAndSchemeValidation(t *testing.T) {
	// Invalid schemes rejected.
	for _, badBroker := range []string{
		"http://10.0.0.5:1883",
		"https://10.0.0.5:8883",
		"unix:///var/run/mqtt.sock",
		"file:///etc/passwd",
	} {
		_, err := NewMQTTSender(MQTTSenderOptions{
			ID:        "bad-scheme",
			ImageMode: ImageModeBase64,
			Broker:    badBroker,
			Topic:     "zhulong/events",
		})
		if !errors.Is(err, ErrInvalidMQTTScheme) {
			t.Fatalf("NewMQTTSender(%q) = %v, want ErrInvalidMQTTScheme", badBroker, err)
		}
		if reason := classifyError(err); reason != "config_invalid" {
			t.Fatalf("classifyError(%v) = %q, want config_invalid", err, reason)
		}
	}

	// Loopback and link-local blocked when AllowLoopback=false.
	for _, blockedBroker := range []string{
		"tcp://127.0.0.1:1883",
		"tcp://localhost:1883",
		"ssl://[::1]:8883",
		"tcp://169.254.169.254:1883",
	} {
		_, err := NewMQTTSender(MQTTSenderOptions{
			ID:        "blocked-broker",
			ImageMode: ImageModeBase64,
			Broker:    blockedBroker,
			Topic:     "zhulong/events",
		})
		if !errors.Is(err, ErrSSRFBlocked) {
			t.Fatalf("NewMQTTSender(%q) = %v, want ErrSSRFBlocked", blockedBroker, err)
		}
	}

	// RFC1918 LAN broker allowed.
	lanSender, err := NewMQTTSender(MQTTSenderOptions{
		ID:        "lan-broker",
		ImageMode: ImageModeBase64,
		Broker:    "tcp://192.168.1.50:1883",
		Topic:     "zhulong/events",
	})
	if err != nil {
		t.Fatalf("expected LAN MQTT broker allowed, got %v", err)
	}
	if closeErr := lanSender.Close(); closeErr != nil {
		t.Errorf("Close: %v", closeErr)
	}
}
