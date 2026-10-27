package quotation

import (
	"context"
	"errors"
	"log"
	"sort"
	"sync"
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
	partners  []platform.Partner
	quoter    Quoter
	cache     Cache
	keys      *quotecache.Keyer
	policy    CachePolicy
	now       func() time.Time
	telemetry telemetry
}

type Option func(*Service)

func WithCache(cache Cache, keys *quotecache.Keyer, policy CachePolicy) Option {
	return func(s *Service) { s.cache, s.keys, s.policy = cache, keys, policy }
}

func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

func NewService(partners []platform.Partner, quoter Quoter, opts ...Option) *Service {
	s := &Service{partners: partners, quoter: quoter, now: time.Now, telemetry: newTelemetry()}
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
//
// As tres parceiras sao consultadas ao mesmo tempo (docs/sad.md §4.8, D16): a cotacao leva o tempo
// da mais lenta, e nao a soma das tres. Service.Quote continua em serie, como o teste original exige.
func (s *Service) QuoteWithFallback(ctx context.Context, tenant string, request Request) (Response, error) {
	start := time.Now()
	forPartner := partnerRequest{Broker: tenant, Request: request}
	keys, cached, cache := s.lookup(ctx, tenant, request)

	type outcome struct {
		quote   PresentedQuote
		missing *MissingPartner
	}
	outcomes := make([]outcome, len(s.partners))
	var wg sync.WaitGroup
	for i, p := range s.partners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			quote, missing := s.quotePartner(ctx, p, forPartner, keys[i], cached[i], cache)
			outcomes[i] = outcome{quote: quote, missing: missing}
		}()
	}
	wg.Wait()

	// Montada na ordem das parceiras, para a resposta nao depender de quem terminou primeiro.
	response := Response{TenantID: tenant, Quotes: make([]PresentedQuote, 0, len(s.partners))}
	for _, o := range outcomes {
		if o.missing != nil {
			response.MissingPartners = append(response.MissingPartners, *o.missing)
			response.Degraded = true
			continue
		}
		response.Quotes = append(response.Quotes, o.quote)
		response.Degraded = response.Degraded || o.quote.Stale
	}

	markResponse(ctx, tenant, response)
	if len(response.Quotes) == 0 {
		return Response{}, &NoPartnerError{Missing: response.MissingPartners}
	}

	sortByPremium(response.Quotes)
	response.ElapsedMs = time.Since(start).Milliseconds()
	return response, nil
}

func (s *Service) quotePartner(ctx context.Context, p platform.Partner, forPartner partnerRequest, key string, entry *quotecache.Entry, cache cacheState) (PresentedQuote, *MissingPartner) {
	ctx, span := startPartnerSpan(ctx, p.Name)
	defer span.End()

	if age, ok := s.age(entry); ok && age < s.policy.FreshFor {
		markServed(span, SourceCache, s.record(ctx, p.Name, cache, CacheHit))
		return fromCache(entry, age, false), nil
	}

	quote, err := s.quoter.Quote(ctx, p, forPartner)
	if err == nil {
		markServed(span, SourcePartner, s.record(ctx, p.Name, cache, CacheMiss))
		s.save(ctx, key, quote)
		return PresentedQuote{Quote: quote, Source: SourcePartner}, nil
	}

	markShortCircuit(span, err)
	if age, ok := s.age(entry); ok && age < s.policy.MaxAge {
		markServed(span, SourceCache, s.record(ctx, p.Name, cache, CacheStale))
		return fromCache(entry, age, true), nil
	}

	why := reason(err)
	markMissing(span, why, s.record(ctx, p.Name, cache, CacheMiss))
	return PresentedQuote{}, &MissingPartner{Partner: p.Name, Reason: why}
}

type cacheState int

const (
	cacheOff cacheState = iota
	cacheUp
	cacheDown
)

// record conta a consulta ao cache e devolve o resultado efetivo, que tambem vai para o span.
func (s *Service) record(ctx context.Context, partnerName string, cache cacheState, result string) string {
	switch cache {
	case cacheOff:
		return "off"
	case cacheDown:
		result = CacheError
	}
	s.telemetry.lookup(ctx, partnerName, result)
	return result
}

func (s *Service) lookup(ctx context.Context, tenant string, request Request) ([]string, []*quotecache.Entry, cacheState) {
	keys := make([]string, len(s.partners))
	cached := make([]*quotecache.Entry, len(s.partners))
	if s.cache == nil {
		return keys, cached, cacheOff
	}

	risk := request.riskFields()
	for i, p := range s.partners {
		keys[i] = s.keys.Key(tenant, p.Name, risk)
	}

	entries, err := s.cache.Lookup(ctx, keys)
	if err != nil {
		// Sem chaves, esta cotacao tambem nao tenta gravar: o Redis fora custa uma leitura, nao quatro.
		log.Printf("quotation: cache unavailable, quoting without it: %v", err)
		return make([]string, len(s.partners)), cached, cacheDown
	}
	copy(cached, entries)
	return keys, cached, cacheUp
}

func (s *Service) save(ctx context.Context, key string, quote partner.Quote) {
	if s.cache == nil || key == "" {
		return
	}
	if err := s.cache.Save(ctx, key, quote, s.now()); err != nil {
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
