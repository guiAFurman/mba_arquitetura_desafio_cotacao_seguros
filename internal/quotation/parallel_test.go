package quotation

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/partner"
	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/platform"
)

// barrierPartners so devolve uma cotacao depois que as tres chamadas comecaram. Em serie, a primeira
// esperaria para sempre; o teto de 5 s so existe para o teste falhar em vez de travar.
type barrierPartners struct {
	mu      sync.Mutex
	started int
	all     chan struct{}
}

func newBarrierPartners() *barrierPartners { return &barrierPartners{all: make(chan struct{})} }

func (b *barrierPartners) Quote(ctx context.Context, p platform.Partner, _ any) (partner.Quote, error) {
	b.mu.Lock()
	b.started++
	if b.started == len(threePartners) {
		close(b.all)
	}
	b.mu.Unlock()

	select {
	case <-b.all:
		return partner.Quote{Partner: p.Name, PremiumCents: defaultPremiums()[p.Name], Currency: "BRL"}, nil
	case <-time.After(5 * time.Second):
		return partner.Quote{}, errors.New("the other partners were never called while this one was in flight")
	case <-ctx.Done():
		return partner.Quote{}, ctx.Err()
	}
}

func TestTheFallbackPathQuotesThePartnersAtTheSameTime(t *testing.T) {
	response, err := NewService(threePartners, newBarrierPartners()).QuoteWithFallback(context.Background(), "corretora-a", validRequest())
	if err != nil {
		t.Fatalf("QuoteWithFallback: %v", err)
	}
	if len(response.Quotes) != 3 || len(response.MissingPartners) != 0 {
		t.Fatalf("quotes=%d missing=%+v: the three partners must be in flight at the same time", len(response.Quotes), response.MissingPartners)
	}
}

func TestTheParallelResponseIsTheSameWhoeverFinishesFirst(t *testing.T) {
	p := newPartners()
	p.errs["partner-slow"] = failed("partner-slow")
	p.errs["partner-degrading"] = &partner.Error{Partner: "partner-degrading", Reason: "timeout", Err: context.DeadlineExceeded}

	for i := 0; i < 50; i++ {
		response, err := service(p, nil).QuoteWithFallback(context.Background(), "corretora-a", validRequest())
		if err != nil {
			t.Fatalf("QuoteWithFallback: %v", err)
		}
		want := []MissingPartner{{Partner: "partner-slow", Reason: ReasonPartnerError}, {Partner: "partner-degrading", Reason: ReasonTimeout}}
		if len(response.MissingPartners) != 2 || response.MissingPartners[0] != want[0] || response.MissingPartners[1] != want[1] {
			t.Fatalf("run %d: missing_partners %+v, want %+v in partner order", i, response.MissingPartners, want)
		}
	}
}
