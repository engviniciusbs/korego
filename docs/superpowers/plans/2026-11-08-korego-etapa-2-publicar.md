# KoreGO — Etapa 2 (Publicar) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Publicar aplicações e domínios pelo painel: domínios com HTTPS automático pelo Caddy (inclusive o domínio do próprio painel e endereços manuais, que cobrem a migração do nginx), deploy de imagem, Dockerfile, Compose ou Railpack sem tempo fora do ar (exceto Compose), rollback, e deploy automático a cada push por um GitHub App.

**Architecture:** Três camadas novas atrás das fronteiras que já existem:
- **Proxy e domínios.** `node/proxy` define a interface `Proxy` e a implementação Caddy: um container `korego-caddy` com `network_mode: host`, controlado pela Admin API em `127.0.0.1:2019`. O pacote `domains` é a fonte da verdade (SQLite): compila as rotas, reaplica tudo no Caddy na inicialização e a cada 30 s quando detecta divergência, e guarda as configurações do painel (domínio, porta 8000 pública).
- **Deploy.** `jobs` é uma fila com concorrência limitada e um item ativo por chave. Sobre ela, `deploy` executa o pipeline buscar → build → subir → saúde → trocar → limpar, com log em arquivo e no tópico `deploy-logs:<id>`.
- **GitHub.** `github` faz o manifest flow, gera os tokens de instalação e valida os webhooks.

O frontend ganha as telas de Projetos, Aplicações, Deploys, Domínios e Configurações → Painel/GitHub.

**Tech Stack:** Go 1.27, SDK do Docker (`github.com/moby/moby/client`), CLI `docker` (build/buildx/compose) e `git` via `exec`, Railpack (`railpack prepare` + frontend BuildKit), Caddy 2 (imagem oficial, Admin API JSON), chi, coder/websocket, modernc.org/sqlite, `crypto/rsa` (JWT RS256 sem dependência nova), `go.yaml.in/yaml/v3` (já é dependência indireta) · Next.js (export estático), TypeScript, Tailwind, TanStack Query, Vitest, Playwright.

**Spec:** `docs/superpowers/specs/2026-11-03-korego-v1-design.md`, seções 6, 7, 9, 10 e 12 (e 3/4 para as fronteiras). Base: código das etapas 0 e 1 em `main`.

## Global Constraints

- Valem todas as restrições globais das etapas 0 e 1:
  - módulo `github.com/engviniciusbs/korego` e `CGO_ENABLED=0`;
  - testes Go passando em Linux **e** Windows (`t.Skip` para o que só existe em Linux);
  - formato de erro `{ "error": { "code", "message" } }`;
  - interface só escura e sem cantos arredondados;
  - nada sensível versionado: exemplos com `exemplo.com` e `203.0.113.10`.
- Commits: um por tarefa, prefixo `feat(etapa-2): ...` (ou `test`, `ci`, `docs`, `build`), terminando com `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- `internal/node` e os subpacotes não importam nenhum outro pacote interno. `node/proxy` pode importar `node/docker`.
- **Nenhum comando via shell**: `git`, `docker` (CLI), `docker compose`, `docker buildx`, `railpack` e `pm2` rodam por `exec.CommandContext(ctx, nome, args...)` com argumentos separados. Nomes, branches, imagens, caminhos e portas são validados antes.
- **Validações exatas:**
  - slug de aplicação: `^[a-z0-9][a-z0-9-]{0,62}$`;
  - repositório GitHub: `^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`;
  - URL Git pública: `https://` sem usuário/senha;
  - branch: `^[A-Za-z0-9._/-]{1,255}$`, sem `..`, sem começar com `-` ou `/`, sem terminar com `/` ou `.lock`;
  - caminho de Dockerfile/Compose: relativo, sem `..`, dentro do diretório do clone;
  - porta: 1–65535;
  - serviço Compose: `^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`;
  - variável: `^[A-Za-z_][A-Za-z0-9_]{0,127}$`;
  - hostname: minúsculo, total ≤ 253, rótulos `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`, ao menos dois rótulos;
  - endereço manual: `host:porta`, com host IP, `localhost` ou hostname válido.
- **Deploy:**
  - Até **2 builds simultâneos** (configurável em `max_builds`) e **um deploy ativo por aplicação**.
  - Fila com uma vaga por aplicação: o novo pedido substitui o que espera, e o substituído fica `superseded`.
  - Status: `queued`, `building`, `starting`, `running`, `failed`, `superseded`, `cancelled`.
  - Gatilhos: `manual`, `webhook`, `rollback`.
  - Imagens `korego/<app>:<sha-curto>`. Containers `korego-<app>-<sha-curto>-d<deployID>`, com labels `korego.managed=true`, `korego.app=<app>` e `korego.deploy=<id>`.
  - Porta publicada só em **`127.0.0.1:<porta livre>`** na faixa **20000–29999**.
  - Healthcheck: usa o do container ou, sem ele, uma conexão TCP; limite padrão **60 s**.
  - Mantém as **3 imagens mais recentes** por aplicação.
  - Diretórios: builds em `/var/lib/korego/builds/<deploy-id>`, logs em `/var/lib/korego/logs/deploy-<id>.log`.
- **Sem tempo fora do ar:**
  - A rota no Caddy só muda depois que o novo container passa na verificação de saúde.
  - Se a verificação falhar, o novo container é removido, o anterior continua no ar e o deploy fica `failed`.
  - Compose recria containers, com alguns segundos fora do ar, e isso aparece documentado na tela.
  - Rollback não existe para Compose na v1.
- **Reinício do KoreGO:** deploys em `building`/`starting` viram `failed` com motivo `"interrompido"`, e os containers que esses deploys criaram são removidos.
- **Segredos:**
  - Criptografados com AES-256-GCM usando a chave mestra (`secrets.Box`): variáveis de ambiente, chave privada do GitHub App e webhook secret.
  - Valores de variáveis nunca voltam para a interface (só os nomes) e nunca aparecem em logs, respostas da API, auditoria ou argumentos de processo. O token de instalação do GitHub vai ao `git` só por variável de ambiente.
- **Proxy:**
  - Container `korego-caddy`, `network_mode: host`, Admin API em `127.0.0.1:2019`, que nunca é exposta.
  - Dados em volumes `korego-caddy-data:/data` e `korego-caddy-config:/config`.
  - Fonte da verdade: tabela `domains`. O KoreGO reaplica tudo na inicialização e quando detecta que o Caddy reiniciou, com verificação a cada **30 s**. Se o container estiver parado, ele tenta subir de novo.
  - **Portas 80/443 ocupadas por outro processo:** o KoreGO não sobe o Caddy, não mexe no processo e mostra **"Proxy inativo: as portas 80/443 estão em uso por <processo>"** com link para o guia de migração.
- **Domínios:**
  - O destino é uma aplicação do KoreGO ou um endereço manual.
  - Ao salvar, o KoreGO verifica o DNS: resolve o domínio e compara com os IPs da VPS. Se não bater, mostra o aviso com o registro A necessário, mas deixa salvar.
  - O estado do SSL aparece como `pending`, `active` (com a data de expiração) ou `error`.
  - Opções: redirecionar `www` ↔ domínio principal e forçar HTTPS (padrão: ligado).
- **GitHub App:**
  - Manifest com nome `KoreGO-<hostname>`, permissões `contents: read` e `metadata: read`, e o evento `push`.
  - Webhook `POST /api/webhooks/github` público. Verifica `X-Hub-Signature-256` (HMAC SHA-256, comparação em tempo constante) e responde 401 se a assinatura for inválida.
  - Exige o painel acessível por HTTPS público.
- **Painel:** o domínio do painel é uma rota para `127.0.0.1:8000`. Depois que o painel estiver acessível por HTTPS, o KoreGO oferece **desativar a porta 8000 pública**, passando a escutar só em `127.0.0.1:8000`.
- Antes de desenhar telas, usar a skill `frontend-design`. Ao fim de cada tarefa de tela, revisar com `web-design-guidelines`.

## Decisões deste plano (onde a especificação deixa escolha)

- **D1 — Build por CLI.** O build de `dockerfile` usa `docker build` (CLI, BuildKit) via `exec`. A especificação diz "Docker via SDK", com o objetivo de evitar interpolação de shell. Ainda não há interpolação, e o SDK usa o builder legado, que não entende recursos comuns de Dockerfiles atuais (`RUN --mount`). Lista, pull, run, inspect e remoção continuam no SDK.
- **D2 — Nome do container com o deploy.** O nome é `korego-<app>-<sha-curto>-d<deployID>`, porque redeploy e rollback do mesmo commit colidiriam com o container ainda no ar.
- **D3 — Railpack por frontend BuildKit.** O build roda `railpack prepare <dir> --plan-out <dir>/railpack-plan.json`, seguido de `docker buildx build --build-arg BUILDKIT_SYNTAX=ghcr.io/railwayapp/railpack-frontend -f <dir>/railpack-plan.json -t <tag> --load <dir>`. Assim não precisa de um daemon BuildKit separado. Sem `railpack` no PATH, o deploy falha com `RAILPACK_NOT_INSTALLED`, e o instalador oferece instalar.
- **D4 — Deploy substituído.** Quando um deploy novo entra no ar, o anterior que estava `running` passa a `superseded`. A aplicação aponta para o deploy atual em `apps.current_deploy_id`.
- **D5 — Domínio do painel nas Configurações.** O domínio do painel é configurado em Configurações → Painel, e não dentro do assistente de setup. A visão geral mostra um aviso para configurá-lo enquanto não houver um. No setup o proxy pode ainda nem estar ativo (nginx em 80/443), e o assistente continua curto.
- **D6 — Troca de porta sem reiniciar.** A porta 8000 pública é desligada e religada sem reiniciar o processo, por um listener trocável. Para recuperar o acesso existe `korego painel --porta-publica` (SSH), que religa a porta.
- **D7 — Callback do GitHub sem cookie.** O retorno do GitHub (`/api/github/callback`) é autenticado por um `state` de uso único (10 min) e não pela sessão. O cookie é `SameSite=Strict` e não vai em navegação vinda do github.com.

## Review Focus

1. **Trancar-se fora do painel.** Desligar a porta 8000 só pode ser feito por uma requisição que chegou por HTTPS no próprio domínio do painel e com o certificado ativo. Se o domínio quebrar depois, `korego painel --porta-publica` religa a porta sem precisar do painel → testes nas Tarefas 7 e 8.
2. **Vazamento de segredo no deploy.** O token do GitHub e os valores das variáveis não podem aparecer em argumentos de processo, no log do deploy (mesmo que o build os imprima), na resposta da API ou na auditoria → testes nas Tarefas 10, 12 e 13.
3. **Callback do GitHub e `SameSite=Strict`.** O retorno do GitHub funciona sem o cookie de sessão. Um `state` reutilizado, expirado ou desconhecido é recusado → teste na Tarefa 15.
4. **Deploy que falha não derruba a versão no ar.** Isso vale para um container novo que não passa na verificação, sai logo depois de subir, ou não consegue a porta, e também para o KoreGO reiniciando no meio do deploy. A rota não muda, o anterior segue respondendo, e os containers órfãos são removidos → testes nas Tarefas 11, 12 e 21.
5. **Caddy reiniciado, parado ou disputado.** Se o container reiniciar (config vazia), em até 30 s tudo é reaplicado. Se parar, sobe de novo. Com 80/443 do nginx, o proxy fica inativo e nada é alterado → testes nas Tarefas 5, 6 e 21.

---

## Estrutura de arquivos

```
internal/node/docker/              docker.go/local.go ganham Pull, Run, RemoveForce, Images, RemoveImage
internal/node/proxy/               proxy.go (interface, tipos), caddyconfig.go, admin.go, caddy.go, ports_linux.go, ports_other.go, cert.go
internal/node/node.go, local.go    Node ganha Proxy()
internal/store/migrations/0003_publish.sql
internal/store/                    settings.go, domains.go, projects.go, apps.go, deploys.go, github.go
internal/domains/                  domains.go (serviço), validate.go, dns.go, routes.go, reconcile.go, panel.go
internal/serve/listener.go         listener trocável
internal/jobs/queue.go             fila com concorrência e vaga única por chave
internal/deploy/                   exec.go, redact.go, source.go, build.go, run.go, compose.go, ports.go, cleanup.go, service.go, logs.go
internal/github/                   jwt.go, client.go, manifest.go, webhook.go
internal/api/                      proxy.go, domains.go, settings.go, projects.go, apps.go, deploys.go, github.go, webhook.go
internal/config/config.go          max_builds, public_ip, github_api_url, github_web_url
cmd/korego/main.go, painel.go      wiring; subcomando `painel --porta-publica`
web/src/lib/                       domains.ts, settings.ts, projects.ts, apps.ts, deploys.ts, github.ts
web/src/app/(painel)/              dominios/, projetos/, projetos/detalhe/, aplicacoes/nova/, aplicacoes/detalhe/, deploys/detalhe/, configuracoes/painel/, configuracoes/github/
web/src/components/                domains/, apps/, deploys/
scripts/install.sh, docs/migracao-nginx.md, README.md, .github/workflows/ci.yml, web/e2e/publicar.spec.ts
testdata/sample-app/               Dockerfile + index.html para os testes de integração
```

---

### Task 1: Cliente Docker para publicar

**Files:**
- Modify: `internal/node/docker/docker.go`, `internal/node/docker/local.go`, `internal/node/docker/dockertest/fake.go`
- Test: `internal/node/docker/convert_test.go`, `internal/node/docker/local_integration_test.go`

**Interfaces:**
- Produces (acrescentado a `docker.Client`):
  - ```go
    type PortMapping struct{ HostIP string; HostPort, ContainerPort uint16; Proto string } // Proto "tcp" padrão
    type VolumeMount struct{ Volume, Target string }   // volume nomeado; criado se não existir
    type RunSpec struct {
        Name, Image   string
        Labels        map[string]string
        Env           []string          // "NOME=valor"; nunca registrado em log
        Ports         []PortMapping
        Volumes       []VolumeMount
        Cmd           []string
        NetworkMode   string            // "" = padrão; "host" para o Caddy
        RestartPolicy string            // "unless-stopped" padrão
    }
    type Image struct{ ID, Ref string; CreatedAt time.Time }
    Pull(ctx context.Context, ref string, fn func(line string)) error      // fn recebe o progresso já em texto ("<id>: <status>")
    Run(ctx context.Context, s RunSpec) (id string, err error)              // create + start; ErrConflict se o nome existe
    RemoveForce(ctx context.Context, id string) error                       // force, sem apagar volumes; ErrNotFound tolerado → nil
    Images(ctx context.Context, repo string) ([]Image, error)               // imagens cujo repositório é repo (ex.: "korego/blog"), mais nova primeiro
    RemoveImage(ctx context.Context, ref string) error                      // sem force; em uso → ErrConflict
    ListByLabel(ctx context.Context, key, value string) ([]Container, error)
    ```
  - `dockertest.Fake` ganha um campo `func` por método novo.
  - Funções puras testáveis: `toCreateConfig(RunSpec)` (portas, labels, restart policy e rede) e `pullLine(jsonmessage) string`.

- [ ] **Step 1: Testes**

```go
func TestToCreateConfig_PortsLabelsRestart(t *testing.T) // 127.0.0.1:20001→8080/tcp; labels copiados; restart padrão unless-stopped; NetworkMode host sem PortBindings
func TestToCreateConfig_EnvNotInError(t *testing.T)     // erro de validação de RunSpec não contém o valor de nenhuma variável
// integration:
func TestLocal_RunPullImages(t *testing.T)  // Pull busybox; Run com porta 127.0.0.1 livre e label; ListByLabel acha; Images("busybox") ≥ 1; RemoveForce; ListByLabel vazio
func TestLocal_RunNameConflict(t *testing.T) // segundo Run com o mesmo nome → ErrConflict
```

- [ ] **Step 2: Rodar** `go test ./internal/node/docker/...`: FAIL.
- [ ] **Step 3: Implementar.**
- [ ] **Step 4: Rodar** `go test ./... && go vet ./...` e `go test -tags integration ./internal/node/docker/`: PASS.
- [ ] **Step 5: Commit** `feat(etapa-2): cliente Docker com pull, run, imagens e remoção forçada`

---

### Task 2: Banco — configurações e domínios

**Files:**
- Create: `internal/store/migrations/0003_publish.sql`, `internal/store/settings.go`, `internal/store/domains.go`
- Modify: `internal/store/migrate_test.go`
- Test: `internal/store/settings_test.go`, `internal/store/domains_test.go`

**Interfaces:**
- Produces:
  - A migração `0003_publish.sql` cria **todas** as tabelas da etapa (as Tarefas 3 e 14 só acrescentam repositórios):
    ```sql
    CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
    CREATE TABLE projects (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, description TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL);
    CREATE TABLE apps (
        id INTEGER PRIMARY KEY, project_id INTEGER NOT NULL REFERENCES projects(id),
        slug TEXT NOT NULL UNIQUE, source_type TEXT NOT NULL,      -- github | git | image
        repo TEXT NOT NULL DEFAULT '', image TEXT NOT NULL DEFAULT '',
        build_type TEXT NOT NULL,                                   -- dockerfile | compose | railpack | image
        branch TEXT NOT NULL DEFAULT '', dockerfile_path TEXT NOT NULL DEFAULT 'Dockerfile',
        compose_path TEXT NOT NULL DEFAULT 'docker-compose.yml', compose_service TEXT NOT NULL DEFAULT '',
        internal_port INTEGER NOT NULL, health_timeout_s INTEGER NOT NULL DEFAULT 60,
        auto_deploy INTEGER NOT NULL DEFAULT 0, host_port INTEGER NOT NULL DEFAULT 0,
        current_deploy_id INTEGER, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
    );
    CREATE TABLE app_env (app_id INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE, name TEXT NOT NULL, value_enc TEXT NOT NULL, PRIMARY KEY (app_id, name));
    CREATE TABLE deploys (
        id INTEGER PRIMARY KEY, app_id INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
        trigger TEXT NOT NULL, status TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '',
        commit_sha TEXT NOT NULL DEFAULT '', commit_message TEXT NOT NULL DEFAULT '',
        image TEXT NOT NULL DEFAULT '', host_port INTEGER NOT NULL DEFAULT 0, container_id TEXT NOT NULL DEFAULT '',
        rollback_of INTEGER, created_at INTEGER NOT NULL, started_at INTEGER, finished_at INTEGER
    );
    CREATE INDEX idx_deploys_app ON deploys(app_id, id DESC);
    CREATE TABLE domains (
        id INTEGER PRIMARY KEY, hostname TEXT NOT NULL UNIQUE,
        target_type TEXT NOT NULL,                 -- app | address
        app_id INTEGER REFERENCES apps(id), address TEXT NOT NULL DEFAULT '',
        www_redirect TEXT NOT NULL DEFAULT 'none', -- none | www_to_apex | apex_to_www
        force_https INTEGER NOT NULL DEFAULT 1, is_panel INTEGER NOT NULL DEFAULT 0,
        created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
    );
    CREATE TABLE github_app (
        id INTEGER PRIMARY KEY CHECK (id = 1), app_id INTEGER NOT NULL, slug TEXT NOT NULL, owner TEXT NOT NULL,
        html_url TEXT NOT NULL, private_key_enc TEXT NOT NULL, webhook_secret_enc TEXT NOT NULL, created_at INTEGER NOT NULL
    );
    ```
  - `(*DB).GetSetting(ctx, key) (string, bool, error)`, `(*DB).SetSetting(ctx, key, value) error`, `(*DB).DeleteSetting(ctx, key) error`. Chaves usadas: `panel.public_port` (`"true"`/`"false"`, ausente = `true`).
  - `store.Domain{ID int64; Hostname, TargetType string; AppID *int64; Address, WWWRedirect string; ForceHTTPS, IsPanel bool; CreatedAt, UpdatedAt time.Time}`.
  - `(*DB).InsertDomain(ctx, *Domain) error` (hostname repetido → `ErrConflict`), `UpdateDomain`, `DeleteDomain(ctx, id)`, `GetDomain(ctx, id)` (→ `ErrNotFound`), `ListDomains(ctx) ([]Domain, error)` (por hostname), `PanelDomain(ctx) (Domain, bool, error)`, `DomainsByApp(ctx, appID) ([]Domain, error)`.
  - Erros sentinela novos em `internal/store/errors.go`, se ainda não existirem: `ErrConflict` e `ErrNotFound`.

- [ ] **Step 1: Testes**

```go
func TestMigrate_Version3(t *testing.T)            // versão 3; tabelas settings, projects, apps, app_env, deploys, domains, github_app existem
func TestSettings_RoundTrip(t *testing.T)          // ausente → ok=false; Set/Get; Set sobrescreve; Delete
func TestDomains_CRUDAndConflict(t *testing.T)     // insert/get/update/list; hostname repetido → ErrConflict; delete inexistente → ErrNotFound
func TestDomains_PanelAndByApp(t *testing.T)       // PanelDomain acha o is_panel; DomainsByApp filtra
```

- [ ] **Step 2: Rodar** `go test ./internal/store/`: FAIL.
- [ ] **Step 3: Implementar.**
- [ ] **Step 4: Rodar** `go test ./internal/store/`: PASS.
- [ ] **Step 5: Commit** `feat(etapa-2): tabelas de publicação, configurações e domínios`

---

### Task 3: Banco — projetos, aplicações, variáveis e deploys

**Files:**
- Create: `internal/store/projects.go`, `internal/store/apps.go`, `internal/store/deploys.go`
- Test: `internal/store/projects_test.go`, `internal/store/apps_test.go`, `internal/store/deploys_test.go`

**Interfaces:**
- Consumes: as tabelas da Tarefa 2.
- Produces:
  - `store.Project{ID int64; Name, Description string; CreatedAt time.Time}`. CRUD:
    - `InsertProject(ctx, *Project)`: nome repetido → `ErrConflict`.
    - `UpdateProject`, `GetProject`, `ListProjects`.
    - `DeleteProject`: com aplicações → `ErrConflict`.
  - `store.App` espelha a tabela (campos Go em CamelCase; `AutoDeploy bool`; `CurrentDeployID *int64`). Métodos:
    - `InsertApp(ctx, *App)`: slug repetido → `ErrConflict`.
    - `UpdateApp`, `GetApp`, `GetAppBySlug`, `ListApps(ctx, projectID *int64)`.
    - `DeleteApp`: com domínios apontando → `ErrConflict`.
    - `AppsByRepo(ctx, repo, branch string) ([]App, error)`: só `source_type='github'` e `auto_deploy=1`.
    - `UsedHostPorts(ctx) (map[int]bool, error)`: `host_port` das aplicações e dos deploys em `starting`/`running`.
  - Variáveis, com valores já cifrados pela camada de serviço:
    - `SetAppEnv(ctx, appID int64, set map[string]string /*nome→value_enc*/, remove []string) error`, numa transação.
    - `AppEnvNames(ctx, appID) ([]string, error)`, em ordem alfabética.
    - `AppEnvEncrypted(ctx, appID) (map[string]string, error)`.
  - `store.Deploy` espelha a tabela.
  - Métodos de deploy:
    - `InsertDeploy(ctx, *Deploy) error`
    - `GetDeploy(ctx, id)`
    - `ListDeploys(ctx, appID int64, limit int) ([]Deploy, error)`, mais novo primeiro.
    - `UpdateDeployStatus(ctx, id int64, status, reason string, now time.Time) error`: define `started_at` ao entrar em `building` e `finished_at` em estado final.
    - `SetDeployResult(ctx, id int64, image string, hostPort int, containerID, sha, msg string) error`
    - `SetCurrentDeploy(ctx, appID, deployID int64) error`
    - `MarkInterrupted(ctx, now) ([]Deploy, error)`: `building`/`starting` → `failed`, com `reason="interrompido"`; devolve os afetados.
    - `QueuedDeploys(ctx) ([]Deploy, error)`
    - `LatestImages(ctx, appID int64, n int) ([]string, error)`: imagens distintas dos últimos deploys que chegaram a `running` ou `superseded`, mais nova primeiro.

- [ ] **Step 1: Testes**

```go
func TestProjects_DeleteWithAppsConflict(t *testing.T)
func TestApps_SlugConflictAndByRepo(t *testing.T)      // AppsByRepo ignora auto_deploy=0, outra branch e source_type != github
func TestApps_DeleteWithDomainConflict(t *testing.T)
func TestAppEnv_SetRemoveNames(t *testing.T)           // set A,B; remove A; names [B]; Encrypted devolve só B
func TestDeploys_StatusTimestamps(t *testing.T)        // building define started_at; failed define finished_at
func TestDeploys_MarkInterrupted(t *testing.T)         // building e starting viram failed "interrompido"; queued e running intocados
func TestDeploys_LatestImagesDistinct(t *testing.T)    // 5 deploys com 4 imagens, n=3 → 3 distintas, mais nova primeiro
func TestUsedHostPorts(t *testing.T)
```

- [ ] **Step 2: Rodar** `go test ./internal/store/`: FAIL.
- [ ] **Step 3: Implementar.**
- [ ] **Step 4: Rodar** `go test ./internal/store/`: PASS.
- [ ] **Step 5: Commit** `feat(etapa-2): repositórios de projetos, aplicações, variáveis e deploys`

---

### Task 4: Configuração do Caddy e Admin API

**Files:**
- Create: `internal/node/proxy/proxy.go`, `internal/node/proxy/caddyconfig.go`, `internal/node/proxy/admin.go`
- Test: `internal/node/proxy/caddyconfig_test.go`, `internal/node/proxy/admin_test.go`, `internal/node/proxy/testdata/*.json`

**Interfaces:**
- Produces:
  - ```go
    type Route struct {
        Host        string // hostname principal
        Upstream    string // "127.0.0.1:20001"
        ForceHTTPS  bool
        WWWRedirect string // none | www_to_apex | apex_to_www
    }
    ```
  - `BuildConfig(routes []Route) ([]byte, error)` monta o JSON completo do Caddy, determinístico (ordem por host):
    - `admin.listen` é `"127.0.0.1:2019"`.
    - Servidor `korego_https` em `:443` com `automatic_https.disable_redirects: true`. Tem um handler `reverse_proxy` por host, com `host` matcher e cabeçalhos `X-Forwarded-*` padrão.
    - Servidor `korego_http` em `:80`. Para cada host: com `ForceHTTPS`, `static_response` 308 para `https://{http.request.host}{http.request.uri}`; sem ele, o mesmo `reverse_proxy`.
    - `www_to_apex`/`apex_to_www`: o host secundário entra só como redirecionamento 308 para o principal (nos dois servidores) e também recebe certificado.
    - `apps.tls.automation.policies` cobre todos os hosts.
    - **Verificar na documentação do Caddy** que o desafio HTTP-01 continua respondido com um servidor próprio em `:80`. Se não for, ajustar a forma e explicar no relatório.
  - `AdminClient{BaseURL string; HTTP *http.Client}`, com `BaseURL` padrão `http://127.0.0.1:2019`. Métodos:
    - `Load(ctx, cfg []byte) error`: `POST /load`. Status ≠ 200 → erro com o corpo cortado em 2 KiB.
    - `Config(ctx) ([]byte, error)`: `GET /config/`.
    - `Ping(ctx) error`: `GET /config/` respondendo.
  - `SameConfig(a, b []byte) bool` compara depois de normalizar: unmarshal para `any` e marshal de novo. `null` e vazio contam como diferentes de qualquer config com rotas.

- [ ] **Step 1: Testes**

```go
func TestBuildConfig_Golden(t *testing.T)          // 3 rotas (force https, sem force, www_to_apex) → igual a testdata/three-routes.json
func TestBuildConfig_Empty(t *testing.T)           // sem rotas → JSON válido só com admin e servidores sem rotas
func TestBuildConfig_Deterministic(t *testing.T)   // rotas em ordem diferente → mesmo JSON
func TestAdmin_LoadAndConfig(t *testing.T)         // httptest: POST /load recebe o corpo; erro 400 vira erro com o corpo
func TestSameConfig(t *testing.T)                  // espaços e ordem de chaves diferentes → true; "null" vs config → false
```

- [ ] **Step 2: Rodar** `go test ./internal/node/proxy/`: FAIL.
- [ ] **Step 3: Implementar.**
- [ ] **Step 4: Rodar** `go test ./internal/node/proxy/`: PASS.
- [ ] **Step 5: Commit** `feat(etapa-2): configuração JSON do Caddy e cliente da Admin API`

---

### Task 5: Proxy Caddy — container, portas e certificados

**Files:**
- Create: `internal/node/proxy/caddy.go`, `internal/node/proxy/ports_linux.go`, `internal/node/proxy/ports_other.go`, `internal/node/proxy/cert.go`
- Modify: `internal/node/node.go`, `internal/node/local.go`, `internal/node/nodetest/fake.go`
- Test: `internal/node/proxy/caddy_test.go`, `internal/node/proxy/ports_linux_test.go`, `internal/node/proxy/cert_test.go`, `internal/node/proxy/caddy_integration_test.go`

**Interfaces:**
- Consumes: `docker.Client` (Tarefa 1); `BuildConfig`, `AdminClient`, `SameConfig` (Tarefa 4).
- Produces:
  - ```go
    type State string // "active" | "starting" | "blocked" | "docker_unavailable" | "error"
    type Status struct {
        State   State  `json:"state"`
        Process string `json:"process,omitempty"` // "nginx (pid 1402285)" quando blocked
        Message string `json:"message,omitempty"`
    }
    type CertInfo struct {
        State     string    `json:"state"` // pending | active | error
        NotAfter  time.Time `json:"notAfter,omitempty"`
        Issuer    string    `json:"issuer,omitempty"`
        Message   string    `json:"message,omitempty"`
    }
    type Proxy interface {
        Ensure(ctx context.Context) Status                    // sobe o korego-caddy se possível; nunca mexe em processo alheio
        Apply(ctx context.Context, routes []Route) error      // Load da config inteira; ErrInactive se não está active
        InSync(ctx context.Context, routes []Route) (bool, error) // config atual == BuildConfig(routes)
        Cert(ctx context.Context, host string) CertInfo
    }
    var ErrInactive = errors.New("proxy inativo")
    ```
  - `proxy.NewCaddy(d docker.Client, admin *AdminClient) *Caddy`. A constante `CaddyImage` é a tag estável atual de `caddy:2.x-alpine` (fixar a versão exata existente no Docker Hub no momento da implementação).
  - `Ensure`:
    - Container `korego-caddy` existe e roda: `active` se a Admin API responde, senão `starting`.
    - Existe parado: `Start`.
    - Não existe: verifica 80/443 com `PortOwner` e, se livres, roda `Run` com `NetworkMode "host"`, volumes `korego-caddy-data:/data` e `korego-caddy-config:/config`, label `korego.managed=true`, `Cmd ["caddy","run"]` e `RestartPolicy "unless-stopped"`.
    - Porta ocupada por outro processo: `blocked` com o nome do processo.
    - Docker fora: `docker_unavailable`.
  - `PortOwner(port int) (string, bool)`:
    - Em Linux, lê `/proc/net/tcp` e `/proc/net/tcp6`, procura a escuta em `0.0.0.0`/`::` ou qualquer IP naquela porta e resolve o inode por `/proc/*/fd`, devolvendo `"<comm> (pid N)"`. A raiz `/proc` é injetável.
    - Fora de Linux, tenta `net.Listen` e devolve `"outro processo"` se falhar.
  - `Cert` faz dial TLS em `127.0.0.1:443` com `ServerName=host` e timeout de 3 s:
    - certificado validado pelas raízes do sistema → `active` com `NotAfter` e `Issuer`;
    - certificado não confiável (ainda o interno do Caddy) → `pending`;
    - falha de conexão → `error`.
    - O endereço e as raízes são injetáveis para teste.
  - `node.Node` ganha `Proxy() proxy.Proxy`, e `nodetest.Fake` ganha `ProxyImpl proxy.Proxy`.
  - `proxytest.Fake` (`internal/node/proxy/proxytest/fake.go`): funções por método e as rotas da última `Apply` gravadas.

- [ ] **Step 1: Testes**

```go
func TestEnsure_StartsWhenPortsFree(t *testing.T)     // fake docker sem korego-caddy, PortOwner livre → Run com host network, volumes e Cmd
func TestEnsure_BlockedByOtherProcess(t *testing.T)   // PortOwner(80) = "nginx (pid 7)" → State blocked, Process "nginx (pid 7)", Run nunca chamado
func TestEnsure_RestartsStopped(t *testing.T)         // korego-caddy exited → Start chamado
func TestEnsure_DockerDown(t *testing.T)              // List → ErrUnavailable → docker_unavailable
func TestApply_InactiveError(t *testing.T)
func TestInSync_DetectsEmptyAfterRestart(t *testing.T)// admin devolve null → false
func TestPortOwner_ProcFixture(t *testing.T)          // /proc falso com tcp e fd → "nginx (pid 1234)" (só Linux)
func TestCert_StatesWithTestServer(t *testing.T)      // tls.Server com cert da CA de teste confiável → active; não confiável → pending; nada escutando → error
// integration (Linux, Docker): sobe korego-caddy, aplica rota exemplo.localhost → http server de teste, GET via 127.0.0.1:80 com Host → 200; docker restart korego-caddy → InSync false; Apply → true
```

- [ ] **Step 2: Rodar** `go test ./internal/node/...`: FAIL.
- [ ] **Step 3: Implementar.**
- [ ] **Step 4: Rodar** `go test ./... && go vet ./...` e, em Linux ou num container `golang:1.27` com o socket do Docker, o teste de integração: PASS.
- [ ] **Step 5: Commit** `feat(etapa-2): proxy Caddy com container próprio, detecção de portas e estado dos certificados`

---

### Task 6: Serviço de domínios e reconciliação

**Files:**
- Create: `internal/domains/domains.go`, `internal/domains/validate.go`, `internal/domains/dns.go`, `internal/domains/routes.go`, `internal/domains/reconcile.go`
- Modify: `internal/config/config.go`
- Test: `internal/domains/validate_test.go`, `internal/domains/dns_test.go`, `internal/domains/routes_test.go`, `internal/domains/reconcile_test.go`, `internal/domains/domains_test.go`

**Interfaces:**
- Consumes: `store` (Tarefas 2–3), `proxy.Proxy` (Tarefa 5), `events.Hub`.
- Produces:
  - `ValidHostname(s string) bool` e `ValidAddress(s string) bool`, com as regras das Global Constraints.
  - `domains.Service{DB *store.DB; Proxy proxy.Proxy; Hub *events.Hub; Resolver Resolver; HostIPs func() ([]net.IP, error); PublicIP string; Now func() time.Time; Interval time.Duration /*0 = 30s*/}`.
  - `Resolver interface{ LookupIP(ctx context.Context, host string) ([]net.IP, error) }`. Padrão: `net.DefaultResolver` com rede `"ip"`.
  - `HostIPs` padrão: os endereços *global unicast* das interfaces, mais `cfg.PublicIP` se configurado (`toml:"public_ip"`, env `KOREGO_PUBLIC_IP`).
  - `(*Service).CheckDNS(ctx, host) DNSCheck`, com `DNSCheck{OK bool; Resolved []string; Expected []string; Message string}`. Se não bater, `Message` = `Crie um registro A para <host> apontando para <IP>.`.
  - CRUD:
    - `Create(ctx, in DomainInput) (store.Domain, DNSCheck, error)`
    - `Update(ctx, id, in)`
    - `Delete(ctx, id)`
    - `List(ctx) ([]DomainView, error)`
  - `DomainInput{Hostname, TargetType string; AppID *int64; Address, WWWRedirect string; ForceHTTPS bool}`. Validação:
    - inválido → `ErrInvalid` com campo e mensagem;
    - aplicação inexistente → `ErrInvalid`;
    - domínio do painel não é alterável por aqui → `ErrPanelDomain`.
  - Depois de salvar, `Reapply(ctx)`. Se o proxy estiver inativo, o domínio é salvo do mesmo jeito.
  - `DomainView` = `store.Domain` + `Cert proxy.CertInfo` + `AppSlug string`.
  - `Routes(ctx) ([]proxy.Route, error)`:
    - domínio para aplicação → `127.0.0.1:<apps.host_port>`, só se `host_port > 0`;
    - endereço manual → como está;
    - domínio do painel → `127.0.0.1:<porta do painel>`, com a porta vinda de `PanelPort int` no `Service`.
  - `Reapply(ctx) error`: `Routes` + `Proxy.Apply`.
  - `Run(ctx)` (loop):
    - a cada `Interval`, chama `Proxy.Ensure`;
    - se `active` e `!InSync`, chama `Reapply` e registra no log;
    - publica `Status` no tópico `proxy` quando muda;
    - mantém o último `Status`, exposto por `Status()`.
  - `ReapplyApp(ctx, appID int64) error` é o gancho que o deploy chama depois de trocar a porta. Equivale a `Reapply`.

- [ ] **Step 1: Testes**

```go
func TestValidHostname(t *testing.T)   // ok: "exemplo.com", "app.exemplo.com", "a-b.exemplo.com.br", "app.korego.localhost"; não: "Exemplo.com", "exemplo", "-a.com", "a..com", 254 chars, "a_b.com"
func TestValidAddress(t *testing.T)    // ok: "127.0.0.1:5000", "localhost:3000", "[::1]:80", "api.interna:8080"; não: "127.0.0.1", ":80", "x:0", "x:70000", "a b:1"
func TestCheckDNS(t *testing.T)        // resolver falso 203.0.113.10 e HostIPs [203.0.113.10] → OK; 198.51.100.1 → !OK com a mensagem do registro A
func TestRoutes_AppAddressPanel(t *testing.T) // app sem host_port fica de fora; manual igual; painel → 127.0.0.1:8000
func TestCreate_SavesEvenWhenProxyBlocked(t *testing.T) // Apply → ErrInactive; domínio gravado e sem erro
func TestCreate_InvalidAndPanelProtected(t *testing.T)
func TestRun_ReappliesWhenOutOfSync(t *testing.T)      // InSync false na 2ª volta → Apply chamado com as rotas atuais
func TestRun_PublishesStatusChanges(t *testing.T)      // blocked → active publica duas vezes; repetido não publica
```

- [ ] **Step 2: Rodar** `go test ./internal/domains/ ./internal/config/`: FAIL.
- [ ] **Step 3: Implementar.**
- [ ] **Step 4: Rodar** `go test ./... && go vet ./...`: PASS.
- [ ] **Step 5: Commit** `feat(etapa-2): serviço de domínios com verificação de DNS e reconciliação do proxy`

---

### Task 7: Domínio do painel e listener trocável

**Files:**
- Create: `internal/domains/panel.go`, `internal/serve/listener.go`, `cmd/korego/painel.go`
- Modify: `cmd/korego/main.go`
- Test: `internal/domains/panel_test.go`, `internal/serve/listener_test.go`, `cmd/korego/painel_test.go`

**Interfaces:**
- Consumes: o `domains.Service` (Tarefa 6) e `store` settings (Tarefa 2).
- Produces:
  - `serve.Listener`: `NewListener(addr string) (*Listener, error)`; `Accept`, `Close` e `Addr` delegam ao listener atual. `Switch(addr string) error` abre o novo endereço **depois** de fechar o antigo. As conexões já aceitas continuam, e `Accept` segue funcionando sem erro para o `http.Server`. Se abrir o novo falhar, reabre o antigo e devolve o erro.
  - `domains.PanelSettings{Domain string; DomainID int64; Cert proxy.CertInfo; PublicPort bool; ListenAddr string}`.
  - `(*Service).Panel(ctx) (PanelSettings, error)`.
  - `(*Service).SetPanelDomain(ctx, host string) error`:
    - vazio remove;
    - senão cria ou atualiza o domínio `is_panel=1`, `target_type=address`, `force_https=1`;
    - depois chama `Reapply`.
  - `(*Service).SetPublicPort(ctx, enabled bool, viaPanelHTTPS bool) error`:
    - desligar exige `viaPanelHTTPS` e certificado `active`, senão `ErrPanelNotHTTPS`;
    - grava `panel.public_port` e chama o callback `OnListenChange(addr string) error`;
    - o endereço é `127.0.0.1:<porta>` quando desligada e o `cfg.ListenAddr` original quando ligada.
  - `main`:
    - usa `serve.NewListener` com o endereço efetivo (configuração + `panel.public_port`) e liga `OnListenChange` a `listener.Switch`;
    - `update-finish` continua usando `127.0.0.1:<porta>` para a verificação de saúde;
    - subcomando `korego painel --porta-publica [--config ...]`: abre o banco, grava `panel.public_port=true`, imprime `Porta pública reativada. Reinicie: systemctl restart korego`. O comando `uso:` passa a listar `painel`.

- [ ] **Step 1: Testes**

```go
func TestListener_SwitchKeepsServing(t *testing.T)     // http.Server em NewListener("127.0.0.1:0"); GET ok; Switch para outra porta; GET na nova ok; antiga recusa
func TestListener_SwitchFailureRestores(t *testing.T)  // Switch para porta ocupada → erro e a antiga continua aceitando
func TestPanel_DisableRequiresHTTPS(t *testing.T)      // viaPanelHTTPS=false → ErrPanelNotHTTPS; cert pending → ErrPanelNotHTTPS; active + https → OnListenChange("127.0.0.1:8000")
func TestPanel_SetDomainCreatesPanelRoute(t *testing.T)// Routes inclui host → 127.0.0.1:8000; trocar o host substitui; vazio remove
func TestPainelCommand_ReenablesPublicPort(t *testing.T) // banco temporário com public_port=false → comando → true
```

- [ ] **Step 2: Rodar** `go test ./internal/domains/ ./internal/serve/ ./cmd/korego/`: FAIL.
- [ ] **Step 3: Implementar.**
- [ ] **Step 4: Rodar** `go test ./... && go vet ./...`: PASS.
- [ ] **Step 5: Commit** `feat(etapa-2): domínio do painel, porta 8000 sob controle e recuperação pela linha de comando`

---

### Task 8: API de proxy, domínios e painel

**Files:**
- Create: `internal/api/proxy.go`, `internal/api/domains.go`, `internal/api/settings.go`
- Modify: `internal/api/server.go`, `cmd/korego/main.go`
- Test: `internal/api/domains_test.go`, `internal/api/settings_test.go`

**Interfaces:**
- Consumes: `domains.Service` (Tarefas 6–7), auditoria e `requestIsHTTPS` (etapa 0).
- Produces. Todas as rotas exigem sessão; as que alteram estado exigem CSRF.
  - Proxy:
    - `GET /api/proxy` → `proxy.Status` + `{"guideUrl":"https://github.com/engviniciusbs/korego/blob/main/docs/migracao-nginx.md"}`.
    - `POST /api/proxy/retry` → `Ensure` imediato, devolve o status.
  - Domínios:
    - `GET /api/domains` → `{"domains":[DomainView]}`.
    - `POST /api/domains` → 201 `{"domain":..., "dns": DNSCheck}`.
    - `PUT /api/domains/{id}` → 200 com o mesmo corpo.
    - `DELETE /api/domains/{id}` → 204.
    - `GET /api/domains/{id}/dns` → `DNSCheck`.
  - Erros:
    - `INVALID_DOMAIN` (400) com `field`;
    - `DOMAIN_EXISTS` (409);
    - `DOMAIN_NOT_FOUND` (404);
    - `PANEL_DOMAIN_PROTECTED` (409), ao alterar ou apagar o domínio do painel por estas rotas.
  - Painel:
    - `GET /api/settings/panel` → `PanelSettings` + `{"requestHTTPS": bool, "requestHost": "<host>"}`.
    - `PUT /api/settings/panel/domain` com `{"hostname": "..." | ""}`.
    - `PUT /api/settings/panel/public-port` com `{"enabled": bool}`. `viaPanelHTTPS` = `requestIsHTTPS(r) && r.Host == domínio do painel`. Recusa → 409 `PANEL_NOT_HTTPS` "Acesse o painel pelo domínio com HTTPS antes de desativar a porta 8000.".
  - Auditoria: `domain.create`, `domain.update`, `domain.delete` (target = hostname), `panel.domain`, `panel.public_port` (metadata `{"enabled"}`).
  - `main` cria o `domains.Service`, roda `Run` em goroutine com o contexto do servidor e chama `Reapply` uma vez na subida.

- [ ] **Step 1: Testes**

```go
func TestDomainsAPI_CreateReturnsDNSWarning(t *testing.T)
func TestDomainsAPI_InvalidAndConflict(t *testing.T)
func TestDomainsAPI_PanelProtected(t *testing.T)
func TestDomainsAPI_Audited(t *testing.T)
func TestPanelAPI_DisablePortOverPlainHTTP409(t *testing.T)
func TestPanelAPI_DisablePortViaPanelDomain(t *testing.T)    // requisição de 127.0.0.1 com X-Forwarded-Proto https e Host = domínio do painel, cert active → 200 e OnListenChange chamado
func TestProxyAPI_StatusAndRetry(t *testing.T)
```

- [ ] **Step 2: Rodar** `go test ./internal/api/`: FAIL.
- [ ] **Step 3: Implementar.**
- [ ] **Step 4: Rodar** `go test ./... && go vet ./...`: PASS.
- [ ] **Step 5: Commit** `feat(etapa-2): API de proxy, domínios e configurações do painel`

---

### Task 9: Fila de tarefas

**Files:**
- Create: `internal/jobs/queue.go`
- Test: `internal/jobs/queue_test.go`

**Interfaces:**
- Produces:
  - ```go
    type Job struct {
        Key string                       // ex.: "app:12"
        ID  int64                        // ex.: deploy id
        Run func(ctx context.Context)
        OnSuperseded func()              // chamado quando outro Job da mesma chave toma a vaga de espera
    }
    func NewQueue(concurrency int) *Queue   // ≤ 0 → 2
    func (q *Queue) Enqueue(j Job)
    func (q *Queue) Cancel(id int64) bool   // em espera → removido (true); rodando → ctx cancelado (true); senão false
    func (q *Queue) Close()                 // cancela os que rodam e espera terminarem
    ```
  - Regras:
    - no máximo `concurrency` jobs rodando ao mesmo tempo;
    - por chave, no máximo **um rodando e um esperando**, e um novo `Enqueue` da mesma chave substitui o que espera (chamando o `OnSuperseded` dele);
    - chaves diferentes saem em ordem de chegada.

- [ ] **Step 1: Testes**

```go
func TestQueue_ConcurrencyLimit(t *testing.T)     // 5 chaves, concurrency 2 → nunca mais de 2 ao mesmo tempo
func TestQueue_OnePerKey(t *testing.T)            // mesma chave: o segundo só começa quando o primeiro termina
func TestQueue_SupersedesWaiting(t *testing.T)    // A rodando, B e C enfileirados na mesma chave → B.OnSuperseded chamado; C roda depois de A
func TestQueue_CancelWaitingAndRunning(t *testing.T)
func TestQueue_CloseCancelsAndWaits(t *testing.T)
```

- [ ] **Step 2: Rodar** `go test ./internal/jobs/`: FAIL.
- [ ] **Step 3: Implementar.**
- [ ] **Step 4: Rodar** `go test ./internal/jobs/ -count=20`: PASS.
- [ ] **Step 5: Commit** `feat(etapa-2): fila de tarefas com limite de concorrência e vaga única por aplicação`

---

### Task 10: Deploy — busca do código e build

**Files:**
- Create: `internal/deploy/exec.go`, `internal/deploy/redact.go`, `internal/deploy/source.go`, `internal/deploy/build.go`, `internal/deploy/validate.go`
- Test: `internal/deploy/redact_test.go`, `internal/deploy/source_test.go`, `internal/deploy/build_test.go`, `internal/deploy/validate_test.go`

**Interfaces:**
- Produces:
  - `Runner interface{ Run(ctx context.Context, c Cmd, out io.Writer) error }`, com `Cmd{Name string; Args, Env []string; Dir string}`.
  - `ExecRunner` usa `exec.CommandContext`, com stdout e stderr juntos em `out`, `Env = os.Environ() + c.Env` e `GIT_TERMINAL_PROMPT=0`.
  - `NewRedactor(w io.Writer, secrets []string) io.Writer` troca cada segredo de 4 caracteres ou mais por `••••`, mesmo quando o segredo chega partido entre duas chamadas a `Write`. Também mascara o padrão `x-access-token:<qualquer coisa>@`.
  - Validações das Global Constraints: `ValidSlug`, `ValidRepo`, `ValidGitURL`, `ValidBranch`, `ValidRelPath`, `ValidImageRef`, `ValidServiceName` e `ValidEnvName`. `ValidImageRef` aceita referências Docker com registro `host[:porta]/` opcional, `:tag` e `@sha256:` opcionais, tudo minúsculo exceto a tag.
  - `Fetch(ctx, r Runner, dir string, src Source, out io.Writer) (sha, msg string, err error)`:
    - `Source{URL, Branch, Token string}`;
    - roda `git clone --depth 1 --branch <b> --single-branch <URL> <dir>`;
    - com `Token`, passa `GIT_CONFIG_COUNT=1`, `GIT_CONFIG_KEY_0=http.extraHeader` e `GIT_CONFIG_VALUE_0=Authorization: Basic base64("x-access-token:<token>")` **no Env**, nunca nos Args;
    - depois `git -C <dir> rev-parse HEAD` e `git -C <dir> log -1 --format=%s`.
  - `Build(ctx, r Runner, app store.App, dir, tag string, out io.Writer) error`:
    - `dockerfile`: `docker build -t <tag> -f <dir>/<dockerfile_path> <dir>`;
    - `railpack` (D3): sem `railpack` no PATH → `ErrRailpackMissing`;
    - `compose`: `docker compose -p korego-<app> -f <dir>/<compose_path> build`.
  - `ImageForApp(app, sha string, imageID string) string` → `korego/<slug>:<sha[:7]>`. Para `image`, usa os 7 primeiros hex do ID da imagem puxada.
  - `PullAndTag(ctx, d docker.Client, r Runner, ref, tag string, out io.Writer) error`: `d.Pull` seguido de `docker tag <ref> <tag>` via Runner.

- [ ] **Step 1: Testes**

```go
func TestRedactor_SplitWritesAndToken(t *testing.T) // "seg" + "redo123" com segredo "segredo123" → "••••"; "https://x-access-token:abc@github.com" mascarado
func TestFetch_TokenOnlyInEnv(t *testing.T)         // Runner falso: nenhum Arg contém o token nem "x-access-token"; Env tem GIT_CONFIG_VALUE_0 com Basic
func TestFetch_ArgsExact(t *testing.T)
func TestBuild_DockerfileArgs(t *testing.T)
func TestBuild_RailpackMissing(t *testing.T)
func TestBuild_ComposeArgs(t *testing.T)
func TestValidators(t *testing.T)                   // tabela com casos aceitos e recusados para cada validador, incluindo "../x", "/abs", "-branch", "main.lock", "Imagem:latest"
```

- [ ] **Step 2: Rodar** `go test ./internal/deploy/`: FAIL.
- [ ] **Step 3: Implementar.**
- [ ] **Step 4: Rodar** `go test ./internal/deploy/`: PASS.
- [ ] **Step 5: Commit** `feat(etapa-2): busca do código e build do deploy sem expor segredos`

---

### Task 11: Deploy — subir, verificar, trocar e limpar

**Files:**
- Create: `internal/deploy/run.go`, `internal/deploy/compose.go`, `internal/deploy/ports.go`, `internal/deploy/cleanup.go`
- Modify: `go.mod` (`go.yaml.in/yaml/v3` passa a direta)
- Test: `internal/deploy/run_test.go`, `internal/deploy/compose_test.go`, `internal/deploy/ports_test.go`, `internal/deploy/cleanup_test.go`

**Interfaces:**
- Consumes: `docker.Client` (Tarefa 1), `Runner` (Tarefa 10).
- Produces:
  - `FreePort(used map[int]bool, listen func(addr string) error) (int, error)`: procura na faixa 20000–29999 e pula as portas usadas e as que `listen("127.0.0.1:p")` não consegue abrir. Sem porta livre → `ErrNoFreePort`.
  - `StartContainer(ctx, d docker.Client, app store.App, deployID int64, image string, env []string, hostPort int) (id string, err error)`: `RunSpec` com nome `korego-<slug>-<sha7>-d<id>` (D2), labels, porta `127.0.0.1:<hostPort>:<internal_port>` e `unless-stopped`.
  - `WaitHealthy(ctx, d docker.Client, id string, hostPort int, timeout time.Duration, dial func(addr string) error) error`:
    - com `Health` presente, espera `healthy`; `unhealthy` → erro;
    - sem `Health`, espera um `dial("127.0.0.1:<port>")` bem-sucedido;
    - em qualquer caso, o container em `exited`/`dead` → `ErrContainerExited` com o código de saída;
    - estourou o tempo → `ErrHealthTimeout`;
    - verifica a cada 1 s.
  - `ComposeOverride(app store.App, hostPort int) ([]byte, error)`: YAML com `services.<compose_service>.ports: ["127.0.0.1:<hostPort>:<internal_port>"]` e `labels` `korego.managed: "true"` e `korego.app: <slug>`.
  - `ComposeUp(ctx, r Runner, app, dir string, overridePath string, out io.Writer) error`: `docker compose -p korego-<slug> -f <dir>/<compose_path> -f <override> up -d --remove-orphans`.
  - `Cleanup(ctx, d docker.Client, app store.App, keep []string, out io.Writer) error`: remove as imagens `korego/<slug>:*` que não estão em `keep` (as 3 mais recentes do banco) e ignora `ErrConflict` (em uso).

- [ ] **Step 1: Testes**

```go
func TestFreePort_SkipsUsedAndBusy(t *testing.T)
func TestFreePort_Exhausted(t *testing.T)
func TestStartContainer_SpecExact(t *testing.T)       // nome, labels, porta só em 127.0.0.1, env repassado
func TestWaitHealthy_UsesHealthcheck(t *testing.T)    // starting, starting, healthy → nil
func TestWaitHealthy_Unhealthy(t *testing.T)
func TestWaitHealthy_TCPFallback(t *testing.T)        // sem Health: dial falha 2x e depois ok
func TestWaitHealthy_ExitedFailsFast(t *testing.T)    // exited na 1ª verificação → ErrContainerExited antes do timeout
func TestWaitHealthy_Timeout(t *testing.T)
func TestComposeOverride_YAML(t *testing.T)
func TestCleanup_KeepsThreeAndInUse(t *testing.T)
```

- [ ] **Step 2: Rodar** `go test ./internal/deploy/`: FAIL.
- [ ] **Step 3: Implementar.**
- [ ] **Step 4: Rodar** `go test ./internal/deploy/`: PASS.
- [ ] **Step 5: Commit** `feat(etapa-2): subir container, verificar saúde, Compose e limpeza de imagens`

---

### Task 12: Serviço de deploy

**Files:**
- Create: `internal/deploy/service.go`, `internal/deploy/logs.go`
- Modify: `internal/config/config.go`, `cmd/korego/main.go`
- Test: `internal/deploy/service_test.go`, `internal/deploy/logs_test.go`, `internal/deploy/service_integration_test.go`

**Interfaces:**
- Consumes: Tarefas 1, 3, 9, 10 e 11; `secrets.Box`; `events.Hub`; o gancho `ReapplyApp` (Tarefa 6); `TokenSource` (Tarefa 14).
- Produces:
  - ```go
    type Router interface{ ReapplyApp(ctx context.Context, appID int64) error }
    type TokenSource interface{ CloneToken(ctx context.Context, repo string) (string, error) } // nil = sem GitHub
    type Service struct {
        DB *store.DB; Docker docker.Client; Runner Runner; Box *secrets.Box; Hub *events.Hub
        Queue *jobs.Queue; Router Router; Tokens TokenSource
        DataDir string                    // builds/ e logs/ abaixo dele
        Now func() time.Time
        Dial func(addr string) error      // padrão: net.DialTimeout 2s
        AllowFileURLs bool                // só testes de integração (file:// em Git)
    }
    func (s *Service) Recover(ctx context.Context) error
    func (s *Service) Deploy(ctx context.Context, appID int64, trigger, sha, msg string) (store.Deploy, error)
    func (s *Service) Rollback(ctx context.Context, appID, fromDeployID int64) (store.Deploy, error)
    func (s *Service) Cancel(ctx context.Context, deployID int64) error
    ```
  - `Recover`, na subida:
    - roda `MarkInterrupted`;
    - remove com `RemoveForce` os containers com label `korego.deploy=<id>` desses deploys, exceto o `current` da aplicação;
    - reenfileira os `queued`.
  - `Deploy` grava o deploy `queued` e chama `Queue.Enqueue{Key:"app:<id>", ID: deployID, OnSuperseded: status superseded}`. Publica `{id, appId, status}` no tópico `deploys` a cada mudança.
  - Pipeline (`run`):
    1. `building`: para `github`/`git`, `Fetch` com token (`Tokens.CloneToken`), depois `Build`. Para `image`, `PullAndTag`. Para `compose`, `Fetch` + `Build` (compose build).
    2. `starting`:
       - não-Compose: escolhe a porta com `FreePort` e roda `StartContainer` + `WaitHealthy` (com `health_timeout_s`);
       - Compose: grava o override em `<dir>/docker-compose.korego.yml`, roda `ComposeUp` + `WaitHealthy` do container do serviço de entrada (achado por `ListByLabel("com.docker.compose.project","korego-<slug>")` + label de serviço), e a porta da aplicação é fixa (`apps.host_port`, alocada na 1ª vez).
    3. Troca:
       - atualiza `apps.host_port` para a porta nova e roda `Router.ReapplyApp`;
       - só então roda `RemoveForce` no container do deploy atual anterior (não-Compose);
       - grava `SetDeployResult`, `SetCurrentDeploy`, status `running`, e o anterior vira `superseded` (D4).
    4. Limpeza: `Cleanup` com `LatestImages(app, 3)` e remoção de `builds/<id>`.
  - Falha em qualquer passo:
    - remove o container novo, se existir;
    - **não toca** em rota nem no container anterior;
    - status `failed` com `reason` em português (por exemplo `"verificação de saúde falhou: tempo esgotado (60s)"`).
  - `ctx` cancelado → `cancelled`, com a mesma limpeza.
  - `Rollback`:
    - Compose → `ErrRollbackUnsupported`;
    - a imagem do deploy de origem precisa estar entre as 3 mantidas, senão `ErrImageGone`;
    - pula os passos 1 e 2 de build (`trigger "rollback"`, `rollback_of`).
  - Variáveis: os valores vêm de `AppEnvEncrypted` + `Box.Decrypt`. Todos os valores e o token entram no `NewRedactor` do log do deploy.
  - `logs.go`:
    - `DeployLog` escreve no arquivo `logs/deploy-<id>.log` e publica `{"n": <linha>, "text": "..."}` no tópico `deploy-logs:<id>`;
    - `ReadLog(dataDir, id, from int) ([]LogEntry, error)`;
    - `LogEntry{N int; Text string}`.
  - `Config.MaxBuilds` (`toml:"max_builds"`, env `KOREGO_MAX_BUILDS`, padrão 2).
  - `main` cria a `jobs.Queue` e o `deploy.Service`, chama `Recover` antes de servir e fecha a fila no desligamento.

- [ ] **Step 1: Testes** (com Runner, Docker e Router falsos e relógio injetável)

```go
func TestDeploy_DockerfileHappyPath(t *testing.T)       // building → starting → running; ReapplyApp chamado depois de WaitHealthy; container antigo removido depois do Reapply; anterior superseded
func TestDeploy_HealthFailKeepsOld(t *testing.T)        // WaitHealthy erro → novo removido; ReapplyApp nunca chamado; host_port inalterado; antigo intacto; failed com reason
func TestDeploy_ExitedContainer(t *testing.T)
func TestDeploy_SecretsRedactedInLog(t *testing.T)      // build imprime o valor de uma variável e o token → log tem "••••"; tópico idem
func TestDeploy_SupersededInQueue(t *testing.T)
func TestDeploy_CancelRunning(t *testing.T)
func TestRollback_UnsupportedAndImageGone(t *testing.T)
func TestRollback_SkipsBuild(t *testing.T)              // Runner não recebe git/docker build; StartContainer com a imagem antiga
func TestRecover_InterruptedAndOrphans(t *testing.T)    // building vira failed "interrompido"; container com korego.deploy desse id removido; current preservado; queued reenfileirado
func TestDeployLog_NumberedAndRead(t *testing.T)
// integration (Linux, Docker): repositório git local file:// com testdata/sample-app → deploy running; segundo deploy com página diferente, polling contínuo na porta via Router falso que registra a porta → nenhuma requisição falha durante a troca; deploy com HEALTHCHECK impossível → failed e o primeiro segue respondendo
```

- [ ] **Step 2: Rodar** `go test ./internal/deploy/`: FAIL.
- [ ] **Step 3: Implementar.**
- [ ] **Step 4: Rodar** `go test ./... && go vet ./...` e o teste de integração em Linux: PASS.
- [ ] **Step 5: Commit** `feat(etapa-2): pipeline de deploy com troca sem queda, rollback e recuperação`

---

### Task 13: API de projetos, aplicações e deploys

**Files:**
- Create: `internal/api/projects.go`, `internal/api/apps.go`, `internal/api/deploys.go`
- Modify: `internal/api/server.go`, `cmd/korego/main.go`
- Test: `internal/api/projects_test.go`, `internal/api/apps_test.go`, `internal/api/deploys_test.go`

**Interfaces:**
- Consumes: `store` (Tarefa 3), `deploy.Service` (Tarefa 12), `secrets.Box` e os validadores (Tarefa 10).
- Produces. Rotas com sessão; as que alteram estado também com CSRF:
  - Projetos:
    - `GET /api/projects` → `{"projects":[{id,name,description,appCount}]}`.
    - `POST /api/projects` → 201.
    - `PUT /api/projects/{id}`.
    - `DELETE /api/projects/{id}` (com aplicações → 409 `PROJECT_NOT_EMPTY`).
  - Aplicações:
    - `GET /api/apps?project=<id>` → `{"apps":[AppView]}`. `AppView` = campos da aplicação em camelCase + `{currentDeploy: {id,status,commitSha,finishedAt}|null, domains:[hostname], url: "http://127.0.0.1:<host_port>"|""}`.
    - `POST /api/apps` com `AppInput` → 201. A validação completa cobre o campo exigido para cada combinação de origem e build: `github`/`git` exigem repo/URL e branch; `image` exige imagem e `build_type=image`; `compose` exige `compose_service`. Inválido → 400 `INVALID_APP` com `field`. Slug repetido → 409 `APP_EXISTS`.
    - `GET`/`PUT /api/apps/{id}`.
    - `DELETE /api/apps/{id}`:
      - com domínios → 409 `APP_HAS_DOMAINS`;
      - senão remove os containers (`ListByLabel korego.app`, ou `docker compose -p korego-<slug> down` para Compose) e as imagens;
      - depois apaga.
  - Variáveis:
    - `GET /api/apps/{id}/env` → `{"names":[...]}`.
    - `PUT /api/apps/{id}/env` com `{"set":{"NOME":"valor"},"remove":["NOME"]}` → 204. Os nomes são validados e os valores cifrados.
  - Deploys:
    - `POST /api/apps/{id}/deploys` → 202 `{"deploy":...}` (trigger `manual`).
    - `GET /api/apps/{id}/deploys?limit=20` → `{"deploys":[...]}`.
    - `GET /api/deploys/{id}`.
    - `GET /api/deploys/{id}/log?from=0` → `{"entries":[{n,text}], "done": bool}`.
    - `POST /api/deploys/{id}/cancel` → 204.
    - `POST /api/apps/{id}/rollback` com `{"deployId": n}` → 202.
  - Erros:
    - `APP_NOT_FOUND`, `DEPLOY_NOT_FOUND` (404);
    - `ROLLBACK_NOT_SUPPORTED`, `IMAGE_GONE`, `DEPLOY_FINISHED` (409);
    - `NO_GITHUB` (409), quando a origem é `github` e não há App conectado.
  - O tópico `deploy-logs:<id>` não precisa de fonte: o serviço publica direto. A tela lê `GET .../log` e assina o tópico, juntando por `n`.
  - Auditoria:
    - `project.create`, `project.update`, `project.delete`;
    - `app.create`, `app.update`, `app.delete`;
    - `app.env` (metadata só com os nomes alterados);
    - `deploy.create` (`{"trigger"}`), `deploy.cancel`, `deploy.rollback`.

- [ ] **Step 1: Testes**

```go
func TestAppsAPI_ValidationPerSource(t *testing.T)    // tabela: github sem branch, image sem imagem, compose sem service, slug inválido, porta 0
func TestAppsAPI_EnvNeverReturnsValues(t *testing.T)  // PUT com valor "s3cr3t-valor" → GET env, GET app, GET apps e a auditoria não contêm o valor
func TestAppsAPI_DeleteWithDomains409(t *testing.T)
func TestDeploysAPI_CreateListLogCancel(t *testing.T)
func TestDeploysAPI_RollbackErrors(t *testing.T)
func TestProjectsAPI_NotEmpty409(t *testing.T)
```

- [ ] **Step 2: Rodar** `go test ./internal/api/`: FAIL.
- [ ] **Step 3: Implementar.**
- [ ] **Step 4: Rodar** `go test ./... && go vet ./...`: PASS.
- [ ] **Step 5: Commit** `feat(etapa-2): API de projetos, aplicações, variáveis e deploys`

---

### Task 14: Cliente do GitHub App

**Files:**
- Create: `internal/github/jwt.go`, `internal/github/client.go`, `internal/github/manifest.go`, `internal/store/github.go`
- Modify: `internal/config/config.go`
- Test: `internal/github/jwt_test.go`, `internal/github/client_test.go`, `internal/github/manifest_test.go`, `internal/store/github_test.go`

**Interfaces:**
- Produces:
  - `store.GitHubApp{AppID int64; Slug, Owner, HTMLURL, PrivateKeyEnc, WebhookSecretEnc string; CreatedAt time.Time}`, `(*DB).SaveGitHubApp`, `GetGitHubApp` (→ `ErrNotFound`) e `DeleteGitHubApp`.
  - `AppJWT(appID int64, key *rsa.PrivateKey, now time.Time) (string, error)`: RS256 com `iat = now-60s`, `exp = now+9min` e `iss = "<appID>"`, sem dependência externa.
  - `Manifest(panelURL, hostname string) []byte` gera o JSON com estes campos:
    - `name`: `KoreGO-<hostname>`, cortado em 34 caracteres;
    - `url`: `<panelURL>`;
    - `hook_attributes`: `{url: <panelURL>/api/webhooks/github, active: true}`;
    - `redirect_url`: `<panelURL>/api/github/callback`;
    - `public`: `false`;
    - `default_permissions`: `{contents: read, metadata: read}`;
    - `default_events`: `["push"]`.
  - `Client{APIURL, WebURL string; HTTP *http.Client; Now func() time.Time}`. Padrões `https://api.github.com` e `https://github.com`, vindos de `cfg.GitHubAPIURL`/`cfg.GitHubWebURL` (`toml:"github_api_url"`/`"github_web_url"`), para os testes.
  - `(*Client).ConvertManifest(ctx, code) (Conversion, error)`: `POST /app-manifests/{code}/conversions` → `Conversion{ID int64; Slug, PEM, WebhookSecret, HTMLURL string; Owner string}`.
  - `(*Client).Installations(ctx, jwt) ([]Installation, error)`, com `Installation{ID int64; Account string}`.
  - `(*Client).InstallationToken(ctx, jwt string, installationID int64) (token string, expires time.Time, err error)`.
  - `(*Client).Repos(ctx, token string) ([]Repo, error)` pagina pelo cabeçalho `Link`, com `Repo{FullName, DefaultBranch string; Private bool}`.
  - `github.Service{DB *store.DB; Box *secrets.Box; Client *Client}`:
    - `Connected(ctx) (store.GitHubApp, bool, error)`;
    - `Repos(ctx) ([]RepoView, error)`, de todas as instalações, com `RepoView{FullName, DefaultBranch string; InstallationID int64}`;
    - `CloneToken(ctx, repo string) (string, error)` acha a instalação que vê o repositório e guarda o token em cache até 5 min antes de expirar. Implementa `deploy.TokenSource`.

- [ ] **Step 1: Testes**

```go
func TestAppJWT_VerifiesWithPublicKey(t *testing.T) // decodifica header/claims; assinatura RS256 válida; iat/exp/iss exatos
func TestManifest_Fields(t *testing.T)
func TestConvertManifest(t *testing.T)               // httptest devolve a conversão; campos mapeados
func TestRepos_Pagination(t *testing.T)              // duas páginas pelo Link → 3 repositórios
func TestCloneToken_CachesUntilExpiry(t *testing.T)  // 2 chamadas → 1 POST access_tokens; relógio avança para 4 min antes de expirar → novo POST
func TestCloneToken_RepoNotInstalled(t *testing.T)   // → ErrRepoNotInstalled
func TestStoreGitHubApp_RoundTrip(t *testing.T)
```

- [ ] **Step 2: Rodar** `go test ./internal/github/ ./internal/store/`: FAIL.
- [ ] **Step 3: Implementar.**
- [ ] **Step 4: Rodar** `go test ./... && go vet ./...`: PASS.
- [ ] **Step 5: Commit** `feat(etapa-2): cliente do GitHub App com JWT, tokens de instalação e repositórios`

---

### Task 15: Conexão do GitHub e webhook

**Files:**
- Create: `internal/github/webhook.go`, `internal/api/github.go`, `internal/api/webhook.go`
- Modify: `internal/api/server.go`, `cmd/korego/main.go`
- Test: `internal/github/webhook_test.go`, `internal/api/github_test.go`, `internal/api/webhook_test.go`, `internal/api/testdata/push.json`, `internal/api/testdata/ping.json`

**Interfaces:**
- Consumes: `github.Service`/`Client` (Tarefa 14), `domains.Service.Panel` (Tarefa 7), `deploy.Service.Deploy` (Tarefa 12), `store.AppsByRepo` (Tarefa 3).
- Produces:
  - `VerifySignature(secret, body []byte, header string) bool`: `sha256=<hex>`, com `hmac.Equal`.
  - `ParsePush(body []byte) (Push, error)`:
    - `Push{Repo, Branch, After, Message string; Deleted bool}`;
    - `ref` é `refs/heads/<b>`, e tags são ignoradas (erro `ErrNotBranch`);
    - `Deleted` quando `after` é só zeros.
  - Rotas com sessão + CSRF:
    - `GET /api/github` → `{"connected": bool, "app": {slug, owner, htmlUrl, installUrl: "<WebURL>/apps/<slug>/installations/new"}|null, "installations": [...], "panelReady": bool}`. `panelReady` exige o domínio do painel com certificado `active`.
    - `POST /api/github/manifest` com `{"org": ""}`:
      - painel não pronto → 409 `PANEL_NOT_HTTPS`;
      - senão → `{"action": "<WebURL>/settings/apps/new?state=<s>" | "<WebURL>/organizations/<org>/settings/apps/new?state=<s>", "manifest": "<json>"}`;
      - o frontend envia um formulário POST para `action` com o campo `manifest`;
      - `state`: 32 bytes aleatórios, guardado em memória por 10 min e de uso único.
    - `GET /api/github/repos` → `{"repos":[RepoView]}`.
    - `DELETE /api/github`: apaga as credenciais locais e diz no JSON que o App deve ser removido no GitHub, com o link.
  - `GET /api/github/callback?code&state` é **pública** (D7):
    - `state` válido → `ConvertManifest`, cifra o PEM e o webhook secret, `SaveGitHubApp`, auditoria `github.connect` e 302 para `/configuracoes/github/?conectado=1`;
    - `state` inválido, expirado ou reutilizado → 302 para `/configuracoes/github/?erro=STATE_INVALID`;
    - falha na conversão → `?erro=CONVERSION_FAILED`.
  - `POST /api/webhooks/github` é **pública**:
    - corpo limitado a 10 MiB;
    - sem App conectado → 404;
    - assinatura inválida → 401 `INVALID_SIGNATURE`;
    - `X-GitHub-Event: ping` → 204;
    - `push` → para cada aplicação de `AppsByRepo(repo, branch)`, `Deploy(trigger "webhook", sha, msg)` e auditoria `deploy.webhook` com usuário nulo; responde 202 `{"queued": n}`;
    - outros eventos → 204;
    - deduplica `X-GitHub-Delivery` em memória (últimas 1000 entregas): repetida → 202 `{"queued":0,"duplicate":true}`.
  - As rotas públicas ficam **fora** do grupo com `RequireSession`/`RequireCSRF`.
  - `main` cria o `github.Service` e o passa como `Tokens` do `deploy.Service` (até aqui `nil`).

- [ ] **Step 1: Testes**

```go
func TestVerifySignature(t *testing.T)               // payload real do testdata com segredo de teste: válido → true; um byte alterado → false; header sem prefixo → false
func TestParsePush(t *testing.T)                     // branch, tag ignorada, delete
func TestCallback_WorksWithoutCookie(t *testing.T)   // POST manifest com sessão gera state; GET callback sem cookie → 302 ?conectado=1 e App salvo
func TestCallback_StateReuseExpiredUnknown(t *testing.T)
func TestManifest_RequiresPanelHTTPS(t *testing.T)
func TestWebhook_InvalidSignature401(t *testing.T)
func TestWebhook_PushEnqueuesMatchingApps(t *testing.T) // 2 apps no repo (main, auto) + 1 em outra branch + 1 sem auto → 1 deploy webhook
func TestWebhook_DuplicateDelivery(t *testing.T)
func TestWebhook_NoSessionNoCSRFNeeded(t *testing.T)
```

- [ ] **Step 2: Rodar** `go test ./internal/github/ ./internal/api/`: FAIL.
- [ ] **Step 3: Implementar.**
- [ ] **Step 4: Rodar** `go test ./... && go vet ./...`: PASS.
- [ ] **Step 5: Commit** `feat(etapa-2): conexão do GitHub App pelo manifest e deploy automático por webhook`

---

### Task 16: Camada de dados do frontend

**Files:**
- Create: `web/src/lib/domains.ts`, `web/src/lib/settings.ts`, `web/src/lib/projects.ts`, `web/src/lib/apps.ts`, `web/src/lib/deploys.ts`, `web/src/lib/github.ts`, `web/src/lib/validate.ts`
- Test: `web/src/lib/__tests__/validate.test.ts`, `deploys.test.ts`, `apps.test.ts`

**Interfaces:**
- Consumes: o JSON das Tarefas 8, 13 e 15; `api()`, `useLive` e `useLiveStream` (etapas 0 e 1).
- Produces:
  - Tipos com os mesmos nomes de campo da API.
  - Hooks de leitura:
    - `useProxyStatus()`, que também assina o tópico `proxy`;
    - `useDomains()`, `usePanelSettings()`, `useProjects()`;
    - `useApps(projectId?)`, `useApp(id)`, `useAppEnv(id)`;
    - `useDeploys(appId)`, que refaz a busca a cada evento do tópico `deploys` daquela aplicação (no máximo 1 vez por segundo);
    - `useDeploy(id)`;
    - `useGitHub()`, `useGitHubRepos()`.
  - Mutações, que invalidam as chaves certas:
    - `useSaveDomain`, `useDeleteDomain`, `useRetryProxy`;
    - `useSetPanelDomain`, `useSetPublicPort`;
    - `useSaveProject`, `useDeleteProject`, `useSaveApp`, `useDeleteApp`, `useSaveEnv`;
    - `useStartDeploy`, `useCancelDeploy`, `useRollback`;
    - `useStartGitHubManifest`, que devolve `{action, manifest}` e envia o formulário com `submitManifestForm(action, manifest)`;
    - `useDisconnectGitHub`.
  - `validate.ts` espelha as regras das Global Constraints (slug, hostname, endereço, branch, repositório, imagem, caminho, variável, porta) para mostrar o erro antes de enviar. O servidor continua sendo a autoridade.
  - `deploys.ts` traz:
    - `class DeployLogBuffer`, com `setInitial(entries)`, `append(entries)` e `lines` (junta por `n` sem duplicar, e uma lacuna de `n` dispara `needsRefetch`);
    - `statusLabel(status)` em pt-BR (`queued` "Na fila", `building` "Construindo", `starting` "Subindo", `running` "No ar", `failed` "Falhou", `superseded` "Substituído", `cancelled` "Cancelado");
    - `isActive(status)`.
  - `apps.ts` traz `defaultsFor(sourceType, buildType)` com os campos visíveis e os obrigatórios do formulário.

- [ ] **Step 1: Testes**

```ts
test("validadores aceitam e recusam os mesmos casos do servidor") // tabela espelhando TestValidators e TestValidHostname
test("DeployLogBuffer junta por n sem duplicar e detecta lacuna")
test("statusLabel e isActive")
test("defaultsFor: compose exige serviço; image esconde branch e caminhos")
```

- [ ] **Step 2: Rodar** `cd web && pnpm vitest run`: FAIL.
- [ ] **Step 3: Implementar.**
- [ ] **Step 4: Rodar** `pnpm vitest run && pnpm lint`: PASS.
- [ ] **Step 5: Commit** `feat(etapa-2): dados e validações do frontend para publicar`

---

### Task 17: Telas de domínios e do painel

**Files:**
- Create: `web/src/app/(painel)/dominios/page.tsx`, `web/src/components/domains/domain-dialog.tsx`, `web/src/components/domains/proxy-banner.tsx`, `web/src/app/(painel)/configuracoes/painel/page.tsx`
- Modify: `web/src/components/shell/sidebar.tsx`, `web/src/app/(painel)/page.tsx`
- Test: `web/src/app/__tests__/dominios.test.tsx`, `web/src/app/__tests__/painel.test.tsx`

**Interfaces:**
- Consumes: Tarefa 16; `Dialog`, `Tabs` e o kit de UI.
- Produces:
  - `ProxyBanner`, mostrado em Domínios, em Configurações → Painel e na visão geral, quando o estado não é `active`:
    - `blocked`: **"Proxy inativo: as portas 80/443 estão em uso por <processo>"**, com link "Guia de migração" (`guideUrl`) e "Tentar novamente";
    - `docker_unavailable`: "Docker indisponível";
    - `starting`: "Proxy iniciando…".
  - Página `/dominios/`:
    - tabela com hostname, destino (aplicação com link ou endereço), SSL (`Pendente`, `Ativo até <data>` ou `Erro: <mensagem>`), opções (`www`, HTTPS) e ações (editar, remover com confirmação);
    - o domínio do painel aparece marcado "Painel" e sem ações, com link para Configurações → Painel.
  - `DomainDialog`:
    - campos: hostname, destino (aplicação: select de `useApps()`; endereço: campo `host:porta` com o exemplo `127.0.0.1:5000`), `www` (nenhum, `www` → principal, principal → `www`) e "Forçar HTTPS" (ligado);
    - depois de salvar, se `dns.ok` for falso, mostra o aviso **com o registro A** e mantém o domínio salvo.
  - Página `/configuracoes/painel/`:
    - campo do domínio do painel com o estado do certificado;
    - interruptor "Porta 8000 pública": desligar só é possível quando `requestHTTPS` for verdadeiro, `requestHost` for o domínio e o certificado estiver ativo. Senão, o controle fica desabilitado com a explicação. Desligar pede confirmação com o texto "Se o domínio deixar de funcionar, reative pelo servidor com `korego painel --porta-publica`.".
  - Visão geral: um aviso "Configure o domínio do painel para usar HTTPS" com link, enquanto não houver domínio (D5).
  - Sidebar:
    - "Domínios" habilitado (`/dominios/`);
    - "Configurações" ganha os filhos "Painel" (`/configuracoes/painel/`) e "GitHub" (`/configuracoes/github/`; a tela vem na Tarefa 19).

- [ ] **Step 1: Testes**

```ts
test("banner bloqueado mostra o processo e o link do guia")
test("salvar domínio com DNS errado mostra o registro A e mantém na lista")
test("domínio do painel aparece sem ações de editar/remover")
test("porta 8000 não pode ser desligada fora do domínio com HTTPS")
test("desligar a porta pede confirmação com o comando de recuperação")
```

- [ ] **Step 2: Rodar** `pnpm vitest run`: FAIL.
- [ ] **Step 3: Implementar** (skill `frontend-design` antes).
- [ ] **Step 4: Verificar** `pnpm vitest run && pnpm lint && pnpm build`: PASS. Revisar com `web-design-guidelines`.
- [ ] **Step 5: Commit** `feat(etapa-2): telas de domínios e do domínio do painel`

---

### Task 18: Telas de projetos e aplicações

**Files:**
- Create: `web/src/app/(painel)/projetos/page.tsx`, `web/src/app/(painel)/projetos/detalhe/page.tsx`, `web/src/app/(painel)/aplicacoes/nova/page.tsx`, `web/src/app/(painel)/aplicacoes/detalhe/page.tsx`, `web/src/components/apps/app-form.tsx`, `web/src/components/apps/env-editor.tsx`, `web/src/components/apps/repo-picker.tsx`
- Modify: `web/src/components/shell/sidebar.tsx`
- Test: `web/src/app/__tests__/projetos.test.tsx`, `web/src/components/apps/__tests__/app-form.test.tsx`, `web/src/components/apps/__tests__/env-editor.test.tsx`

**Interfaces:**
- Consumes: Tarefa 16; `Dialog`, `Tabs` e o kit de UI.
- Produces:
  - `/projetos/`:
    - lista de projetos com a contagem de aplicações;
    - "Novo projeto" (diálogo com nome e descrição);
    - remover só quando vazio.
  - `/projetos/detalhe/?id=`:
    - aplicações do projeto com o status do deploy atual, os domínios e o botão "Deploy";
    - "Nova aplicação" leva a `/aplicacoes/nova/?projeto=<id>`.
  - `AppForm` (criar e editar), com os campos visíveis por `defaultsFor`:
    - origem: GitHub (com `RepoPicker` dos repositórios das instalações; sem App conectado, um link para Configurações → GitHub), URL Git pública ou imagem;
    - tipo de build;
    - branch, caminho do Dockerfile ou do Compose, serviço de entrada, porta interna, tempo da verificação de saúde (padrão 60) e "Deploy automático a cada push" (só para GitHub);
    - nome (slug), validado ao digitar;
    - Compose mostra o aviso "Compose recria os containers a cada deploy: alguns segundos fora do ar. Rollback não disponível.".
  - `/aplicacoes/detalhe/?id=&aba=` com abas:
    - **Visão**: status, deploy atual, endereço local, domínios com link para Domínios e "Deploy agora";
    - **Deploys** (Tarefa 19);
    - **Variáveis** (`EnvEditor`);
    - **Configurações** (`AppForm` em modo edição e "Remover aplicação", que explica `APP_HAS_DOMAINS` quando houver).
  - `EnvEditor`:
    - lista os nomes com o valor sempre `••••••`;
    - "Adicionar" e "Substituir valor" abrem campos de senha (`type=password`, `autocomplete=off`);
    - "Remover" por variável;
    - salvar envia só o que mudou;
    - nunca exibe um valor existente.
  - Sidebar: "Projetos" habilitado (`/projetos/`).

- [ ] **Step 1: Testes**

```ts
test("formulário mostra campos por origem e tipo de build")
test("slug inválido bloqueia o envio com a mensagem")
test("compose mostra o aviso de recriação e de rollback")
test("EnvEditor nunca mostra valores e envia só as mudanças")
test("remover aplicação com domínios explica o motivo")
```

- [ ] **Step 2: Rodar** `pnpm vitest run`: FAIL.
- [ ] **Step 3: Implementar** (skill `frontend-design` antes).
- [ ] **Step 4: Verificar** `pnpm vitest run && pnpm lint && pnpm build`: PASS. Revisar com `web-design-guidelines`.
- [ ] **Step 5: Commit** `feat(etapa-2): telas de projetos e aplicações com variáveis criptografadas`

---

### Task 19: Telas de deploys e do GitHub

**Files:**
- Create: `web/src/components/deploys/deploy-list.tsx`, `web/src/components/deploys/deploy-log.tsx`, `web/src/app/(painel)/deploys/detalhe/page.tsx`, `web/src/app/(painel)/configuracoes/github/page.tsx`
- Modify: `web/src/app/(painel)/aplicacoes/detalhe/page.tsx`
- Test: `web/src/components/deploys/__tests__/deploy-log.test.tsx`, `web/src/components/deploys/__tests__/deploy-list.test.tsx`, `web/src/app/__tests__/github.test.tsx`

**Interfaces:**
- Consumes: Tarefa 16 (`DeployLogBuffer`, hooks) e Tarefa 18.
- Produces:
  - `DeployList` (aba Deploys):
    - colunas: status, gatilho (Manual, Push ou Rollback), commit (SHA curto + mensagem), início e duração;
    - ações: "Ver log", "Cancelar" (ativos) e "Voltar para esta versão" (entre os 3 mais recentes `running`/`superseded`, fora o atual; escondido para Compose), com confirmação;
    - o deploy atual vem marcado "No ar".
  - `DeployLog`:
    - assina `deploy-logs:<id>` primeiro, busca `GET /log` e junta por `n`; se houver lacuna, busca de novo a partir dela;
    - rola automaticamente só quando o usuário está no fim;
    - status e `reason` no topo, com o texto do motivo para os que falharam;
    - fonte monoespaçada, sem cantos arredondados.
  - `/deploys/detalhe/?id=`: o `DeployLog` em tela cheia, com um link de volta à aplicação.
  - `/configuracoes/github/`:
    - sem App conectado:
      - painel não pronto: explica que o GitHub precisa do painel em HTTPS, com link para Configurações → Painel;
      - pronto: "Conectar GitHub" (campo opcional de organização), que chama `useStartGitHubManifest` e envia o formulário;
    - com App conectado: nome, dono, link "Instalar nos repositórios" (`installUrl`), lista das instalações e "Desconectar" (confirmação explicando que o App deve ser apagado no GitHub);
    - lê `?conectado=1` e `?erro=` e mostra a mensagem certa.

- [ ] **Step 1: Testes**

```ts
test("DeployLog junta histórico e ao vivo sem duplicar e rebusca na lacuna")
test("DeployList esconde rollback para compose e no deploy atual")
test("cancelar pede confirmação e chama a API")
test("GitHub sem painel HTTPS explica e não mostra o botão")
test("GitHub mostra erro de state inválido vindo da URL")
```

- [ ] **Step 2: Rodar** `pnpm vitest run`: FAIL.
- [ ] **Step 3: Implementar** (skill `frontend-design` antes).
- [ ] **Step 4: Verificar** `pnpm vitest run && pnpm lint && pnpm build`: PASS. Revisar com `web-design-guidelines`.
- [ ] **Step 5: Commit** `feat(etapa-2): telas de deploys com log ao vivo, rollback e conexão do GitHub`

---

### Task 20: Instalador, guia de migração e README

**Files:**
- Create: `docs/migracao-nginx.md`
- Modify: `scripts/install.sh`, `scripts/test-install.sh`, `README.md`

**Interfaces:**
- Produces:
  - `install.sh`: depois do Docker, verifica `git` e `railpack`.
    - Sem `git`, instala pelo `apt-get` (com `KOREGO_YES=1`, ou depois de perguntar).
    - Sem `railpack`, oferece instalar pelo script oficial do Railpack (baixado para um arquivo temporário e executado com `sh`, nunca por pipe direto) e segue sem ele se o usuário recusar.
    - Continua sem mexer em serviços existentes.
  - `test-install.sh`: um caso com `git` ausente instala; um caso com `railpack` recusado segue sem erro.
  - `docs/migracao-nginx.md` segue o roteiro da seção 12 da especificação, com exemplos fictícios:
    1. backup de `/etc/nginx` e `/etc/letsencrypt`;
    2. ler os `proxy_pass` (`grep -R proxy_pass /etc/nginx/sites-enabled`);
    3. cadastrar cada domínio no KoreGO como **endereço manual**;
    4. `systemctl stop nginx && systemctl disable nginx`;
    5. "Tentar novamente" no proxy;
    6. conferir cada domínio por HTTPS;
    7. **Volta**: `docker stop korego-caddy && systemctl enable --now nginx`.
    - Termina com uma seção "Painel em HTTPS e porta 8000", que inclui `korego painel --porta-publica`.
  - README:
    - seção "Publicar" (aplicações, deploy automático, domínios, rollback e as limitações do Compose);
    - estado atualizado para "Etapa 2";
    - opções `max_builds`, `public_ip` e `pm2_home`;
    - comandos dos testes de integração.

- [ ] **Step 1: Escrever** os casos novos em `test-install.sh`.
- [ ] **Step 2: Rodar** `sh scripts/test-install.sh` (no container de teste já usado no CI): FAIL.
- [ ] **Step 3: Implementar** o instalador e escrever os documentos.
- [ ] **Step 4: Verificar** `shellcheck scripts/*.sh` sem avisos e `sh scripts/test-install.sh`: PASS.
- [ ] **Step 5: Commit** `docs(etapa-2): instalador com git e Railpack, guia de migração do nginx e README`

---

### Task 21: Ponta a ponta e integração no CI

**Files:**
- Create: `web/e2e/publicar.spec.ts`, `testdata/sample-app/Dockerfile`, `testdata/sample-app/index.html`
- Modify: `web/playwright.config.ts`, `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: tudo acima.
- Produces:
  - `testdata/sample-app`: `FROM nginx:1-alpine`, que copia `index.html` com o texto `korego-sample` e define `HEALTHCHECK` com `wget -qO- http://127.0.0.1/`.
  - Projeto Playwright `publicar` (`dependencies: ["fundacao"]`), que pula sem Docker **fora do CI** e falha no CI. Fluxo:
    1. login (helper com código de recuperação) → Projetos → "Novo projeto" `e2e`;
    2. "Nova aplicação" por imagem `nginx:1-alpine`, porta interna 80, slug `site-e2e` → "Deploy agora" → status "No ar" em até 120 s, com o log mostrando o pull;
    3. Domínios → novo domínio `site-e2e.korego.localhost`, destino `site-e2e`, "Forçar HTTPS" desligado;
    4. quando o proxy estiver `active`, uma requisição do Playwright (`request.get("http://127.0.0.1/", { headers: { Host: "site-e2e.korego.localhost" } })`) devolve a página padrão do nginx em até 60 s;
    5. segundo "Deploy agora" → durante a troca, um laço de requisições pelo proxy não recebe nenhuma resposta ≠ 200;
    6. aba Deploys mostra o primeiro como "Substituído";
    7. "Voltar para esta versão" no primeiro → "No ar";
    8. Segurança mostra `deploy.create`, `deploy.rollback` e `domain.create`;
    9. limpeza: remove o domínio e a aplicação pela interface; o `afterAll` remove `korego-caddy` e os containers `korego-site-e2e-*` à força.
  - O proxy no e2e precisa das portas 80/443 livres no runner. Se `PortOwner` acusar uso, o teste falha com a mensagem do processo.
  - CI:
    - o job `integration` passa a rodar `go test -tags integration ./internal/node/docker/... ./internal/node/proxy/... ./internal/deploy/...`, com `timeout-minutes: 20`;
    - o job `e2e` roda os três projetos.

- [ ] **Step 1: Escrever** o teste e2e e o `sample-app`.
- [ ] **Step 2: Rodar** localmente o que for possível. No Windows, o Docker Desktop pode não oferecer `network_mode: host`, então o spec `publicar` pode precisar rodar num Linux ou só no CI; registrar no relatório.
- [ ] **Step 3: Atualizar** o CI.
- [ ] **Step 4: Verificar** com `go test ./...`, `pnpm vitest run`, e o push do branch com `gh run watch`: todos os jobs verdes, e `publicar` **rodou** (não foi pulado) no log do job `e2e`.
- [ ] **Step 5: Commit** `test(etapa-2): fluxo ponta a ponta de publicar e integração de proxy e deploy no CI`

---

## Depois desta etapa

- Publicar `v0.3.0` e atualizar a VPS pelo painel.
- Configurar o domínio do painel e desligar a porta 8000 pública.
- Seguir `docs/migracao-nginx.md` para trocar o nginx pelo Caddy, com os dados reais só em `docs/local/`.
- Fim da v1. Os próximos itens (bancos com um clique, terminal web, backups…) terão especificações próprias.
