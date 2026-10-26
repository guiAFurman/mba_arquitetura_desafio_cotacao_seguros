# Progresso do desafio

Roteiro de trabalho para não se perder entre sessões. O enunciado completo está em [enunciado.md](enunciado.md); este arquivo só acompanha **o que já foi feito, o que falta e em que ordem**.

**Fluxo de branches:** todo o trabalho e os commits vão na `development`. A `main` só recebe o merge, feito pelo GitHub, quando a entrega estiver pronta. O avaliador lê **apenas a `main`**.

Legenda: `[x]` feito · `[ ]` pendente · ⚠️ atenção

---

## Fase 0 — Ambiente da máquina

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

---

## Fase 1 — Reproduzir o "antes"

- [x] `make down && make reproduce` no código original → [evidencias/reproduce-antes.txt](evidencias/reproduce-antes.txt)
- [ ] ⚠️ Gráfico do p95 do "antes" no Prometheus, **capturado com o ambiente de pé** (some no `make down`)
- [ ] ⚠️ Trace da cascata em série no Jaeger (opcional, mas útil para a seção 2 do SAD)
- [ ] Ler [roteiro-cenario-de-falha.md](roteiro-cenario-de-falha.md)

**Números medidos (2026-10-26):**

| | Baseline (10 req, 1 por vez) | Carga (200 req, 50 em voo) |
|---|---|---|
| Sucesso | 5/10 (50%) | 120/200 (60%) |
| p50 | 1,89 s | 6,11 s |
| p95 | 2,03 s | 8,01 s |
| Vazão | 0,5 req/s | 8,0 req/s |
| Falhas | 5 × 502 `partner-flaky` | 80 × 502 `partner-flaky` |

---

## Fase 2 — Entender antes de decidir

- [ ] Ler o código: `internal/partner/client.go`, `internal/quotation/{service,handler,request}.go`, `cmd/quotation-api/main.go`, `internal/platform/config.go`
- [ ] Ler `cmd/partner-mock/feasibility_test.go` (molde dos testes determinísticos)
- [ ] Ler [guia-sad.md](guia-sad.md) e [smoke-test-factibilidade.md](smoke-test-factibilidade.md)

---

## Fase 3 — SAD, seções 1 a 4 (decidir antes de codar)

Arquivo: `docs/sad.md`

- [ ] **Seção 1 — Introdução:** propósito, escopo, restrições × decisões, **3 pressupostos** (valor, origem, consequência se falso), incluindo o **pressuposto de recotação**
- [ ] **Seção 2 — Visão geral:** C4 nível 1, C4 nível 2 "antes", C4 nível 2 "depois", delta em lista
- [ ] **Seção 3 — Requisitos:** **6 RF** (≥ 2 criados pela arquitetura) e **5 RNF** (≥ 3 com "hoje" medido, usando os números da Fase 1)
- [ ] **Seção 4 — Detalhamento:**
  - [ ] Decisão do **circuit breaker** (biblioteca, escopo, limiares, janela, tempo aberto, meio aberto)
  - [ ] Decisão do **timeout** (valor e se o estouro conta como falha)
  - [ ] Decisão do **cache** (TTL, granularidade, **chave escrita por extenso com `tenant_id`**, invalidação, descarte LGPD, comportamento com breaker aberto)
  - [ ] Decisão do **fallback** (parcial, cotação anterior ou recusa) e mudança no contrato de `Response`
  - [ ] C4 nível 3 da fatia implementada
  - [ ] **4 limites conhecidos** (concorrência por parceira, breaker em memória, TTL como única invalidação, corretora vizinha barulhenta)

**Decisões fixadas** (preencher conforme decidir; o código tem que usar exatamente estes valores):

| Parâmetro | Valor | Onde está defendido |
|---|---|---|
| Biblioteca de circuit breaker | — | SAD §4 |
| Escopo do breaker | — | SAD §4 |
| Falhas/janela para abrir | — | SAD §4 |
| Tempo aberto | — | SAD §4 |
| Requisições no meio aberto | — | SAD §4 |
| Timeout do cliente | — | SAD §4 |
| Estouro de timeout conta como falha? | — | SAD §4 |
| TTL do cache | — | SAD §4 |
| Chave do cache (por extenso) | — | SAD §4 e README |
| Estratégia de fallback | — | SAD §4 |
| Campo/cabeçalho de degradação | — | SAD §4 e README |

---

## Fase 4 — PoC (um mecanismo por vez, com teste junto)

- [ ] **Timeout + circuit breaker** em `internal/partner/client.go` (estado aberto **não chama** a parceira)
  - [ ] Teste determinístico do breaker abrindo (rajada da `partner-flaky` nas sequências 49–57)
- [ ] **Cache Redis** com chave contendo `tenant_id`
- [ ] **Fallback** em `internal/quotation/service.go`, refletido em `internal/quotation/request.go`
  - [ ] Teste determinístico do cache ou do fallback
- [ ] Novos parâmetros em `internal/platform/config.go` e ligação em `cmd/quotation-api/main.go`
- [ ] `make test` e `make smoke` verdes
- [ ] *(Bônus, opcional)* paralelizar a agregação

---

## Fase 5 — Instrumentação

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

---

## Fase 6 — Evidências do "depois"

Tudo em `docs/evidencias/`, cada uma com **comando/consulta + artefato + legenda de uma linha**. Capturar **com o ambiente de pé**.

- [ ] 1. Relatório do `make reproduce` "depois" (mesma carga) → `reproduce-depois.txt`
- [ ] 2. Saída do `make test` → `make-test.txt`
- [ ] 3. Trace com breaker aberto (export JSON do Jaeger)
- [ ] 4. Trace servido de cache (export JSON do Jaeger)
- [ ] 5. Gráfico do estado do breaker no tempo + PromQL + janela
- [ ] 6. Gráfico do hit rate do cache + PromQL + janela
- [ ] 7. Gráficos do p95 do `POST /quotes`, "antes" e "depois", + PromQL + janela

---

## Fase 7 — SAD, seções 5 a 8 (com os números medidos)

- [ ] **Seção 5 — Implementação:** mecanismo → arquivo real; decisão de **hospedagem** (on-premise, cloud ou híbrido)
- [ ] **Seção 6 — Operação:** **3 alertas** (métrica, limiar, ação), **1 runbook** completo, caminho de mudança do TTL
- [ ] **Seção 7 — DR:** RTO/RPO por classe de dado; **3 cenários** (Redis perdido, parceira fora 6 h, perda do site/região)
- [ ] **Seção 8 — Custos:** conta de parceiro (hoje, hit rate 0, ≥ 2 hit rates sustentados pelo TTL), infra, pessoal, payback, **custo de auditoria ano 1 e ano 5** por cotação apresentada
- [ ] Revisar as seções 1 a 4 com o que a implementação provou

---

## Fase 8 — Fechamento

- [ ] README do processo na raiz (links, como subir, implementado × proposta, nomes de métricas/atributos, o que faria diferente, parágrafo do "coração da solução")
- [ ] Revisão de coerência: todo caminho, biblioteca, métrica e endpoint citado **existe** ou está marcado como **proposta**
- [ ] Revisão de coerência: TTL, limiares e nomes de métrica **iguais** no SAD e no código
- [ ] Revisão de coerência: hit rate da §8 compatível com o TTL da §4 e o pressuposto de recotação da §1
- [ ] `docs/enunciado.md`, `docs/sad.md` e `docs/evidencias/` no lugar
- [ ] Push da `development` e merge na `main` pelo GitHub
- [ ] Fork **público**

---

## Diário

| Data | O que foi feito | Commit |
|---|---|---|
| 2026-10-26 | Enunciado formatado em `docs/enunciado.md` | `5f98f8c` |
| 2026-10-26 | Ambiente: Go 1.25.7, Make 4.4.1, Docker Desktop, WSL 2 | — |
| 2026-10-26 | Relatório "antes" do `make reproduce` | `4787623` |
