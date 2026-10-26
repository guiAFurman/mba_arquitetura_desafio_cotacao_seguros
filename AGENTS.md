# Instruções para agentes de IA

Regras operacionais deste repositório, para qualquer agente (Claude Code, Copilot, Cursor etc.) que trabalhe nele. O enunciado do desafio está em [docs/enunciado.md](docs/enunciado.md) e o roteiro de trabalho em [docs/progresso.md](docs/progresso.md).

## Antes de começar qualquer tarefa

1. Leia [docs/progresso.md](docs/progresso.md) para saber em que fase o trabalho está e o que já foi decidido.
2. Confira se a tarefa pedida bate com a fase atual. Se pular uma fase (por exemplo, codar antes de o SAD fixar o parâmetro), avise antes de seguir.
3. Use os valores da tabela **"Decisões fixadas"** do progresso. Não invente TTL, limiar ou nome de métrica que não esteja lá ou no `docs/sad.md`.

## Ao terminar cada passo

Sempre atualize [docs/progresso.md](docs/progresso.md) **no mesmo commit** do trabalho:

- marque `[x]` nos itens concluídos;
- preencha a tabela **"Decisões fixadas"** quando uma decisão for tomada no SAD;
- preencha a tabela de **métricas e atributos criados** quando instrumentar algo;
- acrescente uma linha no **Diário** (data, o que foi feito, commit). O hash do commit atual entra no diário do commit seguinte, porque ele só existe depois do commit.

## Git

- Todo commit vai na branch **`development`**. Nunca commitar, fazer push ou abrir merge para a `main`: quem leva para a `main` é o dono, pelo GitHub.
- Commits pequenos, um por passo, com mensagem em português no formato `tipo: descrição` (`docs:`, `feat:`, `test:`, `fix:`, `chore:`).
- Só fazer commit ou push quando o dono pedir.

## Regras do desafio que não podem ser quebradas

- **Não alterar** `cmd/partner-mock/` nem `cmd/loadgen/`, nem os perfis das parceiras no `docker-compose.yml`.
- **Não reescrever a base:** estender o código existente.
- **`make test` e `make smoke` sempre verdes.** Rodar os dois antes de cada commit que mexa em Go.
- **A chave de cache sempre contém o `tenant_id`.** Sem exceção (esse erro reprova a entrega sozinho).
- **Nunca** colocar CPF, placa ou `quote_id` em atributo de span ou de métrica.
- **Nunca** calcular nem inventar prêmio: ele vem sempre de uma parceira ou de um cache rastreável.
- **Nada de ficção:** todo arquivo, biblioteca, métrica ou endpoint citado no SAD ou no README tem que existir no repositório, ou estar marcado como **proposta**.
- **Fora de escopo:** retry com backoff, bulkhead, rate limiting, fila, autenticação e Kubernetes (podem ser citados no SAD, mas não implementados).

## Evidências

- Ficam em `docs/evidencias/`, cada uma com **comando ou consulta + artefato + legenda de uma linha**.
- Relatórios vão como **texto**, nunca como imagem.
- Gráficos e traces são capturados **com o ambiente de pé**: `make down` apaga o Prometheus e o Jaeger. Antes de qualquer `make down`, avise se ainda há evidência a capturar.
- Rodar a carga **sempre com o padrão** do `make reproduce` (10 de baseline, 200 com 50 em voo).

## Ambiente (Windows)

- O Go 1.25 e o GNU Make estão instalados via winget. Em um terminal aberto antes da instalação, recarregue o PATH ou abra um terminal novo.
- O Docker Desktop precisa estar com **"Engine running"** antes de `make up` ou `make reproduce`.
