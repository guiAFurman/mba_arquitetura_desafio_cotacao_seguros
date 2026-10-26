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

**Status atual:** Preparação e 1A concluídos ([sad.md](sad.md) §1 a §4). **2A em andamento:** timeout e circuit breaker prontos; próximo passo é o **cache Redis** e depois o **fallback**.

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

- [ ] **§5 Implementação:** mecanismo → arquivo real; decisão de **hospedagem** (on-premise, cloud ou híbrido) no formato de decisão
- [ ] **§6 Operação e mudanças:**
  - [ ] **3 alertas** (métrica, limiar, ação)
  - [ ] **1 runbook** completo, do sintoma ao escalonamento
  - [ ] Caminho de mudança do TTL
- [ ] **§7 Recuperação de desastres:**
  - [ ] RTO e RPO por classe de dado
  - [ ] **3 cenários** (efeito no cliente, custo do modo degradado, caminho de volta): Redis perdido, parceira fora por 6 h, perda do site ou da região
- [ ] **§8 Tecnologias, custos e pessoal:**
  - [ ] Conta de parceiro: hoje, hit rate 0, e ≥ 2 hit rates que o TTL sustente
  - [ ] Infraestrutura, pessoal e veredito de payback
  - [ ] **Custo de auditoria** no ano 1 e no ano 5, por **cotação apresentada**
- [ ] Revisar as §1–4 com o que a implementação provou

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
| Campo/cabeçalho de degradação | `degraded` + `missing_partners` + `source`/`age_seconds` por cotação | SAD §4.4 (D10) e README |
| Hospedagem | — | SAD §5 |

---

## Entrega 2 — PoC

Depende da **1A**: nenhum número entra no código sem estar defendido no SAD.

### 2A. Mecanismos (um por vez, com o teste junto)

- [x] **Timeout** em `internal/partner/client.go` (`WithTimeouts`, por parceira; `TimedOut` e `CanceledByCaller` classificam o erro)
- [x] **Circuit breaker** em `internal/partner/breaker.go`: gobreaker v2.4.0, um por parceira, três estados nomeados (`StateClosed`, `StateHalfOpen`, `StateOpen`), e o estado aberto **não chama** a parceira (`ErrCircuitOpen`)
- [ ] **Cache Redis** com chave contendo `tenant_id`, e política de invalidação implementada
- [ ] **Fallback** em `internal/quotation/service.go`, refletido no `Response` de `internal/quotation/request.go`
- [x] Parâmetros de timeout e breaker em `internal/platform/config.go` (`PARTNER_TIMEOUTS`, `PARTNER_TIMEOUT_DEFAULT`, `BREAKER_*`), ligados em `cmd/quotation-api/main.go`
- [ ] Parâmetros do cache (Redis, TTL, segredo do HMAC) em `internal/platform/config.go`

> ⚠️ **Estado intermediário:** com o breaker ligado e o handler ainda no `Service.Quote` original (tudo-ou-nada), um circuito aberto vira **502 rápido**. Na carga padrão, o sucesso caiu para 3% (p95 2,28 s). Isso é esperado até o fallback entrar; **não serve como evidência do "depois"**.

### 2B. Instrumentação

- [ ] Métrica de **estado/transição do breaker** por parceira
- [ ] Métrica de **hit/miss do cache** (permite calcular o hit rate em PromQL)
- [ ] **Latência por parceira** (nova ou reaproveitando `http_client_request_duration_seconds`, declarado)
- [ ] Marcação no trace: **requisição curto-circuitada** pelo breaker
- [ ] Marcação no trace: **resposta servida de cache**
- [ ] Conferir que **nenhum CPF, placa ou `quote_id`** aparece em span ou métrica
- [ ] Conferir no Prometheus e no Jaeger que tudo aparece

**Nomes criados** (têm que bater com o código e com o README):

| Tipo | Nome | Atributos |
|---|---|---|
| — | — | — |

### 2C. Testes

- [x] Teste determinístico do **breaker abrindo**, sem `sleep`: `TestBreakerOpensOnTheFlakySequenceAndStopsCallingThePartner` usa as 60 primeiras respostas medidas da `partner-flaky` (smoke §2.2) e prova que abre na **requisição 12** e que, aberto, **não chama** a parceira. Mais 4 testes: timeout conta como falha, cancelamento não conta, 4xx não conta, breakers independentes por parceira
- [x] Teste do timeout do cliente (`internal/partner/timeout_test.go`) e da configuração (`internal/platform/config_resilience_test.go`)
- [ ] Teste determinístico do **cache ou do fallback**
- [ ] `make test` e `make smoke` verdes, incluindo os testes originais

### 2D. Evidências do "depois"

Tudo em `docs/evidencias/`, cada uma com **comando/consulta + artefato + legenda de uma linha**. Capturar **com o ambiente de pé**.

- [ ] 1. Relatório do `make reproduce` "depois", com a mesma carga → `reproduce-depois.txt`
- [ ] 2. Saída do `make test` → `make-test.txt`
- [ ] 3. Trace com breaker aberto (export JSON do Jaeger)
- [ ] 4. Trace servido de cache (export JSON do Jaeger)
- [ ] 5. Gráfico do estado do breaker no tempo + PromQL + janela
- [ ] 6. Gráfico do hit rate do cache + PromQL + janela
- [ ] 7. Gráficos do p95 do `POST /quotes`, "antes" e "depois", + PromQL + janela

---

## Fechamento

Depende das duas entregas.

- [ ] ⚠️ O `README.md` da raiz **ainda é o enunciado original**: ele é substituído pelo README do processo (o enunciado já está em `docs/enunciado.md`)
- [ ] **README do processo** na raiz, com as cinco coisas:
  - [ ] links para o SAD e as evidências
  - [ ] como subir o ambiente e reproduzir
  - [ ] o que foi implementado e o que ficou como proposta
  - [ ] nomes das métricas e dos atributos criados
  - [ ] o que faria diferente com mais tempo
  - [ ] um parágrafo com o "coração da solução": o que foi protegido, com o quê e quanto custou
- [ ] **Coerência:** todo caminho, biblioteca, métrica e endpoint citado **existe** ou está marcado como **proposta**
- [ ] **Coerência:** TTL, limiares e nomes de métrica **iguais** no SAD e no código
- [ ] **Coerência:** hit rate da §8 compatível com o TTL da §4 e o pressuposto de recotação da §1
- [ ] `docs/enunciado.md`, `docs/sad.md` e `docs/evidencias/` no lugar
- [ ] Push da `development` e merge na `main` pelo GitHub
- [ ] Fork **público**

---

## Extras (opcional)

Só depois de todo o obrigatório pronto. Não conta para nenhum critério de aceite.

- [ ] **Paralelizar a agregação** em `internal/quotation/service.go`, sobrepondo as três parceiras em vez de somá-las
  - [ ] Registrar no SAD como decisão (contexto, opções, escolha, consequências), porque muda o p95
  - [ ] Refazer as evidências de p95 se for implementado

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
| 2026-10-26 | 2A: timeout por parceira e circuit breaker por parceira, com testes; SAD §4.2 corrigido (abre na requisição 12 com o gobreaker) | este commit |
