package main

import (
	"fmt"
	"net/netip"
	"strconv"

	"github.com/livant05/rrhh-go/internal/handlers"
	"github.com/livant05/rrhh-go/internal/ratelimit"
)

// rateConfig is the public-endpoint throttling configuration read from the
// environment.
type rateConfig struct {
	AuthPerMin        int            // AUTH_RATE_PER_MIN: failed logins (and register) per client IP
	AuthAttemptPerMin int            // AUTH_ATTEMPT_RATE_PER_MIN: all login attempts, successes included, per client IP
	ADMSPerMin        int            // ADMS_RATE_PER_MIN: device ingestion per client IP (provisional)
	TrustedProxies    []netip.Prefix // TRUSTED_PROXY_CIDRS: proxies whose X-Real-IP is believed
}

// loadRateConfig reads the rate-limit settings through getenv. Rates must be
// positive integers; an invalid value fails startup naming the variable
// rather than silently disabling a protection. Unset TRUSTED_PROXY_CIDRS
// trusts nobody.
func loadRateConfig(getenv func(string) string) (rateConfig, error) {
	var cfg rateConfig
	var err error
	if cfg.AuthPerMin, err = positiveIntEnv(getenv, "AUTH_RATE_PER_MIN", handlers.DefaultAuthRatePerMin); err != nil {
		return rateConfig{}, err
	}
	if cfg.AuthAttemptPerMin, err = positiveIntEnv(getenv, "AUTH_ATTEMPT_RATE_PER_MIN", handlers.DefaultAuthAttemptRatePerMin); err != nil {
		return rateConfig{}, err
	}
	if cfg.ADMSPerMin, err = positiveIntEnv(getenv, "ADMS_RATE_PER_MIN", handlers.DefaultADMSRatePerMin); err != nil {
		return rateConfig{}, err
	}
	if cfg.TrustedProxies, err = ratelimit.ParseCIDRs(getenv("TRUSTED_PROXY_CIDRS")); err != nil {
		return rateConfig{}, fmt.Errorf("TRUSTED_PROXY_CIDRS: %w", err)
	}
	return cfg, nil
}

func positiveIntEnv(getenv func(string) string, key string, def int) (int, error) {
	v := getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", key, v)
	}
	return n, nil
}
