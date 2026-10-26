package quotation

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/partner"
	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/platform"
)

const TenantHeader = "X-Tenant-Id"

const requestLimit = 1 << 20

type API struct {
	service  *Service
	tenants  map[string]bool
	fallback bool
}

type APIOption func(*API)

// WithFallback troca o tudo-ou-nada pela resposta parcial (docs/sad.md §4.4). Sem esta opcao, a API
// responde como no starter, que e o que os testes originais exigem (D9).
func WithFallback() APIOption {
	return func(a *API) { a.fallback = true }
}

func NewAPI(service *Service, tenants []string, opts ...APIOption) *API {
	known := make(map[string]bool, len(tenants))
	for _, t := range tenants {
		known[t] = true
	}
	a := &API{service: service, tenants: known}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// unavailableBody e o 503 de negocio do RF-08: distinto do 502 generico, diz o que faltou.
type unavailableBody struct {
	Message string           `json:"error"`
	Missing []MissingPartner `json:"missing_partners"`
}

func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /quotes", a.quote)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		platform.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	return mux
}

func (a *API) quote(w http.ResponseWriter, r *http.Request) {
	tenant := r.Header.Get(TenantHeader)
	if tenant == "" {
		platform.WriteError(w, http.StatusBadRequest, TenantHeader+" is required")
		return
	}

	if !a.tenants[tenant] {
		platform.WriteError(w, http.StatusForbidden, "broker not enabled on this platform")
		return
	}

	var request Request
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, requestLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if err := request.Normalize(); err != nil {
		platform.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	if a.fallback {
		a.quoteWithFallback(w, r, tenant, request)
		return
	}

	response, err := a.service.Quote(r.Context(), tenant, request)
	if err != nil {
		a.respondPartnerFailure(w, err)
		return
	}

	w.Header().Set("X-Tenant-Id", tenant)
	platform.WriteJSON(w, http.StatusOK, response)
}

func (a *API) quoteWithFallback(w http.ResponseWriter, r *http.Request, tenant string, request Request) {
	response, err := a.service.QuoteWithFallback(r.Context(), tenant, request)
	var none *NoPartnerError
	switch {
	case errors.As(err, &none):
		platform.WriteJSON(w, http.StatusServiceUnavailable, unavailableBody{Message: none.Error(), Missing: none.Missing})
		return
	case err != nil:
		platform.WriteError(w, http.StatusBadGateway, "failed to query the partner insurers")
		return
	}

	w.Header().Set("X-Tenant-Id", tenant)
	platform.WriteJSON(w, http.StatusOK, response)
}

func (a *API) respondPartnerFailure(w http.ResponseWriter, err error) {
	var failure *partner.Error
	if errors.As(err, &failure) {
		platform.WriteJSON(w, http.StatusBadGateway, platform.ErrorBody{
			Message: "partner insurer unavailable",
			Partner: failure.Partner,
		})
		return
	}
	platform.WriteError(w, http.StatusBadGateway, "failed to query the partner insurers")
}
