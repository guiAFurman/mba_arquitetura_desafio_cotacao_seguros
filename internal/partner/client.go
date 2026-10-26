package partner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/platform"
)

type Quote struct {
	Partner         string `json:"partner"`
	QuoteID         string `json:"quote_id"`
	PremiumCents    int64  `json:"premium_cents"`
	Currency        string `json:"currency"`
	CoverageCents   int64  `json:"coverage_cents"`
	ValidForSeconds int64  `json:"valid_for_seconds"`
}

type Client struct {
	http     *http.Client
	timeouts platform.Timeouts
}

type Option func(*Client)

// WithTimeouts limita a espera por parceira (docs/sad.md §4.1): o http.Client deixa de ter Timeout
// zero e passa a ter o maior teto configurado, e cada chamada ainda corta no teto da sua parceira.
// Sem esta opcao, o cliente espera o tempo que a parceira quiser, como no starter.
func WithTimeouts(t platform.Timeouts) Option {
	return func(c *Client) {
		c.timeouts = t
		c.http.Timeout = t.Max()
	}
}

func NewClient(opts ...Option) *Client {
	c := &Client{http: &http.Client{Transport: platform.InstrumentTransport(http.DefaultTransport)}}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

const responseLimit = 1 << 20

func (c *Client) Quote(ctx context.Context, p platform.Partner, request any) (Quote, error) {
	if timeout := c.timeouts.For(p.Name); timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	body, err := json.Marshal(request)
	if err != nil {
		return Quote{}, &Error{Partner: p.Name, Reason: fmt.Sprintf("invalid request: %v", err)}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/quotes", bytes.NewReader(body))
	if err != nil {
		return Quote{}, &Error{Partner: p.Name, Reason: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")

	response, err := c.http.Do(req)
	if err != nil {
		return Quote{}, &Error{Partner: p.Name, Reason: err.Error(), Err: err}
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, responseLimit))
		return Quote{}, &Error{
			Partner: p.Name,
			Status:  response.StatusCode,
			Reason:  fmt.Sprintf("partner replied %d", response.StatusCode),
		}
	}

	var quote Quote
	if err := json.NewDecoder(io.LimitReader(response.Body, responseLimit)).Decode(&quote); err != nil {
		return Quote{}, &Error{Partner: p.Name, Reason: fmt.Sprintf("unreadable response: %v", err), Err: err}
	}

	quote.Partner = p.Name
	return quote, nil
}

type Error struct {
	Partner string
	Status  int
	Reason  string
	Err     error
}

func (e *Error) Error() string {
	return fmt.Sprintf("partner %s: %s", e.Partner, e.Reason)
}

func (e *Error) Unwrap() error { return e.Err }

// TimedOut diz se a parceira estourou o timeout dela (docs/sad.md §4.1): conta como falha.
func TimedOut(err error) bool { return errors.Is(err, context.DeadlineExceeded) }

// CanceledByCaller diz se quem desistiu foi a corretora, e nao a parceira: nao conta para o breaker.
func CanceledByCaller(err error) bool { return errors.Is(err, context.Canceled) }
