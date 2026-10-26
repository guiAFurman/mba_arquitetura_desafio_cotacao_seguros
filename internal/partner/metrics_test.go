package partner

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"

	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/otelcheck"
	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/platform"
)

func TestBreakerStateAndTransitionsAreEmitted(t *testing.T) {
	otelcheck.Install()
	// Parceira propria do teste, para nao somar com os breakers dos outros testes.
	p := platform.Partner{Name: "partner-metrics-test", BaseURL: "http://metrics"}
	breakers := NewBreakers(repeat(&Error{Partner: p.Name, Status: 503, Reason: "partner replied 503"}, 1),
		[]platform.Partner{p}, sadBreaker, nil)

	if state, ok := otelcheck.Sum(t, MetricBreakerState, attribute.String("partner", p.Name)); !ok || state != int64(StateClosed) {
		t.Fatalf("%s for %s = %d (exists=%v), want %d (closed)", MetricBreakerState, p.Name, state, ok, StateClosed)
	}

	// Os contadores sao acumulados no processo: o teste mede o quanto cada um cresceu.
	openedBefore, _ := otelcheck.Sum(t, MetricBreakerTransitions,
		attribute.String("partner", p.Name), attribute.String("from", "closed"), attribute.String("to", "open"))
	failuresBefore := otelcheck.Count(t, MetricQuoteDuration, attribute.String("partner", p.Name), attribute.String("outcome", OutcomeError))
	shortBefore := otelcheck.Count(t, MetricQuoteDuration, attribute.String("partner", p.Name), attribute.String("outcome", OutcomeCircuitOpen))

	for i := 0; i < int(sadBreaker.MinRequests)+5; i++ {
		_, _ = breakers.Quote(context.Background(), p, nil)
	}

	if state, _ := otelcheck.Sum(t, MetricBreakerState, attribute.String("partner", p.Name)); state != int64(StateOpen) {
		t.Fatalf("%s for %s = %d, want %d (open)", MetricBreakerState, p.Name, state, StateOpen)
	}
	opened, ok := otelcheck.Sum(t, MetricBreakerTransitions,
		attribute.String("partner", p.Name), attribute.String("from", "closed"), attribute.String("to", "open"))
	if !ok || opened-openedBefore != 1 {
		t.Fatalf("%s closed->open for %s grew by %d (exists=%v), want 1", MetricBreakerTransitions, p.Name, opened-openedBefore, ok)
	}

	failures := otelcheck.Count(t, MetricQuoteDuration, attribute.String("partner", p.Name), attribute.String("outcome", OutcomeError)) - failuresBefore
	shortCircuited := otelcheck.Count(t, MetricQuoteDuration, attribute.String("partner", p.Name), attribute.String("outcome", OutcomeCircuitOpen)) - shortBefore
	if failures != uint64(sadBreaker.MinRequests) || shortCircuited != 5 {
		t.Fatalf("%s for %s: %d errors and %d circuit_open, want %d and 5", MetricQuoteDuration, p.Name, failures, shortCircuited, sadBreaker.MinRequests)
	}
}
