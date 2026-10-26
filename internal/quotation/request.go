package quotation

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/partner"
)

type Request struct {
	Driver   Driver  `json:"driver"`
	Vehicle  Vehicle `json:"vehicle"`
	Coverage string  `json:"coverage"`
}

type Driver struct {
	Document  string `json:"document"`
	BirthYear int    `json:"birth_year"`
}

type Vehicle struct {
	Plate      string `json:"plate"`
	Model      string `json:"model"`
	Year       int    `json:"year"`
	ValueCents int64  `json:"value_cents"`
}

var coverages = map[string]bool{"comprehensive": true, "third_party": true}

const defaultCoverage = "comprehensive"

func (r *Request) Normalize() error {
	r.Coverage = strings.TrimSpace(r.Coverage)
	if r.Coverage == "" {
		r.Coverage = defaultCoverage
	}

	r.Driver.Document = strings.TrimSpace(r.Driver.Document)
	r.Vehicle.Plate = strings.ToUpper(strings.TrimSpace(r.Vehicle.Plate))
	r.Vehicle.Model = strings.TrimSpace(r.Vehicle.Model)

	switch {
	case r.Driver.Document == "":
		return fmt.Errorf("driver.document is required")
	case r.Driver.BirthYear < 1900:
		return fmt.Errorf("driver.birth_year is required and must be a valid year")
	case r.Vehicle.Plate == "":
		return fmt.Errorf("vehicle.plate is required")
	case r.Vehicle.Year < 1900:
		return fmt.Errorf("vehicle.year is required and must be a valid year")
	case r.Vehicle.ValueCents <= 0:
		return fmt.Errorf("vehicle.value_cents must be greater than zero")
	case !coverages[r.Coverage]:
		return fmt.Errorf("coverage must be comprehensive or third_party")
	}
	return nil
}

// riskFields sao os campos que mudam o premio, ja normalizados, na ordem da chave do cache
// (docs/sad.md §4.3): documento, ano de nascimento, placa, modelo, ano do veiculo, valor, cobertura.
func (r Request) riskFields() []string {
	return []string{
		r.Driver.Document,
		strconv.Itoa(r.Driver.BirthYear),
		r.Vehicle.Plate,
		r.Vehicle.Model,
		strconv.Itoa(r.Vehicle.Year),
		strconv.FormatInt(r.Vehicle.ValueCents, 10),
		r.Coverage,
	}
}

type partnerRequest struct {
	Broker string `json:"broker"`
	Request
}

// Response so ganhou campos (docs/sad.md §4.4, D10): quem ignora campo novo continua funcionando.
type Response struct {
	TenantID        string           `json:"tenant_id"`
	Degraded        bool             `json:"degraded"`
	Quotes          []PresentedQuote `json:"quotes"`
	MissingPartners []MissingPartner `json:"missing_partners,omitempty"`
	ElapsedMs       int64            `json:"elapsed_ms"`
}

// PresentedQuote e a cotacao da parceira como ela chegou, mais de onde veio e com que idade. O
// quote_id e sempre o original da parceira, inclusive quando vem do cache (SUSEP, docs/sad.md R6).
type PresentedQuote struct {
	partner.Quote
	Source     string `json:"source"`
	AgeSeconds int64  `json:"age_seconds"`
	Stale      bool   `json:"stale"`
}

const (
	SourcePartner = "partner"
	SourceCache   = "cache"
)

type MissingPartner struct {
	Partner string `json:"partner"`
	Reason  string `json:"reason"`
}

const (
	ReasonCircuitOpen  = "circuit_open"
	ReasonTimeout      = "timeout"
	ReasonPartnerError = "partner_error"
)
