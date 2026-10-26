package quotation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/partner"
	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/platform"
	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/quotecache"
)

var (
	now       = time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	sadPolicy = CachePolicy{FreshFor: time.Hour, MaxAge: 24 * time.Hour}
	testKeys  = func() *quotecache.Keyer { k, _ := quotecache.NewKeyer("segredo-de-teste"); return k }()
)

// memoryCache e o Redis em memoria, para testar a politica sem rede.
type memoryCache struct {
	entries map[string]*quotecache.Entry
	down    bool
	calls   int
}

func newMemoryCache() *memoryCache { return &memoryCache{entries: map[string]*quotecache.Entry{}} }

func (m *memoryCache) Lookup(_ context.Context, keys []string) ([]*quotecache.Entry, error) {
	m.calls++
	out := make([]*quotecache.Entry, len(keys))
	if m.down {
		return out, errors.New("redis down")
	}
	for i, k := range keys {
		out[i] = m.entries[k]
	}
	return out, nil
}

func (m *memoryCache) Save(_ context.Context, key string, quote partner.Quote, at time.Time) error {
	m.calls++
	if m.down {
		return errors.New("redis down")
	}
	m.entries[key] = &quotecache.Entry{Quote: quote, CachedAt: at}
	return nil
}

func (m *memoryCache) put(tenant, name string, premium int64, age time.Duration) {
	key := testKeys.Key(tenant, name, validRequest().riskFields())
	m.entries[key] = &quotecache.Entry{
		Quote:    partner.Quote{Partner: name, QuoteID: name + "-original", PremiumCents: premium, Currency: "BRL"},
		CachedAt: now.Add(-age),
	}
}

// partners responde por parceira: erro configurado ou o premio do mapa.
type partners struct {
	premiums map[string]int64
	errs     map[string]error
	calls    map[string]int
}

func newPartners() *partners {
	return &partners{premiums: defaultPremiums(), errs: map[string]error{}, calls: map[string]int{}}
}

func (p *partners) Quote(_ context.Context, partnerCfg platform.Partner, _ any) (partner.Quote, error) {
	p.calls[partnerCfg.Name]++
	if err := p.errs[partnerCfg.Name]; err != nil {
		return partner.Quote{}, err
	}
	return partner.Quote{Partner: partnerCfg.Name, QuoteID: partnerCfg.Name + "-now", PremiumCents: p.premiums[partnerCfg.Name], Currency: "BRL"}, nil
}

func failed(name string) error {
	return &partner.Error{Partner: name, Status: 503, Reason: "partner replied 503"}
}

func service(p *partners, cache Cache) *Service {
	opts := []Option{WithClock(func() time.Time { return now })}
	if cache != nil {
		opts = append(opts, WithCache(cache, testKeys, sadPolicy))
	}
	return NewService(threePartners, p, opts...)
}

func TestOnePartnerDownGivesAPartialResponseInsteadOf502(t *testing.T) {
	p := newPartners()
	p.errs["partner-flaky"] = failed("partner-flaky")

	response, err := service(p, nil).QuoteWithFallback(context.Background(), "corretora-a", validRequest())
	if err != nil {
		t.Fatalf("one partner down and the quotation failed: %v", err)
	}

	if !response.Degraded {
		t.Error("a response without partner-flaky is not marked as degraded")
	}
	if len(response.Quotes) != 2 || response.Quotes[0].Partner != "partner-degrading" || response.Quotes[1].Partner != "partner-slow" {
		t.Fatalf("quotes %+v, expected degrading then slow, sorted by premium", response.Quotes)
	}
	want := []MissingPartner{{Partner: "partner-flaky", Reason: ReasonPartnerError}}
	if len(response.MissingPartners) != 1 || response.MissingPartners[0] != want[0] {
		t.Fatalf("missing_partners %+v, want %+v", response.MissingPartners, want)
	}
	for _, q := range response.Quotes {
		if q.Source != SourcePartner || q.Stale {
			t.Errorf("quote of %s marked %q stale=%v, expected a fresh partner quote", q.Partner, q.Source, q.Stale)
		}
	}
}

func TestTheReasonSaysWhyThePartnerIsMissing(t *testing.T) {
	cases := map[string]error{
		ReasonCircuitOpen:  &partner.Error{Partner: "partner-flaky", Reason: "circuit open", Err: partner.ErrCircuitOpen},
		ReasonTimeout:      &partner.Error{Partner: "partner-flaky", Reason: "timeout", Err: context.DeadlineExceeded},
		ReasonPartnerError: failed("partner-flaky"),
	}
	for want, err := range cases {
		t.Run(want, func(t *testing.T) {
			p := newPartners()
			p.errs["partner-flaky"] = err
			response, _ := service(p, nil).QuoteWithFallback(context.Background(), "corretora-a", validRequest())
			if len(response.MissingPartners) != 1 || response.MissingPartners[0].Reason != want {
				t.Fatalf("missing_partners %+v, want reason %q", response.MissingPartners, want)
			}
		})
	}
}

func TestAFreshCachedQuoteDoesNotCallThePartner(t *testing.T) {
	p := newPartners()
	cache := newMemoryCache()
	cache.put("corretora-a", "partner-slow", 180000, 10*time.Minute)

	response, err := service(p, cache).QuoteWithFallback(context.Background(), "corretora-a", validRequest())
	if err != nil {
		t.Fatalf("QuoteWithFallback: %v", err)
	}

	if p.calls["partner-slow"] != 0 {
		t.Fatalf("partner-slow was called %d times with a 10-minute-old quote in the cache", p.calls["partner-slow"])
	}
	slow := find(t, response, "partner-slow")
	if slow.Source != SourceCache || slow.AgeSeconds != 600 || slow.Stale || slow.QuoteID != "partner-slow-original" {
		t.Fatalf("cached quote presented as %+v, want source=cache age=600 stale=false and the original quote_id", slow)
	}
	if response.Degraded {
		t.Error("a fresh cached quote is not a degradation")
	}
}

func TestAnExpiredQuoteIsOnlyServedWhenThePartnerFails(t *testing.T) {
	cache := newMemoryCache()
	cache.put("corretora-a", "partner-flaky", 90000, 3*time.Hour)

	healthy := newPartners()
	response, _ := service(healthy, cache).QuoteWithFallback(context.Background(), "corretora-a", validRequest())
	if healthy.calls["partner-flaky"] != 1 || find(t, response, "partner-flaky").Source != SourcePartner {
		t.Fatal("a 3-hour-old quote was served while the partner was answering")
	}

	cache.put("corretora-a", "partner-flaky", 90000, 3*time.Hour)
	down := newPartners()
	down.errs["partner-flaky"] = failed("partner-flaky")
	response, _ = service(down, cache).QuoteWithFallback(context.Background(), "corretora-a", validRequest())

	flaky := find(t, response, "partner-flaky")
	if flaky.Source != SourceCache || !flaky.Stale || flaky.AgeSeconds != 3*3600 {
		t.Fatalf("expired quote presented as %+v, want source=cache stale=true age=10800", flaky)
	}
	if !response.Degraded || len(response.MissingPartners) != 0 {
		t.Fatalf("degraded=%v missing=%+v: a stale quote degrades the response without a missing partner",
			response.Degraded, response.MissingPartners)
	}
}

func TestAQuoteOlderThan24hIsNeverServed(t *testing.T) {
	cache := newMemoryCache()
	cache.put("corretora-a", "partner-flaky", 90000, 25*time.Hour)
	p := newPartners()
	p.errs["partner-flaky"] = failed("partner-flaky")

	response, _ := service(p, cache).QuoteWithFallback(context.Background(), "corretora-a", validRequest())
	for _, q := range response.Quotes {
		if q.Partner == "partner-flaky" {
			t.Fatalf("a 25-hour-old quote was served: %+v", q)
		}
	}
}

func TestASuccessfulAnswerIsSavedForTheBroker(t *testing.T) {
	cache := newMemoryCache()
	if _, err := service(newPartners(), cache).QuoteWithFallback(context.Background(), "corretora-a", validRequest()); err != nil {
		t.Fatalf("QuoteWithFallback: %v", err)
	}

	key := testKeys.Key("corretora-a", "partner-flaky", validRequest().riskFields())
	entry := cache.entries[key]
	if entry == nil || entry.Quote.QuoteID != "partner-flaky-now" || !entry.CachedAt.Equal(now) {
		t.Fatalf("entry for %s is %+v, want the partner answer saved at %s", key, entry, now)
	}
}

func TestOneBrokerNeverReceivesTheCachedQuoteOfAnother(t *testing.T) {
	cache := newMemoryCache()
	cache.put("corretora-a", "partner-slow", 111111, time.Minute)
	cache.put("corretora-a", "partner-flaky", 222222, time.Minute)
	cache.put("corretora-a", "partner-degrading", 333333, time.Minute)

	p := newPartners()
	response, err := service(p, cache).QuoteWithFallback(context.Background(), "corretora-b", validRequest())
	if err != nil {
		t.Fatalf("QuoteWithFallback: %v", err)
	}

	for _, q := range response.Quotes {
		if q.Source == SourceCache {
			t.Fatalf("corretora-b received the quote cached for corretora-a: %+v", q)
		}
	}
	if p.calls["partner-slow"] != 1 || p.calls["partner-flaky"] != 1 || p.calls["partner-degrading"] != 1 {
		t.Fatalf("calls %v: corretora-b must be quoted by the partners, not by corretora-a's cache", p.calls)
	}
}

func TestNothingAvailableIsABusinessError(t *testing.T) {
	p := newPartners()
	for _, name := range []string{"partner-slow", "partner-flaky", "partner-degrading"} {
		p.errs[name] = failed(name)
	}

	_, err := service(p, newMemoryCache()).QuoteWithFallback(context.Background(), "corretora-a", validRequest())
	var none *NoPartnerError
	if !errors.As(err, &none) || len(none.Missing) != 3 {
		t.Fatalf("error %v, want a NoPartnerError listing the 3 partners", err)
	}
}

func TestRedisDownDoesNotStopTheQuotation(t *testing.T) {
	cache := newMemoryCache()
	cache.down = true

	response, err := service(newPartners(), cache).QuoteWithFallback(context.Background(), "corretora-a", validRequest())
	if err != nil || len(response.Quotes) != 3 || response.Degraded {
		t.Fatalf("with Redis down: quotes=%d degraded=%v err=%v; want the 3 partner quotes", len(response.Quotes), response.Degraded, err)
	}
	if cache.calls != 1 {
		t.Fatalf("%d cache operations with Redis down; after the failed lookup the quotation must not try to save", cache.calls)
	}
}

func TestTheAPIWithFallbackAnswers200Degraded(t *testing.T) {
	p := newPartners()
	p.errs["partner-flaky"] = failed("partner-flaky")
	api := NewAPI(service(p, nil), []string{"corretora-a"}, WithFallback()).Routes()

	response := postQuotes(api, "corretora-a", validBody)
	if response.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 with a partial response: %s", response.Code, response.Body)
	}
	if response.Header().Get(TenantHeader) != "corretora-a" {
		t.Errorf("X-Tenant-Id %q, want it echoed on success", response.Header().Get(TenantHeader))
	}

	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("unreadable response: %v", err)
	}
	if body["degraded"] != true {
		t.Errorf("degraded = %v, want true", body["degraded"])
	}
	missing, _ := body["missing_partners"].([]any)
	if len(missing) != 1 {
		t.Fatalf("missing_partners = %v, want partner-flaky", body["missing_partners"])
	}
}

func TestTheAPIWithFallbackAnswers503WhenNothingIsAvailable(t *testing.T) {
	p := newPartners()
	for _, name := range []string{"partner-slow", "partner-flaky", "partner-degrading"} {
		p.errs[name] = failed(name)
	}
	api := NewAPI(service(p, nil), []string{"corretora-a"}, WithFallback()).Routes()

	response := postQuotes(api, "corretora-a", validBody)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", response.Code)
	}
	var body unavailableBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("unreadable error: %v", err)
	}
	if body.Message != "no partner insurer available" || len(body.Missing) != 3 {
		t.Fatalf("body %+v, want the business error with the 3 missing partners", body)
	}
}

func find(t *testing.T, response Response, name string) PresentedQuote {
	t.Helper()
	for _, q := range response.Quotes {
		if q.Partner == name {
			return q
		}
	}
	t.Fatalf("no quote from %s in %+v", name, response.Quotes)
	return PresentedQuote{}
}
