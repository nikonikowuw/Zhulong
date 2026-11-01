package algorunner

import (
	"slices"
	"testing"
)

// TestRunnerArgs pins the argv contract of a package process.
//
// It is a pure function test on purpose: argv is the only thing the host tells a
// package about its task, and asserting it here (table-driven, no spawn) is both
// exact and fast, while the end-to-end arg tests still cover the wiring from
// ProcessConfig through os/exec.
func TestRunnerArgs(t *testing.T) {
	tests := []struct {
		name string
		cfg  ProcessConfig
		want []string
	}{
		{
			name: "no plumbing",
			cfg:  ProcessConfig{},
			want: []string{},
		},
		{
			name: "single broker socket",
			cfg:  ProcessConfig{BrokerSocks: []string{"/tmp/zhulong_broker_1.sock"}},
			want: []string{"--broker", "/tmp/zhulong_broker_1.sock"},
		},
		{
			name: "multiple broker sockets keep their order",
			cfg: ProcessConfig{BrokerSocks: []string{
				"/tmp/zhulong_broker_1.sock",
				"/tmp/zhulong_broker_2.sock",
			}},
			want: []string{
				"--broker", "/tmp/zhulong_broker_1.sock",
				"--broker", "/tmp/zhulong_broker_2.sock",
			},
		},
		{
			name: "empty broker socket is dropped",
			cfg:  ProcessConfig{BrokerSocks: []string{"", "/tmp/zhulong_broker_1.sock"}},
			want: []string{"--broker", "/tmp/zhulong_broker_1.sock"},
		},
		{
			name: "name and id are separate flags",
			cfg:  ProcessConfig{AlgorithmID: "pkg_id", AlgorithmName: "人脸检测"},
			want: []string{"--name", "人脸检测", "--algorithm-id", "pkg_id"},
		},
		{
			name: "motion gate is disabled by flag only",
			cfg:  ProcessConfig{DisableMotionGate: true},
			want: []string{"--disable-motion-gate"},
		},
		{
			name: "everything together",
			cfg: ProcessConfig{
				AlgorithmID:       "pkg_id",
				AlgorithmName:     "pkg_name",
				BrokerSocks:       []string{"/tmp/a.sock"},
				DisableMotionGate: true,
			},
			want: []string{
				"--broker", "/tmp/a.sock",
				"--name", "pkg_name",
				"--algorithm-id", "pkg_id",
				"--disable-motion-gate",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewRunnerProcess(tt.cfg).runnerArgs()
			if !slices.Equal(got, tt.want) {
				t.Fatalf("runnerArgs() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestMergeEnvironKeepsOneEntryPerKey pins the dedupe rule: a key that exists in
// the host environment and in the overlay must end up with exactly one entry, and
// with the overlay's value. Two entries would make the effective value depend on
// the C library's getenv() scan order instead of on the host's intent.
func TestMergeEnvironKeepsOneEntryPerKey(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"ZHULONG_ENV_PROBE=from_host",
		"MALFORMED_NO_EQUALS",
	}
	overlay := map[string]string{
		"ZHULONG_ENV_PROBE": "from_package",
		"EXTRA_ONLY":        "1",
	}

	got := mergeEnviron(base, overlay)

	seen := make(map[string]int, len(got))
	for _, kv := range got {
		key, _, found := cutKeyValue(kv)
		if !found {
			t.Fatalf("mergeEnviron kept a malformed entry %q", kv)
		}
		seen[key]++
	}
	if seen["ZHULONG_ENV_PROBE"] != 1 || seen["EXTRA_ONLY"] != 1 || seen["PATH"] != 1 {
		t.Fatalf("keys were duplicated or dropped: %v", seen)
	}
	if seen["MALFORMED_NO_EQUALS"] != 0 {
		t.Fatalf("a malformed host entry was kept: %v", seen)
	}
	if !slices.Contains(got, "ZHULONG_ENV_PROBE=from_package") {
		t.Fatalf("the overlay value did not win: %q", got)
	}
}

func cutKeyValue(kv string) (key, value string, found bool) {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			return kv[:i], kv[i+1:], true
		}
	}
	return kv, "", false
}
