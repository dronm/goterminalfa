package config

import (
	"strings"
	"testing"
)

func TestDeviceProtocolProfiles(t *testing.T) {
	for _, profile := range []string{"", "base", "ffd1.2", "1.2", "ffd12", "unknown"} {
		t.Run(profile, func(t *testing.T) {
			path := configFile(t, func(c map[string]any) {
				c["devices"].([]any)[0].(map[string]any)["protocolProfile"] = profile
			})
			cfg, err := Load(path)
			if profile != "" && profile != "base" && profile != "ffd1.2" {
				if err == nil || !strings.Contains(err.Error(), "protocolProfile") {
					t.Fatalf("unsupported profile accepted: %v", err)
				}
				return
			}
			want := profile
			if want == "" {
				want = "base"
			}
			if err != nil || cfg.Devices[0].ProtocolProfile != want {
				t.Fatalf("profile/default mismatch: %+v, %v", cfg.Devices, err)
			}
		})
	}
	path := configFile(t, func(c map[string]any) {
		ffd := tcpDevice("ffd-device", "127.0.0.1:7778")
		ffd["protocolProfile"] = "ffd1.2"
		c["devices"] = append(c["devices"].([]any), ffd)
	})
	cfg, err := Load(path)
	if err != nil || cfg.Devices[0].ProtocolProfile != "base" || cfg.Devices[1].ProtocolProfile != "ffd1.2" {
		t.Fatalf("profiles are not independent of transport: %+v, %v", cfg.Devices, err)
	}
}
