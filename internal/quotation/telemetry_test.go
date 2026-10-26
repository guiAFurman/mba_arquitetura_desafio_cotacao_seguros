package quotation

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/otelcheck"
	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/partner"
)

func spanNamed(t *testing.T, name string) tracetest.SpanStub {
	t.Helper()
	for _, s := range otelcheck.Spans() {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no span %q among %d spans", name, len(otelcheck.Spans()))
	return tracetest.SpanStub{}
}

func attr(s tracetest.SpanStub, key string) (attribute.Value, bool) {
	for _, kv := range s.Attributes {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

func TestAShortCircuitedPartnerIsMarkedInTheTrace(t *testing.T) {
	otelcheck.Install()
	otelcheck.ResetSpans()

	p := newPartners()
	p.errs["partner-flaky"] = &partner.Error{Partner: "partner-flaky", Reason: "circuit open", Err: partner.ErrCircuitOpen}
	_, _ = service(p, nil).QuoteWithFallback(context.Background(), "corretora-a", validRequest())

	span := spanNamed(t, "quote partner-flaky")
	if v, ok := attr(span, AttrShortCircuited); !ok || !v.AsBool() {
		t.Fatalf("span %q has no %s=true: a short-circuited request would look like a mysteriously fast quote", span.Name, AttrShortCircuited)
	}
	if v, _ := attr(span, AttrMissingReason); v.AsString() != ReasonCircuitOpen {
		t.Errorf("%s = %q, want %q", AttrMissingReason, v.AsString(), ReasonCircuitOpen)
	}
	if span.Status.Code != codes.Error {
		t.Errorf("span status %v, want Error for a partner missing from the quotation", span.Status.Code)
	}
	if len(span.Events) == 0 || !strings.Contains(span.Events[0].Name, "partner not called") {
		t.Errorf("events %v, want the 'partner not called' event", span.Events)
	}
}

func TestACachedQuoteIsMarkedInTheTrace(t *testing.T) {
	otelcheck.Install()
	otelcheck.ResetSpans()

	cache := newMemoryCache()
	cache.put("corretora-a", "partner-slow", 180000, 5*time.Minute)
	_, _ = service(newPartners(), cache).QuoteWithFallback(context.Background(), "corretora-a", validRequest())

	span := spanNamed(t, "quote partner-slow")
	if v, _ := attr(span, AttrSource); v.AsString() != SourceCache {
		t.Fatalf("%s = %q on %q, want %q", AttrSource, v.AsString(), span.Name, SourceCache)
	}
	if v, _ := attr(span, AttrCacheResult); v.AsString() != CacheHit {
		t.Fatalf("%s = %q, want %q", AttrCacheResult, v.AsString(), CacheHit)
	}

	called := spanNamed(t, "quote partner-flaky")
	if v, _ := attr(called, AttrSource); v.AsString() != SourcePartner {
		t.Fatalf("%s = %q on %q, want %q", AttrSource, v.AsString(), called.Name, SourcePartner)
	}
}

func TestCacheLookupsAreCountedByResult(t *testing.T) {
	otelcheck.Install()
	count := func(result string) int64 {
		v, _ := otelcheck.Sum(t, MetricCacheLookups, attribute.String("partner", "partner-slow"), attribute.String("result", result))
		return v
	}
	hits, misses, stale := count(CacheHit), count(CacheMiss), count(CacheStale)

	cache := newMemoryCache()
	svc := service(newPartners(), cache)
	_, _ = svc.QuoteWithFallback(context.Background(), "corretora-a", validRequest()) // vazio: miss, grava
	_, _ = svc.QuoteWithFallback(context.Background(), "corretora-a", validRequest()) // fresca: hit

	down := newPartners()
	down.errs["partner-slow"] = &partner.Error{Partner: "partner-slow", Status: 503, Reason: "partner replied 503"}
	cache.put("corretora-a", "partner-slow", 180000, 3*time.Hour)
	_, _ = service(down, cache).QuoteWithFallback(context.Background(), "corretora-a", validRequest()) // vencida na falha

	if got := count(CacheMiss) - misses; got != 1 {
		t.Errorf("%s miss for partner-slow grew by %d, want 1", MetricCacheLookups, got)
	}
	if got := count(CacheHit) - hits; got != 1 {
		t.Errorf("%s hit for partner-slow grew by %d, want 1", MetricCacheLookups, got)
	}
	if got := count(CacheStale) - stale; got != 1 {
		t.Errorf("%s stale for partner-slow grew by %d, want 1", MetricCacheLookups, got)
	}
}

func TestNoPersonalDataInMetricsOrSpans(t *testing.T) {
	otelcheck.Install()
	otelcheck.ResetSpans()

	cache := newMemoryCache()
	p := newPartners()
	p.errs["partner-degrading"] = &partner.Error{Partner: "partner-degrading", Reason: "timeout", Err: context.DeadlineExceeded}
	svc := service(p, cache)
	_, _ = svc.QuoteWithFallback(context.Background(), "corretora-a", validRequest())
	_, _ = svc.QuoteWithFallback(context.Background(), "corretora-a", validRequest())

	request := validRequest()
	forbidden := []string{request.Driver.Document, request.Vehicle.Plate, "partner-slow-now", "partner-flaky-now", "-original"}
	for _, value := range otelcheck.AllAttributeValues(t) {
		for _, personal := range forbidden {
			if strings.Contains(value, personal) {
				t.Fatalf("telemetry carries %q (found in %q): CPF, plate and quote_id never go into a span or a metric", personal, value)
			}
		}
	}
}
