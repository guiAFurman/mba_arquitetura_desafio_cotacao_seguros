package platform

import (
	"testing"
	"time"
)

func TestDefaultTimeoutsAndBreakerAreTheOnesDefendedInTheSAD(t *testing.T) {
	cfg, err := LoadConfig(env(nil))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	timeouts := map[string]time.Duration{
		"partner-slow":      2 * time.Second,
		"partner-flaky":     500 * time.Millisecond,
		"partner-degrading": 500 * time.Millisecond,
		"partner-unknown":   2 * time.Second,
	}
	for partner, want := range timeouts {
		if got := cfg.Timeouts.For(partner); got != want {
			t.Errorf("timeout of %s is %s, want %s (docs/sad.md §4.1)", partner, got, want)
		}
	}

	want := Breaker{MinRequests: 10, FailureRatio: 0.5, Interval: 30 * time.Second, OpenTimeout: 15 * time.Second, HalfOpenRequests: 2}
	if cfg.Breaker != want {
		t.Errorf("breaker %+v, want %+v (docs/sad.md §4.2)", cfg.Breaker, want)
	}
}

func TestTimeoutsAndBreakerReadTheEnvironment(t *testing.T) {
	cfg, err := LoadConfig(env(map[string]string{
		"PARTNER_TIMEOUTS":           " a=1s , b=250ms ",
		"PARTNER_TIMEOUT_DEFAULT":    "3s",
		"BREAKER_MIN_REQUESTS":       "20",
		"BREAKER_FAILURE_RATIO":      "0.6",
		"BREAKER_INTERVAL":           "1m",
		"BREAKER_OPEN_TIMEOUT":       "10s",
		"BREAKER_HALF_OPEN_REQUESTS": "3",
	}))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.Timeouts.For("a") != time.Second || cfg.Timeouts.For("b") != 250*time.Millisecond || cfg.Timeouts.For("c") != 3*time.Second {
		t.Errorf("unexpected timeouts %+v", cfg.Timeouts)
	}
	want := Breaker{MinRequests: 20, FailureRatio: 0.6, Interval: time.Minute, OpenTimeout: 10 * time.Second, HalfOpenRequests: 3}
	if cfg.Breaker != want {
		t.Errorf("breaker %+v, want %+v", cfg.Breaker, want)
	}
}

func TestInvalidResilienceConfigFails(t *testing.T) {
	cases := map[string]map[string]string{
		"timeout without duration":  {"PARTNER_TIMEOUTS": "partner-slow"},
		"timeout not a duration":    {"PARTNER_TIMEOUTS": "partner-slow=fast"},
		"zero timeout":              {"PARTNER_TIMEOUTS": "partner-slow=0s"},
		"default timeout negative":  {"PARTNER_TIMEOUT_DEFAULT": "-1s"},
		"ratio above one":           {"BREAKER_FAILURE_RATIO": "1.5"},
		"ratio zero":                {"BREAKER_FAILURE_RATIO": "0"},
		"zero minimum requests":     {"BREAKER_MIN_REQUESTS": "0"},
		"zero half-open requests":   {"BREAKER_HALF_OPEN_REQUESTS": "0"},
		"interval not a duration":   {"BREAKER_INTERVAL": "30"},
		"open timeout not positive": {"BREAKER_OPEN_TIMEOUT": "0s"},
	}

	for name, vars := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadConfig(env(vars)); err == nil {
				t.Fatalf("invalid configuration (%v) was accepted", vars)
			}
		})
	}
}
