// Package quotecache guarda as cotacoes de cada parceira no Redis (docs/sad.md §4.3).
package quotecache

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Version entra na chave: trocar a versao faz o cache inteiro deixar de ser lido, e o EXPIRE limpa o
// resto em ate 24 h.
const Version = "v1"

// Keyer monta a chave
//
//	quote:v1:{tenant_id}:{partner}:{hmac_sha256(documento|ano_nascimento|placa|modelo|ano_veiculo|valor_centavos|cobertura)}
//
// A corretora vai em texto aberto e e o contrato de isolamento: a mesma placa, cotada por outra
// corretora, e outra chave. CPF e placa so entram pelo HMAC, e nunca aparecem legiveis no Redis.
type Keyer struct {
	secret []byte
}

// NewKeyer usa o segredo da plataforma. Sem segredo, sorteia um: o cache continua isolado e ilegivel,
// mas so vale enquanto o processo viver.
func NewKeyer(secret string) (keyer *Keyer, generated bool) {
	if secret != "" {
		return &Keyer{secret: []byte(secret)}, false
	}
	random := make([]byte, 32)
	_, _ = rand.Read(random)
	return &Keyer{secret: random}, true
}

func (k *Keyer) Key(tenant, partner string, risk []string) string {
	mac := hmac.New(sha256.New, k.secret)
	mac.Write([]byte(strings.Join(risk, "|")))
	return "quote:" + Version + ":" + tenant + ":" + partner + ":" + hex.EncodeToString(mac.Sum(nil))
}

// TenantPattern casa todas as chaves de uma corretora, para apaga-las com SCAN + DEL.
func TenantPattern(tenant string) string {
	return "quote:" + Version + ":" + tenant + ":*"
}
