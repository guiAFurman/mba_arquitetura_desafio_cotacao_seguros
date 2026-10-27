# Progresso do desafio

Roteiro de trabalho para não se perder entre sessões. O enunciado completo está em [enunciado.md](enunciado.md); este arquivo só acompanha **o que já foi feito, o que falta e em que ordem**.

**Fluxo de branches:** todo o trabalho e os commits vão na `development`. A `main` só recebe o merge, feito pelo GitHub, quando a entrega estiver pronta. O avaliador lê **apenas a `main`**.

Legenda: `[x]` feito · `[ ]` pendente · ⚠️ atenção

---

## Visão geral

O trabalho está dividido em quatro blocos:

| Bloco | O que é | Onde fica | Obrigatório? |
|---|---|---|---|
| **Preparação** | ambiente, "antes" medido, leitura | `docs/evidencias/` | sim |
| **Entrega 1 — SAD** | o documento que decide e justifica | `docs/sad.md` | sim |
| **Entrega 2 — PoC** | código, instrumentação, testes e evidências | `internal/`, `cmd/quotation-api/`, `docs/evidencias/` | sim |
| **Fechamento** | README do processo e revisão de coerência | raiz e `docs/` | sim |
| **Extras** | o bônus, que não conta para a nota | — | não |

### Ordem de execução

As duas entregas **não são independentes**: o SAD fixa os números antes do código, e o código devolve as medições para o SAD.

```text
Preparação
  └─► 1A. SAD §1–4 (decide TTL, limiares, timeout, chave, fallback)
        └─► 2A. Mecanismos  ─► 2B. Instrumentação ─► 2C. Testes ─► 2D. Evidências "depois"
              └─► 1B. SAD §5–8 (com os números medidos) + revisão do §1–4
                    └─► Fechamento ─► Extras (opcional, só com o resto pronto)
```

**Status atual:** **Entrega 1 concluída** ([sad.md](sad.md), versão 1.0, as oito seções) e **Entrega 2 concluída** (2A mecanismos, 2B instrumentação, 2C testes, 2D evidências). Bônus (agregação paralela) e **README do processo** concluídos. Falta só o **push da `development` e o merge na `main` pelo GitHub**, com o fork público.

---

## Preparação

### Ambiente da máquina

- [x] Go 1.25.7 instalado (winget `GoLang.Go`)
- [x] GNU Make 4.4.1 instalado (winget `ezwinports.make`)
- [x] Docker Desktop 29.9.2 + Docker Compose instalados (winget `Docker.DockerDesktop`)
- [x] WSL 2 funcionando
- [x] `make test` e `make smoke` verdes no código original

<details>
<summary>Como o WSL foi destravado (caso precise refazer em outra máquina)</summary>

Sintoma: o Docker Desktop dizia *"Virtual Machine Platform is not enabled"* mesmo com o recurso habilitado. Os pacotes de virtualização (incluindo o serviço `vmcompute`) ficaram presos em "pendente de reinício". Todos os comandos abaixo foram rodados no PowerShell **como administrador**:

```bash
dism.exe /online /enable-feature /featurename:Microsoft-Windows-Subsystem-Linux /all /norestart
dism.exe /online /enable-feature /featurename:VirtualMachinePlatform /all /norestart
bcdedit /set hypervisorlaunchtype auto
powercfg /h off
dism.exe /online /cleanup-image /restorehealth
dism.exe /online /enable-feature /featurename:VirtualMachinePlatform /all
```

Depois, reiniciar em **Iniciar → Energia → Reiniciar** (não "Desligar"). Observações:

- `Enable-WindowsOptionalFeature` falha no PowerShell 7 com "Classe não registrada"; use `dism.exe`.
- Para conferir se deu certo, o serviço `vmcompute` tem que existir e estar rodando.
- A hibernação foi desligada (`powercfg /h off`); para religar, use `powercfg /h on`.

</details>

### Reproduzir o "antes"

- [x] `make down && make reproduce` no código original → [evidencias/reproduce-antes.txt](evidencias/reproduce-antes.txt)
- [x] Gráfico do p95 do "antes" no Prometheus → [evidencias/p95-antes.png](evidencias/p95-antes.png)
- [x] Trace da cascata em série → [evidencias/trace-antes-cascata-serie.json](evidencias/trace-antes-cascata-serie.json)
- [x] Trace do tudo-ou-nada → [evidencias/trace-antes-erro-tudo-ou-nada.json](evidencias/trace-antes-erro-tudo-ou-nada.json)
- [x] Consultas compradas por cotação entregue (545 para 125) → [evidencias/consultas-antes.txt](evidencias/consultas-antes.txt)
- [x] Índice com comando, artefato e legenda de cada uma → [evidencias/antes.md](evidencias/antes.md)

**Números medidos (2026-10-26):**

| | Baseline (10 req, 1 por vez) | Carga (200 req, 50 em voo) |
|---|---|---|
| Sucesso | 5/10 (50%) | 120/200 (60%) |
| p50 | 1,89 s | 6,11 s |
| p95 | 2,03 s | 8,01 s |
| Vazão | 0,5 req/s | 8,0 req/s |
| Falhas | 5 × 502 `partner-flaky` | 80 × 502 `partner-flaky` |

O p95 no Prometheus aparece como ~9,7 s porque é estimado pelos buckets do histograma (7,5 s a 10 s). Para comparar antes × depois, vale o número do relatório em texto.

### Entender antes de decidir

- [x] Ler [roteiro-cenario-de-falha.md](roteiro-cenario-de-falha.md)
- [x] Ler o código: `internal/partner/client.go`, `internal/quotation/{service,handler,request}.go`, `cmd/quotation-api/main.go`, `internal/platform/config.go`
- [x] Ler `cmd/partner-mock/feasibility_test.go` (molde dos testes determinísticos)
- [x] Ler [guia-sad.md](guia-sad.md) e [smoke-test-factibilidade.md](smoke-test-factibilidade.md)
- [x] Registrar o que a leitura revelou → [notas-leitura.md](notas-leitura.md)

> ⚠️ **Achado da leitura:** `TestOnePartnerDownBringsDownTheWholeRequest` exige que `Service.Quote` devolva erro quando uma parceira falha. O fallback **não pode** entrar dentro de `Service.Quote` sem quebrar esse teste. Ver [notas-leitura.md §1](notas-leitura.md#1-️-dois-testes-existentes-travam-o-comportamento-atual).

---

## Entrega 1 — SAD (`docs/sad.md`)

O SAD é escrito para três leitores: o **CTO** (quer a conta), **quem opera às 3h** (quer o runbook) e o **encarregado de dados** (quer rastreabilidade e isolamento). Regras: tudo rastreável a arquivo real ou marcado como **proposta**, números em vez de adjetivos, diagramas C4 em Mermaid com setas rotuladas.

### 1A. Seções 1 a 4 — decidir antes de codar

- [x] Estudo das opções de cada decisão, com prós, contras e problemas → [estudo-decisoes.md](estudo-decisoes.md)
- [x] Escolher as 10 decisões (D1 a D10) e preencher a tabela "Decisões fixadas" (todas as inclinações do estudo foram aceitas)

- [x] **§1 Introdução:** propósito, escopo, restrições × decisões
  - [x] **4 pressupostos** (P1 a P4) com valor, origem e consequência se falso; o **P1 é o de recotação** (40% em 1 h, na mesma corretora)
- [x] **§2 Visão geral:**
  - [x] C4 nível 1 (contexto)
  - [x] C4 nível 2 "antes"
  - [x] C4 nível 2 "depois"
  - [x] Delta em lista
- [x] **§3 Requisitos:**
  - [x] **9 RF** (RF-05 a RF-09 criados pela arquitetura)
  - [x] **7 RNF** (RNF-01 a RNF-04 com "hoje" medido)
- [x] **§4 Detalhamento:** cada decisão no formato *contexto → opções → escolha → consequências*, com alternativa descartada e consequências ruins
  - [x] Decisão do **circuit breaker** (biblioteca, escopo, limiares, janela, tempo aberto, meio aberto)
  - [x] Decisão do **timeout** (valor e se o estouro conta como falha)
  - [x] Decisão do **cache** (TTL, granularidade, **chave por extenso com `tenant_id`**, invalidação, descarte LGPD, comportamento com o breaker aberto)
  - [x] Decisão do **fallback** (parcial, cotação anterior ou recusa) e mudança no contrato de `Response`
  - [x] C4 nível 3 da fatia implementada
  - [x] **4 limites conhecidos** com gatilho e efeito na corretora: concorrência por parceira, breaker em memória, TTL como única invalidação, corretora vizinha barulhenta
- [x] Os 4 diagramas Mermaid validados (renderizados com Mermaid 11.4.1, sem erro)

> **Pendências do SAD que dependem da Entrega 2:** os caminhos marcados **[proposta]** no SAD (`internal/partner/breaker.go`, `internal/quotecache/`, o novo método em `service.go`) e os nomes de métrica precisam existir ou ser corrigidos no SAD antes do fechamento.

### 1B. Seções 5 a 8 — fechar com os números medidos

Depende da Entrega 2 (2D).

Estudo das decisões D11 a D15, com as fontes externas: [estudo-decisoes-1b.md](estudo-decisoes-1b.md) (todas as inclinações aceitas).

- [x] **§5 Implementação:** mecanismo → arquivo, biblioteca, configuração e teste; **hospedagem** (D11: nuvem pública em São Paulo, auditoria com bloqueio de escrita) no formato de decisão
- [x] **§6 Operação e mudanças:**
  - [x] Quem olha cada métrica e atributo criados no 2B, e para quê
  - [x] Achado do breaker com pouco tráfego → **limite conhecido L5** (D14), sem mudar código
  - [x] **4 alertas** (A1 a A4) com métrica, limiar e ação
  - [x] **Runbook R1** completo ("circuito da `partner-degrading` fora de fechado há 10 min"), do sintoma ao escalonamento, com o que não fazer
  - [x] Caminho de mudança do TTL (D15)
- [x] **§7 Recuperação de desastres:**
  - [x] RTO e RPO por classe de dado (D12)
  - [x] **3 cenários** com efeito, o que degrada e o que para, custo em reais e caminho de volta: Redis perdido (R$ 576/h; o custo chega antes do colapso), parceira fora 6 h (R$ 1.728 a menos em consultas; no "antes" seriam R$ 18.000 de receita perdida), perda da região (R$ 3.000/h)
- [x] **§8 Tecnologias, custos e pessoal:**
  - [x] Conta de parceiro: hoje (0%), 20% e 40% (P1), com a coerência com o TTL de 1 h explicada
  - [x] Infraestrutura (preços oficiais da AWS em São Paulo), pessoal (Levels.fyi) e veredito: payback de **0,44 a 0,94 mês**; equilíbrio em **2,3%** de hit rate
  - [x] **Custo de auditoria** no ano 1 (R$ 42) e no ano 5 (R$ 352), por **cotação apresentada**, com o erro de unidade de 1,67×
- [x] Revisar as §1–4: versão 1.0, D11 a D15 em §1.4, **P5 a P7** em §1.5, **§3.3 hoje × depois medido**, **L5** em §4.7
- [x] Coerência conferida: todo caminho, link, teste, métrica e variável citados existem; os números do SAD são os padrões do `config.go`
- [x] Tamanho: a §4 foi enxugada de 5.064 para ~3.000 palavras (repetições com a §5.1 e a §6.2, exemplo JSON reduzido, L5 junto dos outros limites), sem tirar item exigido; o SAD ficou com ~9 mil palavras (~18 páginas) mais 4 diagramas, dentro da faixa do enunciado

### Decisões fixadas

Preencher conforme decidir na §4. O código tem que usar **exatamente** estes valores.

| Parâmetro | Valor | Onde está defendido |
|---|---|---|
| Biblioteca de circuit breaker | `github.com/sony/gobreaker/v2` | SAD §4.2 (D2) |
| Escopo do breaker | um por parceira (3 breakers) | SAD §4.2 (D3) |
| Falhas/janela para abrir | ≥ 50% de falha com no mínimo 10 requisições, contadores zerados a cada 30 s no estado fechado | SAD §4.2 (D4) |
| Tempo aberto | 15 s | SAD §4.2 (D4) |
| Requisições no meio aberto | 2 (2 sucessos fecham; 1 falha reabre) | SAD §4.2 (D4) |
| Timeout do cliente | por parceira: slow 2000 ms · flaky 500 ms · degrading 500 ms | SAD §4.1 (D1) |
| Estouro de timeout conta como falha? | sim; cancelamento pelo cliente não conta | SAD §4.1 (D1) |
| Granularidade do cache | por parceira | SAD §4.3 (D5) |
| TTL do cache | 1 h fresca; vencida servida só no fallback até 24 h; `EXPIRE` de 24 h | SAD §4.3 (D6) |
| Chave do cache (por extenso) | `quote:v1:{tenant_id}:{partner}:{hmac_sha256(documento\|ano_nascimento\|placa\|modelo\|ano_veiculo\|valor_centavos\|cobertura)}` | SAD §4.3 (D7) e README |
| Estratégia de fallback | resposta parcial; 503 de negócio só se nada estiver disponível | SAD §4.4 (D8) |
| Onde fica o fallback | novo método em `internal/quotation/service.go`; `Service.Quote` mantido | SAD §4.4 (D9) |
| Agregação | paralela no `QuoteWithFallback`; teto estrutural de 2,0 s (maior timeout) | SAD §4.8 (D16) |
| Campo/cabeçalho de degradação | `degraded` + `missing_partners` + `source`/`age_seconds` por cotação | SAD §4.4 (D10) e README |
| Hospedagem | nuvem pública, AWS `sa-east-1`; auditoria em S3 com Object Lock (proposta) | SAD §5.2 (D11) |
| Alertas | A1 breaker fora de fechado 10 min; A2 p95 > 80% do timeout; A3 Redis fora; A4 hit rate < 20% por 1 dia | SAD §6.2 (D13) |

---

## Entrega 2 — PoC

Depende da **1A**: nenhum número entra no código sem estar defendido no SAD.

### 2A. Mecanismos (um por vez, com o teste junto)

- [x] **Timeout** em `internal/partner/client.go` (`WithTimeouts`: o `http.Client` deixa de ter `Timeout` zero e passa a 2 s, o maior teto; cada chamada corta no teto da sua parceira; `TimedOut` e `CanceledByCaller` classificam o erro)
- [x] **Circuit breaker** em `internal/partner/breaker.go`: gobreaker v2.4.0, um por parceira, três estados nomeados (`StateClosed`, `StateHalfOpen`, `StateOpen`), e o estado aberto **não chama** a parceira (`ErrCircuitOpen`)
- [x] **Cache Redis** em `internal/quotecache/`: chave `quote:v1:{tenant_id}:{partner}:{hmac}` (`key.go`), `MGET` e `SET` com `EX` de 24 h (`redis.go`), versão na chave, 100 ms por operação
- [x] **Fallback** em `internal/quotation/service.go` (`QuoteWithFallback`): cotação fresca do cache → parceira pelo breaker → cotação vencida (até 24 h) → `missing_partners`; 503 de negócio quando nada está disponível
  - [x] `Service.Quote` **e** `NewAPI` mantêm o comportamento original (os dois testes originais que exigem 502 continuam verdes); o fallback entra por `QuoteWithFallback` e `WithFallback()`, ligados só no `main.go`
- [x] Contrato em `internal/quotation/request.go`: `degraded`, `missing_partners`, e `source`/`age_seconds`/`stale` por cotação (só campos novos)
- [x] Redis ligado no `docker-compose.yml`: só `REDIS_ADDR`, `CACHE_KEY_SECRET` e `depends_on` da `quotation-api`; perfis das parceiras intocados
- [x] Parâmetros de timeout e breaker em `internal/platform/config.go` (`PARTNER_TIMEOUTS`, `PARTNER_TIMEOUT_DEFAULT`, `BREAKER_*`), ligados em `cmd/quotation-api/main.go`
- [x] Parâmetros do cache em `internal/platform/config.go` (`REDIS_ADDR`, `REDIS_TIMEOUT`, `CACHE_FRESH_TTL`, `CACHE_MAX_AGE`, `CACHE_KEY_SECRET`)

**Conferência ponta a ponta (não é evidência; a evidência é a 2D):** `make down && make reproduce` com o 2A completo deu **100% de sucesso** no baseline e na carga, p95 sob carga de **203 ms** (contra 8,01 s no "antes") e o circuito da `partner-flaky` abrindo. As chaves no Redis não têm CPF nem placa legíveis e têm TTL de 24 h.

### 2B. Instrumentação

- [x] Métrica de **estado** e de **transição** do breaker por parceira (`internal/partner/metrics.go`)
- [x] Métrica de **hit/miss do cache** (`internal/quotation/telemetry.go`)
- [x] **Latência por parceira**: métrica nova, com `outcome` (inclui `circuit_open`, que a métrica de HTTP não vê)
- [x] Marcação no trace: **requisição curto-circuitada** pelo breaker
- [x] Marcação no trace: **resposta servida de cache**
- [x] **Nenhum CPF, placa ou `quote_id`** em span ou métrica: teste `TestNoPersonalDataInMetricsOrSpans` e conferência nos dados reais (420 traces e todos os rótulos do Prometheus)
- [x] Conferido no Prometheus (os 5 nomes abaixo) e no Jaeger (193 traces curto-circuitadas e 220 com cotação de cache, sem `HTTP POST` embaixo)
- [x] Testes que provam a emissão: `internal/partner/metrics_test.go` e `internal/quotation/telemetry_test.go`, com o OTel em memória (`internal/otelcheck/`)

**Nomes criados** (conferidos no Prometheus; têm que bater com o código, com o SAD §4.5 e com o README):

| Tipo | Nome no Prometheus | Nome no código | Atributos |
|---|---|---|---|
| gauge (0 fechado, 1 meio-aberto, 2 aberto) | `partner_breaker_state` | `partner.breaker.state` | `partner` |
| contador | `partner_breaker_transitions_total` | `partner.breaker.transitions` | `partner`, `from`, `to` |
| contador | `quote_cache_lookups_total` | `quote.cache.lookups` | `partner`, `result` (`hit`, `miss`, `stale`, `error`) |
| histograma (s) | `partner_quote_duration_seconds` | `partner.quote.duration` | `partner`, `outcome` (`success`, `timeout`, `error`, `circuit_open`) |
| span | `quote {parceira}` | — | `partner.name`, `quote.source`, `cache.result`, `breaker.short_circuited`, `quote.missing_reason` |
| atributos no span `POST /quotes` | — | — | `tenant.id`, `quote.degraded`, `quote.count`, `quote.from_cache`, `quote.missing_partners` |

> ⚠️ **Para a 2D:** com a carga padrão, as 5 cotações já estão no cache quando o circuito da `partner-flaky` abre, então **nenhuma requisição é curto-circuitada** e o trace da evidência 3 não aparece. Com `make load ARGS="-distinct 200"` (cotações novas) apareceram 193. A evidência 3 vai precisar dessa variação **declarada**. O ciclo completo do gauge também precisa de tráfego depois da abertura: o fechado → aberto → meio-aberto já apareceu sozinho, mas o meio-aberto → fechado exige 2 sucessos.

### 2C. Testes

- [x] Teste determinístico do **breaker abrindo**, sem `sleep`: `TestBreakerOpensOnTheFlakySequenceAndStopsCallingThePartner` usa as 60 primeiras respostas medidas da `partner-flaky` (smoke §2.2) e prova que abre na **requisição 12** e que, aberto, **não chama** a parceira. Mais 4 testes: timeout conta como falha, cancelamento não conta, 4xx não conta, breakers independentes por parceira
- [x] Teste do timeout do cliente (`internal/partner/timeout_test.go`) e da configuração (`internal/platform/config_resilience_test.go`)
- [x] Testes determinísticos do **cache e do fallback** (relógio fixo, cache em memória, sem `sleep`): `internal/quotation/fallback_test.go` (11 testes, incluindo isolamento entre corretoras, vencida só na falha, nada acima de 24 h e o 503) e `internal/quotecache/quotecache_test.go` (chave e TTL de 24 h com o relógio do miniredis)
- [ ] `make test` e `make smoke` verdes, incluindo os testes originais

### 2D. Evidências do "depois"

Tudo em `docs/evidencias/`, cada uma com **comando/consulta + artefato + legenda de uma linha**. Capturar **com o ambiente de pé**.

Índice completo, com comando, artefato e legenda de cada uma: [evidencias/depois.md](evidencias/depois.md).

> As evidências 1, 2, 4 e 7 e as complementares foram **refeitas depois do bônus** (agregação paralela, sessão 2); as versões em série ficaram com o sufixo `-serie`. As 3, 5 e 6 são da sessão 1 e continuam valendo (breaker e cache iguais).

- [x] 1. Relatório do `make reproduce` "depois", com a mesma carga → [reproduce-depois.txt](evidencias/reproduce-depois.txt): p95 sob carga **8,01 s → 202 ms**, sucesso **60% → 100%**, vazão 8,0 → 730 req/s; baseline 2,03 s → 1,68 s
- [x] 2. Saída do `make test` → [make-test.txt](evidencias/make-test.txt): 102 testes passando (64 originais, 38 novos), cada um marcado `[original]` ou `[novo]`, e o detector de corrida limpo
- [x] 3. Trace com breaker aberto → [trace-depois-breaker-aberto-rapido.json](evidencias/trace-depois-breaker-aberto-rapido.json): as duas parceiras curto-circuitadas, a slow do cache, **0,59 ms** e nenhum `HTTP POST` (variação declarada 4). Complemento da variação 1: [trace-depois-breaker-aberto.json](evidencias/trace-depois-breaker-aberto.json)
- [x] 4. Trace servido de cache → [trace-depois-cache.json](evidencias/trace-depois-cache.json) (execução padrão)
- [x] 5. Estado do breaker no tempo → [breaker-estado-depois.png](evidencias/breaker-estado-depois.png) + [breaker-transicoes-depois.txt](evidencias/breaker-transicoes-depois.txt) (variação declarada 2: sondas a cada 8 s)
- [x] 6. Hit rate do cache → [hit-rate-depois.png](evidencias/hit-rate-depois.png) (variação declarada 3: `corretora-b`, cache frio)
- [x] 7. p95 do `POST /quotes` → [p95-depois.png](evidencias/p95-depois.png) e [p95-antes.png](evidencias/p95-antes.png), com linha de referência de 9,7 s para a mesma escala
- [x] Extra: consultas por cotação entregue → [consultas-depois.txt](evidencias/consultas-depois.txt): **4,36 → 0,27**
- [x] Extra: nenhum dado pessoal nos dados reais → [sem-dado-pessoal-depois.txt](evidencias/sem-dado-pessoal-depois.txt)

**Números usados no SAD:** a RNF-01 a RNF-05 têm o "depois" medido (p95 202 ms; sucesso 100%; 0,27 consulta por cotação; vazão 730 req/s; chamadas com circuito aberto = 0, provado pelo trace da evidência 3). Os limiares de alerta da §6 podem partir do que se viu: a `partner-flaky` oscilou 10 vezes entre meio-aberto e aberto antes de fechar.

---

## Fechamento

Depende das duas entregas.

- [x] O `README.md` da raiz deixou de ser o enunciado original (que está em `docs/enunciado.md`) e passou a ser o README do processo
- [x] **README do processo** na raiz, com as cinco coisas (conferido: os 8 nomes de métrica e os 10 atributos declarados são achados no código Go; caminhos, links e variáveis existem):
  - [x] links para o SAD e as evidências
  - [x] como subir o ambiente e reproduzir
  - [x] o que foi implementado e o que ficou como proposta
  - [x] **a chave do cache por extenso** (o enunciado exige no SAD **e** no README)
  - [x] **o contrato da resposta degradada** (`degraded`, `missing_partners`, `source`, `age_seconds`, `stale`) e o **503 novo** (`no partner insurer available`): o enunciado manda documentar a mudança de contrato no README
  - [x] as variáveis de configuração novas (`PARTNER_TIMEOUTS`, `BREAKER_*`, `REDIS_*`, `CACHE_*`) e que `Service.Quote`/`NewAPI` sem `WithFallback()` mantêm o comportamento original por causa dos testes originais
  - [x] **limitações do ambiente documentadas** (o enunciado manda documentar no README em vez de contornar em silêncio): a carga padrão dura 11 s e as métricas são exportadas a cada 10 s, por isso a subida do hit rate não aparece com ela (evidência 6 veio da variação 3); com a carga padrão nenhuma requisição é curto-circuitada, porque as 5 cotações já estão no cache (evidência 3 veio da variação 4); o hit rate da carga padrão foi 91%, e não ~98%, porque o cache não guarda falha da `partner-flaky`
  - [x] nomes das métricas e dos atributos criados, com **os dois nomes** de cada métrica (o do código, `partner.breaker.state`, e o do Prometheus, `partner_breaker_state`): o avaliador procura o nome declarado no repositório, e o nome do Prometheus está nos comentários das constantes em `internal/partner/metrics.go` e `internal/quotation/telemetry.go`
  - [x] o que faria diferente com mais tempo
  - [x] um parágrafo com o "coração da solução": o que foi protegido, com o quê e quanto custou
- [x] **Coerência:** todo caminho, biblioteca, métrica e endpoint citado **existe** ou está marcado como **proposta**
- [x] **Coerência:** TTL, limiares e nomes de métrica **iguais** no SAD e no código
- [x] **Coerência:** hit rate da §8 compatível com o TTL da §4 e o pressuposto de recotação da §1
- [x] `docs/enunciado.md`, `docs/sad.md` e `docs/evidencias/` no lugar
- [ ] Push da `development` e merge na `main` pelo GitHub
- [ ] Fork **público**

---

## Extras (opcional)

Só depois de todo o obrigatório pronto. Não conta para nenhum critério de aceite.

- [x] **Paralelizar a agregação** em `internal/quotation/service.go` (`QuoteWithFallback`), sobrepondo as três parceiras em vez de somá-las; `Service.Quote` continua em série, como o teste original exige
  - [x] Testes: `TestTheFallbackPathQuotesThePartnersAtTheSameTime` (barreira: as três em voo juntas, sem `sleep`) e `TestTheParallelResponseIsTheSameWhoeverFinishesFirst`; detector de corrida limpo (no contêiner `golang:1.25`)
  - [x] Registrado no SAD como decisão **D16** (§4.8), com alternativa descartada e consequências ruins; teto estrutural de 3,0 s para 2,0 s, alvos da RNF-01 (≤ 2,0 s) e da RNF-04 (≥ 25 req/s) revistos; L1 agravado
  - [x] Evidências refeitas (sessão 2): baseline 2,04 s → **1,68 s**, carga 219 ms → **202 ms**, e o trace com as três chamadas começando no mesmo instante → [trace-depois-paralelo.json](evidencias/trace-depois-paralelo.json)

---

## Diário

| Data | O que foi feito | Commit |
|---|---|---|
| 2026-10-26 | Enunciado formatado em `docs/enunciado.md` | `5f98f8c` |
| 2026-10-26 | Ambiente: Go 1.25.7, Make 4.4.1, Docker Desktop, WSL 2 | — |
| 2026-10-26 | Relatório "antes" do `make reproduce` | `4787623` |
| 2026-10-26 | Roteiro de progresso (`docs/progresso.md`) | `1d273e4` |
| 2026-10-26 | Regras operacionais para agentes (`AGENTS.md` + `CLAUDE.md`) | `87dcd6a` |
| 2026-10-26 | Progresso dividido em Preparação, Entrega 1, Entrega 2, Fechamento e Extras | `2ecbe3c` |
| 2026-10-26 | Capturas do "antes": p95 no Prometheus e dois traces do Jaeger | `1ebe588` |
| 2026-10-26 | Leitura do código e dos documentos de apoio (`docs/notas-leitura.md`) | `0f9bad3` |
| 2026-10-26 | Estudo das opções das decisões D1 a D10 (`docs/estudo-decisoes.md`) | `dfeaa63` |
| 2026-10-26 | SAD §1 a §4 (`docs/sad.md`) e tabela de decisões fixadas | `5513fa4` |
| 2026-10-26 | Revisão antes do 2A: evidência das 545 consultas, gatilho do L1 corrigido, gobreaker v2.4.0 conferido na fonte | `6289ebc` |
| 2026-10-26 | 2A: timeout por parceira e circuit breaker por parceira, com testes; SAD §4.2 corrigido (abre na requisição 12 com o gobreaker) | `83b49f4` |
| 2026-10-26 | Revisão do 2A contra o enunciado: `http.Client.Timeout` deixa de ser zero; achado o teste do handler que exige 502 (SAD D9 atualizado) | `93c4aa0` |
| 2026-10-26 | 2A concluído: cache Redis por parceira, resposta parcial e contrato de degradação, com testes; Redis ligado no compose | `431315f` |
| 2026-10-26 | Revisão do 2A: corrigido o custo do Redis fora (5 tentativas de conexão, ~430 ms por operação → 1 tentativa e teto real de 100 ms; sem gravação depois de leitura falha); RF do SAD citam os testes pelo nome | `173116b` |
| 2026-10-26 | 2B: 4 métricas de negócio e 2 marcações no trace, com testes de emissão; nomes conferidos no Prometheus e no Jaeger; SAD §4.5 com os nomes reais | `2f34508` |
| 2026-10-26 | Revisão do 2B: nomes do Prometheus no código (busca estática), afirmação sem evidência tirada do SAD §4.5, itens de §6, 2D e README | `9689381` |
| 2026-10-26 | 2D: as 7 evidências do "depois", mais consultas por cotação e busca de dado pessoal, numa única sessão com 3 variações declaradas | `871a645` |
| 2026-10-26 | Revisão da 2D: evidência 3 trocada por um curto-circuito que responde rápido (0,59 ms, variação 4); limitações para o README; achado do breaker com pouco tráfego | `397c940` |
| 2026-10-26 | Estudo das decisões da 1B (D11 a D15), com preços da AWS, PTAX e salário com fonte | `5beb6d5` |
| 2026-10-26 | 1B: SAD §5 a §8 escritos e §1 a §4 revisados com o que foi medido (SAD versão 1.0) | `60f85cf` |
| 2026-10-27 | Extra: agregação paralela (D16), com testes, decisão no SAD §4.8 e evidências 1, 2, 4 e 7 refeitas | `9357469` |
| 2026-10-27 | Revisão geral antes do README: critérios de aceite conferidos um a um; referências de todos os documentos, testes, build, smoke e os 4 diagramas validados; duas frases do SAD que ainda descreviam o tempo em série corrigidas | `5377e2d` |
| 2026-10-27 | SAD enxugado para ~18 páginas (§4 de 5.064 para ~3.000 palavras) sem tirar item exigido; corrigidos dois números desatualizados da §4 (teto de ~3 s e a contagem de traces da busca de dado pessoal) | `07ac523` |
| 2026-10-27 | README do processo na raiz, no lugar do enunciado original, com as cinco coisas e o parágrafo do coração da solução | este commit |
