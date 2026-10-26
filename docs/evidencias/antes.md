# Evidências do "antes"

Medições feitas em **2026-10-26** sobre o código original (commit `5f98f8c`), antes de qualquer alteração em Go, com a carga padrão do `make reproduce` (10 requisições de baseline, depois 200 com 50 em voo).

**Janela da carga:** baseline de 20:49:05 a 20:49:24 UTC; carga de 20:49:24 a 20:49:48 UTC.

---

## 1. Relatório do `make reproduce`

- **Comando:**
  ```bash
  make down && make reproduce
  ```
- **Artefato:** [reproduce-antes.txt](reproduce-antes.txt) (saída completa, incluindo o build)
- **Legenda:** sob carga, o p95 sobe de 2,03 s para 8,01 s e só 60% das cotações dão certo; todas as falhas são 502 causados pela `partner-flaky`.

| | Baseline (10 req, 1 por vez) | Carga (200 req, 50 em voo) |
|---|---|---|
| Sucesso | 5/10 (50%) | 120/200 (60%) |
| p50 | 1,89 s | 6,11 s |
| p95 | 2,03 s | 8,01 s |
| Vazão | 0,5 req/s | 8,0 req/s |

---

## 2. p95 do `POST /quotes` no Prometheus

- **Consulta (PromQL):**
  ```text
  histogram_quantile(0.95, sum by (le) (rate(http_server_request_duration_seconds_bucket{http_route="/quotes", http_request_method="POST"}[5m])))
  ```
- **Janela:** gráfico de 15 min, de 20:45 a 21:00 UTC; `rate` sobre `[5m]`.
- **Artefato:** [p95-antes.png](p95-antes.png)
- **Legenda:** o p95 sai de cerca de 2,4 s no baseline e salta para cerca de 9,7 s quando entram as 50 requisições em voo.

> **Por que 9,7 s aqui e 8,01 s no relatório?** O relatório do loadgen calcula o p95 sobre as latências exatas. O Prometheus estima o p95 a partir dos *buckets* do histograma e interpola dentro do bucket em que o valor cai, entre 7,5 s e 10 s. A comparação numérica antes × depois usa o relatório em texto; o gráfico mostra a forma e a ordem de grandeza.

---

## 3. Trace da cascata em série (Jaeger)

- **Consulta:** Jaeger em http://localhost:16686 → serviço `quotation-api` → operação `POST /quotes` → janela de 20:48 a 20:55 UTC → ordenado pelo mais demorado. Exportado pela API do Jaeger:
  ```bash
  curl -s http://localhost:16686/api/traces/b19e70b8d0436449e0f1008b7aef24be
  ```
- **Artefato:** [trace-antes-cascata-serie.json](trace-antes-cascata-serie.json)
- **Legenda:** a cotação mais lenta da carga (8,05 s) é a soma das três parceiras chamadas uma depois da outra; só a `partner-degrading`, afundada pela concorrência, consome 6,1 s.

| Span | Início | Duração | Resultado |
|---|---|---|---|
| `POST /quotes` (quotation-api) | +0 ms | 8051 ms | 200 |
| `HTTP POST` → partner-slow | +0 ms | 1701 ms | 200 |
| `HTTP POST` → partner-flaky | +1701 ms | 201 ms | 200 |
| `HTTP POST` → partner-degrading | +1902 ms | 6149 ms | 200 |

---

## 4. Trace do tudo-ou-nada (Jaeger)

- **Consulta:** mesma busca da evidência 3, filtrando os traces com erro. Exportado pela API do Jaeger:
  ```bash
  curl -s http://localhost:16686/api/traces/730e2aa22ebb0c67e6a66732911714f1
  ```
- **Artefato:** [trace-antes-erro-tudo-ou-nada.json](trace-antes-erro-tudo-ou-nada.json)
- **Legenda:** a `partner-flaky` devolve 503, a cotação inteira vira 502 e o span da `partner-degrading` nem chega a existir; a resposta da `partner-slow`, que já tinha chegado, é jogada fora.

| Span | Início | Duração | Resultado |
|---|---|---|---|
| `POST /quotes` (quotation-api) | +0 ms | 1923 ms | **502** |
| `HTTP POST` → partner-slow | +2 ms | 1729 ms | 200 |
| `HTTP POST` → partner-flaky | +1732 ms | 191 ms | **503** |
| `HTTP POST` → partner-degrading | — | — | *não chamada* |

Os dois arquivos JSON foram conferidos: não contêm CPF nem placa.

---

## 5. Consultas compradas por cotação entregue (Prometheus)

- **Consultas (PromQL):**
  ```text
  sum by (server_address, http_response_status_code) (http_client_request_duration_seconds_count)
  sum by (http_response_status_code) (http_server_request_duration_seconds_count{http_route="/quotes"})
  sum(http_client_request_duration_seconds_count) / sum(http_server_request_duration_seconds_count{http_route="/quotes", http_response_status_code="200"})
  ```
- **Janela:** contadores acumulados desde o `make reproduce` desta página até a consulta, às 21:30 UTC, sem reinício do ambiente nem carga adicional.
- **Artefato:** [consultas-antes.txt](consultas-antes.txt)
- **Legenda:** foram compradas 545 consultas para entregar 125 cotações (4,36 por cotação, contra 3 no ideal), porque nas 85 cotações que falharam a `partner-slow` e a `partner-flaky` já tinham sido pagas.

| Parceira | 200 | 503 | Total |
|---|---|---|---|
| `partner-slow` | 210 | — | 210 |
| `partner-flaky` | 125 | 85 | 210 |
| `partner-degrading` | 125 | — | 125 |
| **Total** | | | **545** |

É a base da RNF-03 do SAD.
