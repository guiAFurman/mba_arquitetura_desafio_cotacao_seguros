// Package otelcheck liga, so nos testes, um MeterProvider e um TracerProvider em memoria, para provar
// que as metricas e os spans de negocio sao emitidos de verdade (enunciado, regra 1 aplicada ao
// codigo: metrica citada na documentacao tem que ser emitida).
package otelcheck

import (
	"context"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

var (
	once   sync.Once
	reader *sdkmetric.ManualReader
	spans  *tracetest.InMemoryExporter
)

// Install liga os providers em memoria uma vez por binario de teste: o OTel global so delega para o
// primeiro provider registrado.
func Install() {
	once.Do(func() {
		reader = sdkmetric.NewManualReader()
		otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
		spans = tracetest.NewInMemoryExporter()
		otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans)))
	})
}

func Spans() tracetest.SpanStubs { return spans.GetSpans() }

func ResetSpans() { spans.Reset() }

// Collect le todas as metricas acumuladas ate agora.
func Collect(t *testing.T) []metricdata.Metrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	var all []metricdata.Metrics
	for _, sm := range rm.ScopeMetrics {
		all = append(all, sm.Metrics...)
	}
	return all
}

// Sum devolve o valor de um contador ou de um gauge inteiro para os atributos pedidos (somando os
// pontos que os contem), e se a metrica existe.
func Sum(t *testing.T, name string, want ...attribute.KeyValue) (int64, bool) {
	t.Helper()
	for _, m := range Collect(t) {
		if m.Name != name {
			continue
		}
		var total int64
		switch data := m.Data.(type) {
		case metricdata.Sum[int64]:
			for _, p := range data.DataPoints {
				if has(p.Attributes, want) {
					total += p.Value
				}
			}
		case metricdata.Gauge[int64]:
			for _, p := range data.DataPoints {
				if has(p.Attributes, want) {
					total += p.Value
				}
			}
		}
		return total, true
	}
	return 0, false
}

// Count devolve quantas medicoes um histograma recebeu para os atributos pedidos.
func Count(t *testing.T, name string, want ...attribute.KeyValue) uint64 {
	t.Helper()
	for _, m := range Collect(t) {
		if data, ok := m.Data.(metricdata.Histogram[float64]); ok && m.Name == name {
			var total uint64
			for _, p := range data.DataPoints {
				if has(p.Attributes, want) {
					total += p.Count
				}
			}
			return total
		}
	}
	return 0
}

// AllAttributeValues junta todos os valores de atributo de todas as metricas e spans, para procurar
// dado pessoal.
func AllAttributeValues(t *testing.T) []string {
	t.Helper()
	var values []string
	add := func(set attribute.Set) {
		for _, kv := range set.ToSlice() {
			values = append(values, kv.Value.Emit())
		}
	}
	for _, m := range Collect(t) {
		switch data := m.Data.(type) {
		case metricdata.Sum[int64]:
			for _, p := range data.DataPoints {
				add(p.Attributes)
			}
		case metricdata.Gauge[int64]:
			for _, p := range data.DataPoints {
				add(p.Attributes)
			}
		case metricdata.Histogram[float64]:
			for _, p := range data.DataPoints {
				add(p.Attributes)
			}
		}
	}
	for _, s := range Spans() {
		add(attribute.NewSet(s.Attributes...))
		values = append(values, s.Name)
		for _, e := range s.Events {
			add(attribute.NewSet(e.Attributes...))
			values = append(values, e.Name)
		}
	}
	return values
}

func has(set attribute.Set, want []attribute.KeyValue) bool {
	for _, kv := range want {
		if v, ok := set.Value(kv.Key); !ok || v != kv.Value {
			return false
		}
	}
	return true
}
