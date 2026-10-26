package partner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sony/gobreaker/v2"

	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/platform"
)

// Os tres estados do circuito, com os nomes que aparecem nas metricas e no trace.
const (
	StateClosed   = gobreaker.StateClosed
	StateHalfOpen = gobreaker.StateHalfOpen
	StateOpen     = gobreaker.StateOpen
)

type State = gobreaker.State

// ErrCircuitOpen e devolvido sem chamar a parceira: aberto quer dizer que a chamada nao sai
// (docs/sad.md §4.2, RNF-05). Tambem cobre o meio-aberto quando as requisicoes de teste ja sairam.
var ErrCircuitOpen = errors.New("circuit open")

type quoter interface {
	Quote(ctx context.Context, p platform.Partner, request any) (Quote, error)
}

type StateChange func(partner string, from, to State)

// Breakers e um circuit breaker por parceira (docs/sad.md §4.2, D3) na frente de um quoter.
type Breakers struct {
	next     quoter
	circuits map[string]*gobreaker.CircuitBreaker[Quote]
	metrics  metrics
}

func NewBreakers(next quoter, partners []platform.Partner, cfg platform.Breaker, onChange StateChange) *Breakers {
	b := &Breakers{next: next, circuits: make(map[string]*gobreaker.CircuitBreaker[Quote], len(partners)), metrics: newMetrics()}
	record := func(name string, from, to State) {
		b.metrics.transition(name, from, to)
		if onChange != nil {
			onChange(name, from, to)
		}
	}
	for _, p := range partners {
		b.circuits[p.Name] = gobreaker.NewCircuitBreaker[Quote](settings(p.Name, cfg, record))
	}
	b.registerStateGauge()
	return b
}

func settings(name string, cfg platform.Breaker, onChange StateChange) gobreaker.Settings {
	s := gobreaker.Settings{
		Name:        name,
		MaxRequests: cfg.HalfOpenRequests,
		Interval:    cfg.Interval,
		Timeout:     cfg.OpenTimeout,
		ReadyToTrip: func(c gobreaker.Counts) bool {
			decided := c.TotalSuccesses + c.TotalFailures
			return decided >= cfg.MinRequests && float64(c.TotalFailures)/float64(decided) >= cfg.FailureRatio
		},
		IsSuccessful: countsAsSuccess,
		IsExcluded:   CanceledByCaller,
	}
	if onChange != nil {
		s.OnStateChange = func(name string, from, to gobreaker.State) { onChange(name, from, to) }
	}
	return s
}

// Timeout, erro de rede, 5xx e resposta ilegivel sao falha; 4xx e pedido nosso invalido, nao
// indisponibilidade da parceira.
func countsAsSuccess(err error) bool {
	if err == nil {
		return true
	}
	var failure *Error
	return errors.As(err, &failure) && failure.Status >= 400 && failure.Status < 500
}

func (b *Breakers) Quote(ctx context.Context, p platform.Partner, request any) (Quote, error) {
	circuit, ok := b.circuits[p.Name]
	if !ok {
		return b.next.Quote(ctx, p, request)
	}

	start := time.Now()
	quote, err := circuit.Execute(func() (Quote, error) { return b.next.Quote(ctx, p, request) })
	if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
		err = &Error{Partner: p.Name, Reason: "circuit open", Err: fmt.Errorf("%w: %w", ErrCircuitOpen, err)}
	}
	b.metrics.observe(ctx, p.Name, time.Since(start).Seconds(), err)
	return quote, err
}

// State devolve o estado atual do circuito de uma parceira.
func (b *Breakers) State(partner string) State {
	if circuit, ok := b.circuits[partner]; ok {
		return circuit.State()
	}
	return StateClosed
}
