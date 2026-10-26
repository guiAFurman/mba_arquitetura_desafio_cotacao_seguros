package partner

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/platform"
)

// hangingPartner nunca responde. O handler fica preso ate o teste liberar, porque o servidor so
// percebe que o cliente desistiu depois de ler o corpo, e o servidor de teste espera os handlers
// terminarem para fechar.
func hangingPartner() (platform.Partner, func()) {
	release := make(chan struct{})
	p, closeServer := testPartner(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	return p, func() { close(release); closeServer() }
}

func TestQuoteGivesUpAfterThePartnerTimeout(t *testing.T) {
	p, closeServer := hangingPartner()
	defer closeServer()

	client := NewClient(WithTimeouts(platform.Timeouts{
		Default:   time.Minute,
		ByPartner: map[string]time.Duration{p.Name: 50 * time.Millisecond},
	}))

	start := time.Now()
	_, err := client.Quote(context.Background(), p, map[string]string{})
	if err == nil {
		t.Fatal("a partner that never answers was treated as success")
	}
	if !TimedOut(err) {
		t.Fatalf("error %v is not recognised as a timeout", err)
	}
	if CanceledByCaller(err) {
		t.Fatalf("a timeout was mistaken for a cancellation by the broker: %v", err)
	}
	var failure *Error
	if !errors.As(err, &failure) || failure.Partner != p.Name {
		t.Fatalf("timeout does not identify the partner: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the call took %s; the partner timeout is 50ms", elapsed)
	}
}

func TestTheHTTPClientNoLongerHasAZeroTimeout(t *testing.T) {
	client := NewClient(WithTimeouts(platform.Timeouts{
		Default:   time.Second,
		ByPartner: map[string]time.Duration{"partner-slow": 2 * time.Second, "partner-flaky": 500 * time.Millisecond},
	}))

	if client.http.Timeout != 2*time.Second {
		t.Fatalf("http.Client Timeout is %s, want the longest partner timeout (2s)", client.http.Timeout)
	}
}

func TestCancellationByTheCallerIsNotATimeout(t *testing.T) {
	p, closeServer := hangingPartner()
	defer closeServer()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewClient(WithTimeouts(platform.Timeouts{Default: time.Minute})).Quote(ctx, p, map[string]string{})
	if !CanceledByCaller(err) || TimedOut(err) {
		t.Fatalf("error %v should be a cancellation by the caller, not a timeout", err)
	}
}
