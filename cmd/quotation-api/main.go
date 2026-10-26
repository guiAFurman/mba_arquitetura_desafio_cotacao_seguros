package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/partner"
	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/platform"
	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/quotation"
	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/quotecache"
)

func main() {
	cfg, err := platform.LoadConfig(platform.Environment())
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	ctx, stopListening := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopListening()

	stopTelemetry, err := platform.StartTelemetry(context.Background(), cfg.Telemetry)
	if err != nil {
		log.Fatalf("telemetry: %v", err)
	}

	client := partner.NewClient(partner.WithTimeouts(cfg.Timeouts))
	breakers := partner.NewBreakers(client, cfg.Partners, cfg.Breaker, func(name string, from, to partner.State) {
		log.Printf("quotation-api: circuit of %s went from %s to %s", name, from, to)
	})

	var serviceOpts []quotation.Option
	if cfg.Cache.RedisAddr != "" {
		keys, generated := quotecache.NewKeyer(cfg.Cache.KeySecret)
		if generated {
			log.Print("quotation-api: CACHE_KEY_SECRET not set, using a random secret (the cache only lives as long as this process)")
		}
		store := quotecache.NewRedis(cfg.Cache.RedisAddr, cfg.Cache.RedisTimeout, cfg.Cache.MaxAge)
		defer func() { _ = store.Close() }()
		serviceOpts = append(serviceOpts, quotation.WithCache(store, keys, quotation.CachePolicy{
			FreshFor: cfg.Cache.FreshFor,
			MaxAge:   cfg.Cache.MaxAge,
		}))
	} else {
		log.Print("quotation-api: cache off (REDIS_ADDR=off)")
	}

	api := quotation.NewAPI(
		quotation.NewService(cfg.Partners, breakers, serviceOpts...),
		cfg.Tenants,
		quotation.WithFallback(),
	)

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           platform.InstrumentHandler(api.Routes(), cfg.Telemetry.ServiceName),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		log.Print("quotation-api: signal received, shutting down")
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			log.Printf("quotation-api: forced shutdown: %v", err)
		}
		if err := stopTelemetry(shutdown); err != nil {
			log.Printf("quotation-api: telemetry shutdown: %v", err)
		}
	}()

	log.Printf("quotation-api listening on %s | partners=%d brokers=%v",
		server.Addr, len(cfg.Partners), cfg.Tenants)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server stopped: %v", err)
	}
}
