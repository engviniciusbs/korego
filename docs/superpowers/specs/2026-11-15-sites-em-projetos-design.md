# Sites da VPS em Projetos — design curto

## Intenção
O usuário publicou hoprs.online na VPS e não o encontrou em lugar algum do painel. O KoreGO entrega o tráfego, o domínio e o certificado desses sites, mas Projetos só lista projetos criados no KoreGO. O usuário quer ver em **Projetos** tudo o que está no ar na VPS, sem mudar o jeito como cada site é publicado.

## Objetivos
- A tela Projetos mostra três tipos de cartão, cada um com um selo:
  - **KoreGO**: os projetos do KoreGO, como hoje.
  - **Docker compose**: um cartão por stack, agrupado pelo rótulo `com.docker.compose.project` (o campo `project` do container). Containers sem esse rótulo aparecem como "Container avulso", com o nome do container.
  - **Estático**: um cartão por domínio cuja rota "/" é estática.
- Os domínios de rota `address` entram no cartão da stack cujo container publica a mesma porta em 127.0.0.1 ou 0.0.0.0.
- Cartão estático:
  - mostra a pasta servida, a versão atual (o nome final do caminho real, depois de seguir links) e a data da última mudança;
  - se a pasta não existir, mostra o aviso "Pasta não encontrada".
- Cartão compose: quantidade de containers, quantos estão rodando e os domínios.
- Os cartões que não são do KoreGO:
  - trazem a linha "Atualizado fora do painel" e não têm o botão Remover;
  - levam a uma página só de leitura com domínios (certificado e link para Domínios), containers (estado e link para o detalhe do container) ou a pasta.
- A lista é montada na hora a partir dos domínios e dos containers. Nada novo é gravado.

## Não objetivos
- Converter um desses sites em projeto do KoreGO.
- Editar ou apagar a partir desses cartões.
- O domínio do painel e os containers do próprio KoreGO (`managed`) ficam fora da lista.

## Abordagens
1. **Cartões detectados em Projetos (escolhida).**
2. Uma tela nova "Sites". Recusada: o usuário quer em Projetos.
3. Registrar como projetos/apps no banco. Recusada: seriam deploys falsos e haveria estado para manter em sincronia.

## Decisões
- **Backend:** o DTO de domínio ganha `staticRoots`, uma lista por rota estática com `path`, `root`, `resolved`, `exists` e `modifiedAt`. O campo fica no nível do domínio, e não na rota, para não voltar no corpo de edição. O KoreGO roda como root e lê a pasta com `os.Stat` e `filepath.EvalSymlinks`.
- **Montagem:** feita no navegador (`web/src/lib/sites.ts`), cruzando `useDomains` e `useContainers`.
- **Endereço da página só de leitura:** `/projetos/site/?tipo=compose&nome=<projeto>` ou `?tipo=estatico&nome=<hostname>`. Também há `tipo=avulso`.
- **Domínio de porta sem container correspondente:** vira um cartão próprio, com o selo "Endereço" e o endereço.

## Fica para o construtor
Estrutura dos componentes, testes e textos secundários, seguindo o visual atual dos cartões de projeto.
