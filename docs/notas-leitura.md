# Notas de leitura

Resumo do que a leitura do código e dos documentos de apoio revelou, com foco no que **muda as decisões** do SAD (1A) e do PoC (Entrega 2). Lido em 2026-10-26, sobre o commit `1ebe588`.

Documentos lidos: [guia-sad.md](guia-sad.md), [roteiro-cenario-de-falha.md](roteiro-cenario-de-falha.md), [smoke-test-factibilidade.md](smoke-test-factibilidade.md), `docker-compose.yml`, todo o `internal/`, `cmd/quotation-api/`, e de `cmd/partner-mock/` e `cmd/loadgen/` o necessário para entender o comportamento.

---

## 1. ⚠️ Dois testes existentes travam o comportamento atual

Este é o achado mais importante. A regra "não quebre o que já passa" colide com o que o desafio pede:

| Teste | Onde | O que exige | Quem quebra |
|---|---|---|---|
| `TestOnePartnerDownBringsDownTheWholeRequest` | `internal/quotation/service_test.go:100` | `Service.Quote` **devolve erro** quando uma parceira falha | o **fallback**, se for colocado dentro de `Service.Quote` |
| `TestQuoteCallsThePartnersSerially` | `internal/quotation/service_test.go:83` | pico de **1** chamada simultânea e duração ≥ 3 × atraso | a **paralelização** (bônus), se for feita em `Service.Quote` |
| `TestQuotesResponds502WithTheNameOfThePartnerThatWentDown` | `internal/quotation/handler_test.go:95` | a API montada com `NewAPI` responde **502** quando uma parceira falha | o **fallback**, se o handler passar a usá-lo sempre (achado na revisão do 2A) |

**Consequência para o desenho:** o fallback não pode simplesmente substituir o `return err` de `Service.Quote`. Caminhos possíveis, a decidir no SAD §4:

- **(a)** manter `Service.Quote` como está (tudo-ou-nada, em série) e criar um caminho novo, por exemplo um método ou tipo que agrega com fallback, usado pelo handler. Os testes antigos continuam verdes testando o comportamento antigo, que segue existindo.
- **(b)** alterar `Service.Quote` e o teste. Isso **viola** a regra do enunciado e conta contra a entrega.
- **(c)** se nenhuma forma limpa existir, documentar a limitação no README do processo, como o enunciado manda.

A opção (a) parece a única que respeita as duas regras ao mesmo tempo. A mesma lógica vale para a paralelização, se o bônus for feito, e para o **handler**: `NewAPI` mantém o 502 original e uma opção nova liga o fallback, usada só pelo `cmd/quotation-api/main.go`.

---

## 2. Mapa do código e pontos de extensão

| Arquivo | O que tem hoje | Ponto de extensão |
|---|---|---|
| `internal/partner/client.go` | `http.Client` com `Timeout` zero; transporte instrumentado pelo `otelhttp` | timeout no `http.Client` (ou por `context`); o breaker pode envolver `Client.Quote` |
| `internal/partner/client.go` | erro tipado `*partner.Error{Partner, Status, Reason}`; erro de rede tem `Status == 0` | o breaker classifica falha por esse tipo; o timeout aparece como erro de rede (`Status == 0`) |
| `internal/quotation/service.go` | interface **`Quoter`** com um único método `Quote(ctx, partner, request)` | **costura principal**: breaker e cache podem ser *decorators* de `Quoter`, sem mexer no `Service` |
| `internal/quotation/service.go` | agrega em série, `return err` na primeira falha, ordena por `premium_cents` | fallback (respeitando o item 1) |
| `internal/quotation/request.go` | `Response{tenant_id, quotes, elapsed_ms}`; `Normalize()` já normaliza placa (maiúsculas) e documento (trim) | campo/bloco de degradação no `Response`; `Normalize()` é a base da **chave de cache normalizada** |
| `internal/quotation/handler.go` | 400/403 de validação; `*partner.Error` → 502 `partner insurer unavailable`; outro erro → 502 genérico | resposta degradada ou recusa explícita |
| `internal/platform/config.go` | tudo por variável de ambiente, com default (`text(env, key, fallback)`) | novos parâmetros (TTL, limiares, timeout, endereço do Redis) seguem o mesmo padrão |
| `internal/platform/telemetry.go` | `MeterProvider` e `TracerProvider` **globais** (`otel.SetMeterProvider`); métricas exportadas a cada **10 s**; `AlwaysSample` | métricas de negócio novas usam `otel.Meter(...)` e `otel.Tracer(...)`, sem tocar neste arquivo |
| `cmd/quotation-api/main.go` | monta `NewService(cfg.Partners, partner.NewClient())` | onde se ligam Redis, breaker e cache |

**Dependências:** o `go.mod` hoje só tem OpenTelemetry. A biblioteca de circuit breaker e o cliente Redis entram como dependências novas.

**Nomes no Prometheus:** o Collector converte os nomes OTel. Por exemplo, `http.server.request.duration` em segundos vira `http_server_request_duration_seconds_bucket`, com rótulos como `http_route`, `http_request_method`, `http_response_status_code` e `server_address`. As métricas novas vão seguir a mesma conversão, e o nome a declarar no README é o que aparece no Prometheus.

---

## 3. O que os mocks revelam (e que define os números)

Fonte: `docker-compose.yml` e `cmd/partner-mock/behavior.go`.

| Parceira | Latência | Falha | Implicação |
|---|---|---|---|
| `partner-slow` | 1500 ms + jitter de 0 a 200 ms → **1500 a 1700 ms**, sempre | nunca | **Um timeout abaixo de ~1,7 s mata a partner-slow em 100% das chamadas** e deixa o breaker dela aberto para sempre. O timeout tem que ficar acima disso, ou ser diferente por parceira. |
| `partner-flaky` | 150 ms + jitter de 0 a 50 ms → **150 a 200 ms** | **40%** com 503, sequência fixa pela semente | rajada de 9 falhas nas sequências **49 a 57**; é onde o breaker abre |
| `partner-degrading` | 120 ms + jitter de 0 a 40 ms, **+300 ms por chamada simultânea acima de 5**, até +6000 ms | nunca | com *n* chamadas em voo: ≈ 120 + 300 × (*n* − 5) ms. Exemplos: 7 em voo ≈ 0,7 s; 11 em voo ≈ 1,9 s; 25 em voo ≈ 6,1 s (teto). Medido no "antes": **6149 ms**. |

Outros fatos dos mocks:

- **O prêmio é determinístico:** é um hash do nome da parceira com o **corpo inteiro da requisição**, que inclui o `broker`. A mesma placa gera prêmios diferentes para corretoras diferentes, o que confirma que o cache **precisa** do `tenant_id` na chave. Também significa que, no mock, a cotação servida de cache tem o mesmo valor que a parceira daria.
- O `quote_id` também é determinístico por (parceira, corpo) e o `valid_for_seconds` vale 300.
- **O contador de sequência é por parceira e só anda quando a parceira é chamada.** Com o breaker aberto, a sequência para. A simulação do `feasibility_test.go` modela exatamente isso.
- Todas as respostas trazem `X-Partner-Seq`, `X-Partner-Inflight` e `X-Partner-Latency-Ms`, úteis para depurar.

---

## 4. O que o `feasibility_test.go` já prova sobre o breaker

Em 210 requisições, que é o volume de um `make reproduce` (fonte: `smoke-test-factibilidade.md`):

| Política | Abre na requisição | Aberturas | Voltas a fechado |
|---|---|---|---|
| 3 falhas consecutivas | 9 | 13 | 4 |
| 5 falhas consecutivas | 42 | 7 | 2 |
| 50% de falha em janela de 10 | 10 | 15 | 6 |
| 60% de falha em janela de 20 | 55 | 8 | 1 |

- Sob concorrência alta na `partner-flaky` (34 simultâneas), a rajada máxima cai de 9 para **5**. O breaker de 5 consecutivas passa a abrir só na requisição 104. **Políticas por taxa em janela são mais robustas** à concorrência do que as de falhas consecutivas.
- Na simulação, o *cooldown* é contado em requisições. Numa biblioteca real ele é **tempo**, então o SAD precisa converter (por exemplo, a vazão de ~8 req/s do "antes" transforma 5 requisições em ~0,6 s).
- O teste é o **molde** dos testes da Entrega 2: usa o `Behavior` real do mock, sem rede, sem `sleep`.

---

## 5. O que o gerador de carga implica

Fonte: `cmd/loadgen/request.go` e o roteiro.

- **5 cotações distintas**, sempre a corretora `corretora-a`, 210 requisições em **laço fechado** (50 *workers* fixos).
- Por isso o hit rate medido vai beirar **98%**, e **não serve** para a planilha da §8. O hit rate da §8 sai do pressuposto de recotação da §1.
- **Cache por parceira × cache agregado:** com a `partner-flaky` falhando 40%, um cache **agregado** só é gravado quando as três respondem. Um cache **por parceira** guarda cada resposta boa independentemente. Isso pesa na decisão.
- **No "depois", a partner-degrading vai parecer pior.** O laço fechado acelera quando a latência cai, e mais chamadas simultâneas chegam nela. O roteiro avisa que isso não é regressão. Compara-se a taxa de sucesso, o p95 da ponta e a vazão. Rodar também uma variação com `-concurrency` menor ajuda, desde que declarado no SAD.
- Opção útil para a demonstração do cache: `make load ARGS="-distinct 1"`.

---

## 6. O que o `guia-sad.md` cobra além do enunciado

- **§1:** separar **restrição** (imposta: Go, ambiente local, três parceiras, cobrança por consulta, retenção de 5 anos, papel de operadora) de **decisão** (TTL, limiares, hospedagem). Pressuposto sem consequência "não conta".
- **§2:** os nomes dos contêineres nos diagramas têm que bater com o compose: `quotation-api`, `partner-slow`, `partner-flaky`, `partner-degrading`, `redis`, `otel-collector`, `jaeger`, `prometheus`. Fechar com o delta e um parágrafo "a quem esta arquitetura serve".
- **§3:** tabela de RNF com as colunas *ID, Requisito, Métrica, Hoje, Alvo, Como medir, Por que este número*. A última coluna é a que mais pesa.
- **§4:** todo parâmetro numérico defendido; C4 nível 3 cobrindo `internal/partner/client.go` e `internal/quotation/service.go`.
- **§5:** a biblioteca que **expõe os três estados explicitamente** vale mais que a mais popular.
- **§6:** runbook de um caso real (exemplo do guia: "breaker da `partner-degrading` aberto há 10 minutos"), com o que o plantonista faz, o que **não** faz e quando escala; o TTL é parâmetro de **negócio** (quem aprova, como chega em produção, como reverte, como mede).
- **§7:** cenário Redis perdido tem dois efeitos: custo (100% das consultas compradas de novo) **e** o volume simultâneo nas parceiras mais que dobra a partir de 50% de hit, o que empurra a `partner-degrading` para o colapso. Dizer qual efeito chega primeiro.
- **§8:** a auditoria é dimensionada por **cotação apresentada**, não por consulta comprada. Com 60% de hit, quem erra a unidade subestima o acervo em **2,5×**, e o erro é maior no ano 5.

---

## 7. Decisões que a leitura deixou em aberto para o 1A

Estas são as perguntas que o SAD §4 precisa responder, agora com os fatos acima na mão:

1. **Timeout:** único ou por parceira? Ele tem que ficar **acima de ~1,7 s** para a `partner-slow`, mas um valor alto deixa a `partner-degrading` acumular chamadas.
2. **Estouro de timeout conta como falha do breaker?** É o que faz a `partner-degrading` ser vista.
3. **Política do breaker:** falhas consecutivas ou taxa em janela? A leitura favorece taxa em janela, por ser mais robusta à concorrência.
4. **Escopo do breaker:** por parceira (o mais provável) ou por (parceira, corretora)?
5. **Biblioteca:** qual expõe os três estados e permite contar timeout como falha.
6. **Cache por parceira ou agregado**, e o TTL (entre os 300 s técnicos do mock e o teto comercial de 24 h).
7. **Fallback:** resposta parcial, cotação anterior ou recusa, e **como implementar sem quebrar** `TestOnePartnerDownBringsDownTheWholeRequest` (item 1).
8. **Contrato de degradação:** campo booleano, bloco de metadados ou cabeçalho.
