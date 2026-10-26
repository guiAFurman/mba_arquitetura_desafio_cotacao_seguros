package platform

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port      string
	Partners  []Partner
	Tenants   []string
	Telemetry Telemetry
	Timeouts  Timeouts
	Breaker   Breaker
	Cache     Cache
}

// Cache sao os parametros do cache de cotacoes (docs/sad.md §4.3). Sem RedisAddr, nao ha cache.
type Cache struct {
	RedisAddr    string
	RedisTimeout time.Duration
	FreshFor     time.Duration
	MaxAge       time.Duration
	KeySecret    string
}

type Partner struct {
	Name    string
	BaseURL string
}

// Timeouts guarda o teto de espera de cada parceira (docs/sad.md §4.1): latencia maxima em repouso
// + 300 ms, arredondada para o proximo multiplo de 500 ms. Parceira sem valor proprio usa Default.
type Timeouts struct {
	Default   time.Duration
	ByPartner map[string]time.Duration
}

func (t Timeouts) For(partner string) time.Duration {
	if d, ok := t.ByPartner[partner]; ok {
		return d
	}
	return t.Default
}

// Max e o maior teto configurado: nenhuma chamada a parceira espera mais que isso.
func (t Timeouts) Max() time.Duration {
	longest := t.Default
	for _, d := range t.ByPartner {
		longest = max(longest, d)
	}
	return longest
}

// Breaker sao os parametros do circuit breaker por parceira (docs/sad.md §4.2).
type Breaker struct {
	MinRequests      uint32
	FailureRatio     float64
	Interval         time.Duration
	OpenTimeout      time.Duration
	HalfOpenRequests uint32
}

type Telemetry struct {
	ServiceName string
	Endpoint    string
	Enabled     bool
}

const defaultEndpoints = "partner-slow=http://localhost:9001," +
	"partner-flaky=http://localhost:9002," +
	"partner-degrading=http://localhost:9003"

const defaultTenants = "corretora-a,corretora-b"

const defaultCollector = "http://localhost:4317"

const defaultServiceName = "quotation-api"

const defaultPartnerTimeouts = "partner-slow=2s,partner-flaky=500ms,partner-degrading=500ms"

// O maior dos timeouts acima: uma parceira sem valor proprio nunca espera mais que a mais lenta.
const defaultPartnerTimeout = "2s"

func LoadConfig(env func(string) string) (Config, error) {
	cfg := Config{Port: text(env, "PORT", "8080")}

	var err error
	if cfg.Partners, err = parsePartners(text(env, "PARTNER_ENDPOINTS", defaultEndpoints)); err != nil {
		return Config{}, err
	}
	if cfg.Tenants, err = parseTenants(text(env, "TENANTS", defaultTenants)); err != nil {
		return Config{}, err
	}
	if cfg.Telemetry, err = parseTelemetry(env); err != nil {
		return Config{}, err
	}
	if cfg.Timeouts, err = parseTimeouts(env); err != nil {
		return Config{}, err
	}
	if cfg.Breaker, err = parseBreaker(env); err != nil {
		return Config{}, err
	}
	if cfg.Cache, err = parseCache(env); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func parseCache(env func(string) string) (Cache, error) {
	c := Cache{
		RedisAddr: strings.TrimSpace(text(env, "REDIS_ADDR", "localhost:6379")),
		KeySecret: env("CACHE_KEY_SECRET"),
	}
	if strings.EqualFold(c.RedisAddr, "off") {
		c.RedisAddr = ""
	}

	var err error
	if c.RedisTimeout, err = positiveDuration("REDIS_TIMEOUT", text(env, "REDIS_TIMEOUT", "100ms")); err != nil {
		return Cache{}, err
	}
	if c.FreshFor, err = positiveDuration("CACHE_FRESH_TTL", text(env, "CACHE_FRESH_TTL", "1h")); err != nil {
		return Cache{}, err
	}
	if c.MaxAge, err = positiveDuration("CACHE_MAX_AGE", text(env, "CACHE_MAX_AGE", "24h")); err != nil {
		return Cache{}, err
	}

	switch {
	case c.FreshFor > c.MaxAge:
		return Cache{}, fmt.Errorf("CACHE_FRESH_TTL (%s) cannot be longer than CACHE_MAX_AGE (%s)", c.FreshFor, c.MaxAge)
	case c.MaxAge > 24*time.Hour:
		return Cache{}, fmt.Errorf("CACHE_MAX_AGE (%s) exceeds the 24h the insurers honour a premium", c.MaxAge)
	}
	return c, nil
}

func parseTimeouts(env func(string) string) (Timeouts, error) {
	fallback, err := positiveDuration("PARTNER_TIMEOUT_DEFAULT", text(env, "PARTNER_TIMEOUT_DEFAULT", defaultPartnerTimeout))
	if err != nil {
		return Timeouts{}, err
	}

	timeouts := Timeouts{Default: fallback, ByPartner: map[string]time.Duration{}}
	for _, item := range strings.Split(text(env, "PARTNER_TIMEOUTS", defaultPartnerTimeouts), ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		name, raw, found := strings.Cut(item, "=")
		name = strings.TrimSpace(name)
		if !found || name == "" {
			return Timeouts{}, fmt.Errorf("PARTNER_TIMEOUTS: %q is not in the name=duration format", item)
		}
		d, err := positiveDuration("PARTNER_TIMEOUTS", strings.TrimSpace(raw))
		if err != nil {
			return Timeouts{}, err
		}
		timeouts.ByPartner[name] = d
	}
	return timeouts, nil
}

func parseBreaker(env func(string) string) (Breaker, error) {
	var b Breaker
	var err error

	if b.MinRequests, err = positiveCount("BREAKER_MIN_REQUESTS", text(env, "BREAKER_MIN_REQUESTS", "10")); err != nil {
		return Breaker{}, err
	}
	if b.HalfOpenRequests, err = positiveCount("BREAKER_HALF_OPEN_REQUESTS", text(env, "BREAKER_HALF_OPEN_REQUESTS", "2")); err != nil {
		return Breaker{}, err
	}
	if b.Interval, err = positiveDuration("BREAKER_INTERVAL", text(env, "BREAKER_INTERVAL", "30s")); err != nil {
		return Breaker{}, err
	}
	if b.OpenTimeout, err = positiveDuration("BREAKER_OPEN_TIMEOUT", text(env, "BREAKER_OPEN_TIMEOUT", "15s")); err != nil {
		return Breaker{}, err
	}

	raw := text(env, "BREAKER_FAILURE_RATIO", "0.5")
	if b.FailureRatio, err = strconv.ParseFloat(strings.TrimSpace(raw), 64); err != nil || b.FailureRatio <= 0 || b.FailureRatio > 1 {
		return Breaker{}, fmt.Errorf("BREAKER_FAILURE_RATIO: %q is not a ratio between 0 (exclusive) and 1", raw)
	}
	return b, nil
}

func positiveDuration(key, raw string) (time.Duration, error) {
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s: %q is not a positive duration (e.g. 500ms, 2s)", key, raw)
	}
	return d, nil
}

func positiveCount(key, raw string) (uint32, error) {
	n, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 32)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("%s: %q is not a positive integer", key, raw)
	}
	return uint32(n), nil
}

func parseTelemetry(env func(string) string) (Telemetry, error) {
	telemetry := Telemetry{
		ServiceName: text(env, "OTEL_SERVICE_NAME", defaultServiceName),
		Endpoint:    strings.TrimSpace(text(env, "OTEL_EXPORTER_OTLP_ENDPOINT", defaultCollector)),
	}

	switch disabled := strings.ToLower(strings.TrimSpace(env("OTEL_SDK_DISABLED"))); disabled {
	case "", "false":
		telemetry.Enabled = true
	case "true":
		return Telemetry{Enabled: false}, nil
	default:
		return Telemetry{}, fmt.Errorf("OTEL_SDK_DISABLED: %q is neither true nor false", disabled)
	}

	if u, err := url.Parse(telemetry.Endpoint); err != nil || u.Scheme == "" || u.Host == "" {
		return Telemetry{}, fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT: %q is not an absolute URL", telemetry.Endpoint)
	}
	return telemetry, nil
}

func parsePartners(raw string) ([]Partner, error) {
	var partners []Partner
	seen := map[string]bool{}

	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		name, endpoint, found := strings.Cut(item, "=")
		name, endpoint = strings.TrimSpace(name), strings.TrimSpace(endpoint)
		if !found || name == "" || endpoint == "" {
			return nil, fmt.Errorf("PARTNER_ENDPOINTS: %q is not in the name=url format", item)
		}

		if u, err := url.Parse(endpoint); err != nil || u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("PARTNER_ENDPOINTS: %q is not an absolute URL", endpoint)
		}
		if seen[name] {
			return nil, fmt.Errorf("PARTNER_ENDPOINTS: partner %q is repeated", name)
		}

		seen[name] = true
		partners = append(partners, Partner{Name: name, BaseURL: strings.TrimRight(endpoint, "/")})
	}

	if len(partners) == 0 {
		return nil, fmt.Errorf("PARTNER_ENDPOINTS: at least one partner is required")
	}
	return partners, nil
}

func parseTenants(raw string) ([]string, error) {
	var tenants []string
	for _, t := range strings.Split(raw, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tenants = append(tenants, t)
		}
	}
	if len(tenants) == 0 {
		return nil, fmt.Errorf("TENANTS: at least one broker is required")
	}
	return tenants, nil
}

func Environment() func(string) string { return os.Getenv }

func text(env func(string) string, key, fallback string) string {
	if v := env(key); v != "" {
		return v
	}
	return fallback
}
