package valkey

import (
	"strings"
	"testing"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string // substring; empty means the config is valid
	}{
		{"no address at all means everything stays in memory", Config{}, ""},
		{"the old address field is rejected, not aliased", Config{Address: "a:6379"}, "addresses"},
		{"standalone is the default", Config{Addresses: []string{"a:6379"}}, ""},
		{"an unknown mode names the three that exist", Config{Mode: "clustered", Addresses: []string{"a:6379"}}, "standalone"},
		{"standalone refuses a second address", Config{Mode: ModeStandalone, Addresses: []string{"a:6379", "b:6379"}}, "mode"},
		{"sentinel needs a master set", Config{Mode: ModeSentinel, Addresses: []string{"s1:26379"}}, "masterSet"},
		{"sentinel with a master set is valid", Config{Mode: ModeSentinel, Addresses: []string{"s1:26379"}, MasterSet: "dex"}, ""},
		{"cluster is valid on db 0", Config{Mode: ModeCluster, Addresses: []string{"a:6379", "b:6379", "c:6379"}}, ""},
		{"a valkey cluster has no database but zero", Config{Mode: ModeCluster, Addresses: []string{"a:6379"}, DB: 1}, "db"},
		{"a mode without addresses is a mistake, not a way to disable it", Config{Mode: ModeSentinel}, "addresses"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want no error", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want an error mentioning %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Validate() = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}
