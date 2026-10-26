package partner

import (
	"context"
	"errors"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Metricas de negocio da fronteira com as parceiras (docs/sad.md §4.5). Atributos: so o nome da
// parceira, a origem e o destino da transicao e o resultado. Nunca CPF, placa ou quote_id.
const (
	MetricBreakerState       = "partner.breaker.state"
	MetricBreakerTransitions = "partner.breaker.transitions"
	MetricQuoteDuration      = "partner.quote.duration"
)

const (
	OutcomeSuccess     = "success"
	OutcomeTimeout     = "timeout"
	OutcomeError       = "error"
	OutcomeCircuitOpen = "circuit_open"
)

// Os limites dos baldes acompanham os timeouts do docs/sad.md §4.1 (500 ms e 2 s) e o teto de 6 s da
// partner-degrading, para que o p95 por parceira caia em baldes com significado.
var durationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 0.75, 1, 1.5, 2, 3, 6}

type metrics struct {
	transitions metric.Int64Counter
	duration    metric.Float64Histogram
}

func newMetrics() metrics {
	meter := otel.Meter("quotation-api/partner")
	transitions, _ := meter.Int64Counter(MetricBreakerTransitions,
		metric.WithDescription("Circuit breaker state changes, per partner, with origin and destination state."),
		metric.WithUnit("{transition}"))
	duration, _ := meter.Float64Histogram(MetricQuoteDuration,
		metric.WithDescription("Time to get a quote from a partner through its circuit breaker, by outcome; circuit_open is a call that never left."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(durationBuckets...))
	return metrics{transitions: transitions, duration: duration}
}

// O gauge de estado e registrado uma unica vez e le todos os Breakers vivos: o SDK do OTel ignora o
// callback de um segundo registro do mesmo instrumento.
var (
	stateGauge sync.Once
	liveMu     sync.Mutex
	live       []*Breakers
)

// registerStateGauge expoe o estado atual de cada circuito: 0 fechado, 1 meio-aberto, 2 aberto. Ler o
// estado tambem e o que leva um circuito aberto ao meio-aberto quando o tempo aberto acaba.
func (b *Breakers) registerStateGauge() {
	liveMu.Lock()
	live = append(live, b)
	liveMu.Unlock()

	stateGauge.Do(func() {
		_, _ = otel.Meter("quotation-api/partner").Int64ObservableGauge(MetricBreakerState,
			metric.WithDescription("Current circuit breaker state per partner: 0 closed, 1 half-open, 2 open."),
			metric.WithUnit("{state}"),
			metric.WithInt64Callback(observeStates))
	})
}

func observeStates(_ context.Context, o metric.Int64Observer) error {
	liveMu.Lock()
	defer liveMu.Unlock()
	for _, b := range live {
		for name, circuit := range b.circuits {
			o.Observe(int64(circuit.State()), metric.WithAttributes(attribute.String("partner", name)))
		}
	}
	return nil
}

func (m metrics) transition(partner string, from, to State) {
	m.transitions.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("partner", partner),
		attribute.String("from", from.String()),
		attribute.String("to", to.String()),
	))
}

func (m metrics) observe(ctx context.Context, partner string, seconds float64, err error) {
	m.duration.Record(ctx, seconds, metric.WithAttributes(
		attribute.String("partner", partner),
		attribute.String("outcome", outcome(err)),
	))
}

func outcome(err error) string {
	switch {
	case err == nil:
		return OutcomeSuccess
	case errors.Is(err, ErrCircuitOpen):
		return OutcomeCircuitOpen
	case TimedOut(err):
		return OutcomeTimeout
	default:
		return OutcomeError
	}
}
