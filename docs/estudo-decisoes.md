# Estudo das decisões do 1A

Material de apoio para escrever a §4 do SAD. Para cada decisão: o **contexto** (fatos com fonte), as **opções** com prós, contras e problemas, o que o **enunciado exige ou proíbe**, e uma **inclinação** preliminar. As escolhas finais estão no `docs/sad.md` (§4 e §5).

Fontes citadas: [enunciado.md](enunciado.md), [guia-sad.md](guia-sad.md), [roteiro-cenario-de-falha.md](roteiro-cenario-de-falha.md), [smoke-test-factibilidade.md](smoke-test-factibilidade.md), [notas-leitura.md](notas-leitura.md) e o código.

---

## Fatos que valem para todas as decisões

| Fato | Valor | Fonte |
|---|---|---|
| Latência da `partner-slow` | 1500 a 1700 ms, nunca falha | `docker-compose.yml`, `cmd/partner-mock/behavior.go` |
| Latência da `partner-flaky` | 150 a 200 ms, 40% de 503 | idem |
| Latência da `partner-degrading` | 120 a 160 ms + 300 ms por chamada simultânea acima de 5, até +6000 ms | idem |
| Rajada da `partner-flaky` | 9 falhas seguidas nas sequências 49 a 57; sob 34 simultâneas cai para 5 | `smoke-test-factibilidade.md` §2 |
| Custo | R$ 0,04 por consulta; 360.000 consultas por dia útil; ≈ R$ 317 mil/mês contra R$ 660 mil de receita | enunciado, "O cenário" |
| Teto comercial da cotação | 24 h | enunciado, "O cenário" |
| Validade técnica do mock | `valid_for_seconds` = 300 s | `cmd/partner-mock/config.go` |
| "Antes" medido | p95 8,01 s sob carga; 60% de sucesso; 8,0 req/s | [evidencias/antes.md](evidencias/antes.md) |
| Consultas compradas no "antes" | **545 consultas para 125 cotações entregues** (4,36 por cotação; o ideal seria 3) | [evidencias/consultas-antes.txt](evidencias/consultas-antes.txt) |
| Carga padrão | 5 cotações distintas, só `corretora-a`, 50 em voo, laço fechado | `cmd/loadgen/request.go`, roteiro §7 |

O número das 545 consultas é o argumento financeiro do tudo-ou-nada: **cada cotação que falha já pagou as parceiras chamadas antes da falha**. Nas 85 falhas, a `partner-slow` foi paga e jogada fora.

---

## D1. Timeout da chamada à parceira

**Contexto.** Obrigatório (enunciado, Entrega 2 §1): "Defina o Timeout do cliente em `internal/partner/client.go`, defenda o valor no SAD, e diga se o estouro conta como falha para o breaker". Sem timeout, a `partner-degrading` nunca vira sinal de falha, porque ela não falha: ela afunda.

### D1.1 Valor único ou por parceira

| Opção | Prós | Contras | Problemas |
|---|---|---|---|
| **A. Único, 2 s** | Simples: um parâmetro, um número para defender. Cobre a `partner-slow` com folga de 300 ms. | A `partner-degrading` só estoura com **11+** chamadas simultâneas (120 + 300 × 6 ≈ 1,9 s). Pior caso da cotação em série: **6 s** (3 × 2 s). | O breaker da degrading demora para enxergar a degradação: até lá, a plataforma já empilhou chamadas, que é o laço que o enunciado descreve ("vai acumulando chamadas abertas que a deixam mais lenta ainda"). |
| **B. Único, abaixo de 1,7 s** | Corta cedo a degrading. | **Mata a `partner-slow` em 100% das chamadas**: o breaker dela abre e não fecha nunca. | Inviável. Uma parceira saudável viraria "fora do ar" por decisão nossa. |
| **C. Por parceira, com uma regra** (ex.: latência máxima em repouso + 300 ms, arredondada para 500 ms → slow 2,0 s; flaky 0,5 s; degrading 0,5 s) | Cada parceira tem o teto que o comportamento dela justifica. A degrading estoura a partir de **7** simultâneas, logo depois do joelho de 5. Pior caso em série: **3,0 s**. A regra é uma só, então a defesa é uma só. | Três números para configurar e manter. Em produção, a "latência em repouso" muda; a regra precisa ser revista com dado real. | Se a degrading oscilar perto do limite, o breaker dela pode abrir e fechar com frequência. Isso aparece na métrica e precisa de alerta (§6). |

### D1.2 O estouro conta como falha do breaker?

| Opção | Prós | Contras |
|---|---|---|
| **Sim** | É o que faz a `partner-degrading` ser vista pelo breaker. O enunciado praticamente pede isso: "é essa decisão que faz a parceira lenta ser vista pelo mecanismo que você construiu". | Uma oscilação de rede curta pode abrir o circuito. |
| **Não** | O breaker só reage a erro explícito. | O breaker **nunca abre** para a degrading. O timeout corta a espera, mas a plataforma continua mandando chamadas para uma parceira afundada. |

**Detalhe:** cancelamento pelo **cliente** (a corretora desistiu da requisição) não deve contar como falha da parceira. O `client_test.go` já testa que o cancelamento interrompe a chamada (`TestQuoteHonoursCancellation`).

**Inclinação:** C + "sim, conta como falha".

---

## D2. Biblioteca de circuit breaker

**Contexto.** "A biblioteca de circuit breaker é decisão sua, e a escolha se defende no SAD" (enunciado). "A que expõe os três estados explicitamente vale mais que a mais estrelada" (guia-sad §5). Os testes têm que ser determinísticos, sem `sleep` (enunciado, Entrega 2 §5).

| Opção | Prós | Contras | Problemas |
|---|---|---|---|
| **A. `sony/gobreaker` v2** | A mais usada em Go. Estados nomeados (`StateClosed`, `StateHalfOpen`, `StateOpen`). `OnStateChange` dá a métrica de transição de graça. `IsSuccessful` permite classificar o erro (timeout = falha, cancelamento do cliente = não falha). `ReadyToTrip` recebe contadores (requisições, falhas, falhas consecutivas) e permite política por taxa. | A janela do estado fechado é **por tempo**, não por contagem: fixa (`Interval` zera os contadores) ou, desde a v2.4.0, deslizante em baldes de tempo (`BucketPeriod`). | **Sem relógio injetável:** testar a passagem de aberto para meio-aberto exige esperar o `Timeout` passar. O teste de **abertura** é determinístico (não depende de tempo); o de **reabertura** não. O enunciado só exige teste determinístico do breaker **abrindo**, então dá para conviver, mas tem que ser dito. |
| **B. `failsafe-go`** | Janela deslizante por contagem ou por tempo, mais próxima da simulação do `feasibility_test.go`. API moderna. | Traz um "canivete suíço" (retry, bulkhead, rate limiter) que está **fora de escopo** (enunciado: "Se você está escrevendo o quarto pattern, saiu do escopo"). Menos conhecida: a defesa no SAD fica mais longa. | Risco de o avaliador ver padrões fora de escopo importados, mesmo sem usar. |
| **C. Implementação própria** | Controle total: relógio injetável, teste de todas as transições sem `sleep`, política idêntica à do `feasibility_test.go`. | Mais código para escrever, testar e defender. "Biblioteca" no enunciado sugere usar uma pronta. | O próprio teste de factibilidade já é uma implementação de breaker; reescrever soa como reinventar. |

**Inclinação:** A, declarando no SAD a limitação de teste do meio-aberto e cobrindo o meio-aberto pela evidência de gráfico (evidência 5).

---

## D3. Escopo do breaker

**Contexto.** "Decida e defenda o escopo: um breaker por parceira, um por par (parceira, corretora), ou um só para todas. Um breaker global derruba a plataforma inteira porque uma das três afundou" (enunciado, Entrega 2 §1).

| Opção | Prós | Contras | Problemas |
|---|---|---|---|
| **A. Por parceira** (3 breakers) | Cada fornecedor independente é isolado. A falha de uma não afeta as outras. Métrica simples: 3 séries. | Uma corretora que gera muito tráfego ruim (por exemplo, cotações que a parceira rejeita) pode abrir o circuito para todas. | É o "limite conhecido" da corretora vizinha barulhenta, que o enunciado já manda declarar. |
| **B. Por (parceira, corretora)** | Isola corretoras entre si. | Em produção são "algumas centenas" de corretoras: centenas × 3 breakers, cada um com pouco tráfego, demorando muito para atingir o mínimo de requisições e abrir. **Uma parceira fora do ar é descoberta separadamente por cada corretora**, e cada uma paga as chamadas até o seu breaker abrir. | Cardinalidade: um rótulo `tenant_id` na métrica do breaker multiplica as séries. |
| **C. Global** (1 breaker) | Simples. | Uma parceira afundada derruba as três. Contraria o objetivo de um agregador. | O enunciado já antecipa que "dificilmente é o que você quer". |

**Inclinação:** A. A falha é da **parceira**, não da corretora: as condições comerciais diferem por corretora, mas a disponibilidade do motor de precificação é uma só.

---

## D4. Política de disparo e parâmetros do breaker

**Contexto.** Os parâmetros vêm do SAD: "quantas falhas em qual janela abrem, quanto tempo o circuito fica aberto, quantas requisições o meio aberto deixa passar, o que fecha e o que reabre" (enunciado). O `feasibility_test.go` já validou quatro políticas contra os defaults.

### D4.1 Regra de abertura

| Opção | Abre em | Prós | Contras |
|---|---|---|---|
| **3 consecutivas** | requisição 9 | Abre rápido. | Com 40% de falha, 3 seguidas acontecem por acaso com frequência (0,4³ = 6,4% a cada trinca): o circuito vai abrir demais. |
| **5 consecutivas** | requisição 42 | Mais tolerante. | Sob concorrência a rajada cai de 9 para 5 e o disparo vai para a requisição **104** (smoke §2.3). Frágil. |
| **≥ 50% de falha com mínimo de 10 requisições** | requisição 10 | Robusta à concorrência, porque olha a proporção e não a ordem. Combina com o perfil de 40% da flaky: abre na rajada, não no ruído normal. | Precisa de um **mínimo de requisições** para não abrir com 1 falha em 1 chamada. |
| **≥ 60% de falha em 20** | requisição 55 | Mais conservadora. | Demora mais; mais consultas pagas para parceira ruim. |

### D4.2 Os outros parâmetros

| Parâmetro | O que decide | Faixa razoável | Tensão |
|---|---|---|---|
| Janela do estado fechado (`Interval` no gobreaker) | de quanto em quanto tempo os contadores zeram | 30 a 60 s | Curta demais: nunca junta o mínimo de requisições. Longa demais: uma falha antiga pesa no presente. |
| Tempo aberto (`Timeout` no gobreaker) | quanto tempo a parceira **não é chamada** | 10 a 30 s | Curto: economiza pouco e testa a parceira de novo antes de ela se recuperar. Longo: a corretora fica mais tempo sem aquela parceira. **Precisa durar mais que o intervalo de exportação de métrica (10 s, `telemetry.go`) para aparecer no gráfico.** |
| Requisições no meio-aberto (`MaxRequests`) | quantos sucessos fecham o circuito | 2 ou 3 | Com 40% de falha, a chance de 2 sucessos seguidos é 36%; de 3, 21,6%. Com 3, o circuito quase nunca fecha durante a carga, e a evidência do ciclo completo fica difícil. O smoke validou 2 com recuperações (6 voltas a fechado na política de 50%/10). |

**Inclinação:** ≥ 50% com mínimo de 10 requisições, janela de 30 s, 15 s aberto, 2 sucessos para fechar.

---

## D5. Cache por parceira ou pela cotação agregada

**Contexto.** "Cache por parceira sobrevive a uma parceira fora, cache agregado economiza mais e vence inteiro de uma vez" (enunciado, Entrega 2 §2).

| Opção | Prós | Contras | Problemas |
|---|---|---|---|
| **A. Por parceira** (uma entrada por parceira e risco) | Com a flaky falhando 40%, as outras duas são gravadas mesmo assim. Combina com resposta parcial e com servir cotação antiga só da parceira que falhou. Cada entrada guarda o `quote_id` de origem: auditoria por cotação. | 3 leituras no Redis por requisição (dá para fazer em uma só, com `MGET`). | Entradas de idades diferentes na mesma resposta: a da slow pode ter 50 min e a da flaky, 2 min. A resposta precisa mostrar a idade **por cotação**. |
| **B. Agregado** (uma entrada por risco) | 1 leitura. Quando acerta, economiza as 3 consultas de uma vez. | **Só é gravado quando as três respondem.** Com 40% de falha da flaky, cerca de 40% das cotações nunca entram no cache. | Não sobrevive a uma parceira fora: se a flaky cair por 6 h, nenhuma cotação nova entra no cache nesse período. |

**Inclinação:** A.

---

## D6. TTL e política do cache

**Contexto.** "O TTL é decisão de negócio e tem o teto comercial de 24 horas [...] Parar antes é o esperado, mas diga por quê, e o número que você escolher é o mesmo que sustenta o hit rate da planilha da seção 8" (enunciado). A política também tem que dizer: o que acontece com a entrada quando o breaker abre, o que invalida antes da hora e como as entradas somem (prazo de descarte da LGPD).

### D6.1 TTL

| Opção | Prós | Contras | Problemas |
|---|---|---|---|
| **A. 300 s** (validade do mock) | Coerente com o que a parceira declara. Risco de preço vencido quase zero. | Hit rate baixo: só pega recotação dentro de 5 min. Economia pequena na §8. | O enunciado deixa claro que 300 s é o **TTL técnico do mock**, não o teto de negócio; usar isso exige fazer a conta da §8 com o hit rate que 300 s sustenta. |
| **B. 15 a 60 min** | Cobre a conversa de venda, em que o corretor recota a mesma placa várias vezes (enunciado: "o corretor recota a mesma placa várias vezes na mesma conversa"). Bem abaixo das 24 h. | O hit rate depende do pressuposto de recotação da §1, que **não tem dado medido**: é premissa. | Exibir uma cotação de 50 min como se fosse nova. A resposta precisa mostrar a idade. |
| **C. 24 h** | Máxima economia. | Cotação de quase 24 h apresentada como normal. Se a parceira mudar o preço, o corretor vende o que não será honrado. | O enunciado: "maior que isso não é cache agressivo, é cotação que ninguém honra". No limite, 24 h ainda é permitido, mas sem margem. |

**Observação sobre o hit rate:** a recotação **entre corretoras diferentes** ("o cliente pede a mesma cotação em mais de uma corretora", enunciado) **não gera acerto de cache**, porque a chave tem o `tenant_id` e o prêmio é diferente por corretora. Só conta a recotação **na mesma corretora**. Isso limita o hit rate que o SAD pode prometer.

### D6.2 Cotação vencida quando o breaker abre (*stale-if-error*)

| Opção | Prós | Contras |
|---|---|---|
| **Não servir vencida** | Simples; nada velho chega à corretora. | Quando a parceira cai, aquela parceira some da resposta mesmo havendo uma cotação de 2 h atrás. |
| **Servir vencida até 24 h, marcada** (TTL "macio" para servir normal, TTL "duro" de 24 h no Redis) | Aproveita o teto comercial só na emergência. A corretora vê a idade e decide. O TTL duro do Redis vira o **mecanismo de descarte** da LGPD. | Duas idades para explicar (fresca e vencida). Mais um caminho de código e de teste. |

### D6.3 Invalidação antes da hora e descarte

| Mecanismo | Situação | Observação |
|---|---|---|
| Só o TTL | padrão | É um dos 4 limites conhecidos que o enunciado manda declarar ("o TTL como única forma de invalidar o cache"). |
| Prefixo de versão na chave (`v1`, `v2`) | mudança de contrato ou de tabela de preço | Troca a versão e tudo o que é antigo deixa de ser lido; o TTL limpa depois. |
| Apagar por corretora | corretora sai da plataforma ou pede eliminação (LGPD) | Exige conseguir achar as chaves de uma corretora: o `tenant_id` no começo da chave permite `SCAN` por prefixo. Pode ficar como **proposta**. |
| Descarte LGPD | prazo máximo de guarda | `EXPIRE` do Redis no TTL duro. **Não depende** do Redis ser efêmero no compose (enunciado: "prazo de descarte de dado pessoal é obrigação da LGPD, não configuração de container"). |

**Inclinação:** TTL fresco de 1 h, vencida servida só no fallback até 24 h, `EXPIRE` de 24 h como descarte, versão na chave.

---

## D7. Chave do cache

**Contexto.** "Ela precisa conter, no mínimo, a corretora e a identificação normalizada do risco cotado" e "chave de cache sem `tenant_id` reprova" (enunciado). A corretora viaja no **cabeçalho**, não no corpo (enunciado, "Dicas finais"). O prêmio do mock depende do **corpo inteiro** (`behavior.go`).

### D7.1 O que identifica o risco

| Opção | Prós | Contras | Problemas |
|---|---|---|---|
| **A. Só placa** | Simples. | Dois motoristas diferentes com o mesmo carro recebem a mesma cotação. | **Erro de preço**: o prêmio depende do motorista, da cobertura e do valor do veículo. |
| **B. Todos os campos que mudam o prêmio**, normalizados por `Request.Normalize()`: documento, ano de nascimento, placa, modelo, ano do veículo, valor e cobertura | Correto: qualquer mudança no risco é outra chave. Reaproveita a normalização que já existe. | Chave longa. | Se o contrato ganhar um campo novo e ele não entrar na chave, volta o erro de preço. A versão na chave ajuda. |

### D7.2 CPF e placa em texto aberto na chave?

| Opção | Prós | Contras |
|---|---|---|
| **Em texto aberto** (`...:12345678901:ABC1D23:...`) | Fácil de depurar. | CPF e placa aparecem em qualquer ferramenta que liste chaves do Redis, em log de comando lento etc. Vai contra a minimização da LGPD. |
| **SHA-256 dos campos** | Nenhum dado pessoal legível na chave. | CPF tem pouca entropia: dá para descobrir o CPF testando todos os candidatos contra o hash. |
| **HMAC-SHA256 com segredo da plataforma** | Mesmo efeito do hash, e sem o segredo não dá para reverter por força bruta. Pseudonimização defensável perante o encarregado de dados. | Um segredo a mais para gerenciar; se trocar, o cache inteiro vira miss (o que também é um jeito de invalidar tudo). |

**Formato candidato:**

```text
quote:v1:{tenant_id}:{partner}:{hmac_sha256(documento|ano_nascimento|placa|modelo|ano_veiculo|valor_centavos|cobertura)}
```

**Inclinação:** B + HMAC, com `tenant_id` em texto aberto (ele não é dado pessoal e permite apagar por corretora).

---

## D8. Estratégia de fallback

**Contexto.** "Três saídas são legítimas, e você escolhe uma e defende" (enunciado, Entrega 2 §3). Em qualquer uma, "a corretora tem que saber que aquilo é degradado" e "prêmio inventado continua proibido". A ordenação por prêmio continua valendo na resposta degradada (enunciado, "Outras regras").

| Opção | Prós | Contras | Problemas |
|---|---|---|---|
| **A. Resposta parcial** | A corretora continua vendendo com 2 de 3 propostas. Sobe a taxa de sucesso (hoje 60%) sem inventar nada. Combina com cache por parceira. **Não desperdiça** as consultas já pagas (o problema das 545 consultas). | A corretora pode não ver a proposta mais barata, que estava justamente na parceira que faltou. | Se **nenhuma** parceira responder e não houver cache, sobra uma resposta vazia: precisa de um erro de negócio para esse caso. |
| **B. Cotação anterior do cache** | Resposta completa, com as 3 parceiras. | Depende de ter cache: a **primeira** cotação de um risco nunca tem fallback. Com cache por parceira, é praticamente a A com outro nome. | Cotação antiga apresentada como resposta inteira; a idade tem que estar visível. |
| **C. Recusa explícita** | Honesta quando o produto exige as 3 propostas (por exemplo, por regra de comparação justa para o consumidor). | Não melhora a taxa de sucesso; só troca um 502 genérico por um erro de negócio claro. | Joga fora as respostas já pagas. Não resolve o problema financeiro. |

**Inclinação:** A, usando a cotação vencida do cache (D6.2) para preencher a parceira que falhou quando ela existir, e recusa explícita só quando **nada** estiver disponível.

> **Atenção à regra "escolha uma":** a combinação acima precisa ser apresentada no SAD como **uma** estratégia (resposta parcial) com uma **política de cache** (servir vencida na falha), não como três estratégias ao mesmo tempo. O caso "nada disponível" é a borda da resposta parcial, não uma segunda estratégia.

---

## D9. Onde o fallback vive no código

**Contexto.** `TestOnePartnerDownBringsDownTheWholeRequest` exige que `Service.Quote` devolva erro quando uma parceira falha. A regra "Não quebre o que já passa" é inegociável ([notas-leitura.md §1](notas-leitura.md)).

| Opção | Prós | Contras | Problemas |
|---|---|---|---|
| **A. Novo método no `Service`** (por exemplo, um que agrega com fallback), usado pelo handler; `Service.Quote` fica como está | Testes antigos continuam verdes. Tudo continua em `service.go`, como o enunciado indica ("CACHE e FALLBACK aqui"). | `Service.Quote` vira código que só os testes usam. | Precisa ser declarado no README: o comportamento antigo foi mantido para preservar os testes. |
| **B. Novo tipo que envolve o `Service`** (pacote novo) | Separação clara. | Afasta o fallback do `service.go`. | Mesma questão do código antigo sem uso. |
| **C. Mudar `Service.Quote` e o teste** | Código mais limpo. | **Viola a regra.** | Conta contra a entrega. |

**Inclinação:** A.

---

## D10. Como a resposta diz que está degradada

**Contexto.** "Um campo booleano, um bloco de metadados, um cabeçalho: escolha, documente no README do processo e justifique no SAD" (enunciado, "O contrato de hoje"). O contrato atual é `Response{tenant_id, quotes, elapsed_ms}` em `request.go`.

| Opção | Prós | Contras |
|---|---|---|
| **A. Só cabeçalho** (`X-Quote-Degraded: true`) | Não muda o corpo. | Fácil de perder: muitos clientes não leem cabeçalho. Não cabe o detalhe (qual parceira faltou, idade da cotação). |
| **B. Só booleano** (`"degraded": true`) | Simples, compatível com quem ignora campo novo. | Não diz **o quê** degradou. |
| **C. Booleano + detalhe** (`degraded`, `missing_partners` com motivo, e em cada cotação `source` e `age_seconds`) | A corretora sabe exatamente o que faltou e o que é antigo. Suporta a auditoria: cada cotação de cache carrega o `quote_id` original e a idade. Só **acrescenta** campos: clientes antigos continuam funcionando. | Contrato maior para documentar. |

**Inclinação:** C.

---

## Problemas transversais (não são decisões, mas afetam várias)

1. **A carga padrão pode não abrir o breaker no "depois".** Com cache por parceira e só 5 cotações distintas, depois que cada uma é gravada quase nenhuma chamada chega às parceiras. O breaker da flaky pode não juntar o mínimo de requisições para abrir. A evidência 5 (estado do breaker no tempo) pode precisar de uma **variação declarada** da carga, como `-distinct 200`, que o enunciado permite desde que declarada. O teste determinístico continua provando a abertura.
2. **Estouro no cache frio.** Com o cache vazio e muitas requisições de riscos diferentes ao mesmo tempo (por exemplo, logo depois de perder o Redis), todas são *miss* e vão às parceiras juntas, e a `partner-degrading` afunda (é o cenário (a) da §7 do guia em miniatura). Na carga padrão isso quase não aparece: as 10 requisições do baseline gravam as 5 cotações distintas antes da rajada de 50. Agrupar chamadas iguais (*single-flight*) resolveria, mas é concorrência além do que o desafio pede: fica como **limite conhecido** ou proposta.
3. **O "depois" vai fazer a degrading parecer pior.** O laço fechado acelera quando a latência cai (roteiro §7). Comparar taxa de sucesso, p95 da ponta e vazão; não a latência isolada da degrading.
4. **O hit rate da carga (~98%) não vai para a §8.** O da §8 sai do pressuposto de recotação da §1 (enunciado, "Dicas finais").

---

## Resumo das inclinações

| # | Decisão | Inclinação preliminar |
|---|---|---|
| D1 | Timeout | Por parceira (slow 2,0 s; flaky 0,5 s; degrading 0,5 s); estouro conta como falha; cancelamento do cliente não conta |
| D2 | Biblioteca | `sony/gobreaker` v2 |
| D3 | Escopo | Por parceira |
| D4 | Política | ≥ 50% de falha com mínimo de 10 requisições; janela de 30 s; 15 s aberto; 2 sucessos para fechar |
| D5 | Granularidade do cache | Por parceira |
| D6 | TTL e política | 1 h fresca; vencida até 24 h só no fallback; `EXPIRE` de 24 h como descarte; versão na chave |
| D7 | Chave | `quote:v1:{tenant_id}:{partner}:{hmac_sha256(campos normalizados)}` |
| D8 | Fallback | Resposta parcial; recusa explícita (503) só se nada estiver disponível |
| D9 | Onde fica o fallback | Novo método no `Service`, mantendo `Service.Quote` |
| D10 | Contrato | `degraded` + `missing_partners` + `source`/`age_seconds` por cotação |
