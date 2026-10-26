# Evidências do "depois"

Medições feitas em **2026-10-26**, numa única sessão (de 22:38 a 22:50 UTC), sobre o commit `9689381`, com timeout, circuit breaker, cache e resposta parcial ligados. O "antes" está em [antes.md](antes.md).

Cada evidência tem **comando ou consulta + artefato + legenda**. Os gráficos foram capturados com o ambiente de pé, porque `make down` apaga o Jaeger e o Prometheus.

## Como a sessão foi montada

| Etapa | Horário (UTC) | O que rodou | Para quê |
|---|---|---|---|
| Execução padrão | 22:38:26 a 22:38:37 | `make down && make reproduce` (10 de baseline, 200 com 50 em voo) | evidências 1, 4, 7 e a conta de consultas; **é a mesma carga do "antes"** |
| Variação declarada 1 | 22:39:01 a 22:39:17 | `make load ARGS="-distinct 200"` | evidência 3 |
| Variação declarada 2 | 22:40:04 a 22:43:42 | uma cotação nova a cada 8 s, até os dois circuitos fecharem | completar o degrau da evidência 5 |
| Variação declarada 3 | 22:45:43 a 22:46:34 | `make load ARGS="-tenant corretora-b -distinct 50 -concurrency 2 -requests 600 -baseline 0"` | evidência 6 |

**Por que as variações** (o enunciado permite variação declarada para explorar o comportamento, desde que a evidência principal venha dos defaults):

- **Variação 1:** a carga padrão repete só 5 cotações. Elas já estão no cache quando o circuito da `partner-flaky` abre, então nenhuma requisição chega a ser curto-circuitada. Com 200 cotações distintas, as requisições chegam às parceiras e o curto-circuito aparece.
- **Variação 2:** o meio-aberto só fecha depois de 2 requisições de teste com sucesso (`BREAKER_HALF_OPEN_REQUESTS=2`), e isso exige tráfego depois que a carga acaba.
- **Variação 3:** a carga padrão dura 11 s, e as métricas são exportadas a cada 10 s. O contador de *hit* nasce já com 205 entre duas amostras, e o Prometheus não vê a subida. Com a `corretora-b` (cache frio, porque a chave tem a corretora) e um ritmo mais lento, o aquecimento dura cerca de um minuto.

Nenhuma variação mexe nos perfis das parceiras: o `docker-compose.yml` das parceiras é o original.

---

## 1. Relatório antes/depois

- **Comando:**
  ```bash
  make down && make reproduce
  ```
- **Artefato:** [reproduce-depois.txt](reproduce-depois.txt) (saída completa). O "antes" é [reproduce-antes.txt](reproduce-antes.txt).
- **Legenda:** com a mesma carga, o p95 sob carga cai de 8,01 s para 219 ms, o sucesso sobe de 60% para 100% e a vazão de 8,0 para 695 req/s.

| | Antes: baseline | Depois: baseline | Antes: carga | Depois: carga |
|---|---|---|---|---|
| Sucesso | 5/10 (50%) | **10/10 (100%)** | 120/200 (60%) | **200/200 (100%)** |
| p50 | 1,89 s | 196 ms | 6,11 s | **4 ms** |
| p95 | 2,03 s | 2,04 s | 8,01 s | **219 ms** |
| Vazão | 0,5 req/s | 1,0 req/s | 8,0 req/s | **695,1 req/s** |
| Falhas | 5 × 502 | 0 | 80 × 502 | 0 |

O p95 do baseline continua em ~2 s porque as 5 primeiras requisições encontram o cache vazio e esperam a `partner-slow` (1,5 a 1,7 s). A vazão sobe tanto porque o gerador é de laço fechado: com respostas de poucos milissegundos, os 50 *workers* disparam muito mais rápido (o roteiro avisa sobre isso, §7).

---

## 2. Saída do `make test`

- **Comando:**
  ```bash
  go clean -testcache && make test
  ```
  (mais `go test -count=1 -v ./...` para listar cada teste pelo nome)
- **Artefato:** [make-test.txt](make-test.txt)
- **Legenda:** `make test` termina com código 0; são 100 testes passando, nenhum falhando ou pulado, cada um marcado como `[original]` (veio no repositório) ou `[novo]` (desta entrega).

---

## 3. Trace com o breaker aberto

- **Consulta:** Jaeger em http://localhost:16686 → serviço `quotation-api` → tag `breaker.short_circuited=true`, janela da variação 1. Exportado pela API do Jaeger:
  ```bash
  curl -s http://localhost:16686/api/traces/94274bac30398eb4e69527eacf15b272
  ```
- **Artefato:** [trace-depois-breaker-aberto.json](trace-depois-breaker-aberto.json)
- **Legenda:** com o circuito aberto, a `partner-flaky` e a `partner-degrading` não são chamadas: os spans delas duram 0 ms e não têm `HTTP POST` embaixo; só a `partner-slow` sai para a rede, e a corretora recebe 200 com as duas ausentes em `missing_partners`.

| Span | Início | Duração | Atributos |
|---|---|---|---|
| `POST /quotes` | +0 ms | 1663 ms | 200, `quote.degraded=true`, `quote.missing_partners=2` |
| `quote partner-slow` | +0 ms | 1662 ms | `quote.source=partner`, `cache.result=miss` |
| └ `HTTP POST` → partner-slow | +0 ms | 1662 ms | 200 |
| `quote partner-flaky` | +1661 ms | **0 ms** | `breaker.short_circuited=true`, `quote.missing_reason=circuit_open` |
| `quote partner-degrading` | +1661 ms | **0 ms** | `breaker.short_circuited=true`, `quote.missing_reason=circuit_open` |

Na janela da variação 1, 197 traces tiveram ao menos uma parceira curto-circuitada, e 140 tiveram as duas.

---

## 4. Trace servido de cache

- **Consulta:** Jaeger → serviço `quotation-api` → tag `quote.from_cache=3`, janela da execução padrão (22:38:20 a 22:38:40 UTC). Exportado pela API do Jaeger:
  ```bash
  curl -s http://localhost:16686/api/traces/f15cb4c9bb5b03e986dd87a91b04a205
  ```
- **Artefato:** [trace-depois-cache.json](trace-depois-cache.json)
- **Legenda:** as três cotações vêm do cache: os três spans de parceira têm `quote.source=cache` e nenhum tem `HTTP POST` embaixo. Na execução padrão, 163 das 210 cotações saíram assim.

| Span | Duração | Atributos |
|---|---|---|
| `POST /quotes` | 26 ms | 200, `quote.from_cache=3`, `quote.degraded=false` |
| `quote partner-slow` | 0 ms | `quote.source=cache`, `cache.result=hit` |
| `quote partner-flaky` | 0 ms | `quote.source=cache`, `cache.result=hit` |
| `quote partner-degrading` | 0 ms | `quote.source=cache`, `cache.result=hit` |

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

---

## 7. p95 do `POST /quotes`

- **Consulta (PromQL):**
  ```text
  label_replace(histogram_quantile(0.95, sum by (le) (rate(http_server_request_duration_seconds_bucket{http_route="/quotes", http_request_method="POST"}[5m]))), "serie", "p95 do depois", "", "")
    or label_replace(vector(9.7), "serie", "referencia: p95 do antes no mesmo histograma, 9,7 s", "", "")
  ```
- **Janela:** gráfico de 2 min, de 22:37:10 a 22:39:10 UTC (a execução padrão inteira, até antes da variação 1); `rate` sobre `[5m]`.
- **Artefatos:** [p95-depois.png](p95-depois.png) e, do "antes", [p95-antes.png](p95-antes.png).
- **Legenda:** o p95 estimado pelo histograma fica em ~0,23 s, contra a linha de referência de 9,7 s, que é o p95 do "antes" no mesmo histograma; a linha de referência põe os dois na mesma escala.

A série do "depois" só aparece a partir de 22:38:53, porque a execução padrão dura 11 s e as métricas são exportadas a cada 10 s. **A comparação numérica entre antes e depois é a do relatório em texto** (evidência 1): 8,01 s → 219 ms.

---

## Medições complementares

### Consultas compradas por cotação entregue (RNF-03)

- **Consultas (PromQL), lidas em 22:39:00 UTC**, depois da execução padrão e antes da variação 1:
  ```text
  sum(http_client_request_duration_seconds_count) / sum(http_server_request_duration_seconds_count{http_route="/quotes", http_response_status_code="200"})
  sum by (result) (quote_cache_lookups_total)
  ```
- **Artefato:** [consultas-depois.txt](consultas-depois.txt); o "antes" é [consultas-antes.txt](consultas-antes.txt).
- **Legenda:** com a mesma carga, foram compradas 57 consultas para entregar 210 cotações (0,27 por cotação), contra 545 para 125 no "antes" (4,36 por cotação).

| | Antes | Depois |
|---|---|---|
| Consultas compradas | 545 | **57** |
| Cotações entregues (HTTP 200) | 125 | **210** |
| Consultas por cotação entregue | 4,36 | **0,27** |
| Hit rate do cache nesta carga | — | 91% (573 de 630) |

O hit rate de 91% é propriedade de uma carga que repete 5 cotações: não vai para a §8 do SAD.

### Nenhum dado pessoal na telemetria (SAD §4.5)

- **Comandos:** busca nas 1041 traces da última hora exportadas do Jaeger e nas 409 séries do Prometheus, pelos CPFs, placas e `quote_id` usados nas cargas desta sessão.
- **Artefato:** [sem-dado-pessoal-depois.txt](sem-dado-pessoal-depois.txt)
- **Legenda:** zero ocorrências de CPF, placa ou `quote_id` nos traces e nos rótulos das métricas; os rótulos de negócio só têm nomes de parceira, resultado, desfecho e estado.
