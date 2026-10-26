package quotation

import (
	"context"
	"errors"
	"log"
	"sort"
	"time"

	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/partner"
	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/platform"
	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/quotecache"
)

type Quoter interface {
	Quote(ctx context.Context, p platform.Partner, request any) (partner.Quote, error)
}

// Cache e o que o agregador precisa do cache de cotacoes (internal/quotecache).
type Cache interface {
	Lookup(ctx context.Context, keys []string) ([]*quotecache.Entry, error)
	Save(ctx context.Context, key string, quote partner.Quote, at time.Time) error
}

// CachePolicy sao os dois prazos do docs/sad.md §4.3: ate FreshFor a cotacao e servida sem chamar a
// parceira; ate MaxAge ela so e servida se a parceira falhar, marcada como vencida.
type CachePolicy struct {
	FreshFor time.Duration
	MaxAge   time.Duration
}

type Service struct {
	partners []platform.Partner
	quoter   Quoter
	cache    Cache
	keys     *quotecache.Keyer
	policy   CachePolicy
	now      func() time.Time
}

type Option func(*Service)

func WithCache(cache Cache, keys *quotecache.Keyer, policy CachePolicy) Option {
	return func(s *Service) { s.cache, s.keys, s.policy = cache, keys, policy }
}

func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

func NewService(partners []platform.Partner, quoter Quoter, opts ...Option) *Service {
	s := &Service{partners: partners, quoter: quoter, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Quote e o comportamento original, tudo-ou-nada: os testes que vieram com o repositorio o exigem
// (docs/sad.md §4.4, D9). A API so o usa quando montada sem WithFallback.
func (s *Service) Quote(ctx context.Context, tenant string, request Request) (Response, error) {
	start := time.Now()
	forPartner := partnerRequest{Broker: tenant, Request: request}

	quotes := make([]PresentedQuote, 0, len(s.partners))
	for _, p := range s.partners {
		quote, err := s.quoter.Quote(ctx, p, forPartner)
		if err != nil {
			return Response{}, err
		}
		quotes = append(quotes, PresentedQuote{Quote: quote, Source: SourcePartner})
	}

	sortByPremium(quotes)

	return Response{
		TenantID:  tenant,
		Quotes:    quotes,
		ElapsedMs: time.Since(start).Milliseconds(),
	}, nil
}

// NoPartnerError e a borda da resposta parcial: nenhuma parceira respondeu e nao havia cotacao no
// cache para nenhuma delas (RF-08).
type NoPartnerError struct {
	Missing []MissingPartner
}

func (e *NoPartnerError) Error() string { return "no partner insurer available" }

// QuoteWithFallback agrega com cache por parceira e resposta parcial (docs/sad.md §4.6). Para cada
// parceira: cotacao fresca do cache; senao a parceira, pelo breaker; se ela falhar, a cotacao vencida
// do cache; senao, a parceira entra em missing_partners com o motivo.
func (s *Service) QuoteWithFallback(ctx context.Context, tenant string, request Request) (Response, error) {
	start := time.Now()
	forPartner := partnerRequest{Broker: tenant, Request: request}
	keys, cached := s.lookup(ctx, tenant, request)

	response := Response{TenantID: tenant, Quotes: make([]PresentedQuote, 0, len(s.partners))}
	for i, p := range s.partners {
		entry := cached[i]
		if age, ok := s.age(entry); ok && age < s.policy.FreshFor {
			response.Quotes = append(response.Quotes, fromCache(entry, age, false))
			continue
		}

		quote, err := s.quoter.Quote(ctx, p, forPartner)
		if err == nil {
			response.Quotes = append(response.Quotes, PresentedQuote{Quote: quote, Source: SourcePartner})
			s.save(ctx, keys, i, quote)
			continue
		}

		if age, ok := s.age(entry); ok && age < s.policy.MaxAge {
			response.Quotes = append(response.Quotes, fromCache(entry, age, true))
			response.Degraded = true
			continue
		}
		response.MissingPartners = append(response.MissingPartners, MissingPartner{Partner: p.Name, Reason: reason(err)})
		response.Degraded = true
	}

	if len(response.Quotes) == 0 {
		return Response{}, &NoPartnerError{Missing: response.MissingPartners}
	}

	sortByPremium(response.Quotes)
	response.ElapsedMs = time.Since(start).Milliseconds()
	return response, nil
}

func (s *Service) lookup(ctx context.Context, tenant string, request Request) ([]string, []*quotecache.Entry) {
	cached := make([]*quotecache.Entry, len(s.partners))
	if s.cache == nil {
		return nil, cached
	}

	keys := make([]string, len(s.partners))
	risk := request.riskFields()
	for i, p := range s.partners {
		keys[i] = s.keys.Key(tenant, p.Name, risk)
	}

	entries, err := s.cache.Lookup(ctx, keys)
	if err != nil {
		log.Printf("quotation: cache unavailable, quoting without it: %v", err)
	}
	copy(cached, entries)
	return keys, cached
}

func (s *Service) save(ctx context.Context, keys []string, i int, quote partner.Quote) {
	if s.cache == nil {
		return
	}
	if err := s.cache.Save(ctx, keys[i], quote, s.now()); err != nil {
		log.Printf("quotation: cache unavailable, quote not saved: %v", err)
	}
}

func (s *Service) age(entry *quotecache.Entry) (time.Duration, bool) {
	if entry == nil {
		return 0, false
	}
	return max(s.now().Sub(entry.CachedAt), 0), true
}

func fromCache(entry *quotecache.Entry, age time.Duration, stale bool) PresentedQuote {
	return PresentedQuote{Quote: entry.Quote, Source: SourceCache, AgeSeconds: int64(age.Seconds()), Stale: stale}
}

func reason(err error) string {
	switch {
	case errors.Is(err, partner.ErrCircuitOpen):
		return ReasonCircuitOpen
	case partner.TimedOut(err):
		return ReasonTimeout
	default:
		return ReasonPartnerError
	}
}

func sortByPremium(quotes []PresentedQuote) {
	sort.Slice(quotes, func(i, j int) bool {
		return quotes[i].PremiumCents < quotes[j].PremiumCents
	})
}
