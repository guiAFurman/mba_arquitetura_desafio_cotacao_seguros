# Prumo Cota: resiliência na fronteira com as seguradoras

Entrega do desafio *Fundamentos de Arquitetura de Solução*. O enunciado está em [docs/enunciado.md](docs/enunciado.md).

| | |
|---|---|
| **SAD** | [docs/sad.md](docs/sad.md): as oito seções, os quatro diagramas C4 e todo número defendido |
| **Evidências** | [docs/evidencias/antes.md](docs/evidencias/antes.md) e [docs/evidencias/depois.md](docs/evidencias/depois.md): cada uma com o comando ou a consulta, o artefato e a legenda |
| **Autor** | Guilherme A. Furmankiewicz |

## O coração da solução

Protegi a cotação contra a instabilidade das três seguradoras, **na fronteira com elas**. Um **timeout por parceira** transforma a lentidão da `partner-degrading` em falha que se conta. Um **circuit breaker por parceira** para de chamar, e de pagar, a parceira que está falhando. Um **cache por parceira**, com a corretora na chave, evita comprar de novo a mesma consulta e cobre a parceira que caiu com a última cotação dela. A **resposta parcial** entrega o que existe e diz o que faltou, em vez de devolver 502. E as três parceiras são consultadas **ao mesmo tempo**. Com a mesma carga do "antes", o sucesso foi de 60% para 100%, o p95 sob carga de 8,01 s para 202 ms, e as consultas compradas por cotação entregue de 4,36 para 0,27. Isso custa ~R$ 7,4 mil por mês (operação, Redis e auditoria) e R$ 53 mil de construção, contra uma economia de R$ 63 mil a R$ 127 mil por mês em consultas: o payback fica abaixo de um mês, e o cache se paga a partir de 2,3% de hit rate (SAD §8). Custa também complexidade: um contrato de resposta maior, estado de breaker em memória e concorrência no código.

## Como subir e reproduzir

Precisa de Docker com Compose v2 e Go 1.25.

```bash
make down && make reproduce   # sobe tudo, espera ficar saudável e roda a carga padrão (10 de baseline, 200 com 50 em voo)
make test                     # a suíte inteira: 102 testes, 64 originais e 38 novos
make smoke                    # a prova determinística de que um breaker abre com os defaults
```

Depois do `make reproduce`: a API em http://localhost:8080, o Jaeger em http://localhost:16686 e o Prometheus em http://localhost:9090. Para ver o contrato novo, a mesma cotação do enunciado (ela sai com `degraded: true` quando a `partner-flaky` falha, o que acontece em 40% das chamadas):

```bash
curl -s localhost:8080/quotes -H 'Content-Type: application/json' -H 'X-Tenant-Id: corretora-a' \
  -d '{"driver":{"document":"12345678901","birth_year":1985},"vehicle":{"plate":"ABC1D23","model":"Onix 1.0","year":2020,"value_cents":7500000},"coverage":"comprehensive"}'
```

As variações de carga usadas em algumas evidências, e o porquê de cada uma, estão em [docs/evidencias/depois.md](docs/evidencias/depois.md). Nenhuma mexe nos perfis das parceiras.

## O que foi implementado e o que ficou como proposta

| Implementado | Onde | Decisão no SAD |
|---|---|---|
| Timeout por parceira: 2000 ms, 500 ms e 500 ms; o `http.Client` deixa de ter `Timeout` zero | `internal/partner/client.go` | §4.1 |
| Circuit breaker por parceira (`github.com/sony/gobreaker/v2`): abre com ≥ 50% de falha em ≥ 10 requisições numa janela de 30 s, fica 15 s aberto, 2 requisições de teste; aberto, **não chama** a parceira | `internal/partner/breaker.go` | §4.2 |
| Cache por parceira em Redis (`github.com/redis/go-redis/v9`): fresca por 1 h, vencida servida só na falha da parceira até 24 h, apagada pelo Redis em 24 h | `internal/quotecache/` | §4.3 |
| Resposta parcial e contrato de degradação; 503 de negócio quando nada está disponível | `internal/quotation/service.go`, `request.go`, `handler.go` | §4.4 |
| As três parceiras consultadas ao mesmo tempo (bônus) | `internal/quotation/service.go` | §4.8 |
| Métricas de negócio e marcações no trace | `internal/partner/metrics.go`, `internal/quotation/telemetry.go` | §4.5 |

**A chave do cache, por extenso:**

```text
quote:v1:{tenant_id}:{partner}:{hmac_sha256(documento|ano_nascimento|placa|modelo|ano_veiculo|valor_centavos|cobertura)}
```

O `tenant_id` vem do cabeçalho `X-Tenant-Id`, já validado; os campos do risco entram depois de `Request.Normalize()`; o HMAC usa o segredo `CACHE_KEY_SECRET`, e CPF e placa nunca aparecem legíveis no Redis. A invalidação antes da hora é pela versão na chave (`v1`); o descarte é o `EXPIRE` de 24 h.

**Mudança no contrato de `POST /quotes`.** Só foram **acrescentados** campos; quem os ignora continua funcionando:

| Campo | Onde | Significado |
|---|---|---|
| `degraded` | resposta | `true` se alguma parceira faltou ou alguma cotação é vencida |
| `missing_partners` | resposta | `{partner, reason}`, com `reason` ∈ `circuit_open`, `timeout`, `partner_error`; omitido quando nenhuma faltou |
| `source` | cada cotação | `partner` (consultada agora) ou `cache` |
| `age_seconds` | cada cotação | idade desde a consulta original |
| `stale` | cada cotação | `true` se passou de 1 h e só foi servida por causa de uma falha |

Erro novo: quando nenhuma parceira responde e não há cotação no cache, **503** `{"error":"no partner insurer available","missing_partners":[...]}`, distinto do 502 genérico. Os demais erros do enunciado não mudaram.

**Comportamento original preservado.** Dois testes que vieram no repositório exigem o tudo-ou-nada (502 quando uma parceira falha) e um exige a agregação em série. Por isso `Service.Quote` e a API montada com `NewAPI` mantêm o comportamento original, e o caminho novo entra por `Service.QuoteWithFallback` e pela opção `WithFallback()`, que o `cmd/quotation-api/main.go` liga (SAD §4.4, D9).

**Configuração nova** (padrões em `internal/platform/config.go`, que são os valores defendidos no SAD): `PARTNER_TIMEOUTS`, `PARTNER_TIMEOUT_DEFAULT`, `BREAKER_MIN_REQUESTS`, `BREAKER_FAILURE_RATIO`, `BREAKER_INTERVAL`, `BREAKER_OPEN_TIMEOUT`, `BREAKER_HALF_OPEN_REQUESTS`, `REDIS_ADDR` (`off` desliga o cache), `REDIS_TIMEOUT`, `CACHE_FRESH_TTL`, `CACHE_MAX_AGE` e `CACHE_KEY_SECRET`. O `docker-compose.yml` só define, para a `quotation-api`, o endereço do Redis e um segredo de desenvolvimento.

**Ficou como proposta, sem código:** o acervo de auditoria de 5 anos sem regravação (fora de escopo pelo enunciado; desenho e custo no SAD §5.2 e §8), apagar o cache por corretora, o breaker com estado compartilhado entre réplicas e a cópia da auditoria fora da região (SAD §4.7 e §7.4).

## Métricas e atributos criados

O nome no código é o do OpenTelemetry; o do Prometheus é o que o Collector gera a partir dele, e os dois aparecem lado a lado nas constantes de `internal/partner/metrics.go` e `internal/quotation/telemetry.go`.

| Métrica no Prometheus | Nome no código | Tipo | Rótulos |
|---|---|---|---|
| `partner_breaker_state` | `partner.breaker.state` | gauge: 0 fechado, 1 meio-aberto, 2 aberto | `partner` |
| `partner_breaker_transitions_total` | `partner.breaker.transitions` | contador | `partner`, `from`, `to` |
| `quote_cache_lookups_total` | `quote.cache.lookups` | contador | `partner`, `result` (`hit`, `miss`, `stale`, `error`) |
| `partner_quote_duration_seconds` | `partner.quote.duration` | histograma | `partner`, `outcome` (`success`, `timeout`, `error`, `circuit_open`) |

**Atributos de trace.** No span `quote {parceira}`, um por parceira: `partner.name`, `quote.source`, `cache.result`, `breaker.short_circuited`, `quote.missing_reason`. A requisição curto-circuitada é marcada com `breaker.short_circuited=true`; a resposta servida de cache, com `quote.source=cache`. No span `POST /quotes`: `tenant.id`, `quote.degraded`, `quote.count`, `quote.from_cache`, `quote.missing_partners`. Nenhum CPF, placa ou `quote_id` entra em span ou métrica.

## Limitações do ambiente

Documentadas aqui, como pede o enunciado, em vez de contornadas em silêncio:

- **A carga padrão não mostra a subida do hit rate.** Ela dura ~10 s, e as métricas são exportadas a cada 10 s pela telemetria pronta (`internal/platform/telemetry.go`, fora do escopo): o contador de acertos aparece no Prometheus já com o valor final. O gráfico da evidência 6 vem de uma variação declarada, com cache frio e ritmo mais lento.
- **A carga padrão não produz requisição curto-circuitada.** Quando o circuito da `partner-flaky` abre, as 5 cotações da carga já estão no cache. O trace da evidência 3 vem de uma variação declarada.
- **O hit rate da carga padrão foi 91%, e não ~98%.** O cache é por parceira e não guarda a falha da `partner-flaky`, então a cotação em que ela falhou volta a chamá-la. Esse número é da carga; o da §8 do SAD vem do pressuposto de recotação (40%).
- **Com pouco tráfego, o breaker quase não abre** (SAD §4.7, L5): uma cotação a cada ~3 s não junta, na janela de 30 s, falhas suficientes da `partner-flaky`.

## O que eu faria diferente com mais tempo

- **Janela deslizante no breaker** (`BucketPeriod` do gobreaker), para resolver o L5 sem abrir por acaso, refazendo os testes e as evidências do breaker.
- **Limite de chamadas simultâneas por parceira**, para o L1 e para o cache frio depois de perder o Redis; é bulkhead, fora do escopo deste desafio.
- **Agrupar chamadas iguais em voo** (*single-flight*), para que a rajada de cache frio não multiplique as consultas pagas.
- **Medir o pressuposto de recotação em produção** antes de investir, porque a economia da §8 depende dele; o alerta de hit rate (SAD §6.2, A4) é o começo disso.
- **Implementar o acervo de auditoria** e a cópia fora da região, que hoje são proposta.
- **Testar o meio-aberto sem relógio real**, com uma biblioteca de breaker que aceite relógio injetado ou um adaptador sobre a atual.

## Outros documentos

[docs/notas-leitura.md](docs/notas-leitura.md), [docs/estudo-decisoes.md](docs/estudo-decisoes.md) e [docs/estudo-decisoes-1b.md](docs/estudo-decisoes-1b.md) têm a leitura do código e o estudo das opções de cada decisão, com as fontes externas de preço e salário.
