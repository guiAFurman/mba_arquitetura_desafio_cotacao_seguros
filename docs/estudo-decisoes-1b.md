# Estudo das decisões da 1B (SAD §5 a §8)

Continuação de [estudo-decisoes.md](estudo-decisoes.md). As decisões D1 a D10 (timeout, breaker, cache, fallback) já estão no SAD. Aqui ficam as que as seções 5 a 8 introduzem, e os números de fora do repositório, cada um com fonte.

---

## Fontes externas usadas (consultadas em 2026-10-26)

| Dado | Valor | Fonte |
|---|---|---|
| S3 Standard, São Paulo | US$ 0,0405 por GB-mês | AWS Price List API, `AmazonS3/current/sa-east-1/index.json`, publicado em 2026-10-15 |
| S3 Glacier Instant Retrieval, São Paulo | US$ 0,0083 por GB-mês | idem |
| S3 PUT, São Paulo | US$ 0,007 por 1.000 | idem |
| ElastiCache Valkey `cache.t4g.small` (1,37 GiB), São Paulo | US$ 0,0488 por hora | AWS Price List API, `AmazonElastiCache/current/sa-east-1/index.json`, publicado em 2026-10-14 |
| ElastiCache Redis OSS `cache.t4g.small`, São Paulo | US$ 0,0610 por hora | idem |
| Câmbio | R$ 5,0403 por US$ | média da PTAX de venda de 13/10 a 23/10/2026, API do Banco Central (`api.bcb.gov.br`, série SGS 1). O valor de 26/10 (R$ 5,5007) destoa dos anteriores e ficou fora |
| Remuneração de engenheiro sênior no Brasil | R$ 248.264 por ano (mediana, remuneração total) | Levels.fyi, *Senior Software Engineer, Brazil*, 1.257 salários informados, atualizado em 2026-10-26 |

---

## Novos pressupostos

| # | Pressuposto | Valor | Origem | Consequência se for falso |
|---|---|---|---|---|
| P5 | Custo da empresa por pessoa | 1,7 × a remuneração total (R$ 35.171/mês para um sênior) | estimativa de encargos e benefícios sobre a mediana do Levels.fyi | Se for 2,0×, a operação passa de R$ 7,0 mil para R$ 8,3 mil por mês e o payback continua abaixo de 1 mês nos dois hit rates |
| P6 | Tamanho do registro de auditoria | 1 KB por cotação apresentada (corretora, instante, 3 cotações com parceira, `quote_id`, prêmio, origem e idade) | estimativa pelo formato da `Response` | O custo de armazenamento é tão pequeno que nem 10× muda o veredito; muda a escolha de classe de armazenamento |
| P7 | Corretoras ativas | 300 | enunciado: "algumas centenas em produção" | Muda só o número de objetos gravados na auditoria (lote por corretora por hora) |

---

## D11. Onde a plataforma roda (§5)

**Contexto.** O enunciado diz que "retenção, residência e segregação, não preferência" decidem. LGPD: a Prumo é operadora, o contrato proíbe uso cruzado entre corretoras e exige descarte. SUSEP: auditoria por 5 anos, **não regravável**.

| Opção | Prós | Contras | Problemas |
|---|---|---|---|
| **A. Nuvem pública, região de São Paulo** (AWS `sa-east-1`) | Dado pessoal fica no Brasil (sem transferência internacional). Armazenamento com bloqueio de escrita (S3 Object Lock em modo *compliance*: nem o administrador apaga ou regrava antes do prazo). Custo por uso: o Redis e a auditoria somam ~R$ 360/mês. Várias zonas de disponibilidade na mesma região. | Preço em dólar. A região é ~76% mais cara que a dos EUA. | Só há uma região AWS no Brasil: a perda da região inteira exige um plano fora dela (§7). |
| **B. On-premise** | Controle físico total. | Investimento inicial em servidores, armazenamento WORM e datacenter; equipe para operar hardware; capacidade fixa para um tráfego que oscila. | Para um volume de ~160 GB em 5 anos, o custo fixo de um armazenamento WORM próprio não se justifica. |
| **C. Híbrido** (auditoria on-premise, processamento na nuvem) | A auditoria fica sob controle físico. | Dois ambientes para operar, link dedicado, mais pontos de falha no caminho da auditoria. | A complexidade não compra nada que a opção A, com bloqueio de escrita, já não entregue. |

**Inclinação:** A, com a auditoria num bucket S3 com Object Lock em modo *compliance* (5 anos) e prefixo por corretora; e, como **proposta** para a perda da região, uma cópia da auditoria em outro provedor com região em São Paulo.

---

## D12. RTO e RPO por classe de dado (§7)

| Classe de dado | RPO | RTO | Por quê |
|---|---|---|---|
| Cache de cotações (Redis) | **tudo** (pode perder) | 15 min para voltar a ter cache; **0** para voltar a cotar | O cache é otimização: sem ele a API cota normalmente (testado). Perder custa dinheiro, não cotação. |
| Estado do breaker | **tudo** | 0 | Vive na memória; cada processo reaprende em ~10 chamadas (L2). |
| Registro de auditoria **[proposta]** | **≤ 1 min** | 4 h | É infração regulatória perder. O lote por minuto limita a perda; a cópia em outra região/provedor cobre a perda do site. |
| Código e configuração | 0 (Git) | 1 h | Tudo versionado; reconstruir é aplicar de novo. |
| Telemetria (traces e métricas) | 1 h | 4 h | Serve para operar, não para cotar. |

---

## D13. Alertas (§6)

Limiares tirados do que foi medido na 2D e dos parâmetros do SAD.

| # | Alerta | Consulta e limiar | Ação |
|---|---|---|---|
| A1 | Parceira com circuito aberto ou oscilando por muito tempo | `max_over_time(partner_breaker_state{partner=~".+"}[10m]) >= 1` e `min_over_time(...[10m]) >= 1` (fora de "fechado" por 10 min seguidos) | Runbook R1 |
| A2 | Parceira encostando no timeout | `histogram_quantile(0.95, sum by (partner, le) (rate(partner_quote_duration_seconds_bucket{outcome="success"}[10m])))` acima de 80% do timeout dela (1,6 s, 0,4 s, 0,4 s) por 10 min | Avisar a seguradora; avaliar o timeout (é parâmetro do SAD, muda com o SAD) |
| A3 | Redis fora | `sum(rate(quote_cache_lookups_total{result="error"}[5m])) > 0` por 5 min | Restaurar o Redis; enquanto isso, a conta de parceiro sobe para o hit rate 0 (§7) |
| A4 | Hit rate abaixo do pressuposto | `sum(rate(...{result="hit"}[1h])) / sum(rate(...[1h])) < 0,20` por 1 dia | Não é incidente: é o P1 sendo desmentido. Vai para o dono do produto rever o TTL e a §8 |

Na 2D, a `partner-flaky` oscilou 10 vezes entre meio-aberto e aberto em ~5 minutos antes de fechar. Por isso o A1 olha "fora de fechado por 10 min", e não cada abertura: abrir e fechar é o breaker funcionando.

---

## D14. O breaker com pouco tráfego (§4.2 / limite conhecido)

**Achado da 2D.** Com uma cotação a cada ~3 s, cada janela de 30 s junta ~10 chamadas, no limite do mínimo, e a `partner-flaky` ficou abaixo de 50% de falha. O circuito não abriu.

| Opção | Prós | Contras |
|---|---|---|
| **A. Declarar como limite conhecido (L5)** | Nada muda no código nem nas evidências. Em produção (P3, ~100 chamadas por janela) o efeito não aparece. | Em horário de pouco movimento, uma parceira ruim é chamada por mais tempo. |
| B. Janela deslizante de 60 s (`BucketPeriod`) | Junta mais chamadas sem "esquecer" na virada. | Muda um número defendido; exige refazer os testes e parte das evidências. |
| C. Mínimo de 5 requisições | Abre com menos tráfego. | Abre por acaso com mais facilidade (5 chamadas com 3 falhas já disparam). |

**Inclinação:** A.

---

## D15. Caminho de mudança do TTL (§6)

| Etapa | Como |
|---|---|
| Quem pede | Produto, com o dado do alerta A4 (hit rate) ou de reclamação de preço vencido |
| Quem aprova | Produto (risco de preço) **e** encarregado de dados (prazo de guarda); o teto de 24 h é contratual e não muda |
| Como chega em produção | Pull request no repositório de configuração alterando `CACHE_FRESH_TTL`, revisado; o SAD §4.3 muda no mesmo PR |
| Como se reverte | Reverter o PR; a configuração anterior volta no próximo deploy |
| Como se mede se melhorou | Hit rate (`quote_cache_lookups_total`), consultas por cotação entregue (RNF-03) e reclamações de preço não honrado, comparando a semana antes e a depois |

---

## As contas da §8 (com os números acima)

**Conta de parceiro** (R$ 0,04 por consulta, 3 consultas por cotação, 120.000 cotações por dia útil, 22 dias):

| Cenário | Hit rate | Consultas por mês | Custo por mês | Economia contra hoje | % da receita (R$ 660 mil) |
|---|---|---|---|---|---|
| Hoje, sem cache | 0% | 7.920.000 | R$ 316.800 | — | 48,0% |
| Recotação baixa | 20% | 6.336.000 | R$ 253.440 | R$ 63.360 | 38,4% |
| P1 (recotação em 1 h) | 40% | 4.752.000 | R$ 190.080 | R$ 126.720 | 28,8% |

**Infraestrutura acrescentada:**

| Item | Dimensionamento | Custo por mês |
|---|---|---|
| Redis (ElastiCache Valkey `cache.t4g.small`, 2 nós) | ~216 mil entradas vivas × ~1 KB ≈ 216 MB, cabe em 1,37 GiB; 2 nós para alta disponibilidade | R$ 359 |
| Auditoria, ano 1 (S3 Standard com Object Lock) | 2,64 mi registros por mês × 1 KB = 2,64 GB por mês; 31,7 GB no fim do ano 1 | R$ 42 no ano (≈ R$ 4/mês) + R$ 2 de gravação |
| Auditoria, ano 5 | 158,4 GB acumulados | R$ 352 no ano em Standard, ou R$ 72 em Glacier Instant Retrieval |

O erro de unidade que o guia aponta: dimensionar a auditoria por **consulta comprada** em vez de **cotação apresentada** subestima o acervo em 1/(1 − 0,40) = **1,67×** com o P1. O cache derruba a consulta, mas não o registro.

**Pessoal:** construção de 1,5 pessoa-mês (1 desenvolvedor sênior por 1 mês e 0,5 mês de SRE) = R$ 52.756, uma vez; operação de 0,2 SRE = R$ 7.034 por mês.

**Veredito:** custo mensal da solução ≈ R$ 7.398. Economia líquida de R$ 55.962/mês com 20% de hit rate (payback em **0,94 mês**) e de R$ 119.322/mês com o P1 (payback em **0,44 mês**). O cache se paga a partir de **2,3%** de hit rate. Fica de fora, a favor da solução, a economia do tudo-ou-nada (no "antes" medido, 4,36 consultas por cotação entregue, contra 3 no ideal).
