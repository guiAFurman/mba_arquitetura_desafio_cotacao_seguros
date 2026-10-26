package quotation

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/partner"
)

// Instrumentacao de negocio do agregador (docs/sad.md §4.5). Atributos permitidos: corretora,
// parceira, origem da cotacao, resultado do cache e estado do circuito. Nunca CPF, placa ou quote_id.
//
// O nome no Prometheus e o que o Collector gera a partir do nome OTel, e e o declarado no README.
const MetricCacheLookups = "quote.cache.lookups" // Prometheus: quote_cache_lookups_total

// Resultado de cada consulta ao cache, por parceira. O hit rate e hit / todas.
const (
	CacheHit   = "hit"   // cotacao fresca servida; a parceira nao foi chamada
	CacheMiss  = "miss"  // nada fresco: a parceira foi chamada (ou teria sido, se o circuito estava aberto)
	CacheStale = "stale" // a parceira falhou e a cotacao vencida foi servida
	CacheError = "error" // o Redis nao respondeu
)

// Atributos de span. As duas marcacoes que o enunciado pede sao quote.source=cache e
// breaker.short_circuited=true.
const (
	AttrTenant         = "tenant.id"
	AttrPartner        = "partner.name"
	AttrSource         = "quote.source"
	AttrCacheResult    = "cache.result"
	AttrShortCircuited = "breaker.short_circuited"
	AttrMissingReason  = "quote.missing_reason"
	AttrDegraded       = "quote.degraded"
	AttrQuotes         = "quote.count"
	AttrFromCache      = "quote.from_cache"
	AttrMissing        = "quote.missing_partners"
)

var tracer = otel.Tracer("quotation-api/quotation")

type telemetry struct {
	lookups metric.Int64Counter
}

func newTelemetry() telemetry {
	lookups, _ := otel.Meter("quotation-api/quotation").Int64Counter(MetricCacheLookups,
		metric.WithDescription("Quote cache lookups per partner, by result: hit, miss, stale or error."),
		metric.WithUnit("{lookup}"))
	return telemetry{lookups: lookups}
}

func (t telemetry) lookup(ctx context.Context, partnerName, result string) {
	t.lookups.Add(ctx, 1, metric.WithAttributes(
		attribute.String("partner", partnerName),
		attribute.String("result", result),
	))
}

// startPartnerSpan abre o span de uma parceira; a chamada HTTP, quando sai, vira filha dele. Um span
// sem filho HTTP e uma cotacao que nao chamou a parceira, e os atributos dizem por que.
func startPartnerSpan(ctx context.Context, partnerName string) (context.Context, trace.Span) {
	return tracer.Start(ctx, "quote "+partnerName, trace.WithAttributes(attribute.String(AttrPartner, partnerName)))
}

func markServed(span trace.Span, source, cacheResult string) {
	span.SetAttributes(attribute.String(AttrSource, source), attribute.String(AttrCacheResult, cacheResult))
}

func markShortCircuit(span trace.Span, err error) {
	if errors.Is(err, partner.ErrCircuitOpen) {
		span.SetAttributes(attribute.Bool(AttrShortCircuited, true))
		span.AddEvent("circuit open: partner not called")
	}
}

func markMissing(span trace.Span, why, cacheResult string) {
	span.SetAttributes(attribute.String(AttrMissingReason, why), attribute.String(AttrCacheResult, cacheResult))
	span.SetStatus(codes.Error, "partner missing from the quotation: "+why)
}

// markResponse resume a cotacao no span da requisicao, que o otelhttp abriu.
func markResponse(ctx context.Context, tenant string, response Response) {
	fromCache := 0
	for _, q := range response.Quotes {
		if q.Source == SourceCache {
			fromCache++
		}
	}
	trace.SpanFromContext(ctx).SetAttributes(
		attribute.String(AttrTenant, tenant),
		attribute.Bool(AttrDegraded, response.Degraded),
		attribute.Int(AttrQuotes, len(response.Quotes)),
		attribute.Int(AttrFromCache, fromCache),
		attribute.Int(AttrMissing, len(response.MissingPartners)),
	)
}
