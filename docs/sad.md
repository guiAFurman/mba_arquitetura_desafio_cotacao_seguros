# SAD — Prumo Cota: resiliência na fronteira com as seguradoras

*Solution Architecture Document* da plataforma de cotação de seguro auto da Prumo Tecnologia em Seguros, depois da intervenção que protege a fronteira com as seguradoras parceiras.

| | |
|---|---|
| **Versão** | 1.0 — as oito seções, com os números medidos do PoC ([evidencias/depois.md](evidencias/depois.md)). |
| **Data** | 2026-10-27 (escrito a partir de 2026-10-26) |
| **Autor** | Guilherme A. Furmankiewicz |
| **Base** | repositório deste fork, branch `development`; enunciado em [enunciado.md](enunciado.md) |
| **Evidências** | [evidencias/](evidencias/) — o "antes" está em [evidencias/antes.md](evidencias/antes.md) |

**Convenção de rastreabilidade.** Todo caminho citado existe no repositório, salvo quando marcado como **[proposta]**, que é o que será criado na Entrega 2 (PoC) ou que fica só no papel. Números sem fonte não aparecem: cada um aponta para uma medição em `docs/evidencias/`, para o enunciado ou para o código.

---

## 1. Introdução

### 1.1 Propósito

Este documento sustenta **uma decisão**: como proteger a cotação agregada do Prumo Cota contra a instabilidade das três seguradoras parceiras, usando timeout, circuit breaker, cache e fallback na fronteira com elas, e quanto isso custa e economiza.

Ele é escrito para três leitores:

| Leitor | O que procura aqui | Onde está |
|---|---|---|
| **CTO da Prumo** | se o gasto se paga e em quanto tempo | §1.5 (pressupostos), §8 |
| **Plantonista** (quem opera às 3h) | o que olhar, qual limiar, o que fazer | §4 (comportamento), §6, §7 |
| **Encarregado de dados e auditoria** | como provar à ANPD que uma corretora nunca vê o dado de outra, e à SUSEP que a cotação servida de cache é rastreável | §4.3 (chave e descarte), §4.4 (contrato), §7 |

### 1.2 Escopo

**Dentro:** a `quotation-api` e a sua **fronteira** com as seguradoras — o cliente HTTP (`internal/partner/client.go`), a agregação (`internal/quotation/service.go`), o contrato de resposta (`internal/quotation/request.go`), o cache em Redis e a instrumentação de negócio.

**Fora:** o motor de precificação das seguradoras (a Prumo não precifica), o CRM e o front-end das corretoras, cadastro de corretoras, cobrança, autenticação, e os padrões que o enunciado declara fora de escopo (retry com backoff, bulkhead, rate limiting, fila, Kubernetes). Esses aparecem neste documento só como limite ou proposta, nunca como implementação.

### 1.3 Restrições (impostas — não são escolhas deste documento)

| # | Restrição | Origem |
|---|---|---|
| R1 | Go 1.25, Docker com Compose v2, Redis como cache, OpenTelemetry com o SDK já inicializado, Mermaid nos diagramas | enunciado, "Tecnologias obrigatórias" |
| R2 | Ambiente local, sem conta em cloud e sem custo | enunciado, "Suba o ambiente" |
| R3 | Três parceiras com comportamento fixo e determinístico (`cmd/partner-mock/`), que não podem ser alteradas | enunciado, "Restrições" |
| R4 | Cobrança de R$ 0,04 por consulta à seguradora, não por venda | enunciado, "O cenário" |
| R5 | A Prumo é **operadora** de dados pessoais (LGPD); a corretora é controladora; uso cruzado entre corretoras é proibido | enunciado, "O que a regulação impõe" |
| R6 | Toda cotação apresentada é auditável por **5 anos**, sem regravação (SUSEP), inclusive a servida de cache ou de fallback | enunciado, "O que a regulação impõe" |
| R7 | Prêmio vem sempre de uma parceira ou de cache rastreável; a plataforma nunca calcula prêmio | enunciado, "Restrições" |
| R8 | As seguradoras honram o prêmio por até **24 h** (teto comercial) | enunciado, "O cenário" |
| R9 | Os testes que já vêm no repositório continuam verdes | enunciado, "Restrições" |

### 1.4 Decisões (escolhas deste documento, defendidas na §4)

Timeout por parceira (D1), biblioteca e escopo do breaker (D2, D3), política de disparo (D4), granularidade do cache (D5), TTL e política de vencimento (D6), chave do cache (D7), estratégia de fallback (D8), onde o fallback vive no código (D9), contrato da resposta degradada (D10); e, nas seções 5 a 7, onde a plataforma roda (D11), RTO e RPO por classe de dado (D12), os alertas (D13), o breaker com pouco tráfego como limite conhecido (D14) e o caminho de mudança do TTL (D15); e, como bônus, a agregação paralela (D16, §4.8). O estudo das opções está em [estudo-decisoes.md](estudo-decisoes.md) (D1 a D10) e em [estudo-decisoes-1b.md](estudo-decisoes-1b.md) (D11 a D15).

### 1.5 Pressupostos

Cada pressuposto tem valor, origem e o que muda se ele for falso.

| # | Pressuposto | Valor | Origem | Consequência se for falso |
|---|---|---|---|---|
| **P1** | **Recotação na mesma corretora:** fração das cotações que repetem o mesmo risco (mesmo motorista, veículo e cobertura), **na mesma corretora**, em até 1 h da anterior | **40%** | Estimativa. O enunciado afirma que "o corretor recota a mesma placa várias vezes na mesma conversa", sem número. Não há dado de produção; a métrica de hit rate do cache (§4.5) é o instrumento para medi-lo. | É o hit rate da §8. Se for **10%**, a economia de consultas cai de ≈ R$ 127 mil/mês para ≈ R$ 32 mil/mês e o veredito de payback da §8 precisa ser refeito. A recotação **entre corretoras** não conta: a chave do cache tem a corretora (§4.3). |
| **P2** | **Latência das parceiras em repouso** | slow ≤ 1700 ms · flaky ≤ 200 ms · degrading ≤ 160 ms | `docker-compose.yml` e `cmd/partner-mock/behavior.go` (latência + jitter); confirmado no trace do "antes" ([evidencias/antes.md](evidencias/antes.md), evidência 3) | Os timeouts da §4.1 são derivados destes valores. Se a `partner-slow` passar de 2 s em repouso, **toda** chamada a ela estoura e o breaker dela fica aberto o tempo todo: a parceira some das respostas por decisão nossa. Os timeouts precisam ser recalibrados com o p99 real de cada parceira. |
| **P3** | **Ritmo de produção:** 120.000 cotações por dia útil concentradas em 10 h de horário comercial | ≈ 3,3 cotações/s, ou seja ≈ 3,3 chamadas/s por parceira sem cache | Volume do enunciado ("O cenário"); a janela de 10 h é estimativa | Dimensiona a janela do breaker (§4.2): com 3,3 chamadas/s, o mínimo de 10 requisições é atingido em ≈ 3 s. Se o tráfego for muito mais espalhado (24 h), o mínimo demora mais para ser atingido e o breaker reage mais devagar. |
| **P4** | **Validade comercial do prêmio** | 24 h | Enunciado (R8) | A cotação vencida servida no fallback (§4.3, até 24 h) passaria a ser preço não honrado. A janela de vencida teria de cair para o `valid_for_seconds` devolvido pela parceira (300 s no mock). |
| **P5** | **Custo da empresa por pessoa** | 1,7 × a remuneração total (R$ 35.171/mês para um sênior) | Estimativa de encargos e benefícios sobre a mediana do Levels.fyi (§8.4) | Com 2,0×, o payback da §8 vai de 0,94 para 1,13 mês (20%) e de 0,44 para 0,53 mês (P1): o veredito não muda. |
| **P6** | **Tamanho do registro de auditoria** | 1 KB por cotação apresentada | Estimativa pelo formato da `Response` (§4.4): corretora, instante e as cotações com parceira, `quote_id`, prêmio, origem e idade | O armazenamento é tão barato (R$ 352 no ano 5, §8.3) que nem 10× muda o veredito; muda a classe de armazenamento escolhida. |
| **P7** | **Corretoras ativas** | 300 | Enunciado: "algumas centenas em produção" | Muda só o número de gravações da auditoria em lote (§8.3), que custa centavos. |

---

## 2. Visão geral da arquitetura

### 2.1 Contexto (C4 nível 1)

A Prumo **intermedeia**: recebe o pedido da corretora, consulta as seguradoras e devolve as propostas. Ela não precifica.

```mermaid
flowchart LR
  classDef person fill:#08427b,color:#fff,stroke:#052e56
  classDef system fill:#1168bd,color:#fff,stroke:#0b4884
  classDef external fill:#999,color:#fff,stroke:#6b6b6b

  corretor(["Corretor de seguros<br/><i>pessoa · usa o sistema da corretora</i>"]):::person
  plantao(["Plantonista Prumo<br/><i>pessoa</i>"]):::person
  prumo["Prumo Cota<br/><i>sistema · cotação de seguro auto multi-corretora</i>"]:::system
  seg1["Seguradora A<br/><i>sistema externo · motor de precificação</i>"]:::external
  seg2["Seguradora B<br/><i>sistema externo · motor de precificação</i>"]:::external
  seg3["Seguradora C<br/><i>sistema externo · motor de precificação</i>"]:::external

  corretor -- "pede cotação de um risco<br/>HTTPS/JSON · X-Tenant-Id" --> prumo
  prumo -- "devolve propostas ordenadas por prêmio<br/>HTTPS/JSON" --> corretor
  prumo -- "consulta prêmio · R$ 0,04 por consulta<br/>HTTPS/JSON" --> seg1
  prumo -- "consulta prêmio · R$ 0,04 por consulta<br/>HTTPS/JSON" --> seg2
  prumo -- "consulta prêmio · R$ 0,04 por consulta<br/>HTTPS/JSON" --> seg3
  plantao -- "observa traces e métricas<br/>HTTP (UI do Jaeger e do Prometheus)" --> prumo
```

No ambiente deste repositório, as seguradoras A, B e C são os contêineres `partner-slow`, `partner-flaky` e `partner-degrading` (`cmd/partner-mock/`).

### 2.2 Contêineres — antes (C4 nível 2)

Como o sistema está no commit de partida, conforme `docker-compose.yml`.

```mermaid
flowchart LR
  classDef person fill:#08427b,color:#fff,stroke:#052e56
  classDef container fill:#438dd5,color:#fff,stroke:#2e6295
  classDef external fill:#999,color:#fff,stroke:#6b6b6b
  classDef idle fill:#fff,color:#666,stroke:#999,stroke-dasharray: 5 5

  corretor(["Corretora<br/><i>pessoa</i>"]):::person

  subgraph prumo["Prumo Cota"]
    api["quotation-api<br/><i>contêiner · Go 1.25</i><br/>agrega em série, sem timeout,<br/>tudo-ou-nada"]:::container
    redis[("redis<br/><i>contêiner · Redis 7</i><br/>sobe, mas ninguém usa")]:::idle
    otel["otel-collector<br/><i>contêiner · OTel Collector</i>"]:::container
    jaeger["jaeger<br/><i>contêiner · Jaeger all-in-one</i>"]:::container
    prom["prometheus<br/><i>contêiner · Prometheus 3</i>"]:::container
  end

  slow["partner-slow<br/><i>sistema externo · 1,5–1,7 s</i>"]:::external
  flaky["partner-flaky<br/><i>sistema externo · 40% de 503</i>"]:::external
  degr["partner-degrading<br/><i>sistema externo · afunda acima de 5 simultâneas</i>"]:::external

  corretor -- "POST /quotes<br/>HTTP/JSON · X-Tenant-Id" --> api
  api -- "1º: POST /quotes<br/>HTTP/JSON · sem timeout" --> slow
  api -- "2º: POST /quotes<br/>HTTP/JSON · sem timeout" --> flaky
  api -- "3º: POST /quotes<br/>HTTP/JSON · sem timeout" --> degr
  api -- "traces e métricas genéricas<br/>OTLP/gRPC :4317" --> otel
  otel -- "traces<br/>OTLP/gRPC" --> jaeger
  prom -- "raspa métricas a cada 5 s<br/>HTTP :8889" --> otel
```

O que o diagrama mostra, e que foi medido ([evidencias/antes.md](evidencias/antes.md)): a latência é a **soma** das três (trace de 8,05 s, dos quais 6,1 s na `partner-degrading`), e uma falha da `partner-flaky` derruba a cotação inteira (502, com o span da `partner-degrading` ausente).

### 2.3 Contêineres — depois (C4 nível 2)

```mermaid
flowchart LR
  classDef person fill:#08427b,color:#fff,stroke:#052e56
  classDef container fill:#438dd5,color:#fff,stroke:#2e6295
  classDef external fill:#999,color:#fff,stroke:#6b6b6b
  classDef proposal fill:#fff,color:#333,stroke:#d4a017,stroke-dasharray: 5 5

  corretor(["Corretora<br/><i>pessoa</i>"]):::person

  subgraph prumo["Prumo Cota"]
    api["quotation-api<br/><i>contêiner · Go 1.25</i><br/>timeout + circuit breaker por parceira,<br/>cache por parceira, resposta parcial,<br/>as 3 parceiras em paralelo"]:::container
    redis[("redis<br/><i>contêiner · Redis 7</i><br/>cache de cotações por parceira")]:::container
    otel["otel-collector<br/><i>contêiner · OTel Collector</i>"]:::container
    jaeger["jaeger<br/><i>contêiner · Jaeger all-in-one</i>"]:::container
    prom["prometheus<br/><i>contêiner · Prometheus 3</i>"]:::container
    audit[("acervo de auditoria<br/><i>[proposta] · armazenamento WORM</i><br/>5 anos")]:::proposal
  end

  slow["partner-slow<br/><i>sistema externo</i>"]:::external
  flaky["partner-flaky<br/><i>sistema externo</i>"]:::external
  degr["partner-degrading<br/><i>sistema externo</i>"]:::external

  corretor -- "POST /quotes<br/>HTTP/JSON · X-Tenant-Id" --> api
  api -- "lê e grava cotações por parceira<br/>RESP/TCP :6379 · MGET, SET EX" --> redis
  api -- "POST /quotes · timeout 2000 ms<br/>HTTP/JSON · só se o circuito não estiver aberto" --> slow
  api -- "POST /quotes · timeout 500 ms<br/>HTTP/JSON · só se o circuito não estiver aberto" --> flaky
  api -- "POST /quotes · timeout 500 ms<br/>HTTP/JSON · só se o circuito não estiver aberto" --> degr
  api -- "traces e métricas de negócio<br/>OTLP/gRPC :4317" --> otel
  api -. "registro de cotação apresentada<br/>[proposta]" .-> audit
  otel -- "traces<br/>OTLP/gRPC" --> jaeger
  prom -- "raspa métricas a cada 5 s<br/>HTTP :8889" --> otel
```

### 2.4 O delta

| Tipo | O que muda | Onde |
|---|---|---|
| **Acrescenta** | timeout por parceira | `internal/partner/client.go` e `internal/platform/config.go` |
| **Acrescenta** | circuit breaker por parceira (gobreaker v2) | `internal/partner/breaker.go` |
| **Acrescenta** | cache por parceira em Redis | pacote novo `internal/quotecache/` (`key.go`, `redis.go`) |
| **Acrescenta** | resposta parcial e campos de degradação no contrato | `internal/quotation/service.go`, `internal/quotation/request.go`, `internal/quotation/handler.go` |
| **Acrescenta** | agregação paralela no caminho com fallback (bônus, §4.8) | `internal/quotation/service.go` |
| **Acrescenta** | métricas e marcações de trace de negócio | `internal/partner/metrics.go`, `internal/quotation/telemetry.go` |
| **Muda de papel** | o `redis` deixa de ser um contêiner ocioso e passa a ser dependência da API (não obrigatória: sem ele, a API funciona sem cache) | `docker-compose.yml`, `cmd/quotation-api/main.go` |
| **Muda de papel** | a resposta deixa de ser tudo-ou-nada: 502 só sai do caminho antigo, que fica preservado para os testes originais | `internal/quotation/service.go` |
| **Fica no papel** | acervo de auditoria de 5 anos | **[proposta]** — custo na §8, sem código (fora de escopo pelo enunciado) |
| **Não sai nada** | o Collector, o Jaeger, o Prometheus e as três parceiras continuam como estão | — |

**A quem esta arquitetura serve.** À corretora, que deixa de receber 502 quando uma das três parceiras falha e passa a receber o que existe, sabendo o que faltou. Ao CTO, porque cada consulta que o cache evita é R$ 0,04 a menos, e cada cotação que não morre no meio deixa de desperdiçar as consultas já pagas. Ao plantonista, que passa a ver o estado de cada parceira em vez de deduzi-lo de 502 genéricos. E ao encarregado de dados, porque o isolamento entre corretoras deixa de depender de cuidado e passa a estar na chave do cache.

---

## 3. Requisitos

### 3.1 Funcionais

Os RF-01 a RF-04 saem do contrato que já existe; os RF-05 a RF-09 são **criados por esta arquitetura**. Cada um é uma frase testável.

| ID | Requisito | Origem | Como se testa |
|---|---|---|---|
| RF-01 | `POST /quotes` sem o cabeçalho `X-Tenant-Id` responde **400** `{"error":"X-Tenant-Id is required"}`; com corretora fora de `TENANTS`, **403** `{"error":"broker not enabled on this platform"}`. | contrato atual, `internal/quotation/handler.go` | `internal/quotation/handler_test.go` |
| RF-02 | A resposta de sucesso lista as cotações **ordenadas por `premium_cents` crescente**, inclusive quando é degradada. | contrato atual, `internal/quotation/service.go`; enunciado, "Outras regras" | `TestQuoteAggregatesTheThreePartnersSortedByPremium` (original) e `TestOnePartnerDownGivesAPartialResponseInsteadOf502` (degradada), em `internal/quotation/` |
| RF-03 | O corpo inválido responde **400** com as mensagens de `Request.Normalize()`. | contrato atual, `internal/quotation/request.go` | `internal/quotation/request_test.go` |
| RF-04 | Toda cotação apresentada carrega a parceira, o prêmio, a corretora (`tenant_id`) e o `quote_id` **original** da parceira, inclusive quando vem do cache. | R6 (SUSEP) | `TestAFreshCachedQuoteDoesNotCallThePartner` e `TestASuccessfulAnswerIsSavedForTheBroker` (`internal/quotation/fallback_test.go`) |
| RF-05 | Quando uma parceira falha (erro, timeout ou circuito aberto) e as outras respondem, a API responde **200** com as cotações disponíveis, `"degraded": true` e a parceira ausente em `missing_partners`, com o motivo. | **nova** (D8) | `TestOnePartnerDownGivesAPartialResponseInsteadOf502`, `TestTheReasonSaysWhyThePartnerIsMissing` e `TestTheAPIWithFallbackAnswers200Degraded` (`internal/quotation/fallback_test.go`) |
| RF-06 | Uma cotação servida do cache vem com `"source": "cache"` e `age_seconds` (idade desde a consulta original); uma consultada agora vem com `"source": "partner"`. | **nova** (D6, D10) | `TestAFreshCachedQuoteDoesNotCallThePartner`, `TestAnExpiredQuoteIsOnlyServedWhenThePartnerFails` e `TestAQuoteOlderThan24hIsNeverServed` (`internal/quotation/fallback_test.go`) |
| RF-07 | Com o circuito de uma parceira **aberto**, a API **não faz** a chamada HTTP a ela; a parceira aparece em `missing_partners` com o motivo `circuit_open`, ou é preenchida pela cotação vencida do cache. | **nova** (D3, D4) | `TestBreakerOpensOnTheFlakySequenceAndStopsCallingThePartner` (`internal/partner/breaker_test.go`): a parceira não recebe nenhuma das 20 chamadas feitas com o circuito aberto |
| RF-08 | Se nenhuma parceira responder e não houver cotação no cache, a API responde **503** `{"error":"no partner insurer available","missing_partners":[...]}`, distinto do 502 atual. | **nova** (D8) | `TestNothingAvailableIsABusinessError` e `TestTheAPIWithFallbackAnswers503WhenNothingIsAvailable` (`internal/quotation/fallback_test.go`) |
| RF-09 | Uma cotação gravada para uma corretora **nunca** é servida a outra, mesmo com corpo idêntico. | **nova** (D7); R5 | `TestOneBrokerNeverReceivesTheCachedQuoteOfAnother` (`internal/quotation/fallback_test.go`) e `TestTheSameRiskIsADifferentKeyForEachBroker` (`internal/quotecache/quotecache_test.go`) |

### 3.2 Não funcionais

O "hoje" das linhas RNF-01, RNF-02, RNF-03 e RNF-04 foi medido com `make down && make reproduce` em 2026-10-26 ([evidencias/antes.md](evidencias/antes.md)); o da RNF-03 vem dos contadores do Prometheus dessa mesma execução. Todas as medições usam a carga padrão: 10 requisições de baseline e 200 com 50 em voo.

| ID | Requisito | Métrica | Hoje | Alvo | Como medir | Por que este número |
|---|---|---|---|---|---|---|
| RNF-01 | Latência da cotação sob carga | p95 do `POST /quotes`, fase de carga | **8,01 s** | **≤ 2,0 s** | relatório do `make reproduce`; em gráfico: `histogram_quantile(0.95, sum by (le) (rate(http_server_request_duration_seconds_bucket{http_route="/quotes"}[5m])))` | É o **teto estrutural** da agregação paralela (§4.8, D16): a cotação espera a parceira mais lenta, e o maior timeout da §4.1 é 2,0 s. Em série seria a soma, 2,0 + 0,5 + 0,5 = 3,0 s. Nenhuma requisição deveria passar disso, com ou sem cache. |
| RNF-02 | Cotações entregues | fração de respostas **200** (completas ou degradadas) na fase de carga | **60%** (120 de 200) | **≥ 99%** | relatório do `make reproduce` (`success`) | Com resposta parcial, só falha quando as três parceiras falham juntas e não há cache. A `partner-slow` nunca falha em repouso (P2), então esse caso é raro. |
| RNF-03 | Consultas compradas por cotação entregue | `sum(http_client_request_duration_seconds_count) / sum(http_server_request_duration_seconds_count{http_route="/quotes", http_response_status_code="200"})` | **4,36** (545 consultas para 125 entregues; [evidencias/consultas-antes.txt](evidencias/consultas-antes.txt)) | **≤ 1,8** em produção | consulta PromQL ao lado, no fim de cada execução | 3 consultas × (1 − 40% de hit rate do P1) = 1,8. O "hoje" acima de 3 é o desperdício do tudo-ou-nada: as consultas feitas antes de uma falha são pagas e jogadas fora. |
| RNF-04 | Vazão sob 50 em voo | requisições por segundo na fase de carga | **8,0 req/s** | **≥ 25 req/s** | relatório do `make reproduce` (`throughput`) | Lei de Little com 50 em voo e o teto de 2,0 s da RNF-01: 50 ÷ 2,0 = 25 req/s. |
| RNF-05 | Custo de uma parceira com circuito aberto | chamadas HTTP feitas a uma parceira enquanto o circuito dela está aberto | não existe breaker | **0** | trace da requisição curto-circuitada **sem** span `HTTP POST` para aquela parceira (evidência 3 da Entrega 2) | Aberto quer dizer que a chamada não sai (enunciado, Entrega 2 §1). Uma chamada feita e descartada não economiza os R$ 0,04 nem o tempo. |
| RNF-06 | Isolamento entre corretoras | respostas servidas do cache a uma corretora diferente da que gerou a entrada | 0 (não há cache) | **0**, sempre | teste determinístico do RF-09; inspeção do formato da chave (§4.3) | R5: uma resposta cruzada é incidente de dados pessoais com dever de notificação à ANPD. Não existe tolerância. |
| RNF-07 | Prazo máximo de guarda no cache | maior TTL restante de uma chave `quote:*` no Redis | não há cache | **≤ 86.400 s** (24 h) | `redis-cli --scan --pattern 'quote:*'` seguido de `TTL` em cada chave | P4 e LGPD: nada fica guardado além do teto comercial. O descarte é feito pelo próprio Redis, não por rotina manual. |

### 3.3 Hoje e depois, medidos

O "depois" foi medido com o PoC completo, com a mesma carga padrão ([evidencias/depois.md](evidencias/depois.md)).

| ID | Hoje | Depois | Alvo | Atingido? | Evidência |
|---|---|---|---|---|---|
| RNF-01 | 8,01 s | **202 ms** | ≤ 2,0 s | sim | [reproduce-depois.txt](evidencias/reproduce-depois.txt) |
| RNF-02 | 60% | **100%** | ≥ 99% | sim | [reproduce-depois.txt](evidencias/reproduce-depois.txt) |
| RNF-03 | 4,36 | **0,27** nesta carga | ≤ 1,8 em produção | sim, mas o 0,27 vem de uma carga que repete 5 cotações (91% de acerto); em produção o valor esperado é o do P1, 1,8 | [consultas-depois.txt](evidencias/consultas-depois.txt) |
| RNF-04 | 8,0 req/s | **730 req/s** | ≥ 25 req/s | sim; o laço fechado do gerador acelera quando a latência cai (roteiro §7) | [reproduce-depois.txt](evidencias/reproduce-depois.txt) |
| RNF-05 | não existia | **0** chamadas | 0 | sim: nenhum `HTTP POST` para as parceiras curto-circuitadas | [trace-depois-breaker-aberto-rapido.json](evidencias/trace-depois-breaker-aberto-rapido.json) |
| RNF-06 | 0 (sem cache) | **0** | 0 | sim | `TestOneBrokerNeverReceivesTheCachedQuoteOfAnother`, em [make-test.txt](evidencias/make-test.txt) |
| RNF-07 | sem cache | **24 h** | ≤ 24 h | sim | `TestRedisKeepsTheQuoteForAtMostTheMaxAge`, em [make-test.txt](evidencias/make-test.txt) |

---

## 4. Detalhamento da arquitetura

Cada decisão está no formato **contexto → opções → escolha → consequências**. As opções foram estudadas em [estudo-decisoes.md](estudo-decisoes.md), que traz as tabelas completas de prós e contras.

### 4.1 Timeout por parceira (D1)

**Contexto.** O `http.Client` em `internal/partner/client.go` tem `Timeout` zero: a API espera o que a parceira quiser. A `partner-degrading` nunca falha, ela afunda: 120 ms em repouso, +300 ms por chamada simultânea acima de 5, até +6000 ms (`docker-compose.yml`). No "antes", ela levou **6149 ms** em uma única chamada ([evidencias/antes.md](evidencias/antes.md)). Sem teto de espera, essa lentidão nunca vira sinal para o breaker.

**Opções.** (a) um timeout único de 2 s; (b) um timeout único abaixo de 1,7 s; (c) um timeout por parceira, derivado de uma regra.

**Escolha: (c).** Regra: **latência máxima em repouso (P2) + 300 ms, arredondada para cima ao próximo múltiplo de 500 ms.**

| Parceira | Máxima em repouso (P2) | + 300 ms | **Timeout** | Estoura quando |
|---|---|---|---|---|
| `partner-slow` | 1700 ms | 2000 ms | **2000 ms** | a parceira ficar ≥ 300 ms mais lenta que o normal |
| `partner-flaky` | 200 ms | 500 ms | **500 ms** | idem |
| `partner-degrading` | 160 ms | 460 ms | **500 ms** | a partir de **7** chamadas simultâneas (120 + 40 + 300 × 2 = 760 ms) |
| **Teto da cotação** | | | **2000 ms** | com a agregação paralela (§4.8), o maior dos três; em série seria a soma, 3000 ms. É o teto estrutural da RNF-01 |

**O estouro conta como falha para o breaker.** Cancelamento pelo **cliente** (a corretora desistiu da requisição) **não** conta: não é a parceira que falhou.

**Como fica no código** (`internal/partner/client.go`): o `http.Client` deixa de ter `Timeout` zero e passa a ter o maior teto configurado (2000 ms), que nenhuma chamada ultrapassa; cada chamada ainda corta no teto da sua parceira, com `context.WithTimeout`. Os valores vêm de `PARTNER_TIMEOUTS` em `internal/platform/config.go`, e os padrões são os da tabela acima.

**Descartadas.** (a) deixa a `partner-degrading` acumular até 11 chamadas simultâneas antes de estourar (120 + 300 × 6 ≈ 1,9 s): ela já estaria afundada quando o breaker passasse a enxergá-la. Em série, ainda poria o teto da cotação em 6 s; com a agregação paralela (§4.8) o teto seria o mesmo, 2 s, e o argumento que sobra, e decide, é o da `partner-degrading`. (b) mata a `partner-slow` em 100% das chamadas, porque ela leva de 1,5 a 1,7 s sempre: o breaker dela abriria e nunca mais fecharia.

**Consequências.**
- Boa: a degradação da `partner-degrading` vira falha contável logo depois do joelho de 5 simultâneas; a cotação nunca passa de ~3 s.
- Ruim: três números para manter, e todos dependem do P2. Se a latência real de uma parceira mudar, o timeout dela fica errado. A regra (e não os números) é o que se revisa.
- Ruim: com a `partner-degrading` perto do limite de 7 simultâneas, o breaker dela tende a abrir e fechar com frequência. Isso precisa de alerta (§6).

### 4.2 Circuit breaker (D2, D3, D4)

**Contexto.** O enunciado exige os três estados visíveis de fora e que **aberto signifique que a chamada não sai** (Entrega 2 §1). O `cmd/partner-mock/feasibility_test.go` já provou que, com os defaults, uma política de 50% de falha em janela de 10 abre na requisição **10** e volta a fechar **6** vezes em 210 requisições ([smoke-test-factibilidade.md](smoke-test-factibilidade.md) §2.1).

#### Biblioteca (D2)

**Opções.** (a) `github.com/sony/gobreaker/v2`; (b) `failsafe-go`; (c) implementação própria.

**Escolha: (a) `sony/gobreaker` v2** (versão mais recente: v2.4.0). Os três estados são constantes nomeadas (`StateClosed`, `StateHalfOpen`, `StateOpen`); o callback `OnStateChange` dá a métrica de transição sem código extra; `ReadyToTrip` recebe os contadores e permite a política por taxa; `IsSuccessful` decide o que é falha (timeout e 5xx são; 4xx não é); e `IsExcluded` tira da conta o que não diz nada sobre a parceira (o cancelamento pelo cliente). Com o circuito aberto, `Execute` devolve `ErrOpenState` **sem executar** a função, o que garante que a chamada HTTP não sai (RNF-05).

**Descartadas.** (b) traz retry, bulkhead e rate limiter, que são padrões fora de escopo, e a defesa ficaria mais longa sem ganho para este caso. (c) reescreve o que o próprio `feasibility_test.go` já simula e aumenta o código a defender.

**Consequência ruim.** O gobreaker não aceita relógio injetado. O teste do breaker **abrindo** é determinístico (não depende de tempo), mas a passagem de aberto para meio-aberto depende do relógio real. O meio-aberto será provado pela evidência de gráfico do estado no tempo, não por teste unitário.

#### Escopo (D3)

**Opções.** (a) um breaker por parceira; (b) um por par (parceira, corretora); (c) um global.

**Escolha: (a) um por parceira.** A falha é do **motor de precificação** da seguradora, que é um só para todas as corretoras. As condições comerciais mudam por corretora; a disponibilidade, não.

**Descartadas.** (b) com centenas de corretoras em produção, cada breaker teria pouco tráfego e demoraria a juntar o mínimo de requisições; uma parceira fora do ar seria descoberta separadamente por cada corretora, e cada uma pagaria as consultas até o seu circuito abrir. (c) uma parceira afundada derrubaria as três.

**Consequência ruim.** Uma corretora que gera muito tráfego ruim pode abrir o circuito para todas (limite L4, §4.7).

#### Política e parâmetros (D4)

| Parâmetro | Valor | Defesa |
|---|---|---|
| **Abre quando** | **≥ 50% de falha, com no mínimo 10 requisições** contadas | Validado pelo `feasibility_test.go`, que abre na requisição 10. Com o gobreaker, que só avalia o disparo depois de uma falha, abre na **requisição 12** (6 falhas em 12) sobre a mesma sequência da `partner-flaky`: preso pelo teste `TestBreakerOpensOnTheFlakySequenceAndStopsCallingThePartner` em `internal/partner/breaker_test.go`. Não abre no ruído normal de 40%. Por ser proporção e não ordem, resiste à concorrência: sob 34 simultâneas a rajada cai de 9 para 5 seguidas, e uma regra de "5 consecutivas" passaria a abrir só na requisição 104 (smoke §2.3). |
| **Janela do estado fechado** (`Interval`) | **30 s** | Com o ritmo de produção do P3 (≈ 3,3 chamadas/s por parceira), 30 s juntam ≈ 100 chamadas, bem acima do mínimo de 10. Mais curta, poderia não juntar o mínimo; mais longa, falhas antigas pesariam no presente. |
| **Tempo aberto** (`Timeout`) | **15 s** | Precisa ser maior que o intervalo de exportação de métricas (10 s, `internal/platform/telemetry.go`) para que o estado aberto apareça na série temporal. É também maior que o maior timeout (2 s): quando o meio-aberto testa, as chamadas que estavam em voo já terminaram e a `partner-degrading` já se esvaziou. Em produção (P3), 15 s aberto evitam ≈ 50 consultas pagas a uma parceira que está falhando. |
| **Meio-aberto** (`MaxRequests`) | **2** requisições de teste; 2 sucessos fecham, 1 falha reabre | Com 40% de falha da `partner-flaky`, a chance de 2 sucessos seguidos é 36%; de 3, 21,6%. O smoke validou 2 sucessos com 6 voltas a fechado em 210 requisições, o que torna o ciclo completo observável. |
| **O que conta como falha** | erro de rede, **timeout** (D1), HTTP ≥ 500, resposta ilegível | O que indica que a parceira não está saudável. |
| **O que não conta** | cancelamento pelo cliente (excluído da conta, via `IsExcluded`); HTTP 4xx (conta como sucesso) | Cancelamento é decisão da corretora; 4xx indica pedido nosso inválido, não indisponibilidade da parceira. |

**Comportamento observável de cada estado:**

| Estado | A parceira é chamada? | O que a corretora vê |
|---|---|---|
| **Fechado** | sim, com o timeout da §4.1 | resposta normal da parceira (`source: partner`) |
| **Aberto** | **não** | cotação vencida do cache, se houver (`source: cache`, com idade); senão a parceira em `missing_partners` com `circuit_open` |
| **Meio-aberto** | só as 2 requisições de teste; as demais seguem como no aberto | como no fechado, para as de teste |

### 4.3 Cache (D5, D6, D7)

#### Granularidade (D5)

**Opções.** (a) uma entrada por parceira e risco; (b) uma entrada por risco com a cotação agregada.

**Escolha: (a) por parceira.** Com a `partner-flaky` falhando 40% das vezes, um cache agregado só seria gravado quando as três respondessem, e cerca de 40% das cotações nunca entrariam nele. Por parceira, as respostas boas entram sempre, e a cotação de uma parceira pode ser usada quando só ela falhar.

**Consequências.** Boa: o cache sobrevive a uma parceira fora do ar por horas (cenário (b) da §7). Ruim: a mesma resposta pode misturar cotações de idades diferentes; por isso a idade vai **em cada cotação** (D10). São 3 leituras por requisição, feitas em um único `MGET`.

#### TTL e política de vencimento (D6)

**Opções.** (a) 300 s, o `valid_for_seconds` do mock; (b) entre 15 min e 1 h; (c) 24 h, o teto comercial.

**Escolha: (b), com dois prazos.**

| Prazo | Valor | O que acontece |
|---|---|---|
| **Fresca** | idade < **1 h** | servida normalmente, sem chamar a parceira (`source: cache`, `age_seconds`) |
| **Vencida** | idade entre 1 h e 24 h | **só** é servida se a parceira falhar (erro, timeout ou circuito aberto); marcada com a idade; a resposta inteira sai com `degraded: true` |
| **Descarte** | **24 h** | a entrada é gravada com `SET ... EX 86400`; o Redis a apaga sozinho (RNF-07) |

**Defesa do 1 h.** É a duração de uma conversa de venda, em que o corretor recota a mesma placa várias vezes (enunciado, "O cenário"). Esse é o P1: 40% de recotação **na mesma corretora** em até 1 h, que é o hit rate da §8. O 1 h fica bem abaixo do teto comercial de 24 h (R8), o que limita o risco de exibir preço que a seguradora já mudou.

**Defesa da vencida até 24 h.** Na falha, uma cotação de 3 h atrás, identificada como tal, é mais útil para a corretora do que nenhuma, e a seguradora ainda a honra (P4). Fora da falha, ela nunca é servida.

**Descartadas.** (a) 300 s pega só a recotação dentro de 5 minutos e reduz a economia da §8 a uma fração; o enunciado deixa claro que esse é o TTL técnico do mock, não o de negócio. (c) 24 h apresenta como normal uma cotação de quase um dia, sem margem nenhuma até o teto.

**Invalidação antes da hora:**

| Mecanismo | Quando | Situação |
|---|---|---|
| TTL (fresca 1 h, descarte 24 h) | sempre | implementado |
| **Versão na chave** (`v1`) | mudança do formato da cotação ou da tabela de preço | implementado: trocar a versão faz todo o cache antigo deixar de ser lido; o TTL limpa o resto em até 24 h |
| Apagar por corretora | corretora deixa a plataforma ou pede eliminação de dados | **[proposta]**: o `tenant_id` é o terceiro segmento da chave, então `SCAN` com o padrão `quote:v1:{tenant_id}:*` seguido de `DEL` resolve, sem código novo na API |

**Consequência ruim.** O TTL é a única invalidação automática: se uma seguradora mudar o preço no meio da hora, a corretora vê o preço antigo até a entrada vencer (limite L3, §4.7).

#### Chave (D7)

**A chave, por extenso:**

```text
quote:v1:{tenant_id}:{partner}:{hmac}
```

| Segmento | Conteúdo | Exemplo |
|---|---|---|
| `quote` | espaço de nomes | `quote` |
| `v1` | versão do formato (invalidação em massa) | `v1` |
| `{tenant_id}` | a corretora, lida do cabeçalho `X-Tenant-Id` **já validado** contra `TENANTS` | `corretora-a` |
| `{partner}` | o nome da parceira, como em `PARTNER_ENDPOINTS` | `partner-flaky` |
| `{hmac}` | HMAC-SHA256, em hexadecimal, dos campos do risco **depois** de `Request.Normalize()`, separados por `\|`: `driver.document`, `driver.birth_year`, `vehicle.plate`, `vehicle.model`, `vehicle.year`, `vehicle.value_cents`, `coverage` | `9f2c…` (64 caracteres) |

Exemplo completo: `quote:v1:corretora-a:partner-flaky:9f2c…e41a`.

**Por que cada parte.**
- **`tenant_id` na chave** é o contrato de isolamento (R5, RNF-06). Ele vem do **cabeçalho**, não do corpo: o corpo não tem a corretora (enunciado, "Dicas finais"). O próprio mock confirma a necessidade: o prêmio é um hash do corpo enviado à parceira, que inclui a corretora (`cmd/partner-mock/behavior.go`), então a mesma placa custa diferente para corretoras diferentes.
- **Todos os campos que mudam o prêmio**, não só a placa: dois motoristas com o mesmo carro, ou o mesmo motorista com outra cobertura, são riscos diferentes.
- **Normalizados** por `Request.Normalize()` (placa em maiúsculas, espaços removidos, cobertura padrão): `abc1d23` e `ABC1D23` são o mesmo risco.
- **HMAC em vez de texto aberto:** CPF e placa não aparecem em nenhuma ferramenta que liste chaves do Redis. Um SHA-256 simples seria reversível por força bruta, porque o CPF tem poucos dígitos; o HMAC com um segredo da plataforma não é. O segredo vem de variável de ambiente.

**Valor da entrada:** a cotação da parceira como ela chegou (`partner`, `quote_id`, `premium_cents`, `currency`, `coverage_cents`, `valid_for_seconds`) mais `cached_at`. Não contém CPF nem placa.

**Se o Redis cair:** a leitura é tratada como *miss* e a gravação é ignorada. A cotação continua funcionando, só sem cache (cenário (a) da §7). Cada operação no Redis tem teto de **100 ms** (`REDIS_TIMEOUT`), sem nova tentativa: um quinto do menor timeout de parceira (500 ms, §4.1). O cache nunca pode custar mais que a consulta que ele evita. Se a leitura falhar, a mesma cotação **não tenta gravar** depois: com o Redis fora, cada cotação perde no máximo uma leitura (≤ 100 ms), e não quatro operações. Os testes `TestRedisDownIsReportedAndLooksLikeAnEmptyCache` e `TestAHungRedisCostsAtMostTheTimeout` prendem esse teto. Com as 5 tentativas de conexão padrão do cliente Redis, a mesma leitura levava ~430 ms (medido).

**Como fica no código:** a chave é montada por `internal/quotecache/key.go` e o Redis é acessado por `internal/quotecache/redis.go` (`MGET` para as três parceiras de uma vez; `SET` com `EX` de 24 h). Os prazos vêm de `CACHE_FRESH_TTL` e `CACHE_MAX_AGE`, o segredo do HMAC de `CACHE_KEY_SECRET`, e `REDIS_ADDR=off` desliga o cache. Sem segredo configurado, a API sorteia um a cada início: o cache continua isolado e ilegível, mas só vale enquanto o processo viver.

### 4.4 Fallback e contrato da resposta (D8, D9, D10)

#### Estratégia (D8)

**Opções.** (a) resposta parcial; (b) cotação anterior do cache; (c) recusa explícita.

**Escolha: (a) resposta parcial.** A corretora recebe as cotações das parceiras disponíveis e a lista explícita das que faltaram, com o motivo.

Na falha de uma parceira, a **política de cache** (§4.3) pode preencher o lugar dela com a cotação vencida, marcada com a idade. Isso não é uma segunda estratégia: é a política de vencimento do cache agindo dentro da resposta parcial. Quando **nada** está disponível (nenhuma parceira respondeu e não há cache), a resposta parcial ficaria vazia; esse é o único caso de recusa, com o **503** do RF-08.

**Defesa.** É a única opção que melhora as duas pontas medidas no "antes": a taxa de sucesso (60%, RNF-02) e o desperdício de consultas (4,36 por cotação entregue, RNF-03). As consultas feitas antes de uma falha deixam de ser jogadas fora.

**Descartadas.** (b) depende de cache: a primeira cotação de um risco nunca teria fallback. (c) troca o 502 genérico por um erro claro, mas mantém a taxa de sucesso e o desperdício como estão.

**Consequências ruins.** A proposta mais barata pode estar justamente na parceira que faltou, e a corretora pode vender a segunda mais barata sem saber. Por isso `missing_partners` é obrigatório na resposta degradada: a corretora decide se recota.

#### Onde vive no código (D9)

**Contexto.** O teste original `TestOnePartnerDownBringsDownTheWholeRequest` (`internal/quotation/service_test.go`) exige que `Service.Quote` devolva erro quando uma parceira falha, e a regra R9 proíbe quebrá-lo.

**Escolha.** `Service.Quote` fica como está. Um **novo método**, `Service.QuoteWithFallback` em `internal/quotation/service.go`, faz a agregação com cache e resposta parcial, e é ele que o handler passa a usar. Os testes originais continuam testando o comportamento original, que continua existindo.

O mesmo vale para o handler: o teste original `TestQuotesResponds502WithTheNameOfThePartnerThatWentDown` (`internal/quotation/handler_test.go`) monta a API com `NewAPI` e exige **502** quando uma parceira falha. Por isso `NewAPI` mantém o comportamento original, e uma **opção nova**, `WithFallback()` em `internal/quotation/handler.go`, liga o caminho com fallback. O `cmd/quotation-api/main.go` usa essa opção; os testes originais, não. Sem isso, o fallback existiria só no código e a corretora continuaria recebendo 502, que é exatamente o que o enunciado diz que "não conta".

**Descartadas.** Mudar `Service.Quote` e o teste viola R9. Um tipo novo em outro pacote afastaria o fallback do `service.go`, que é onde o enunciado o espera.

**Consequência ruim.** `Service.Quote` passa a ser usado só pelos testes. Isso é declarado no README do processo.

#### Contrato da resposta (D10)

**Escolha.** Campos **acrescentados** ao `Response` de `internal/quotation/request.go`, sem remover nenhum. Um cliente que ignore campos novos continua funcionando.

| Campo | Onde | Significado |
|---|---|---|
| `degraded` | resposta | `true` se alguma parceira faltou **ou** se alguma cotação é vencida |
| `missing_partners` | resposta | lista de `{partner, reason}`; `reason` ∈ `circuit_open`, `timeout`, `partner_error`; omitido quando nenhuma parceira faltou |
| `source` | cada cotação | `partner` (consultada agora) ou `cache` |
| `age_seconds` | cada cotação | idade desde a consulta original; 0 quando `source` é `partner` |
| `stale` | cada cotação | `true` se a cotação passou de 1 h e só foi servida por causa de uma falha |

Os campos que vêm da parceira (`partner`, `quote_id`, `premium_cents`, `currency`, `coverage_cents`, `valid_for_seconds`) são repassados **como a parceira os devolveu**, inclusive numa cotação de cache: a plataforma não reescreve dado de seguradora. Por isso uma cotação de cache com 50 min pode trazer `valid_for_seconds: 300`, que é a validade **técnica** que o mock declara. A validade **comercial** é de 24 h (R8, P4), e a idade real está em `age_seconds`.

Exemplo de resposta degradada, com a `partner-flaky` com circuito aberto e sem cotação no cache:

```json
{
  "tenant_id": "corretora-a",
  "degraded": true,
  "quotes": [
    {
      "partner": "partner-degrading",
      "quote_id": "partner-degrading-6c1e0b7a9f3d2e14",
      "premium_cents": 71230,
      "currency": "BRL",
      "coverage_cents": 5000000,
      "valid_for_seconds": 300,
      "source": "partner",
      "age_seconds": 0,
      "stale": false
    },
    {
      "partner": "partner-slow",
      "quote_id": "partner-slow-0a4f9c2b7e1d3f88",
      "premium_cents": 118400,
      "currency": "BRL",
      "coverage_cents": 6000000,
      "valid_for_seconds": 300,
      "source": "cache",
      "age_seconds": 742,
      "stale": false
    }
  ],
  "missing_partners": [
    { "partner": "partner-flaky", "reason": "circuit_open" }
  ],
  "elapsed_ms": 4
}
```

Os valores são ilustrativos. A ordenação por `premium_cents` crescente se mantém (RF-02).

**Rastreabilidade (R6).** Uma cotação servida do cache carrega o `quote_id` **original** da parceira e a sua idade: o registro de auditoria **[proposta]** consegue ligá-la à consulta que a originou. Cada cotação apresentada, de cache ou não, gera um registro (corretora, parceira, `quote_id`, prêmio, instante de apresentação, `source`, idade). A persistência desse registro está fora do escopo do PoC (enunciado, "Fora de escopo") e tem o custo calculado na §8.

### 4.5 O que fica observável

A instrumentação é o método de prova das decisões acima (enunciado, Entrega 2 §4). Os nomes abaixo são os que o código emite e os que aparecem no Prometheus, conferidos com o ambiente de pé; o Collector converte o nome OTel (pontos e unidade) para o formato do Prometheus.

**Métricas de negócio** (código em `internal/partner/metrics.go` e `internal/quotation/telemetry.go`):

| Para provar | Nome no código (OTel) | Nome no Prometheus | Tipo | Rótulos |
|---|---|---|---|---|
| §4.2: o estado de cada circuito no tempo | `partner.breaker.state` | `partner_breaker_state` | gauge: 0 fechado, 1 meio-aberto, 2 aberto | `partner` |
| §4.2: cada mudança de estado, com origem e destino | `partner.breaker.transitions` | `partner_breaker_transitions_total` | contador | `partner`, `from`, `to` |
| §4.3 e P1: o hit rate do cache | `quote.cache.lookups` | `quote_cache_lookups_total` | contador | `partner`, `result` ∈ `hit` (fresca servida, parceira não chamada), `miss` (nada fresco), `stale` (vencida servida na falha), `error` (Redis não respondeu) |
| §4.1: a latência de cada parceira, com significado de domínio | `partner.quote.duration` | `partner_quote_duration_seconds` | histograma (s) | `partner`, `outcome` ∈ `success`, `timeout`, `error`, `circuit_open` |

A latência por parceira é uma métrica **nova**, e não o reaproveitamento de `http_client_request_duration_seconds`, porque a de HTTP não enxerga o que não virou HTTP: com o circuito aberto não há chamada, e `outcome="circuit_open"` mostra essas requisições, que levam ~0 s. Os baldes do histograma acompanham os timeouts da §4.1 (0,5 s e 2 s) e o teto de 6 s da `partner-degrading`.

Consultas que usam essas métricas:

```text
# estado do circuito de cada parceira (o degrau fechado → aberto → meio-aberto → fechado)
partner_breaker_state

# hit rate do cache nos últimos 5 minutos
sum(rate(quote_cache_lookups_total{result="hit"}[5m])) / sum(rate(quote_cache_lookups_total[5m]))

# p95 de cada parceira, só das chamadas que saíram
histogram_quantile(0.95, sum by (partner, le) (rate(partner_quote_duration_seconds_bucket{outcome!="circuit_open"}[5m])))
```

**Marcações no trace** (código em `internal/quotation/telemetry.go`): cada parceira ganha um span `quote {parceira}`, filho do span `POST /quotes`. Quando a parceira é chamada, o `HTTP POST` dela é filho desse span; quando não é, o span fica sem filho e os atributos dizem por quê.

| Marcação | Atributo no span `quote {parceira}` | Como se vê no Jaeger |
|---|---|---|
| **Requisição curto-circuitada** pelo breaker (RNF-05) | `breaker.short_circuited=true`, `quote.missing_reason=circuit_open`, evento `circuit open: partner not called`, status de erro | span de ~0 ms **sem** `HTTP POST` embaixo |
| **Resposta servida de cache** (RF-06) | `quote.source=cache`, `cache.result=hit` (ou `stale`) | span de ~0 ms **sem** `HTTP POST` embaixo |
| Cotação consultada agora | `quote.source=partner`, `cache.result=miss` | span com o `HTTP POST` da parceira embaixo |

O span `POST /quotes` ainda recebe o resumo: `tenant.id`, `quote.degraded`, `quote.count`, `quote.from_cache` e `quote.missing_partners`.

**Nunca** entram em atributo de span ou de métrica: CPF, placa, `quote_id` (dado pessoal e cardinalidade sem teto). O `tenant_id` entra só no span da requisição, e **não** como rótulo de métrica: o escopo do breaker é por parceira, e centenas de corretoras multiplicariam as séries. Isso é preso pelo teste `TestNoPersonalDataInMetricsOrSpans` e foi conferido nos dados reais da sessão do "depois": zero ocorrências em 1041 traces e 409 séries ([evidencias/sem-dado-pessoal-depois.txt](evidencias/sem-dado-pessoal-depois.txt)).

### 4.6 Componentes da fatia implementada (C4 nível 3)

Dentro do contêiner `quotation-api`.

```mermaid
flowchart TB
  classDef component fill:#85bbf0,color:#000,stroke:#5d82a8
  classDef existing fill:#85bbf0,color:#000,stroke:#5d82a8
  classDef proposal fill:#fff,color:#333,stroke:#d4a017,stroke-dasharray: 5 5
  classDef container fill:#438dd5,color:#fff,stroke:#2e6295
  classDef external fill:#999,color:#fff,stroke:#6b6b6b

  corretor(["Corretora"])

  subgraph api["quotation-api (contêiner · Go 1.25)"]
    handler["API HTTP<br/><i>internal/quotation/handler.go</i><br/>valida X-Tenant-Id e corpo;<br/>escolhe 200, 400, 403 ou 503"]:::existing
    service["Agregador com fallback<br/><i>internal/quotation/service.go</i><br/>QuoteWithFallback:<br/>consulta as 3 em paralelo, monta resposta parcial,<br/>ordena por prêmio"]:::existing
    cache["Cache de cotações<br/><i>internal/quotecache/</i><br/>chave quote:v1:tenant:partner:hmac;<br/>fresca 1 h, vencida até 24 h"]:::component
    breaker["Circuit breaker por parceira<br/><i>internal/partner/breaker.go</i><br/>gobreaker v2 · ≥50% em ≥10 req,<br/>janela 30 s, 15 s aberto, 2 de teste"]:::component
    client["Cliente de parceira<br/><i>internal/partner/client.go</i><br/>timeout por parceira:<br/>2000 / 500 / 500 ms"]:::existing
    config["Configuração<br/><i>internal/platform/config.go</i><br/>timeouts, limiares, TTL,<br/>endereço do Redis, segredo do HMAC"]:::existing
    telemetry["Telemetria<br/><i>internal/platform/telemetry.go</i><br/>MeterProvider e TracerProvider globais"]:::existing
  end

  redis[("redis<br/><i>contêiner</i>")]:::container
  otel["otel-collector<br/><i>contêiner</i>"]:::container
  partners["partner-slow · partner-flaky ·<br/>partner-degrading<br/><i>sistemas externos</i>"]:::external

  corretor -- "POST /quotes<br/>HTTP/JSON · X-Tenant-Id" --> handler
  handler -- "tenant + Request normalizado<br/>chamada Go" --> service
  service -- "passo 1: lê as 3 cotações do risco<br/>chamada Go" --> cache
  cache -- "MGET / SET EX 86400<br/>RESP/TCP" --> redis
  service -- "passo 2: para cada parceira sem cotação fresca<br/>chamada Go" --> breaker
  breaker -- "só se fechado ou meio-aberto<br/>chamada Go" --> client
  client -- "POST /quotes com timeout<br/>HTTP/JSON" --> partners
  service -- "passo 3: grava a resposta boa<br/>chamada Go" --> cache
  config -. "parâmetros na inicialização" .-> service
  config -. "parâmetros na inicialização" .-> breaker
  config -. "parâmetros na inicialização" .-> client
  config -. "parâmetros na inicialização" .-> cache
  breaker -- "transições de estado<br/>OTel metrics" --> telemetry
  cache -- "hit / miss / vencida<br/>OTel metrics" --> telemetry
  service -- "spans com source e curto-circuito<br/>OTel traces" --> telemetry
  telemetry -- "OTLP/gRPC :4317" --> otel
```

**Fluxo por parceira, dentro do agregador:**

1. Há cotação **fresca** no cache? → usa (`source: cache`). A parceira **não** é chamada.
2. O circuito da parceira está **aberto**? → não chama. Há cotação **vencida** (até 24 h)? → usa, com `stale: true`. Senão → `missing_partners` com `circuit_open`.
3. Chama a parceira pelo breaker, com o timeout dela. Sucesso → usa (`source: partner`) e grava no cache.
4. Falhou (timeout ou erro)? → há cotação vencida? → usa, com `stale: true`. Senão → `missing_partners` com `timeout` ou `partner_error`.

As três parceiras seguem esse fluxo **ao mesmo tempo** (§4.8, D16). Depois das três: se há ao menos uma cotação, responde **200** ordenado por prêmio (com `degraded` quando faltou algo); se não há nenhuma, **503** (RF-08).

### 4.7 Limites conhecidos

O que esta arquitetura **não** resolve. Nenhum destes é para implementar; declarar a fronteira faz parte da arquitetura.

| # | Limite | Gatilho que o expõe | Efeito na corretora |
|---|---|---|---|
| **L1** | **Sem limite de chamadas simultâneas por parceira.** O timeout corta a espera, mas não impede que a API abra muitas chamadas ao mesmo tempo. | Cache frio com carga alta e riscos variados: depois de uma perda do Redis (cenário (a) da §7), ou em horário de pico com cotações novas, dezenas de requisições chegam juntas, todas são *miss* e vão às parceiras ao mesmo tempo. A `partner-degrading` passa de 7 simultâneas, estoura o timeout e o circuito dela abre. Na carga padrão do `make reproduce` isso quase não aparece, porque as 10 requisições do baseline já gravam as 5 cotações distintas antes da rajada de 50. | Logo depois da perda do cache, a `partner-degrading` falta em várias cotações (`missing_partners: timeout`, depois `circuit_open`), até o cache aquecer e o breaker fechar. |
| **L2** | **O breaker vive na memória do processo** e não atravessa réplicas. | Mais de uma réplica da `quotation-api`, ou um reinício do processo. | Com N réplicas, cada uma descobre sozinha que a parceira caiu: até N × 10 chamadas pagas antes de todas abrirem. Depois de um reinício, o circuito volta fechado e a parceira ruim é chamada de novo. **[proposta]** A própria biblioteca traz uma variante com estado compartilhado (`distributed_gobreaker.go` no gobreaker v2.4.0), que pode guardar o estado no mesmo Redis quando houver mais de uma réplica. |
| **L3** | **O TTL é a única forma automática de invalidar o cache.** | A seguradora muda o preço, ou uma corretora renegocia as condições comerciais, no meio da hora. | A corretora vê o preço antigo por até 1 h (fresca) e, durante uma falha da parceira, por até 24 h (vencida, mas marcada). A invalidação por corretora fica como proposta (§4.3). |
| **L4** | **Uma corretora pode consumir a capacidade das outras.** O escopo do breaker é por parceira, e não há cota por corretora. | Uma corretora com volume alto, ou com pedidos que a parceira rejeita, gera falhas e lentidão que abrem o circuito da parceira. | **Todas** as corretoras perdem aquela parceira por 15 s a cada abertura, por causa de uma só. |

Um quinto limite apareceu medindo (D14):

| # | Limite | Gatilho que o expõe | Efeito na corretora |
|---|---|---|---|
| **L5** | **Com pouco tráfego, o breaker quase não abre.** A janela de 30 s precisa de no mínimo 10 chamadas, e a proporção de falha só é avaliada dentro dela. | Horário de pouco movimento: na 2D, uma cotação a cada ~3 s juntou ~10 chamadas por janela, e a `partner-flaky` ficou abaixo de 50% de falha (15 em 40); o circuito não abriu ([evidencias/variacao-curto-circuito-rapido.txt](evidencias/variacao-curto-circuito-rapido.txt)). Em produção (P3) cada janela junta ~100 chamadas. | Nesses horários, uma parceira ruim continua sendo chamada: a cotação sai com ela em `missing_partners` (`partner_error` ou `timeout`) e a consulta é paga, em vez de curto-circuitada. A janela deslizante (`BucketPeriod` do gobreaker v2.4.0) é a alternativa estudada e não adotada. |

A agregação paralela (§4.8) **agrava o L1**: em série, a `partner-degrading` só recebia a chamada depois da `partner-slow` (~1,7 s depois), o que espalhava as chamadas no tempo; em paralelo, ela recebe a chamada no mesmo instante em que a requisição chega, e a concorrência nela passa a ser a da própria entrada.

Mais um ponto que vale citar, e que não é código desta entrega (enunciado, "Fora de escopo"): duas requisições concorrentes da mesma corretora para o mesmo risco, uma servida de cache e outra da parceira, podem devolver **dois prêmios diferentes para o mesmo risco no mesmo minuto**, se a parceira tiver mudado o preço desde a gravação. Os dois são preços reais e rastreáveis; nenhum é inventado.

### 4.8 Agregação paralela (D16, bônus)

**Contexto.** Em série, a cotação leva a **soma** das três parceiras: no "antes", 1701 + 201 + 6149 ms no trace mais lento ([evidencias/antes.md](evidencias/antes.md), evidência 3). Com cache e breaker, a soma só pesa quando há *miss*: a cotação nova espera a `partner-slow` (~1,7 s) e depois as outras duas. O teste original `TestQuoteCallsThePartnersSerially` exige que `Service.Quote` continue em série (R9). O enunciado deixa a paralelização como bônus e pede que, se implementada, seja tratada como decisão, porque muda o p95.

**Opções.** (a) manter em série; (b) consultar as três ao mesmo tempo, no caminho com fallback; (c) consultar em paralelo com um teto de tempo para a cotação inteira, devolvendo o que chegou até ali.

**Escolha: (b).** `Service.QuoteWithFallback` dispara as três parceiras ao mesmo tempo, espera as três e monta a resposta **na ordem das parceiras**, para que ela não dependa de quem terminou primeiro. `Service.Quote` continua em série. A leitura do cache continua sendo um único `MGET`, antes das chamadas.

**Defesa.** A cotação passa a levar o tempo da parceira **mais lenta**, e não a soma. O teto estrutural cai de 3,0 s para 2,0 s (o maior timeout da §4.1), e os alvos da RNF-01 e da RNF-04 foram revistos com ele. Medido com a carga padrão ([evidencias/depois.md](evidencias/depois.md)): o p95 do baseline, em que as 5 primeiras cotações vão às parceiras, cai de 2,04 s (em série, [evidencias/reproduce-depois-serie.txt](evidencias/reproduce-depois-serie.txt)) para **1,68 s**; no trace de uma cotação nova, as três chamadas começam no mesmo instante e a cotação leva 1,63 s, o tempo da `partner-slow` ([evidencias/trace-depois-paralelo.json](evidencias/trace-depois-paralelo.json)). O número de consultas compradas não muda: 57 para 210 cotações nas duas execuções.

**Descartadas.** (a) mantém a soma, que é exatamente o que o "antes" mostrou como causa da latência. (c) acrescenta um terceiro prazo além dos timeouts e da janela do breaker: os timeouts por parceira já limitam a cotação a 2,0 s, e um teto menor descartaria respostas que a parceira ainda daria dentro do prazo dela, pagas e jogadas fora.

**Consequências ruins.** A `partner-degrading` deixa de receber as chamadas espaçadas pela `partner-slow` e passa a recebê-las no ritmo da entrada, o que agrava o L1 (§4.7). Cada requisição passa a abrir três chamadas ao mesmo tempo, em vez de uma por vez. O código tem concorrência: o teste `TestTheFallbackPathQuotesThePartnersAtTheSameTime` prova que as três ficam em voo juntas, `TestTheParallelResponseIsTheSameWhoeverFinishesFirst` prova que a resposta não depende da ordem de chegada, e a suíte passa no detector de corrida (`go test -race`, [evidencias/make-test.txt](evidencias/make-test.txt)).

---

## 5. Implementação

### 5.1 Como se constrói: de cada mecanismo ao arquivo

| Mecanismo (§4) | Arquivo | Biblioteca | Configuração | Teste que o prende |
|---|---|---|---|---|
| Timeout por parceira (D1) | `internal/partner/client.go` | `net/http` | `PARTNER_TIMEOUTS`, `PARTNER_TIMEOUT_DEFAULT` | `internal/partner/timeout_test.go` |
| Circuit breaker por parceira (D2 a D4) | `internal/partner/breaker.go` | `github.com/sony/gobreaker/v2` v2.4.0 | `BREAKER_MIN_REQUESTS`, `BREAKER_FAILURE_RATIO`, `BREAKER_INTERVAL`, `BREAKER_OPEN_TIMEOUT`, `BREAKER_HALF_OPEN_REQUESTS` | `internal/partner/breaker_test.go` |
| Chave do cache (D7) | `internal/quotecache/key.go` | `crypto/hmac` | `CACHE_KEY_SECRET` | `internal/quotecache/quotecache_test.go` |
| Cache em Redis (D5, D6) | `internal/quotecache/redis.go` | `github.com/redis/go-redis/v9` v9.22.0 | `REDIS_ADDR`, `REDIS_TIMEOUT`, `CACHE_FRESH_TTL`, `CACHE_MAX_AGE` | `internal/quotecache/quotecache_test.go` (com `github.com/alicebob/miniredis/v2`) |
| Resposta parcial e contrato (D8 a D10) | `internal/quotation/service.go`, `internal/quotation/request.go`, `internal/quotation/handler.go` | — | — | `internal/quotation/fallback_test.go` |
| Agregação paralela (D16) | `internal/quotation/service.go` | `sync` | — | `internal/quotation/parallel_test.go` |
| Métricas e marcações de trace (§4.5) | `internal/partner/metrics.go`, `internal/quotation/telemetry.go` | OpenTelemetry (SDK do starter) | — | `internal/partner/metrics_test.go`, `internal/quotation/telemetry_test.go` |
| Valores padrão e validação | `internal/platform/config.go` | — | todas as acima | `internal/platform/config_resilience_test.go` |
| Ligação de tudo | `cmd/quotation-api/main.go`, `docker-compose.yml` | — | — | `make reproduce` ([evidencias/depois.md](evidencias/depois.md)) |

**Por que esta biblioteca de breaker:** expõe os três estados como constantes e o callback de transição que vira métrica sem código extra (§4.2, D2). **Por que este cliente Redis:** é o cliente oficial do Redis para Go e permite limitar cada operação pelo contexto, o que garantiu o teto de 100 ms (§4.3).

**Como se testa sem depender de sorte**, no padrão do `cmd/partner-mock/feasibility_test.go`: o teste do breaker usa as 60 primeiras respostas **medidas** da `partner-flaky` (smoke §2.2) e prova que o circuito abre na requisição 12 e que, aberto, a parceira não é chamada; o do cache usa o relógio do miniredis para avançar 24 h; os do fallback usam relógio fixo e cache em memória. Nenhum usa `sleep`. A suíte inteira passa, inclusive no detector de corrida: 102 testes, 64 originais e 38 novos ([evidencias/make-test.txt](evidencias/make-test.txt)).

**O que fica como proposta, sem código:** o acervo de auditoria (§4.4, fora de escopo pelo enunciado), apagar o cache por corretora (§4.3), o breaker com estado compartilhado entre réplicas (L2) e a cópia da auditoria fora da região (§7).

### 5.2 Onde roda (D11)

**Contexto.** O enunciado diz que "retenção, residência e segregação, não preferência" decidem onde a plataforma roda. A Prumo é operadora de dado pessoal (R5); a auditoria tem 5 anos sem regravação (R6); e o volume a guardar é pequeno: ~2,6 GB por mês de auditoria (§8).

**Opções.** (a) nuvem pública na região de São Paulo; (b) on-premise; (c) híbrido, com a auditoria on-premise e o processamento na nuvem.

**Escolha: (a) nuvem pública na região de São Paulo** (AWS `sa-east-1`), com:
- a `quotation-api` em contêiner, em mais de uma zona de disponibilidade;
- o cache em ElastiCache (Valkey, compatível com Redis), com 2 nós;
- **[proposta]** a auditoria num bucket S3 com **Object Lock em modo *compliance*** e retenção de 5 anos: nesse modo, ninguém, nem o administrador da conta, apaga ou regrava um objeto antes do prazo; o prefixo é a corretora, para segregar e apagar por corretora quando o prazo vencer.

**Defesa.** O dado pessoal fica no Brasil, sem transferência internacional (residência). O bloqueio de escrita atende ao "não regravável" da SUSEP sem hardware próprio (retenção). O prefixo por corretora e as permissões por prefixo separam as corretoras (segregação). O custo é por uso: Redis e auditoria somam ~R$ 365 por mês (§8).

**Descartadas.** (b) on-premise exige investimento em servidores, armazenamento com bloqueio de escrita e datacenter para guardar ~160 GB em 5 anos, além de capacidade fixa para um tráfego que oscila. (c) híbrido põe dois ambientes para operar e um link no caminho da auditoria, sem entregar nada que o bloqueio de escrita da opção (a) não entregue.

**Consequências ruins.** O preço é em dólar: o S3 Standard em São Paulo custa US$ 0,0405 por GB-mês, ~76% acima dos US$ 0,023 da região da Virgínia. Só existe uma região da AWS no Brasil: perder a região inteira exige um plano fora dela, que é o cenário (c) da §7.

---

## 6. Operação e gestão de mudanças

### 6.1 O que se olha, e quem olha

Cada métrica e atributo criados na §4.5 tem um dono. O que não tem dono não foi criado.

| Sinal | Quem olha | Para quê |
|---|---|---|
| `partner_breaker_state` e `partner_breaker_transitions_total` | plantonista | saber qual parceira está fora e há quanto tempo (alerta A1, runbook R1) |
| `partner_quote_duration_seconds` por `outcome` | plantonista; gestão de parceiros | ver a parceira se aproximando do timeout antes de o circuito abrir (A2); levar à seguradora o número de quando ela degrada |
| `quote_cache_lookups_total{result="error"}` | plantonista | Redis fora (A3) |
| `quote_cache_lookups_total` (hit rate) | dono do produto; CTO | conferir o pressuposto P1 em produção (A4) e a economia da §8 |
| span `quote {parceira}` com `breaker.short_circuited` e `quote.source` | plantonista e suporte | explicar à corretora por que uma cotação veio sem uma parceira, ou de cache |
| `tenant.id` no span `POST /quotes` | suporte | encontrar no Jaeger as cotações de uma corretora que reclamou |

### 6.2 Alertas

| # | Alerta | Métrica e limiar | Ação |
|---|---|---|---|
| **A1** | Parceira fora de "fechado" por 10 minutos | `min_over_time(partner_breaker_state[10m]) >= 1` | Runbook R1 |
| **A2** | Parceira encostando no timeout | `histogram_quantile(0.95, sum by (partner, le) (rate(partner_quote_duration_seconds_bucket{outcome="success"}[10m])))` acima de 80% do timeout dela (1,6 s; 0,4 s; 0,4 s) por 10 minutos | Avisar a gestão de parceiros; se persistir, rever o timeout dela (§4.1), mudando o SAD junto |
| **A3** | Redis fora | `sum(rate(quote_cache_lookups_total{result="error"}[5m])) > 0` por 5 minutos | Restaurar o Redis (cenário (a) da §7). As cotações continuam; o custo sobe |
| **A4** | Hit rate abaixo do pressuposto | `sum(rate(quote_cache_lookups_total{result="hit"}[1h])) / sum(rate(quote_cache_lookups_total[1h])) < 0.20` por 1 dia | Não é incidente: é o P1 sendo desmentido. O dono do produto revê o TTL (§6.4) e a §8 |

**Por que o A1 olha 10 minutos fora de "fechado", e não cada abertura:** abrir e fechar é o breaker funcionando. Na 2D, a `partner-flaky` oscilou 10 vezes entre meio-aberto e aberto em ~5 minutos antes de fechar ([evidencias/breaker-transicoes-depois.txt](evidencias/breaker-transicoes-depois.txt)); um alerta por abertura acordaria o plantonista dez vezes por nada.

### 6.3 Runbook R1: "o circuito da `partner-degrading` está fora de fechado há 10 minutos"

1. **Confirmar o sintoma.** No Prometheus, `partner_breaker_state{partner="partner-degrading"}`: o valor fica em 2 (aberto) ou alterna 1 e 2 (meio-aberto falhando). Em `partner_quote_duration_seconds_count{partner="partner-degrading"}`, o `outcome` que cresce diz o motivo: `timeout` (ela está lenta) ou `error` (ela está respondendo erro).
2. **Medir o efeito na corretora.** No Jaeger, a tag `quote.missing_reason=circuit_open` mostra as cotações sem a `partner-degrading`. Elas saem com **200**, `degraded: true` e a parceira em `missing_partners`, ou com a cotação de cache marcada como `stale`. **Nenhuma corretora está sem cotação** enquanto as outras duas parceiras estiverem fechadas.
3. **Verificar se a causa é nossa.** Se o `outcome` é `timeout`, olhar a concorrência: a `partner-degrading` afunda acima de 5 chamadas simultâneas. Um pico de tráfego nosso (por exemplo, cache frio depois de perder o Redis, L1) causa exatamente isso. Se `quote_cache_lookups_total{result="error"}` está subindo, o problema é o Redis: ir para o cenário (a) da §7.
4. **O que fazer.** Nada no breaker: ele está protegendo a parceira e a corretora. Se a causa é nossa (Redis), restaurar o Redis. Se é da parceira, abrir chamado com a seguradora, com o horário de início e o `outcome`.
5. **O que NÃO fazer.**
   - Não aumentar o timeout da `partner-degrading` em produção para "fazer passar": isso aumenta as chamadas em voo e a afunda mais (§4.1), e muda um número defendido no SAD sem revisão.
   - Não desligar o breaker nem reiniciar a API para "fechar" o circuito: o reinício zera o estado (L2) e manda tráfego cheio para uma parceira que está caindo.
   - Não apagar o cache: as cotações vencidas são o que mantém a parceira na resposta.
6. **Quando escalar.** Para a gestão de parceiros, se passar de **30 minutos**. Para o CTO e o dono do produto, se **duas parceiras** ficarem fora ao mesmo tempo (as cotações passam a ter uma proposta só) ou se o circuito da **`partner-slow`** abrir, porque ela nunca falha no perfil normal (P2) e a abertura indica mudança na parceira ou no nosso timeout.
7. **Encerrar.** O incidente fecha quando `partner_breaker_state` volta a 0 e fica assim por 10 minutos; registrar o horário de início e de fim, que alimentam o custo do modo degradado (§7).

### 6.4 Mudança do TTL (D15)

O TTL é parâmetro de **negócio**: mexe no risco de preço vencido e no custo.

| Etapa | Como |
|---|---|
| Quem pede | O dono do produto, com o alerta A4 ou com reclamação de preço não honrado |
| Quem aprova | Produto (risco de preço) **e** encarregado de dados (prazo de guarda). O teto de 24 h é contratual (R8) e a própria configuração recusa um valor maior (`internal/platform/config.go`) |
| Como chega em produção | Pull request no repositório de configuração alterando `CACHE_FRESH_TTL`, com a §4.3 do SAD alterada no mesmo pull request |
| Como se reverte | Revertendo o pull request; o valor anterior volta no deploy seguinte |
| Como se mede se melhorou | Hit rate (`quote_cache_lookups_total`), consultas por cotação entregue (RNF-03) e reclamações de preço não honrado, uma semana antes contra uma semana depois |

O mesmo caminho vale para os limiares do breaker e os timeouts: são números defendidos na §4, e mudar o número sem mudar o SAD é exatamente a divergência entre documento e código que o projeto evita.

---

## 7. Recuperação de desastres

### 7.1 RTO e RPO por classe de dado (D12)

| Classe de dado | RPO (quanto se pode perder) | RTO (em quanto tempo volta) | Por quê |
|---|---|---|---|
| Cache de cotações (Redis) | **tudo** | **0** para voltar a cotar; 15 min para voltar a ter cache | O cache é otimização: sem ele a API cota normalmente (`TestRedisDownDoesNotStopTheQuotation`). Perdê-lo custa dinheiro, não cotação |
| Estado do breaker | **tudo** | 0 | Vive na memória (L2); cada processo reaprende em ~12 chamadas |
| Registro de auditoria **[proposta]** | **≤ 1 min** | 4 h | Perder é infração regulatória (R6). Gravação em lote a cada minuto limita a perda; a cópia fora da região cobre a perda do site |
| Código e configuração | 0 | 1 h | Tudo versionado no Git; reconstruir é aplicar de novo |
| Telemetria | 1 h | 4 h | Serve para operar, não para cotar |

### 7.2 Cenário (a): o Redis inteiro se perde

- **O que a corretora vê:** nada no contrato. As cotações continuam saindo **200**, só que todas consultadas agora (`source: partner`), mais lentas (~1,7 s, o tempo da `partner-slow`, já que as três são consultadas ao mesmo tempo) e sem a cotação vencida para cobrir uma parceira em falha.
- **O que degrada:** o custo e a latência. **O que para:** nada.
- **Custo do modo degradado:** o hit rate cai do P1 (40%) para 0%, e a conta de parceiro volta de R$ 190.080 para R$ 316.800 por mês (§8): **R$ 126.720 por mês**, ou **~R$ 576 por hora** de horário comercial (22 dias × 10 h).
- **Qual efeito chega primeiro.** O custo chega na primeira hora. O colapso da `partner-degrading` exige mais de 5 chamadas simultâneas nela. Com o P3 (~3,3 cotações por segundo) e ~0,14 s por chamada em repouso, a concorrência nela vai de ~0,3 (com 40% de acerto) para ~0,5 chamada em voo sem cache: o volume sobe 1,67×, mas fica longe de 5. O colapso só vem em pico, ou na rajada de cache frio de riscos novos (L1): aí o timeout de 500 ms corta a espera, o circuito dela abre e as cotações saem sem ela até o cache aquecer.
- **Caminho de volta:** subir um nó novo (o ElastiCache troca o nó primário pela réplica sozinho); o cache se reconstrói com o próprio tráfego em ~1 h (o TTL fresco). Alerta A3 detecta.

### 7.3 Cenário (b): uma parceira fica fora por seis horas

- **O que a corretora vê:** cotações **200** com `degraded: true` e a parceira em `missing_partners` (`timeout`, `partner_error` e, depois que o circuito abre, `circuit_open`), ou com a cotação vencida dela até 24 h, marcada com `stale` e a idade.
- **O que degrada:** a cotação tem 2 propostas em vez de 3, e a mais barata pode estar na parceira fora. **O que para:** nada.
- **Custo do modo degradado:** em 6 h de horário comercial passam ~72.000 cotações (12.000 por hora). Com o circuito aberto, a parceira não é consultada: deixam de ser compradas ~43.200 consultas (com o P1), **R$ 1.728 a menos**. A receita, cobrada por cotação entregue, se mantém. O custo real é o de conversão, se a proposta que faltou era a que fecharia a venda: não há dado para quantificar, e está declarado como tal.
- **Comparação com o "antes":** sem fallback, uma parceira fora derruba a cotação inteira. As mesmas 72.000 cotações virariam 502: **R$ 18.000 de receita perdida** (a R$ 0,25), mais as consultas já compradas às outras parceiras e jogadas fora.
- **Caminho de volta:** automático. O breaker testa a parceira a cada 15 s (meio-aberto) e fecha com 2 sucessos seguidos. Alerta A1 e runbook R1 acompanham.

### 7.4 Cenário (c): perda do site ou da região

- **Perda de uma zona de disponibilidade:** coberta pela própria arquitetura da §5.2 (API em mais de uma zona, Redis com 2 nós). A corretora não vê; os circuitos de cada processo reaprendem (L2).
- **Perda da região inteira:** a plataforma **para**. Na data deste documento, o provedor escolhido tem uma única região no Brasil.
  - **O que a corretora vê:** nenhuma cotação.
  - **Custo:** **R$ 3.000 por hora** de horário comercial em receita (12.000 cotações a R$ 0,25), ou R$ 30.000 por dia útil.
  - **Caminho de volta [proposta]:** a auditoria é copiada continuamente para um bucket com bloqueio de escrita em outro provedor com região em São Paulo, o que mantém o RPO de auditoria em ≤ 1 min sem tirar o dado do Brasil; a infraestrutura é descrita como código, para ser recriada lá. RTO de 24 h para a plataforma voltar a cotar, com o cache vazio (cenário (a)) e os circuitos fechados.

---

## 8. Tecnologias, custos e pessoal

Todos os preços são oficiais e foram consultados em 2026-10-26: AWS Price List API para `sa-east-1` (S3 publicado em 2026-10-15; ElastiCache em 2026-10-14) e câmbio de **R$ 5,0403 por US$** (média da PTAX de venda de 13/10 a 23/10/2026, série SGS 1 do Banco Central). As fontes e os pressupostos P5 a P7 estão na §1.5 e em [estudo-decisoes-1b.md](estudo-decisoes-1b.md).

### 8.1 Tecnologias

Go 1.25; `github.com/sony/gobreaker/v2` v2.4.0; `github.com/redis/go-redis/v9` v9.22.0; Valkey ou Redis 7 (o PoC usa `redis:7-alpine`, do compose; em produção, ElastiCache Valkey, compatível com o mesmo protocolo); OpenTelemetry, Collector, Prometheus e Jaeger, já do starter; **[proposta]** S3 com Object Lock para a auditoria.

### 8.2 Conta de parceiro, por hit rate

R$ 0,04 por consulta, 3 consultas por cotação, 120.000 cotações por dia útil, 22 dias úteis, receita de R$ 660.000 por mês (enunciado).

| Cenário | Hit rate | Consultas por mês | Custo por mês | Economia contra hoje | % da receita |
|---|---|---|---|---|---|
| Hoje, sem cache | 0% | 7.920.000 | R$ 316.800 | — | 48,0% |
| Recotação baixa | 20% | 6.336.000 | R$ 253.440 | R$ 63.360 | 38,4% |
| **P1: 40% de recotação em 1 h** | 40% | 4.752.000 | R$ 190.080 | **R$ 126.720** | 28,8% |

**Coerência com as §1 e §4.** O hit rate da linha principal é o **P1** (40% de recotação na mesma corretora em até 1 h), sustentado pelo TTL fresco de **1 h** da §4.3: uma recotação dentro da hora acerta; depois dela, não. A linha de 20% é a sensibilidade para metade do pressuposto. Não há linha acima de 40%: o TTL de 1 h não sustenta mais do que a recotação dentro da hora, e a recotação em outra corretora não acerta, porque a chave tem a corretora. **O hit rate medido nas evidências (91%) não entra aqui:** é propriedade de uma carga que repete 5 cotações.

**Fica de fora, a favor da solução:** a economia do tudo-ou-nada. No "antes" medido, foram 4,36 consultas por cotação entregue, contra 3 no ideal ([evidencias/consultas-antes.txt](evidencias/consultas-antes.txt)), porque as consultas feitas antes de uma falha eram jogadas fora. Esta conta usa 3 por cotação, como o enunciado.

### 8.3 Infraestrutura acrescentada

| Item | Dimensionamento | Custo |
|---|---|---|
| Redis (ElastiCache Valkey `cache.t4g.small`, 1,37 GiB, US$ 0,0488/h, 2 nós) | ~216 mil entradas vivas (216.000 gravações por dia com o P1, guardadas por 24 h) × ~1 KB ≈ 216 MB | US$ 71,25 = **R$ 359 por mês** (com Redis OSS no lugar do Valkey, R$ 449) |
| Auditoria **[proposta]**, armazenamento | 2,64 milhões de cotações apresentadas por mês × 1 KB (P6) = 2,64 GB por mês | ver a tabela abaixo |
| Auditoria, gravação | lote por corretora por hora: 300 corretoras (P7) × 10 h × 22 dias = 66.000 gravações por mês, a US$ 0,007 por 1.000 | **R$ 2,33 por mês** |
| `quotation-api`, Collector, Prometheus, Jaeger | já existem no "antes"; a instrumentação nova acrescenta 4 métricas com rótulos de baixa cardinalidade (partner, result, outcome, from, to) | sem mudança relevante |

**Auditoria por cotação apresentada, ano 1 e ano 5:**

| | Acervo no fim do ano | Custo de armazenamento no ano (S3 Standard, US$ 0,0405/GB-mês) | Mesmo acervo em Glacier Instant Retrieval (US$ 0,0083/GB-mês) |
|---|---|---|---|
| **Ano 1** | 31,7 GB | **R$ 42** | R$ 9 |
| **Ano 5** | 158,4 GB (os cinco anos guardados juntos) | **R$ 352** | R$ 72 |

A unidade é a **cotação apresentada**, não a consulta comprada: o cache derruba a consulta, mas a cotação servida de cache continua sendo apresentada a um consumidor e continua precisando de registro (R6). Quem dimensionasse pelo que sobrou de consultas compradas erraria para baixo em 1 ÷ (1 − 0,40) = **1,67×** com o P1, e o erro em reais seria maior no ano 5, com os cinco anos guardados ao mesmo tempo.

### 8.4 Pessoal

Custo da empresa por pessoa sênior: R$ 248.264 por ano de remuneração (mediana do Levels.fyi para engenheiro sênior no Brasil, 1.257 salários, atualizado em 2026-10-26) × 1,7 de encargos e benefícios (P5) = **R$ 35.171 por mês**.

| Fase | Quem | Esforço | Custo |
|---|---|---|---|
| Construir | 1 desenvolvedor sênior (mecanismos, testes, instrumentação) e 1 SRE (ElastiCache, bucket com bloqueio de escrita, alertas) | 1 mês + 0,5 mês | **R$ 52.756**, uma vez |
| Operar | SRE do time de plataforma, em fração (alertas, runbook, revisão mensal de hit rate e custo com o produto) | 0,2 pessoa | **R$ 7.034 por mês** |

### 8.5 Veredito

| | Hit rate de 20% | P1 (40%) |
|---|---|---|
| Economia de consultas por mês | R$ 63.360 | R$ 126.720 |
| Custo mensal da solução (operação R$ 7.034 + Redis R$ 359 + auditoria ~R$ 5) | R$ 7.398 | R$ 7.398 |
| **Economia líquida por mês** | **R$ 55.962** | **R$ 119.322** |
| **Payback da construção (R$ 52.756)** | **0,94 mês** | **0,44 mês** |

**O cache se paga, e em menos de um mês nos dois cenários.** O ponto de equilíbrio é um hit rate de **2,3%** (R$ 7.398 ÷ R$ 316.800): mesmo que o P1 esteja muito errado, a decisão se sustenta. Se o P5 for 2,0× em vez de 1,7×, a construção sobe para R$ 62.066 e a operação para R$ 8.275 por mês, e o payback passa a **1,13 mês** com 20% e **0,53 mês** com o P1: o veredito não muda. O que o número **não** mostra, e fica a favor da solução, é a receita que deixa de se perder quando uma parceira cai: no cenário (b) da §7, R$ 18.000 em 6 horas.
