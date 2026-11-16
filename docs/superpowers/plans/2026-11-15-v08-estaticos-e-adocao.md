# v0.8: sites estáticos e adoção dos sites da VPS — plano de implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (escolhido pelo usuário) para implementar tarefa a tarefa. Os passos usam caixas `- [ ]`.

**Goal:**
- Apps do tipo "Estático", compiladas pelo KoreGO e publicadas em `/var/www/<slug>/releases`, com o link `current`.
- Adoção dos sites estáticos e das stacks compose que já rodam na VPS.

**Architecture:**
- O pipeline de deploy (`internal/deploy/service.go`) ganha dois ramos novos em `steps()`:
  - `static`: clone, build em container descartável, publicação da versão e troca do link;
  - `compose-local`: atualização da stack no lugar.
- As peças de disco (versões, link `current`, `.env`) ficam em arquivos próprios no pacote `deploy`, testáveis com `t.TempDir()`.
- O proxy transforma a rota "app" de uma app estática em rota de arquivos.
- A adoção é feita por dois endpoints novos, com ferramentas MCP correspondentes.
- O painel ganha os campos e telas novos, e o botão "Gerenciar pelo KoreGO" nos cartões da seção "Na VPS".

**Tech Stack:** Go 1.27 (SQLite via modernc, chi, testify), Docker CLI pelo `Runner`, Next.js com export estático, Vitest e Testing Library.

**Spec:** `docs/superpowers/specs/2026-11-15-v08-estaticos-e-adocao-design.md` (inclui a seção "Respostas à revisão do construtor").

## Global Constraints
- **Valores de tipo e origem:**
  - novo `buildType`: `static`;
  - nova origem: `sourceType` `local`, sempre com `buildType` `compose-local`;
  - novo trigger de deploy: `adopt`.
- **Nome das versões:** `<UTC YYYYMMDDHHMMSS>-<sha7>`. O prune só apaga pastas que casam `^\d{14}-[0-9a-f]{7}$`. Ficam as 5 mais recentes por nome, e nunca é apagada a versão no ar nem a do deploy anterior.
- **Raiz de publicação:** precisa ficar dentro de `/var/www` depois de `EvalSymlinks`. App nova: `/var/www/<slug>`, recusada se a pasta já existir. Adoção: pasta pai do `current`, que precisa ser link para `<raiz>/releases/<nome>`.
- **Troca do `current`:** atômica. `os.Symlink` para `current.tmp`, seguido de `os.Rename` sobre `current`.
- **Arquivos publicados:** dono root, pastas 0755 e arquivos 0644. É exigido `index.html` na raiz; com SPA desligado, basta qualquer `*.html` na raiz.
- **Build:**
  - `docker run --rm` com o código montado em `/app`, `-w /app/<baseDir>`, `--cpus 2 --memory 2g`, as variáveis da app como `-e` e o comando via `sh -c`.
  - Imagem padrão: `node:22-alpine`. Pasta de saída padrão: `dist`. Diretório base padrão: `.`. Prefixo imutável padrão: `/assets/`.
- **Comando de build detectado (quando vazio):**
  - `pnpm-lock.yaml` → `corepack enable && pnpm install --frozen-lockfile && pnpm run build`;
  - `yarn.lock` → `corepack enable && yarn install --frozen-lockfile && yarn build`;
  - `package-lock.json` → `npm ci && npm run build`;
  - sem `package.json` → sem build: a pasta de saída é copiada como está.
- **compose-local:**
  - A pasta e os arquivos compose vêm dos rótulos `com.docker.compose.project.working_dir` e `com.docker.compose.project.config_files` do container mais recente do projeto, lidos a cada uso. Nunca são gravados.
  - As operações usam `docker compose -p <projeto> -f <arq>... <op>`, com `Dir` igual à pasta da stack.
  - Modo `pull` = `pull` e depois `up -d`. Modo `script` = `<pasta>/<script>`, executado com `Dir` igual à pasta da stack.
- **2FA:**
  - parar uma stack;
  - escolher ou mudar o modo `script` ou o caminho do script (na adoção e no `PUT`);
  - remover qualquer app (já existe).
- **Remover apps:** remover uma app `compose-local` nunca roda `down` nem apaga arquivos. Remover uma app `static` não apaga a raiz de publicação.
- **`.env`:**
  - os valores nunca saem pela API, só os nomes;
  - a gravação é atômica (arquivo temporário + rename), mantém comentários, ordem e linhas que não foram tocadas, e preserva as permissões do arquivo original;
  - gravar não reinicia nada.
- **Textos:** em pt-BR. Os padrões visuais e de código seguem os componentes atuais.
- **Comandos de teste:**
  - Go roda pelo wrapper Docker `/tmp/claude-1000/-mnt-dados-dev-korego/d56a24a8-a477-49ca-8d24-20c36d9628e9/scratchpad/go.sh` (por exemplo `…/go.sh test ./internal/deploy/`).
  - Os dois testes `internal/node/proxy` `TestEnsure_StartsWhenPortsFree` e `TestEnsure_SkipsPullWhenImagePresent` já falham no `main` e devem ser ignorados.
  - Web: `cd web && npx vitest run && npx tsc --noEmit && npx eslint src && npm run build`.

## Review Focus
1. **Adoção do hoprs com versões antigas:** as pastas fora do padrão e a versão no ar sobrevivem ao primeiro prune.
2. **Script fora da pasta da stack** (`../x.sh`, ou um link que sai da pasta): recusado ao salvar e de novo ao executar.
3. **Deploy estático cancelado ou interrompido durante o build:** o `current` não muda, a pasta parcial é apagada e o `Recover` marca o deploy como falho.
4. **`.env` com comentários, linhas em branco, `export X=`, valores com aspas e `=` dentro do valor:** sobrevivem intactos quando outra chave é alterada.
5. **Stack sem containers ou com rótulos ausentes:** a adoção é recusada com uma mensagem clara, e as operações falham sem pânico.

---

### Task 1: Banco — migração 0008 e campos novos
**Files:**
- Create `internal/store/migrations/0008_static_local.sql`.
- Modify `internal/store/apps.go` and `internal/store/deploys.go`.
- Tests in `internal/store/apps_test.go` and `internal/store/deploys_test.go`.

**Interfaces — Produces:**
- **Campos novos em `store.App`:**
  - `PublishRoot string`;
  - `StaticBuildCmd string`;
  - `StaticOutputDir string`;
  - `StaticBaseDir string`;
  - `StaticImage string`;
  - `StaticSPA bool`;
  - `StaticImmutablePrefix string`;
  - `ComposeProject string`;
  - `UpdateMode string` (`""|pull|script`);
  - `UpdateScript string`.
- **Coluna correspondente** de cada campo, com `ALTER TABLE apps ADD COLUMN …` e padrão `''`/`0`.
- **`store.Deploy`:** ganha `ReleaseDir string` (coluna `release_dir`).
- **Funções de store:**
  - `SetDeployRelease(ctx, deployID int64, dir string) error`;
  - `AppByComposeProject(ctx, project string) (App, error)` (devolve `ErrNotFound` quando não existe);
  - `AppByPublishRoot(ctx, root string) (App, error)`.

**Steps:**
- [ ] **Testes:**
  - `TestApps_StaticAndLocalFieldsRoundTrip`: cria uma app `static` e uma `compose-local`, relê e compara todos os campos novos;
  - `TestDeploys_ReleaseDir`: `SetDeployRelease` e releitura;
  - `TestAppByComposeProject`/`TestAppByPublishRoot`: encontrado e `ErrNotFound`.
- [ ] Rodar e confirmar que falham. Implementar a migração e os campos (struct, `appCols`, `scanApp`, insert e update). Rodar `…/go.sh test ./internal/store/` até passar.
- [ ] Commit: `feat(store): campos de app estática e compose local (migração 0008)`.

### Task 2: Disco — versões, link `current` e `.env`
**Files:**
- Create `internal/deploy/release.go`, `internal/deploy/release_test.go`, `internal/deploy/dotenv.go` and `internal/deploy/dotenv_test.go`.

**Interfaces — Produces** (todas em `package deploy`):
- `const WWWRoot = "/var/www"`; `var releaseNameRe = regexp.MustCompile(`^\d{14}-[0-9a-f]{7}$`)`.
- `ReleaseName(t time.Time, sha string) string`: com SHA vazio, usa `0000000`.
- `DetectBuildCommand(dir string) string`, conforme as regras globais. Devolve `""` sem `package.json`.
- `InsideRoot(root, path string) (string, error)`: aplica `EvalSymlinks` aos dois e devolve o caminho resolvido. O erro diz que o caminho está fora da raiz.
- `PublishRelease(srcDir, publishRoot, name string, spa bool) (string, error)`: copia `srcDir` para `<publishRoot>/releases/<name>`, normaliza as permissões, confere o `.html` conforme a regra e devolve o caminho absoluto. Em caso de erro, apaga a pasta parcial. Recusa links simbólicos que saiam de `srcDir`.
- `SwapCurrent(publishRoot, releaseDir string) error`: troca atômica.
- `CurrentRelease(publishRoot string) (string, error)`: destino resolvido de `current`.
- `PruneReleases(publishRoot string, keep int, protect ...string) error`.
- `ValidateAdoptRoot(current string) (publishRoot, releaseDir string, err error)`: aplica as regras de adoção (link, `releases/<nome>` irmão e dentro de `WWWRoot`). Recebe `wwwRoot` por variável de pacote `wwwRoot = WWWRoot`, sobrescrita nos testes.
- `EnvNames(path string) ([]string, error)` e `SetEnv(path string, set map[string]string) error`: com arquivo ausente, `SetEnv` cria o arquivo com modo 0600. Linhas `export K=V` também contam como chave. Uma chave nova vai para o fim do arquivo. O valor é gravado entre aspas duplas, com `\` e `"` escapados, quando tem espaço, `#`, aspas ou `$`.

**Steps:**
- [ ] **Testes de `release_test.go`** (com `wwwRoot` apontando para um `TempDir`):
  - `ReleaseName(time.Date(2026,11,15,13,30,41,0,time.UTC),"9faa1c9abc")=="20261115133041-9faa1c9"`;
  - `DetectBuildCommand` nos quatro casos;
  - `PublishRelease`:
    - copia arquivos e subpastas com 0644 e 0755;
    - falha sem `index.html` com SPA ligado e apaga a pasta parcial;
    - com SPA desligado, passa com `about.html`;
    - recusa um link para `/etc/passwd`;
  - `SwapCurrent`: depois da troca, `CurrentRelease` aponta para a nova versão e não sobra `current.tmp`;
  - `PruneReleases` (Review Focus 1): com 7 versões no padrão mais `old-site/` e `20261114.bak`, `keep=5` e a 1ª protegida, ficam as 5 mais novas, a protegida e as duas pastas fora do padrão;
  - `ValidateAdoptRoot`: aceita `<www>/hoprs/current → <www>/hoprs/releases/x`; recusa `current` como pasta real, link para fora de `releases/` e raiz fora de `wwwRoot`;
  - `InsideRoot("…/stack", "../x.sh")` dá erro; um link que sai da pasta também dá erro (Review Focus 2).
- [ ] **Testes de `dotenv_test.go`** (Review Focus 4): um arquivo com comentário, linha em branco, `export A=1`, `B="x y"`, `C=a=b` e `D=` (vazio).
  - `EnvNames` devolve `[A B C D]`.
  - `SetEnv({"C":"novo","E":"com espaço"})` mantém todas as outras linhas byte a byte, troca só a linha `C` e acrescenta `E="com espaço"`.
  - O modo do arquivo é preservado.
  - Com arquivo ausente, o arquivo é criado com 0600.
- [ ] Implementar e rodar `…/go.sh test ./internal/deploy/ -run 'Release|Prune|Swap|Adopt|Inside|Detect|Env'` até passar.
- [ ] Commit: `feat(deploy): versões estáticas, troca de current e edição de .env`.

### Task 3: Pipeline — deploy, rollback e recuperação de app estática
**Files:**
- Create `internal/deploy/static.go`.
- Modify `internal/deploy/service.go`: `steps` (`:617`), `Rollback` (`:298`), `Recover` (`:379`) e `cleanup` (`:965`).
- Tests in `internal/deploy/static_test.go`, using the fakes in `fake_test.go`.

**Interfaces:**
- **Consome:** Task 1 (campos e `SetDeployRelease`) e Task 2 (todas as funções de disco).
- **Produz:**
  - `func (p *pipeline) static() bool`;
  - `func (p *pipeline) runStatic(ctx) error`;
  - `func StaticBuildCmd(app store.App, srcDir string, env []string) (Cmd, bool)`: devolve `false` quando não há build;
  - `func StaticPublishRoot(app store.App) string`: `app.PublishRoot` ou `WWWRoot/<slug>`.

**Fluxo de `runStatic`:**
1. `source()` faz o clone, sem o `Build()`.
2. Se `StaticBuildCmd` devolver um comando, ele é executado. O comando é `docker run --rm --cpus 2 --memory 2g -v <src>:/app -w /app/<baseDir> [-e K=V…] <imagem> sh -c "<cmd>"`.
3. `PublishRelease(<src>/<baseDir>/<outputDir>, root, ReleaseName(now, sha), spa)`.
4. `SetDeployRelease`.
5. `SwapCurrent`.
6. `record(bg, "")` com porta 0.
7. `markRunning`.
8. `PruneReleases(root, 5, nova, release do deploy anterior)`.

**Regras:**
- **Cancelamento ou erro antes do passo 5:** apaga a pasta da versão e não toca no `current`.
- **`Rollback` de app estática:** exige que `src.ReleaseDir` exista. Cria o deploy com trigger `rollback` e `ReleaseDir` igual ao da origem. O pipeline só faz os passos 5 a 7. Sem a pasta, devolve um erro novo, `ErrReleaseGone`.
- **`Recover`:** um deploy estático interrompido fica `running`/current se `CurrentRelease(root)` for igual ao `ReleaseDir` dele. Caso contrário, fica `failed`, e a pasta é apagada se não for o `current`.
- **`cleanup`:** não roda para apps estáticas.
- **Fetch e prepare:** iguais aos de hoje, conforme a origem (github ou git).

**Steps:**
- [ ] **Testes** (runner falso que, ao receber `docker run`, cria `dist/index.html` dentro de `<src>`):
  - `TestStatic_DeployPublishesAndSwaps`:
    - status final `running`;
    - `ReleaseDir` gravado;
    - `current` apontando para ele;
    - argumentos do `docker run` com `--cpus 2`, `--memory 2g` e `node:22-alpine`;
    - `-e` com as variáveis;
    - nenhum container criado.
  - `TestStatic_NoPackageJSONCopiesAsIs`: nenhum `docker run` é executado.
  - `TestStatic_BuildFailsKeepsCurrent`: com o build falhando, `current` não muda e não sobra pasta em `releases/`.
  - `TestStatic_CancelDuringBuild` (Review Focus 3): mesmo resultado, com status `cancelled`.
  - `TestStatic_Rollback`: volta para a versão anterior sem nenhum `docker run`. `TestStatic_RollbackGone`: devolve `ErrReleaseGone`.
  - `TestStatic_RecoverInterrupted`: os dois casos (o `current` é ou não é a versão do deploy).
- [ ] Implementar e rodar `…/go.sh test ./internal/deploy/` até passar.
- [ ] Commit: `feat(deploy): pipeline de apps estáticas com rollback por link`.

### Task 4: API e proxy das apps estáticas
**Files:**
- Modify `internal/api/apps.go` (`AppInput`, `appView`, `build()`, `teardown`).
- Modify `internal/domains/sites.go` (`appUpstream`/`siteOf`).
- Tests in `internal/api/apps_test.go` and `internal/domains/*_test.go`.

**Interfaces:**
- **Consome:** Tasks 1 a 3.
- **Produz** (campos JSON de `AppInput` e `appView`):
  - `staticBuildCmd`, `staticOutputDir`, `staticBaseDir`, `staticImage`, `staticSpa`, `staticImmutablePrefix`;
  - `publishRoot` (só leitura);
  - `composeProject`, `updateMode`, `updateScript` (usados na Task 6).

**Validação de `build()` para `static`:**
- **Origem e porta:** a origem é `github` ou `git`. `internalPort` é ignorado e gravado como 0.
- **Caminhos:** `staticOutputDir` e `staticBaseDir` passam em `ValidRelPath`, com os padrões aplicados.
- **Imagem e prefixo:** `staticImage` passa em `ValidImageRef`. `staticImmutablePrefix` fica vazio ou começa e termina com `/`.
- **Comando de build:** `staticBuildCmd` tem até 1000 caracteres e não pode ter quebra de linha.
- **Criação:** o create recusa com `409 PUBLISH_ROOT_EXISTS` se `/var/www/<slug>` já existir. A checagem usa uma `wwwRoot` injetável nos testes. A mensagem sugere usar "Gerenciar pelo KoreGO" na seção "Na VPS".

**Proxy:**
- Rota "app" com app `static`: em `sites.go`, vira rota de arquivos com `root = StaticPublishRoot(app)+"/current"`, `spa` e `immutablePrefix` da app. Não exige `HostPort`.
- `Deploy` de app estática não chama `ReapplyApp`, porque o Caddy segue o link sozinho. O `ReapplyApp` só é chamado na criação e na adoção.

**Remoção:** o `teardown` de app estática não apaga nada do disco.

**Steps:**
- [ ] **Testes:**
  - `TestAppsAPI_CreateStatic`:
    - aplica os padrões;
    - `internalPort` vira 0;
    - recusa `staticOutputDir: "../x"`;
    - recusa a criação quando a pasta existe (`PUBLISH_ROOT_EXISTS`).
  - `TestSites_StaticAppRoute`: um domínio com rota "app" para app estática gera rota de arquivos com o `root` certo e o SPA.
  - `TestAppsAPI_DeleteStaticKeepsFiles`.
- [ ] Implementar e rodar `…/go.sh test ./internal/api/ ./internal/domains/ ./internal/node/proxy/` até passar (exceto os 2 testes já quebrados).
- [ ] Commit: `feat(api): apps estáticas na API e no proxy`.

### Task 5: Stacks compose no lugar — serviço
**Files:**
- Create `internal/deploy/local.go` and `internal/deploy/local_test.go`.
- Modify `internal/deploy/service.go`: ramo `local` em `steps`, `prepare` e `source`, e `Recover`.

**Interfaces:**
- **Consome:** Task 1 e Task 2 (`InsideRoot`, `EnvNames`, `SetEnv`).
- **Produz:**
  - `type Stack struct { Project, Dir string; Files []string; Containers []docker.Container }`.
  - `func FindStack(ctx, d docker.Client, project string) (Stack, error)`:
    - usa o container mais recente pelo `Created`;
    - `Files` vem de `config_files`, separado por vírgula;
    - devolve `ErrStackNotFound` quando não há containers e `ErrStackLabels` quando faltam rótulos.
  - `func (s *Service) StackAction(ctx, appID int64, op string) error`: `op` ∈ `start|stop|restart`. O comando vai para o log do servidor; não há deploy.
  - `func ValidUpdateScript(stackDir, script string) (string, error)`: usa `InsideRoot` e exige um arquivo regular.
  - `func StackEnvPath(st Stack) string`: `<Dir>/.env`.
  - `Deploy` passa a aceitar apps `compose-local`. O ramo `p.local()` em `steps` faz o seguinte:
    1. `FindStack`;
    2. conforme o modo:
       - **pull:** `docker compose -p P -f F… pull` e depois `… up -d`;
       - **script:** `ValidUpdateScript` de novo e então executa `<script>`;
    3. com sucesso, `markRunning` sem porta e sem `record` de imagem.
  - `prepare` e `source` não fazem nada para a origem `local`.
  - `Rollback` devolve `ErrRollbackUnsupported` para `compose-local`.
  - `Recover` marca como `failed` um deploy `compose-local` interrompido.

**Steps:**
- [ ] **Testes:**
  - `TestFindStack`:
    - escolhe o container mais novo;
    - divide os arquivos corretamente;
    - devolve os dois erros (Review Focus 5).
  - `TestStackAction_Commands`: os argumentos exatos e o `Dir` de `start`, `stop` e `restart`.
  - `TestLocalUpdate_Pull` e `TestLocalUpdate_Script`: a sequência de comandos e o `Dir`.
  - `TestLocalUpdate_ScriptEscapes`: com um link que sai da pasta, o deploy falha sem executar nada.
  - `TestLocalUpdate_Cancel`: o status fica `cancelled`.
  - `TestRecover_LocalInterrupted`.
- [ ] Implementar e rodar `…/go.sh test ./internal/deploy/` até passar.
- [ ] Commit: `feat(deploy): stacks compose gerenciadas no lugar`.

### Task 6: API de adoção, ações da stack, `.env` e MCP
**Files:**
- Create `internal/api/adopt.go` and `internal/api/adopt_test.go`.
- Modify `internal/api/apps.go`: validação de `compose-local`, `envNames`/`envSet` para `compose-local` lendo o `.env`, 2FA no `PUT` quando o script muda, `teardown` sem efeito.
- Modify `internal/api/routes.go` and `internal/api/mcp_tools.go`.

**Interfaces:**
- **Consome:** Tasks 1, 2, 4 e 5.
- **Produz** (rotas REST):
  - `POST /api/adopt/static`: corpo `{projectId, slug, current, sourceType, repo, branch, static…}`. Resposta `201 {app, domains:[hostnames trocados]}`.
  - `POST /api/adopt/compose`: corpo `{projectId, slug, composeProject, updateMode, updateScript, code?}`. Exige o código quando `updateMode=script`, como em `confirmCode` (`proxy_migration.go:108`) para sessão e `X-Korego-Code` para token.
  - `POST /api/apps/{id}/stack/{op}`: `op` ∈ `start|stop|restart`. O `stop` passa pelo `gate` e pede `code` no corpo para sessão.
- **Produz** (ferramentas MCP, via `addREST`):
  - `app_adopt_static`;
  - `app_adopt_compose` (gated quando há script);
  - `app_compose_action` (gated no `stop`);
  - o `app_update` passa a aceitar `code`.

**Adoção estática, numa transação:**
1. `ValidateAdoptRoot`.
2. Recusa se já existir app com aquela `PublishRoot`.
3. Cria a app com `PublishRoot` e `AutoDeploy=false`.
4. Cria o deploy `adopt` com status `running`, `ReleaseDir` = versão atual e SHA tirado do nome quando segue o padrão. Grava como `CurrentDeploy`.
5. Todo domínio com rota estática cujo `root` resolvido é igual ao `current` passa a rota "app" para a nova app, mantendo SPA e prefixo imutável na app.
6. `ReapplyApp`.

**Adoção compose:**
1. `FindStack` precisa ter sucesso.
2. Recusa se já existir app com o mesmo `ComposeProject`.
3. Com o modo `script`, `ValidUpdateScript`.
4. Cria a app `local`/`compose-local`, sem deploy.

**Auditoria:** `app.adopted` e `app.stack_action`, com o código da ação.

**Seção "Na VPS":** sem mudança no backend. A detecção do painel passa a ignorar o que foi adotado (Task 8).

**Steps:**
- [ ] **Testes:**
  - **Adoção estática:**
    - `TestAdoptStatic_SwitchesDomainsAndKeepsReleases`: o domínio muda para "app", as versões continuam no disco, o deploy `adopt` aparece no histórico e o `autoDeploy` fica `false`;
    - `TestAdoptStatic_RejectsBadLayout`: recusa com 422;
    - `TestAdoptStatic_Twice`: a segunda adoção recebe 409.
  - **Adoção compose:**
    - `TestAdoptCompose_ScriptNeedsCode`: um token sem código recebe 403 `CODE_REQUIRED`; com código, 201;
    - `TestAdoptCompose_NoContainers`: 422 com mensagem.
  - **Ações e edição:**
    - `TestStackStop_Gated`;
    - `TestUpdateScriptChange_Gated`: o `PUT` que muda `updateScript` sem código recebe 403;
    - `TestLocalEnv_NamesOnly`: o `GET` devolve só os nomes, e o `PUT` grava no `.env` e não na tabela `app_env`;
    - `TestDeleteLocal_NoDown`: nenhum comando `compose down`.
  - **MCP:** `TestMCP_AdoptToolsListed`.
- [ ] Implementar e rodar `…/go.sh test ./internal/api/` até passar.
- [ ] Commit: `feat(api): adoção de sites da VPS e ações de stack`.

### Task 7: Painel — apps estáticas
**Files:**
- Modify `web/src/lib/apps.ts` (types, `defaultsFor`).
- Modify `web/src/components/apps/app-form.tsx`: opção "Estático" em `BUILDS` e os campos novos. A porta e o health check ficam escondidos.
- Modify `web/src/app/(painel)/aplicacoes/detalhe/page.tsx`: para estático, a visão geral mostra a raiz e a versão atual no lugar de porta e URL interna. O botão Voltar do histórico fica desabilitado, com dica, quando o deploy não tem `releaseDir`.
- Modify `web/src/lib/deploys.ts` (`releaseDir` no tipo).
- Modify `web/src/components/domains/*`: o editor lista as apps estáticas como alvo "app". Conferir que já lista todas.
- Tests in `web/src/components/apps/__tests__/app-form.test.tsx` and `web/src/app/__tests__/app-detail*.test.tsx`.

**Steps:**
- [ ] **Testes:**
  - escolher "Estático" mostra os campos com os padrões (`dist`, `.`, `node:22-alpine`, `/assets/`) e esconde a porta;
  - o envio manda `buildType: "static"` sem `internalPort` obrigatório;
  - o detalhe de uma app estática mostra a raiz e a versão;
  - o 409 `PUBLISH_ROOT_EXISTS` aparece com o texto sobre "Gerenciar pelo KoreGO".
- [ ] Implementar. Rodar o vitest, o tsc, o eslint e o build.
- [ ] Commit: `feat(web): apps estáticas no formulário e no detalhe`.

### Task 8: Painel — adoção e stacks
**Files:**
- Create `web/src/components/sites/adopt-dialog.tsx` and `web/src/lib/adopt.ts`.
- Modify `web/src/components/sites/site-card.tsx`: botão "Gerenciar pelo KoreGO" nos tipos `estatico` e `compose`.
- Modify `web/src/lib/sites.ts`: ignorar estáticos sem rota estática, o que já acontece porque a rota vira "app", e stacks cujo projeto está em app `compose-local`. O `useDetectedSites` passa a usar `useApps()`.
- Modify `web/src/app/(painel)/aplicacoes/detalhe/page.tsx`, para `compose-local`:
  - cartão "Stack" com pasta, arquivos e containers (vindos do `useContainers`) e os botões Iniciar, Reiniciar e Parar (Parar pede o código 2FA no diálogo);
  - botão "Atualizar" (`deploy_start`);
  - aba Variáveis com o aviso "Editando <pasta>/.env";
  - sem Voltar.
- Tests in `web/src/components/sites/__tests__/adopt-dialog.test.tsx` and `web/src/lib/__tests__/sites.test.ts` (adoptados somem).

**Diálogo de adoção:**
- **Estático:** projeto (existente, ou "Novo projeto" com nome), slug (sugerido a partir do hostname), origem (GitHub com seletor de repositório como no formulário de app, ou git), branch e campos de build com os padrões.
  - Aviso: "As versões antigas fora do padrão `AAAAMMDDHHMMSS-sha` ficam intocadas; as do padrão entram na limpeza que guarda 5."
  - Depois de salvar: tela com os passos para desligar o workflow do Actions e a caixa "Já desliguei o workflow do Actions; publicar a cada push", que faz o `PUT` com `autoDeploy: true`.
- **Compose:** projeto, slug e modo de atualização (`pull`, ou script com o campo do caminho e o código 2FA).

**Steps:**
- [ ] **Testes:**
  - o diálogo estático envia o corpo certo e mostra a caixa do Actions;
  - o diálogo compose com script exige o código;
  - a stack adotada some de "Na VPS" e aparece no projeto;
  - o botão Parar pede o código.
- [ ] Implementar. Rodar o vitest, o tsc, o eslint e o build.
- [ ] Commit: `feat(web): adoção de sites da VPS e controle de stacks`.

### Task 9: README e verificação visual
**Files:** Modify `README.md`: seção "Sites estáticos" e "Trazer sites da VPS", e a linha do roadmap da v0.8.

**Steps:**
- [ ] Documentar:
  - os padrões de build;
  - o padrão de nome das versões e o prune;
  - os passos de adoção;
  - as ações que exigem 2FA.
- [ ] Gerar o build, servir `out/` com a API simulada e conferir por screenshot, em 1280px e 390px, o formulário estático, o diálogo de adoção e o detalhe de uma stack, sem rolagem horizontal.
- [ ] Commit: `docs: sites estáticos e adoção na v0.8`.
