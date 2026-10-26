package partner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/platform"
)

// As 60 primeiras respostas da partner-flaky com os defaults do compose, medidas pela sonda da
// docs/smoke-test-factibilidade.md §2.2 (X = 503, . = 200). A rajada das sequencias 49 a 57 esta no
// fim. Como no mock, a sequencia so anda quando a parceira e chamada.
const flakySequence = "...XX.XXX..X..X.X...XXX.....X..X.....XXXXX......XX" + "XXXXXXX.X."

var flaky = platform.Partner{Name: "partner-flaky", BaseURL: "http://flaky"}

// Os parametros do docs/sad.md §4.2.
var sadBreaker = platform.Breaker{
	MinRequests:      10,
	FailureRatio:     0.5,
	Interval:         30 * time.Second,
	OpenTimeout:      15 * time.Second,
	HalfOpenRequests: 2,
}

type scriptedPartner struct {
	outcomes []error
	calls    int
}

func (s *scriptedPartner) Quote(_ context.Context, p platform.Partner, _ any) (Quote, error) {
	err := s.outcomes[s.calls%len(s.outcomes)]
	s.calls++
	if err != nil {
		return Quote{}, err
	}
	return Quote{Partner: p.Name, PremiumCents: 100000}, nil
}

func fromSequence(seq string) *scriptedPartner {
	s := &scriptedPartner{}
	for _, c := range seq {
		if c == 'X' {
			s.outcomes = append(s.outcomes, &Error{Partner: "partner-flaky", Status: 503, Reason: "partner replied 503"})
		} else {
			s.outcomes = append(s.outcomes, nil)
		}
	}
	return s
}

func repeat(err error, n int) *scriptedPartner {
	s := &scriptedPartner{}
	for i := 0; i < n; i++ {
		s.outcomes = append(s.outcomes, err)
	}
	return s
}

func TestBreakerOpensOnTheFlakySequenceAndStopsCallingThePartner(t *testing.T) {
	partner := fromSequence(flakySequence)
	var transitions []string
	breakers := NewBreakers(partner, []platform.Partner{flaky}, sadBreaker, func(_ string, from, to State) {
		transitions = append(transitions, from.String()+"->"+to.String())
	})

	openedAt := 0
	for request := 1; request <= len(flakySequence) && openedAt == 0; request++ {
		_, _ = breakers.Quote(context.Background(), flaky, nil)
		if breakers.State(flaky.Name) == StateOpen {
			openedAt = request
		}
	}

	// 50% de falha com no minimo 10 requisicoes: na 12a chamada sao 6 falhas em 12. O gobreaker so
	// avalia o disparo depois de uma falha, por isso nao abre na 10a, que e um sucesso.
	if openedAt != 12 {
		t.Fatalf("circuit opened on request %d, expected 12", openedAt)
	}
	if len(transitions) != 1 || transitions[0] != "closed->open" {
		t.Fatalf("transitions %v, expected [closed->open]", transitions)
	}

	callsWhenOpened := partner.calls
	for i := 0; i < 20; i++ {
		_, err := breakers.Quote(context.Background(), flaky, nil)
		if !errors.Is(err, ErrCircuitOpen) {
			t.Fatalf("request %d with the circuit open returned %v, expected ErrCircuitOpen", i+1, err)
		}
		var failure *Error
		if !errors.As(err, &failure) || failure.Partner != flaky.Name {
			t.Fatalf("circuit-open error does not identify the partner: %v", err)
		}
	}
	if partner.calls != callsWhenOpened {
		t.Fatalf("the partner was called %d times with the circuit open; open means the call does not leave",
			partner.calls-callsWhenOpened)
	}
}

func TestBreakerCountsATimeoutAsFailure(t *testing.T) {
	timeout := &Error{Partner: flaky.Name, Reason: "timeout", Err: context.DeadlineExceeded}
	breakers := NewBreakers(repeat(timeout, 1), []platform.Partner{flaky}, sadBreaker, nil)

	for i := 0; i < int(sadBreaker.MinRequests); i++ {
		_, _ = breakers.Quote(context.Background(), flaky, nil)
	}
	if state := breakers.State(flaky.Name); state != StateOpen {
		t.Fatalf("%d timeouts in a row left the circuit %s; a slow partner must be seen as failing", sadBreaker.MinRequests, state)
	}
}

func TestBreakerIgnoresCancellationByTheCaller(t *testing.T) {
	canceled := &Error{Partner: flaky.Name, Reason: "canceled", Err: context.Canceled}
	breakers := NewBreakers(repeat(canceled, 1), []platform.Partner{flaky}, sadBreaker, nil)

	for i := 0; i < 3*int(sadBreaker.MinRequests); i++ {
		_, _ = breakers.Quote(context.Background(), flaky, nil)
	}
	if state := breakers.State(flaky.Name); state != StateClosed {
		t.Fatalf("cancellations by the broker opened the circuit (%s); they say nothing about the partner", state)
	}
}

func TestBreakerTreatsA4xxAsThePartnerBeingUp(t *testing.T) {
	badRequest := &Error{Partner: flaky.Name, Status: 400, Reason: "partner replied 400"}
	breakers := NewBreakers(repeat(badRequest, 1), []platform.Partner{flaky}, sadBreaker, nil)

	for i := 0; i < 3*int(sadBreaker.MinRequests); i++ {
		_, _ = breakers.Quote(context.Background(), flaky, nil)
	}
	if state := breakers.State(flaky.Name); state != StateClosed {
		t.Fatalf("4xx answers opened the circuit (%s); a 4xx is our invalid request, not an unavailable partner", state)
	}
}

type byPartner map[string]*scriptedPartner

func (b byPartner) Quote(ctx context.Context, p platform.Partner, request any) (Quote, error) {
	return b[p.Name].Quote(ctx, p, request)
}

func TestBreakerIsPerPartner(t *testing.T) {
	slow := platform.Partner{Name: "partner-slow", BaseURL: "http://slow"}
	partners := byPartner{
		flaky.Name: repeat(&Error{Partner: flaky.Name, Status: 503, Reason: "partner replied 503"}, 1),
		slow.Name:  repeat(nil, 1),
	}
	breakers := NewBreakers(partners, []platform.Partner{slow, flaky}, sadBreaker, nil)

	for i := 0; i < int(sadBreaker.MinRequests); i++ {
		_, _ = breakers.Quote(context.Background(), flaky, nil)
		_, _ = breakers.Quote(context.Background(), slow, nil)
	}

	if state := breakers.State(flaky.Name); state != StateOpen {
		t.Fatalf("partner-flaky circuit is %s, expected open", state)
	}
	if state := breakers.State(slow.Name); state != StateClosed {
		t.Fatalf("partner-slow circuit is %s: one partner failing must not open the circuit of another", state)
	}
	if _, err := breakers.Quote(context.Background(), slow, nil); err != nil {
		t.Fatalf("partner-slow stopped answering because partner-flaky failed: %v", err)
	}
}
