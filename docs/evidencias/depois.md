# Evidências do "depois"

Medições feitas em **duas sessões**, com timeout, circuit breaker, cache e resposta parcial ligados. O "antes" está em [antes.md](antes.md).

- **Sessão 1, agregação em série** (2026-10-26, de 22:38 a 23:09 UTC, commit `9689381`): o código antes do bônus.
- **Sessão 2, agregação paralela** (2026-10-27, de 00:09 a 00:12 UTC, commit seguinte a `60f85cf`): o código entregue, com o bônus (SAD §4.8, D16).

As evidências 1, 2, 4 e 7 e as medições complementares foram **refeitas na sessão 2**, porque a paralelização muda a latência. As evidências 3, 5 e 6 são da **sessão 1** e continuam valendo: o breaker e o cache são os mesmos nas duas, e a paralelização só muda a ordem em que as parceiras são chamadas. As versões da sessão 1 das evidências refeitas ficam como complemento, com o sufixo `-serie`.

Cada evidência tem **comando ou consulta + artefato + legenda**. Os gráficos foram capturados com o ambiente de pé, porque `make down` apaga o Jaeger e o Prometheus.

## Como as sessões foram montadas

| Etapa | Horário (UTC) | O que rodou | Para quê |
|---|---|---|---|
| **Sessão 2:** execução padrão | 2026-10-27, 00:09:17 a 00:09:26 | `make down && make reproduce` (10 de baseline, 200 com 50 em voo) | evidências 1, 4, 7, a conta de consultas e o trace da paralelização; **é a mesma carga do "antes"** |
| Sessão 1: execução padrão | 2026-10-26, 22:38:26 a 22:38:37 | `make down && make reproduce` | as versões `-serie` das evidências 1, 4, 7 e da conta de consultas |
| Sessão 1: variação declarada 1 | 22:39:01 a 22:39:17 | `make load ARGS="-distinct 200"` | complemento da evidência 3 |
| Sessão 1: variação declarada 2 | 22:40:04 a 22:43:42 | uma cotação nova a cada 8 s, até os dois circuitos fecharem | completar o degrau da evidência 5 |
| Sessão 1: variação declarada 3 | 22:45:43 a 22:46:34 | `make load ARGS="-tenant corretora-b -distinct 50 -concurrency 2 -requests 600 -baseline 0"` | evidência 6 |
| Sessão 1: variação declarada 4 | 23:05:42 a 23:08:18 | 20 cotações novas em paralelo e, com os circuitos abertos, as mesmas 20 de novo | evidência 3 |

**Por que as variações** (o enunciado permite variação declarada para explorar o comportamento, desde que a evidência principal venha dos defaults):

- **Variação 1:** a carga padrão repete só 5 cotações. Elas já estão no cache quando o circuito da `partner-flaky` abre, então nenhuma requisição chega a ser curto-circuitada. Com 200 cotações distintas, as requisições chegam às parceiras e o curto-circuito aparece.
- **Variação 2:** o meio-aberto só fecha depois de 2 requisições de teste com sucesso (`BREAKER_HALF_OPEN_REQUESTS=2`), e isso exige tráfego depois que a carga acaba.
- **Variação 3:** a carga padrão dura 11 s, e as métricas são exportadas a cada 10 s. O contador de *hit* nasce já com 205 entre duas amostras, e o Prometheus não vê a subida. Com a `corretora-b` (cache frio, porque a chave tem a corretora) e um ritmo mais lento, o aquecimento dura cerca de um minuto.
- **Variação 4:** na variação 1 a parceira curto-circuitada não é chamada, mas a requisição ainda espera a `partner-slow`, que é chamada (1,7 s no total). Para mostrar uma requisição curto-circuitada que **responde rápido**, como pede o enunciado, as cotações precisam ter o resto no cache. A primeira tentativa, uma cotação a cada ~3 s, **não abriu o circuito**: cada janela de 30 s juntou ~10 chamadas à `partner-flaky`, e a taxa de falha ficou abaixo de 50% (15 em 40). A segunda mandou 20 cotações em paralelo, a `partner-degrading` passou de 6 simultâneas e estourou o timeout, e os dois circuitos abriram; as mesmas 20, reenviadas em seguida, responderam em ~4 ms. O registro das duas tentativas está em [variacao-curto-circuito-rapido.txt](variacao-curto-circuito-rapido.txt).

Nenhuma variação mexe nos perfis das parceiras: o `docker-compose.yml` das parceiras é o original.

---

## 1. Relatório antes/depois

- **Comando:**
  ```bash
  make down && make reproduce
  ```
- **Artefato:** [reproduce-depois.txt](reproduce-depois.txt) (saída completa). O "antes" é [reproduce-antes.txt](reproduce-antes.txt).
- **Legenda:** com a mesma carga, o p95 sob carga cai de 8,01 s para 202 ms, o sucesso sobe de 60% para 100% e a vazão de 8,0 para 730 req/s.

| | Antes: baseline | Depois: baseline | Antes: carga | Depois: carga |
|---|---|---|---|---|
| Sucesso | 5/10 (50%) | **10/10 (100%)** | 120/200 (60%) | **200/200 (100%)** |
| p50 | 1,89 s | 196 ms | 6,11 s | **2 ms** |
| p95 | 2,03 s | **1,68 s** | 8,01 s | **202 ms** |
| Vazão | 0,5 req/s | 1,2 req/s | 8,0 req/s | **729,7 req/s** |
| Falhas | 5 × 502 | 0 | 80 × 502 | 0 |

O p95 do baseline fica em ~1,7 s porque as 5 primeiras requisições encontram o cache vazio e esperam a parceira mais lenta, a `partner-slow` (1,5 a 1,7 s). A vazão sobe tanto porque o gerador é de laço fechado: com respostas de poucos milissegundos, os 50 *workers* disparam muito mais rápido (o roteiro avisa sobre isso, §7).

**Complemento, sessão 1 (em série):** [reproduce-depois-serie.txt](reproduce-depois-serie.txt): baseline com p95 de 2,04 s (a cotação nova esperava as três parceiras em sequência) e carga com p95 de 219 ms e 695 req/s. A diferença no baseline, 2,04 s → 1,68 s, é o efeito da paralelização.

---

## 2. Saída do `make test`

- **Comando:**
  ```bash
  go clean -testcache && make test
  ```
  (mais `go test -count=1 -v ./...` para listar cada teste pelo nome, e `go test -race` no contêiner `golang:1.25`, porque o Windows não tem o compilador C que o detector de corrida exige)
- **Artefato:** [make-test.txt](make-test.txt); a versão da sessão 1 é [make-test-serie.txt](make-test-serie.txt).
- **Legenda:** `make test` termina com código 0; são 102 testes passando (64 que vieram no repositório e 38 desta entrega, sem contar subtestes), nenhum falhando ou pulado, cada um marcado como `[original]` ou `[novo]`, e o detector de corrida não acusa nada.

---

## 3. Trace com o breaker aberto

- **Consulta:** Jaeger em http://localhost:16686 → serviço `quotation-api` → tag `breaker.short_circuited=true`, janela de 23:08:13 a 23:08:19 UTC (passo 2 da variação 4). Exportado pela API do Jaeger:
  ```bash
  curl -s http://localhost:16686/api/traces/c85ec2854929ca873e6bebbeb2330d0e
  ```
- **Artefato:** [trace-depois-breaker-aberto-rapido.json](trace-depois-breaker-aberto-rapido.json)
- **Legenda:** com os circuitos abertos, a `partner-flaky` e a `partner-degrading` não são chamadas (nenhum `HTTP POST` no trace), a `partner-slow` vem do cache, e a cotação responde em 0,59 ms com as duas ausentes em `missing_partners`.

| Span | Início | Duração | Atributos |
|---|---|---|---|
| `POST /quotes` | +0,00 ms | **0,59 ms** | 200, `quote.degraded=true`, `quote.from_cache=1`, `quote.missing_partners=2` |
| `quote partner-slow` | +0,36 ms | 0,01 ms | `quote.source=cache`, `cache.result=hit` |
| `quote partner-flaky` | +0,48 ms | 0,04 ms | `breaker.short_circuited=true`, `quote.missing_reason=circuit_open` |
| `quote partner-degrading` | +0,53 ms | 0,01 ms | `breaker.short_circuited=true`, `quote.missing_reason=circuit_open` |

No passo 2 da variação 4, 15 traces tiveram ao menos uma parceira curto-circuitada, e 6 tiveram as duas.

**Complemento, da variação 1:** [trace-depois-breaker-aberto.json](trace-depois-breaker-aberto.json) (trace `94274bac30398eb4e69527eacf15b272`). Ali a cotação era nova: a `partner-slow` foi chamada (1662 ms, com `HTTP POST`), e a `partner-flaky` e a `partner-degrading`, com o circuito aberto, ficaram em 0 ms e sem `HTTP POST`. O curto-circuito não custa nada; o tempo total é o da parceira que foi chamada. Nessa janela, 197 traces tiveram ao menos uma parceira curto-circuitada, e 140 tiveram as duas.

---

## 4. Trace servido de cache

- **Consulta:** Jaeger → serviço `quotation-api` → tag `quote.from_cache=3`, janela da execução padrão da sessão 2 (2026-10-27, de 00:09:15 a 00:09:30 UTC). Exportado pela API do Jaeger:
  ```bash
  curl -s http://localhost:16686/api/traces/7344d61cbf8f2d9c09667a134edfd55a
  ```
- **Artefato:** [trace-depois-cache.json](trace-depois-cache.json); a versão da sessão 1 é [trace-depois-cache-serie.json](trace-depois-cache-serie.json).
- **Legenda:** as três cotações vêm do cache: os três spans de parceira têm `quote.source=cache`, nenhum tem `HTTP POST` embaixo, e a cotação leva 0,47 ms; na execução padrão, 163 das 210 cotações saíram assim.

| Span | Início | Duração | Atributos |
|---|---|---|---|
| `POST /quotes` | +0,00 ms | **0,47 ms** | 200, `quote.from_cache=3`, `quote.degraded=false` |
| `quote partner-degrading` | +0,37 ms | 0,03 ms | `quote.source=cache`, `cache.result=hit` |
| `quote partner-slow` | +0,40 ms | 0,01 ms | `quote.source=cache`, `cache.result=hit` |
| `quote partner-flaky` | +0,41 ms | 0,01 ms | `quote.source=cache`, `cache.result=hit` |

---

## 5. Estado do breaker no tempo

- **Consulta (PromQL):**
  ```text
  max by (partner) (partner_breaker_state)
  ```
  (0 fechado, 1 meio-aberto, 2 aberto; o `max by` só deixa a legenda com o nome da parceira)
- **Janela:** gráfico de 10 min, de 22:35 a 22:45 UTC; o gauge é exportado a cada 10 s.
- **Artefato:** [breaker-estado-depois.png](breaker-estado-depois.png). Complemento em texto, com o contador de transições e o log: [breaker-transicoes-depois.txt](breaker-transicoes-depois.txt) (consulta `sum by (partner, from, to) (partner_breaker_transitions_total)`).
- **Legenda:** o degrau completo aparece para as duas parceiras instáveis. A `partner-degrading` faz fechado → aberto → meio-aberto → fechado uma vez; a `partner-flaky` abre, oscila 10 vezes entre meio-aberto e aberto (com 40% de falha, 2 sucessos seguidos têm 36% de chance) e fecha às 22:43:41. A `partner-slow` fica fechada o tempo todo.

| Parceira | fechado → aberto | aberto → meio-aberto | meio-aberto → aberto | meio-aberto → fechado |
|---|---|---|---|---|
| `partner-flaky` | 1 | 11 | 10 | 1 |
| `partner-degrading` | 1 | 1 | 0 | 1 |
| `partner-slow` | 0 | 0 | 0 | 0 |

---

## 6. Hit rate do cache

- **Consulta (PromQL):**
  ```text
  sum(increase(quote_cache_lookups_total{result="hit"}[1m])) / sum(increase(quote_cache_lookups_total[1m]))
  ```
- **Janela:** gráfico de 5 min, de 22:43:30 a 22:48:30 UTC; `increase` sobre `[1m]`, para que a janela isole a variação 3 (as sondas da variação 2 acabaram 2 min antes).
- **Artefato:** [hit-rate-depois.png](hit-rate-depois.png); a saída da carga está em [variacao-aquecimento.txt](variacao-aquecimento.txt).
- **Legenda:** o hit rate fica em 0% enquanto as 50 primeiras cotações da `corretora-b` são novas, salta para 90% quando o gerador volta a repeti-las, e sobe até 97% conforme as cotações da `partner-flaky` que tinham falhado também entram no cache.

O salto, em vez de uma curva suave, é propriedade do gerador, que percorre as cotações em ordem. O 0% em 22:43 são as sondas da variação 2, todas com cotação nova. **Este valor é da carga, não de produção:** o hit rate da §8 do SAD sai do pressuposto de recotação P1 (40%).

**Por que uma variação, e não a carga padrão:** o enunciado espera um gráfico que "beira 98%" com a carga padrão. Aqui, a carga padrão dura 11 s e as métricas são exportadas a cada 10 s (`internal/platform/telemetry.go`, que é a telemetria pronta e fica fora do escopo), então o Prometheus não vê a subida. É uma limitação do ambiente, documentada no README do processo. Na carga padrão, o hit rate medido pelos contadores foi de **91%** (573 de 630), e não ~98%: o cache é por parceira e não guarda as falhas da `partner-flaky`, então as cotações em que ela falhou voltam a chamá-la na rodada seguinte ([consultas-depois.txt](consultas-depois.txt)).

---

## 7. p95 do `POST /quotes`

- **Consulta (PromQL):**
  ```text
  label_replace(histogram_quantile(0.95, sum by (le) (rate(http_server_request_duration_seconds_bucket{http_route="/quotes", http_request_method="POST"}[5m]))), "serie", "p95 do depois (agregacao paralela)", "", "")
    or label_replace(vector(9.7), "serie", "referencia: p95 do antes no mesmo histograma, 9,7 s", "", "")
  ```
- **Janela:** gráfico de 2 min, de 00:08:00 a 00:10:00 UTC de 2026-10-27 (a execução padrão da sessão 2 inteira); `rate` sobre `[5m]`; resolução fixa de 5 s.
- **Artefatos:** [p95-depois.png](p95-depois.png) e, do "antes", [p95-antes.png](p95-antes.png); a versão da sessão 1 é [p95-depois-serie.png](p95-depois-serie.png).
- **Legenda:** o p95 estimado pelo histograma fica em ~0,23 s (0,229 s às 00:09:55), contra a linha de referência de 9,7 s, que é o p95 do "antes" no mesmo histograma; a linha de referência põe os dois na mesma escala.

A série do "depois" só aparece a partir de 00:09:40, porque a execução padrão dura 9 s e as métricas são exportadas a cada 10 s. **A comparação numérica entre antes e depois é a do relatório em texto** (evidência 1): 8,01 s → 202 ms.

---

## Medições complementares

### Consultas compradas por cotação entregue (RNF-03)

- **Consultas (PromQL), lidas às 00:10:00 UTC de 2026-10-27**, depois da execução padrão da sessão 2, sem nenhuma outra carga:
  ```text
  sum(http_client_request_duration_seconds_count) / sum(http_server_request_duration_seconds_count{http_route="/quotes", http_response_status_code="200"})
  sum by (result) (quote_cache_lookups_total)
  ```
- **Artefato:** [consultas-depois.txt](consultas-depois.txt); o "antes" é [consultas-antes.txt](consultas-antes.txt); a sessão 1 é [consultas-depois-serie.txt](consultas-depois-serie.txt), com os mesmos números.
- **Legenda:** com a mesma carga, foram compradas 57 consultas para entregar 210 cotações (0,27 por cotação), contra 545 para 125 no "antes" (4,36 por cotação); a paralelização muda o tempo, não quantas consultas são compradas.

| | Antes | Depois |
|---|---|---|
| Consultas compradas | 545 | **57** |
| Cotações entregues (HTTP 200) | 125 | **210** |
| Consultas por cotação entregue | 4,36 | **0,27** |
| Hit rate do cache nesta carga | — | 91% (573 de 630) |

O hit rate de 91% é propriedade de uma carga que repete 5 cotações: não vai para a §8 do SAD.

### Nenhum dado pessoal na telemetria (SAD §4.5)

- **Comandos:** busca nas 210 traces exportadas do Jaeger e nas 268 séries do Prometheus da sessão 2, pelos CPFs, placas e `quote_id` usados na carga.
- **Artefato:** [sem-dado-pessoal-depois.txt](sem-dado-pessoal-depois.txt); a sessão 1, com as quatro variações (1041 traces e 409 séries), é [sem-dado-pessoal-depois-serie.txt](sem-dado-pessoal-depois-serie.txt).
- **Legenda:** zero ocorrências de CPF, placa ou `quote_id` nos traces e nos rótulos das métricas, nas duas sessões; os rótulos de negócio só têm nomes de parceira, resultado, desfecho e estado.

### Bônus: as três parceiras consultadas ao mesmo tempo (SAD §4.8, D16)

- **Consulta:** Jaeger → serviço `quotation-api` → tag `quote.from_cache=0`, janela do baseline da sessão 2 (2026-10-27, de 00:09:15 a 00:09:20 UTC), a primeira cotação. Exportado pela API do Jaeger:
  ```bash
  curl -s http://localhost:16686/api/traces/f7b7d9d6d1a180d02745a0a9fbe5b247
  ```
- **Artefato:** [trace-depois-paralelo.json](trace-depois-paralelo.json)
- **Legenda:** numa cotação nova, as três chamadas `HTTP POST` começam no mesmo instante (+3,9 ms) e a cotação leva 1,63 s, o tempo da parceira mais lenta; no "antes", as mesmas três chamadas vinham uma depois da outra e se somavam.

| Span | Início | Duração | Atributos |
|---|---|---|---|
| `POST /quotes` | +0,00 ms | **1628,85 ms** | 200, `quote.from_cache=0` |
| `HTTP POST` → partner-degrading | **+3,88 ms** | 150,48 ms | 200 |
| `HTTP POST` → partner-flaky | **+3,90 ms** | 184,60 ms | 200 |
| `HTTP POST` → partner-slow | **+3,91 ms** | 1624,11 ms | 200 |
