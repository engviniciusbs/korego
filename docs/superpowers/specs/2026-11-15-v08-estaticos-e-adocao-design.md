# v0.8: sites estáticos compilados pelo KoreGO e adoção dos sites da VPS

## Intenção
A v0.7.2 passou a mostrar em Projetos os sites que já rodam na VPS (seção "Na VPS"), mas só para leitura. O usuário quer que esses sites passem a ser do KoreGO:
- os **sites estáticos** são compilados e publicados pelo painel;
- as **stacks docker compose** são operadas pelo painel.

Hoje cada site tem o seu caminho de publicação:
- **hoprs.online e archviewer:** o GitHub Actions compila e envia um tar por SSH para um usuário restrito, que roda `/usr/local/bin/hoprs-site-receive` (ou o equivalente do archviewer). Esse script cria `releases/<UTC>-<sha7>`, exige `index.html`, troca `current` de forma atômica e guarda só as 5 versões mais recentes.
- **mikos:** roda por compose a partir de `/opt/mikos/releases/<sha>/infra`, com `docker-compose.yml` + `docker-compose.nginx.yml`, `.env`, `deploy.sh` e `backup.sh`. As imagens são compiladas fora da VPS e o banco fica no volume `mikos_pgdata`.
- **viniciusbsarmento:** roda por `/opt/viniciusbsarmento/docker-compose.yml`, com imagens de tag `vbs-*:<sha>` e os arquivos `.last_tag` e `.prev_tag`.

## Objetivos

### Parte 1: app do tipo "Estático"
- **Novo `buildType` `static`**, para apps de origem `github` ou `git`. Configuração:
  - **comando de build:** se ficar vazio, é detectado pelo lockfile:
    - `pnpm-lock.yaml` → `corepack enable && pnpm install --frozen-lockfile && pnpm run build`;
    - `yarn.lock` → `corepack enable && yarn install --frozen-lockfile && yarn build`;
    - `package-lock.json` → `npm ci && npm run build`;
    - sem `package.json`: não há build e a pasta de saída é publicada como está;
  - **pasta de saída:** relativa, padrão `dist`;
  - **diretório base:** relativo, padrão `.`, para monorepos como `site/`;
  - **imagem do build:** padrão `node:22-alpine`, validada como as imagens de hoje;
  - **SPA:** sim ou não;
  - **prefixo imutável:** padrão `/assets/`.
- **Deploy:**
  1. O código é baixado como hoje (`Fetch`).
  2. O build roda num container descartável (`docker run --rm`) com o código montado, as variáveis da app como ambiente, limites de CPU e memória e rede liberada (npm precisa).
  3. O log entra no log do deploy, com os segredos mascarados, como hoje.
  4. A pasta de saída é copiada para `/var/www/<slug>/releases/<UTC>-<sha7>`. Se faltar `index.html`, o deploy falha, salvo quando o SPA está desligado e existe outro `.html` na raiz. Nesse caso basta haver um `.html`.
  5. O `current` é trocado de forma atômica (symlink temporário + rename).
  6. Ficam as 5 versões mais recentes, sem nunca apagar a que está no ar nem a do deploy anterior.
  7. Não há container, porta nem health check. O deploy termina como "running" quando o `current` foi trocado.
- **Rollback:** aponta o `current` para a pasta de um deploy anterior que ainda exista no disco, sem build. Se a pasta já tiver sido apagada, o botão fica desabilitado com uma explicação.
- **Domínios:** uma rota do tipo "app" que aponta para uma app estática vira, no Caddy, uma rota de arquivos com `root` = `/var/www/<slug>/current` e os campos SPA e prefixo imutável da app. O editor de domínio passa a listar as apps estáticas, junto com as outras.
- O deploy a cada push, o histórico, o log, o cancelamento e as variáveis funcionam como nas apps de hoje.
- **Formulário Nova aplicação:** em "Tipo de build", a opção "Estático" mostra os campos acima e esconde porta e health check.

### Parte 2: botão "Gerenciar pelo KoreGO" nos cartões de "Na VPS"

**Estático (adoção de pasta existente)**
- O formulário pede o projeto do KoreGO (um existente ou um novo), o slug, o repositório (GitHub ou git), o branch e os campos de build da Parte 1.
- A pasta servida hoje é adotada no lugar: a app guarda uma **raiz de publicação** (`publishRoot`), que nas apps novas é `/var/www/<slug>` e na adoção é a pasta pai do `current` atual (por exemplo `/var/www/hoprs`). As versões existentes são mantidas.
- Na adoção não há deploy automático. O site continua no ar com o `current` atual, e o domínio passa a apontar para a app no mesmo instante. O Caddy serve o mesmo `current`, então o site não fica fora do ar.
- A tela final explica que é preciso desligar o workflow do GitHub Actions e, se a pessoa quiser, remover o usuário SSH e o script de recebimento. O KoreGO não mexe nisso.
- **O prune passa a apagar versões que o KoreGO não criou.** Ele só apaga pastas cujo nome segue `^\d{14}-[0-9a-f]{7}$`, mantendo as 5 mais recentes por nome. Isso é avisado na tela.

**Compose (stack gerenciada no lugar)**
- Nova origem `local`, com o tipo de build `compose-local`. A app guarda:
  - o **nome do projeto compose**;
  - o **modo de atualização**: `pull` (`docker compose pull && docker compose up -d`) ou `script` (caminho relativo à pasta da stack, por exemplo `deploy.sh`).
- **Pasta e arquivos compose não ficam gravados.** A cada operação eles são lidos dos rótulos `com.docker.compose.project.working_dir` e `com.docker.compose.project.config_files` do container mais recente da stack, porque a pasta do mikos muda a cada versão. Se não houver nenhum container da stack (nem parado), as operações falham com uma mensagem clara.
- **Operações:**
  - iniciar, parar e reiniciar, via `docker compose -p <projeto> -f <arquivos...> start|stop|restart`, rodado na pasta da stack;
  - logs, por container, reaproveitando a tela de logs de containers;
  - **Atualizar**, que gera um registro no histórico de deploys com log ao vivo (sem SHA). O resultado vem do código de saída.
- **`.env`:** o arquivo `.env` da pasta da stack aparece com os nomes das variáveis, e os valores ficam só de escrita, como `app_env_set`. Ao gravar, o arquivo inteiro é reescrito de forma atômica, mantendo comentários e ordem. Gravar não reinicia nada; a tela sugere usar "Atualizar".
- **2FA:**
  - escolher ou mudar o modo `script` e o caminho do script exige o código 2FA, porque o script roda como root;
  - o caminho precisa estar dentro da pasta da stack, depois de seguir links;
  - parar a stack também exige 2FA, alinhado a "apagar container".
- Remover a app "Compose na VPS" só tira a stack do KoreGO. Nada é parado nem apagado.
- **Domínios:** continuam como estão (rotas `address`). A detecção da seção "Na VPS" passa a ignorar as stacks e pastas já adotadas, que aparecem no projeto do KoreGO escolhido.

### MCP
As ferramentas de app aceitam os novos tipos. Entram três ferramentas novas:
- `app_adopt_static`;
- `app_adopt_compose`;
- `app_compose_action` (start, stop e restart; stop exige `code`).

A atualização de uma stack usa `deploy_start`.

## Não objetivos
- Deploy de stacks compose pelo git (rebuild a partir do repositório). Fica para a etapa de serviços prontos e backups.
- Upload de artefato pelo GitHub Actions (abordagem B).
- Serviços prontos (Postgres, Supabase, PHP), volumes gerenciados e backups.
- Mexer no workflow do Actions, nas chaves SSH ou nos scripts de recebimento existentes.

## Anti-objetivos
- A adoção derrubar um site ou recriar containers.
- O `.env` ser exibido com valores ou ficar legível pela API.
- O prune apagar algo fora do padrão de nome de versão ou a versão no ar.

## Abordagens
- **Build dos estáticos:**
  - **A. compilar na VPS (escolhida pelo usuário):** segue o fluxo das apps e aproveita o deploy a cada push;
  - B. o Actions envia o artefato. Recusada por agora.
- **Stacks compose:**
  - **2. gerenciar no lugar (escolhida pelo usuário):** baixo risco e sem tocar no build;
  - 1. deploy completo pelo git. Adiado: mexe na produção e no banco do mikos sem backup.

## Decisões minhas (o usuário pode corrigir)
- O padrão de nome das versões é `<UTC YYYYMMDDHHMMSS>-<sha7>`, igual ao script atual, para que as versões antigas e as novas convivam.
- Ficam 5 versões, como no script atual.
- A imagem padrão é `node:22-alpine`. Os limites do build são 2 CPUs e 2 GiB.
- O stop da stack exige 2FA. Iniciar e reiniciar não exigem.
- A versão será a v0.8.0.

## Respostas à revisão do construtor (decisões minhas)

**Pastas e versões**
1. **`publishRoot` dentro de `/var/www`:** precisa ficar dentro de `/var/www` depois de seguir os links. Fora disso, a adoção é recusada com uma mensagem clara. O validador de domínios não muda.
2. **Formato exigido na adoção:** `<raiz>/current` precisa ser um link para `<raiz>/releases/<nome>`; qualquer outro formato é recusado. A adoção cria um deploy "adotado" (`trigger = adopt`, status `running`, SHA tirado do nome da versão quando segue o padrão) apontando para a versão no ar. Assim o histórico tem um ponto de partida e o rollback trabalha só sobre deploys, cada um com a sua pasta gravada.
3. **Domínios na adoção:**
   - **Estático:** todos os domínios cuja rota estática tem `root` igual ao `current` adotado passam para rota "app" na mesma transação da criação da app.
   - **Compose:** os domínios não mudam.
   - **Seção "Na VPS":** um estático adotado deixa de aparecer porque a rota passa a ser "app". Uma stack adotada deixa de aparecer porque o projeto compose dela está numa app `compose-local`.
   - **Stack sem nenhum container**, nem parado: não pode ser adotada.

**Execução do deploy**

4. **Cópia dos arquivos:** o KoreGO roda como root no host e copia a saída do build para a nova versão. Os arquivos ficam com dono root e permissões normalizadas: 0755 nas pastas e 0644 nos arquivos.
5. **Variáveis:**
   - Apps `compose-local` não usam as variáveis gravadas no KoreGO, só o `.env` da stack. A tela de variáveis dessas apps edita o `.env`.
   - Apps estáticas usam as variáveis do KoreGO no build.
6. **Deploys sem container:**
   - **Estático:** `Recover` marca como falho o deploy interrompido antes da troca do `current`, e como running se a troca já tiver acontecido. A comparação é pelo destino do link. O deploy pode ser cancelado durante o build.
   - **`compose-local`:**
     - o deploy não tem SHA;
     - não tem rollback, e o botão não aparece;
     - pode ser cancelado (o processo recebe SIGTERM e depois SIGKILL);
     - se for interrompido por reinício do KoreGO, é marcado como falho.

**Segurança**

7. **2FA no `app_update`:** mudar o modo `script` ou o caminho do script pede `code`, inclusive pelo MCP.
8. **Caminho do script:** é validado ao salvar e de novo antes de cada execução, sempre dentro da pasta lida dos rótulos naquele momento.

**Comportamento**

9. **Deploy a cada push depois da adoção de estático:** fica desligado. A tela final traz a opção "Já desliguei o workflow do Actions; publicar a cada push", que liga o deploy automático. Isso evita que o Actions e o KoreGO publiquem ao mesmo tempo na mesma pasta.
10. **Slug:** continua único. Uma app estática nova é recusada se `/var/www/<slug>` já existir; nesse caso, o caminho é a adoção.

## Fica para o construtor
- O esquema exato da migração 0008.
- Onde ficam o tipo e o `publishRoot`.
- Como a rota "app" estática entra no gerador de config do Caddy.
- A estrutura dos componentes do formulário.
