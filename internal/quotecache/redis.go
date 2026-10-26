package quotecache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/partner"
)

// Entry e o que fica guardado: a cotacao como a parceira a devolveu, mais o instante da consulta
// original. Nao contem CPF nem placa.
type Entry struct {
	Quote    partner.Quote `json:"quote"`
	CachedAt time.Time     `json:"cached_at"`
}

// Redis guarda cada entrada com EXPIRE igual a idade maxima (24 h): o descarte e do proprio Redis,
// nao de uma rotina manual.
type Redis struct {
	client  *redis.Client
	timeout time.Duration
	maxAge  time.Duration
}

// NewRedis limita cada operacao, inteira, a timeout: o cache nunca pode custar mais que a consulta
// que ele evita. Sem isso, o go-redis tenta conectar 5 vezes por operacao com o Redis fora.
func NewRedis(addr string, timeout, maxAge time.Duration) *Redis {
	return &Redis{
		client: redis.NewClient(&redis.Options{
			Addr:                  addr,
			DialTimeout:           timeout,
			DialerRetries:         1,
			ReadTimeout:           timeout,
			WriteTimeout:          timeout,
			PoolTimeout:           timeout,
			MaxRetries:            -1,
			ContextTimeoutEnabled: true,
		}),
		timeout: timeout,
		maxAge:  maxAge,
	}
}

// Lookup devolve uma entrada por chave, na mesma ordem, com nil onde nao ha nada. Um erro quer dizer
// que o Redis nao respondeu: quem chama trata como cache vazio.
func (r *Redis) Lookup(ctx context.Context, keys []string) ([]*Entry, error) {
	entries := make([]*Entry, len(keys))
	if len(keys) == 0 {
		return entries, nil
	}

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	values, err := r.client.MGet(ctx, keys...).Result()
	if err != nil {
		return entries, fmt.Errorf("cache lookup: %w", err)
	}

	for i, v := range values {
		raw, ok := v.(string)
		if !ok {
			continue
		}
		var entry Entry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			continue
		}
		entries[i] = &entry
	}
	return entries, nil
}

func (r *Redis) Save(ctx context.Context, key string, quote partner.Quote, at time.Time) error {
	raw, err := json.Marshal(Entry{Quote: quote, CachedAt: at})
	if err != nil {
		return fmt.Errorf("cache save: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	if err := r.client.Set(ctx, key, raw, r.maxAge).Err(); err != nil {
		return fmt.Errorf("cache save: %w", err)
	}
	return nil
}

func (r *Redis) Close() error { return r.client.Close() }
