package main

import (
	"strings"
	"testing"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadRateConfig_Defaults(t *testing.T) {
	cfg, err := loadRateConfig(envFrom(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AuthPerMin != 10 || cfg.ADMSPerMin != 600 || cfg.AuthSuccessPerMin != 60 {
		t.Fatalf("defaults = %d/%d/%d, want 10/600/60", cfg.AuthPerMin, cfg.ADMSPerMin, cfg.AuthSuccessPerMin)
	}
	if len(cfg.TrustedProxies) != 0 {
		t.Fatalf("unset TRUSTED_PROXY_CIDRS must trust nobody, got %v", cfg.TrustedProxies)
	}
}

func TestLoadRateConfig_Overrides(t *testing.T) {
	cfg, err := loadRateConfig(envFrom(map[string]string{
		"AUTH_RATE_PER_MIN":         "30",
		"AUTH_SUCCESS_RATE_PER_MIN": "90",
		"ADMS_RATE_PER_MIN":         "1200",
		"TRUSTED_PROXY_CIDRS":       "10.0.0.0/8, 172.16.0.0/12,192.168.0.0/16",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AuthPerMin != 30 || cfg.ADMSPerMin != 1200 || cfg.AuthSuccessPerMin != 90 {
		t.Fatalf("got %d/%d/%d, want 30/1200/90", cfg.AuthPerMin, cfg.ADMSPerMin, cfg.AuthSuccessPerMin)
	}
	if len(cfg.TrustedProxies) != 3 || cfg.TrustedProxies[1].String() != "172.16.0.0/12" {
		t.Fatalf("trusted proxies = %v", cfg.TrustedProxies)
	}
}

func TestLoadRateConfig_RejectsInvalidValuesNamingTheVariable(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"auth zero", map[string]string{"AUTH_RATE_PER_MIN": "0"}, "AUTH_RATE_PER_MIN"},
		{"auth negative", map[string]string{"AUTH_RATE_PER_MIN": "-5"}, "AUTH_RATE_PER_MIN"},
		{"auth not a number", map[string]string{"AUTH_RATE_PER_MIN": "ten"}, "AUTH_RATE_PER_MIN"},
		{"success zero", map[string]string{"AUTH_SUCCESS_RATE_PER_MIN": "0"}, "AUTH_SUCCESS_RATE_PER_MIN"},
		{"success negative", map[string]string{"AUTH_SUCCESS_RATE_PER_MIN": "-1"}, "AUTH_SUCCESS_RATE_PER_MIN"},
		{"success not a number", map[string]string{"AUTH_SUCCESS_RATE_PER_MIN": "sixty"}, "AUTH_SUCCESS_RATE_PER_MIN"},
		{"adms zero", map[string]string{"ADMS_RATE_PER_MIN": "0"}, "ADMS_RATE_PER_MIN"},
		{"adms not a number", map[string]string{"ADMS_RATE_PER_MIN": "1e3"}, "ADMS_RATE_PER_MIN"},
		{"bad cidr", map[string]string{"TRUSTED_PROXY_CIDRS": "10.0.0.0/8,nope"}, "TRUSTED_PROXY_CIDRS"},
		{"bare ip is not a cidr", map[string]string{"TRUSTED_PROXY_CIDRS": "10.0.0.1"}, "TRUSTED_PROXY_CIDRS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadRateConfig(envFrom(tt.env))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected an error naming %s, got %v", tt.want, err)
			}
		})
	}
}
