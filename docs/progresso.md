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

**Status atual:** Preparação quase concluída (falta a leitura). Próximo passo: **1A**.

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

- [ ] Ler [roteiro-cenario-de-falha.md](roteiro-cenario-de-falha.md)
- [ ] Ler o código: `internal/partner/client.go`, `internal/quotation/{service,handler,request}.go`, `cmd/quotation-api/main.go`, `internal/platform/config.go`
- [ ] Ler `cmd/partner-mock/feasibility_test.go` (molde dos testes determinísticos)
- [ ] Ler [guia-sad.md](guia-sad.md) e [smoke-test-factibilidade.md](smoke-test-factibilidade.md)

---

## Entrega 1 — SAD (`docs/sad.md`)

O SAD é escrito para três leitores: o **CTO** (quer a conta), **quem opera às 3h** (quer o runbook) e o **encarregado de dados** (quer rastreabilidade e isolamento). Regras: tudo rastreável a arquivo real ou marcado como **proposta**, números em vez de adjetivos, diagramas C4 em Mermaid com setas rotuladas.

### 1A. Seções 1 a 4 — decidir antes de codar

- [ ] **§1 Introdução:** propósito, escopo, restrições × decisões
  - [ ] **3 pressupostos** com valor, origem e consequência se falso (incluir o **pressuposto de recotação**, que gera o hit rate da §8)
- [ ] **§2 Visão geral:**
  - [ ] C4 nível 1 (contexto)
  - [ ] C4 nível 2 "antes"
  - [ ] C4 nível 2 "depois"
  - [ ] Delta em lista
- [ ] **§3 Requisitos:**
  - [ ] **6 RF** com identificador e frase testável (≥ 2 criados pela arquitetura)
  - [ ] **5 RNF** com métrica, valor, unidade, método e origem (≥ 3 com "hoje" medido, usando os números da Preparação)
- [ ] **§4 Detalhamento:** cada decisão no formato *contexto → opções → escolha → consequências*, com alternativa descartada e consequências ruins
  - [ ] Decisão do **circuit breaker** (biblioteca, escopo, limiares, janela, tempo aberto, meio aberto)
  - [ ] Decisão do **timeout** (valor e se o estouro conta como falha)
  - [ ] Decisão do **cache** (TTL, granularidade, **chave por extenso com `tenant_id`**, invalidação, descarte LGPD, comportamento com o breaker aberto)
  - [ ] Decisão do **fallback** (parcial, cotação anterior ou recusa) e mudança no contrato de `Response`
  - [ ] C4 nível 3 da fatia implementada
  - [ ] **4 limites conhecidos** com gatilho e efeito na corretora: concorrência por parceira, breaker em memória, TTL como única invalidação, corretora vizinha barulhenta

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
| Hospedagem | — | SAD §5 |

---

## Entrega 2 — PoC

Depende da **1A**: nenhum número entra no código sem estar defendido no SAD.

### 2A. Mecanismos (um por vez, com o teste junto)

- [ ] **Timeout** em `internal/partner/client.go`
- [ ] **Circuit breaker** em `internal/partner/` (ou pacote novo em `internal/`): três estados nomeados no código, e o estado aberto **não chama** a parceira
- [ ] **Cache Redis** com chave contendo `tenant_id`, e política de invalidação implementada
- [ ] **Fallback** em `internal/quotation/service.go`, refletido no `Response` de `internal/quotation/request.go`
- [ ] Novos parâmetros em `internal/platform/config.go`, ligados em `cmd/quotation-api/main.go`

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

- [ ] Teste determinístico do **breaker abrindo** (rajada da `partner-flaky` nas sequências 49–57), sem `sleep`
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
| 2026-10-26 | Capturas do "antes": p95 no Prometheus e dois traces do Jaeger | este commit |
