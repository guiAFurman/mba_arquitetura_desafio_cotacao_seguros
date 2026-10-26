package quotecache

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/partner"
)

var risk = []string{"12345678901", "1988", "ABC1D23", "Gol 1.0", "2020", "8500000", "comprehensive"}

func TestKeyCarriesTheBrokerAndHidesThePersonalData(t *testing.T) {
	keyer, _ := NewKeyer("segredo-de-teste")
	key := keyer.Key("corretora-a", "partner-flaky", risk)

	if !strings.HasPrefix(key, "quote:v1:corretora-a:partner-flaky:") {
		t.Fatalf("key %q is not quote:v1:{tenant_id}:{partner}:{hmac}", key)
	}
	for _, personal := range []string{"12345678901", "ABC1D23"} {
		if strings.Contains(key, personal) {
			t.Fatalf("key %q exposes %q in plain text", key, personal)
		}
	}
	if hmac := key[strings.LastIndex(key, ":")+1:]; len(hmac) != 64 {
		t.Fatalf("hmac %q is not a hex SHA-256", hmac)
	}
}

func TestTheSameRiskIsADifferentKeyForEachBroker(t *testing.T) {
	keyer, _ := NewKeyer("segredo-de-teste")
	if keyer.Key("corretora-a", "partner-flaky", risk) == keyer.Key("corretora-b", "partner-flaky", risk) {
		t.Fatal("two brokers share a cache key for the same risk: broker B would see the premium negotiated by A")
	}
}

func TestEveryRiskFieldChangesTheKey(t *testing.T) {
	keyer, _ := NewKeyer("segredo-de-teste")
	base := keyer.Key("corretora-a", "partner-flaky", risk)

	for i := range risk {
		changed := append([]string(nil), risk...)
		changed[i] += "0"
		if keyer.Key("corretora-a", "partner-flaky", changed) == base {
			t.Errorf("changing risk field %d (%q) kept the same key: two different risks would share a premium", i, risk[i])
		}
	}
}

func TestTheKeyDependsOnTheSecret(t *testing.T) {
	a, _ := NewKeyer("segredo-a")
	b, _ := NewKeyer("segredo-b")
	if a.Key("corretora-a", "partner-flaky", risk) == b.Key("corretora-a", "partner-flaky", risk) {
		t.Fatal("the key does not depend on the secret: anyone could rebuild it from a CPF")
	}
	if _, generated := NewKeyer(""); !generated {
		t.Fatal("without a secret the keyer must generate one")
	}
}

func TestRedisKeepsTheQuoteForAtMostTheMaxAge(t *testing.T) {
	server := miniredis.RunT(t)
	store := NewRedis(server.Addr(), time.Second, 24*time.Hour)
	defer store.Close()

	quote := partner.Quote{Partner: "partner-flaky", QuoteID: "partner-flaky-1", PremiumCents: 84210, Currency: "BRL"}
	at := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	if err := store.Save(context.Background(), "quote:v1:corretora-a:partner-flaky:abc", quote, at); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if ttl := server.TTL("quote:v1:corretora-a:partner-flaky:abc"); ttl != 24*time.Hour {
		t.Fatalf("entry saved with TTL %s, want 24h (descarte LGPD)", ttl)
	}

	entries, err := store.Lookup(context.Background(), []string{"quote:v1:corretora-a:partner-flaky:abc", "quote:v1:corretora-a:partner-slow:abc"})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if entries[0] == nil || entries[0].Quote != quote || !entries[0].CachedAt.Equal(at) {
		t.Fatalf("entry read back as %+v, want %+v at %s", entries[0], quote, at)
	}
	if entries[1] != nil {
		t.Fatalf("a key that was never saved returned %+v", entries[1])
	}

	server.FastForward(24*time.Hour + time.Second)
	entries, _ = store.Lookup(context.Background(), []string{"quote:v1:corretora-a:partner-flaky:abc"})
	if entries[0] != nil {
		t.Fatal("the entry outlived the 24h max age")
	}
}

func TestRedisDownIsReportedAndLooksLikeAnEmptyCache(t *testing.T) {
	server := miniredis.RunT(t)
	store := NewRedis(server.Addr(), 100*time.Millisecond, 24*time.Hour)
	defer store.Close()
	server.Close()

	entries, err := store.Lookup(context.Background(), []string{"quote:v1:corretora-a:partner-flaky:abc"})
	if err == nil {
		t.Fatal("Redis down and Lookup reported no error")
	}
	if len(entries) != 1 || entries[0] != nil {
		t.Fatalf("Redis down returned %+v, want one empty slot", entries)
	}
}
