# KoreGO — Etapa 0 (Fundação) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Entregar um binário `korego` instalável numa VPS que serve o painel (Next.js embutido) na porta 8000, com setup do admin com 2FA, login, sessões, auditoria, WebSocket com hub de eventos, a fronteira `Node`, instalador, releases e autoatualização.

**Architecture:** Binário Go único (sem CGO) com o export estático do Next.js embutido via `go:embed`. API REST em `/api/*` (chi) e WebSocket em `/api/ws` (coder/websocket). SQLite (modernc) com migrações próprias. Tudo que age no servidor passa pela interface `node.Node`; na etapa 0 ela só expõe informações do host.

**Tech Stack:** Go 1.27, chi v5, modernc.org/sqlite, golang.org/x/crypto/argon2, pquerna/otp, coder/websocket, gopsutil v4, BurntSushi/toml, golang.org/x/mod/semver · Next.js (App Router, última estável), TypeScript, Tailwind CSS, TanStack Query, Vitest, Playwright · pnpm 10, Node 22 · GoReleaser, GitHub Actions, shellcheck.

**Spec:** `docs/superpowers/specs/2026-11-03-korego-v1-design.md` (seções 3, 4, 7, 8, 9, 10, 11). Etapas 1 e 2 terão planos próprios.

## Global Constraints

- Módulo Go: `github.com/engviniciusbs/korego`; `go 1.27`; `CGO_ENABLED=0`; alvos de release `linux/amd64` e `linux/arm64`.
- Testes Go precisam passar em Linux **e** Windows (máquina de desenvolvimento). Verificações de permissão de arquivo usam `t.Skip` em Windows.
- Formato de erro da API, exato: `{ "error": { "code": "<CODIGO>", "message": "<mensagem em português>" } }`. Códigos em inglês maiúsculo; mensagens em português.
- Porta padrão `:8000`. Caminhos: binário `/usr/local/bin/korego`, dados `/var/lib/korego`, config `/etc/korego/config.toml`, chave `/etc/korego/master.key` (0600), serviço `korego.service`.
- Senhas: argon2id. TOTP obrigatório para o admin. 10 códigos de recuperação exibidos uma única vez.
- Sessão: cookie `korego_session`, `HttpOnly`, `SameSite=Strict`, `Secure` quando a requisição é HTTPS, validade 7 dias, armazenada no SQLite com hash do token. CSRF: header `X-CSRF-Token` em toda requisição que altera estado.
- Login: 5 falhas por minuto por IP → bloqueio de 15 minutos (HTTP 429, código `RATE_LIMITED`).
- Nenhum comando externo montado com interpolação de shell: sempre `exec.Command(nome, args...)`.
- Interface **somente escura**, **sem bordas arredondadas** em nenhum elemento. Usar a skill `frontend-design` para idealizar a interface e `web-design-guidelines` para revisá-la.
- **Nada sensível versionado**: exemplos usam `exemplo.com` e `203.0.113.10`; dados reais ficam em `docs/local/` (gitignored).
- Commits: um por tarefa, prefixo `feat(etapa-0): ...` (ou `test(etapa-0)`, `build(etapa-0)`, `docs(etapa-0)`), terminando com `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Replay de código TOTP:** o mesmo código de 6 dígitos usado duas vezes dentro da janela de 30s deve ser recusado no segundo uso → teste na Tarefa 6.
2. **Corrida no setup inicial:** duas confirmações de setup simultâneas com o banco vazio devem criar exatamente um admin → teste na Tarefa 6.
3. **IP falsificado via `X-Forwarded-For`:** um atacante não pode contornar o limite de login trocando o header; só confiar no header quando a conexão vem de loopback (o Caddy, na etapa 2) → teste na Tarefa 5.
4. **WebSocket de outra origem (CSWSH):** uma página em outro domínio não pode abrir `/api/ws` usando o cookie da vítima → teste na Tarefa 8.
5. **Cliente WebSocket lento:** um navegador que não consome mensagens não pode travar o hub nem os outros clientes; eventos excedentes são descartados → teste na Tarefa 8.

Fora desta etapa (ficam no plano da etapa 2, pois dependem do proxy): domínio do painel e desativação da porta 8000 pública.

---

## Estrutura de arquivos

```
cmd/korego/main.go                 subcomandos: serve (padrão), version, update-finish
internal/version/version.go        Version (ldflags)
internal/config/config.go          Config, Load
internal/store/                    db.go, migrate.go, migrations/0001_init.sql, users.go, sessions.go, audit.go
internal/secrets/secrets.go        LoadOrCreateKey, Box
internal/auth/                     password.go, totp.go, recovery.go, ratelimit.go, clientip.go, sessions.go, service.go
internal/audit/audit.go            Logger
internal/node/                     node.go, local.go, nodetest/fake.go
internal/events/hub.go             Hub, fontes sob demanda
internal/api/                      server.go, errors.go, middleware.go, headers.go, setup.go, auth.go, system.go, audit.go, ws.go, update.go
internal/webui/                    embed.go, handler.go, devproxy.go, dist/.gitkeep
internal/update/                   check.go, apply.go, finish.go
web/                               app Next.js
scripts/                           install.sh, korego.service, copy-web.mjs, test-install.sh
.goreleaser.yaml, .github/workflows/ci.yml, .github/workflows/release.yml, Makefile, README.md
```

---

### Task 1: Esqueleto Go, configuração e `/api/health`

**Files:**
- Create: `go.mod`, `cmd/korego/main.go`, `internal/version/version.go`, `internal/config/config.go`, `internal/api/errors.go`, `internal/api/server.go`, `Makefile`
- Test: `internal/config/config_test.go`, `internal/api/errors_test.go`, `internal/api/server_test.go`

**Interfaces:**
- Produces:
  - `version.Version string` (padrão `"dev"`, sobrescrito via `-ldflags "-X github.com/engviniciusbs/korego/internal/version.Version=vX.Y.Z"`)
  - `config.Config{ListenAddr, DataDir, ConfigDir, DevProxy string}`; `config.Load(path string) (Config, error)` — arquivo inexistente não é erro (usa padrões); variáveis `KOREGO_LISTEN`, `KOREGO_DATA_DIR`, `KOREGO_CONFIG_DIR`, `KOREGO_DEV_PROXY` sobrescrevem o arquivo.
  - `api.WriteError(w http.ResponseWriter, status int, code, message string)`; `api.WriteJSON(w http.ResponseWriter, status int, v any)`
  - `api.Deps` (struct que cresce nas tarefas seguintes) e `api.NewRouter(d Deps) http.Handler`

- [ ] **Step 1: Escrever os testes**

```go
func TestLoad_Defaults(t *testing.T) {
    c, err := config.Load(filepath.Join(t.TempDir(), "nao-existe.toml"))
    require.NoError(t, err)
    assert.Equal(t, ":8000", c.ListenAddr)
    assert.Equal(t, "/var/lib/korego", c.DataDir)
    assert.Equal(t, "/etc/korego", c.ConfigDir)
}
func TestLoad_FileThenEnvOverride(t *testing.T) // arquivo define listen=":9000"; KOREGO_LISTEN=":9100" vence → ":9100"
func TestWriteError_Format(t *testing.T)        // corpo == {"error":{"code":"X","message":"m"}}, Content-Type application/json
func TestHealth(t *testing.T)                   // GET /api/health → 200 {"status":"ok","version":"dev"}
func TestUnknownAPIRoute(t *testing.T)          // GET /api/nada → 404 com code "NOT_FOUND"
```

- [ ] **Step 2: Rodar e ver falhar** — `go test ./...` → FAIL (pacotes inexistentes).
- [ ] **Step 3: Implementar** os pacotes acima e `main.go` (subcomando `serve` padrão, com flag `--config` cujo padrão é `/etc/korego/config.toml`, sobe `http.Server` com `ReadHeaderTimeout: 10s` e desligamento gracioso em SIGINT/SIGTERM; `version` imprime a versão). `Makefile` com alvos `test`, `build` (`CGO_ENABLED=0 go build -o bin/korego ./cmd/korego`), `web`.
- [ ] **Step 4: Rodar** — `go test ./... && go vet ./...` → PASS.
- [ ] **Step 5: Commit** — `feat(etapa-0): esqueleto Go, configuração e /api/health`

---

### Task 2: SQLite e migrações

**Files:**
- Create: `internal/store/db.go`, `internal/store/migrate.go`, `internal/store/migrations/0001_init.sql`
- Test: `internal/store/migrate_test.go`

**Interfaces:**
- Produces:
  - `store.Open(path string) (*store.DB, error)` — abre com `_pragma=journal_mode(WAL)`, `foreign_keys(1)`, `busy_timeout(5000)` e aplica migrações; `(*DB).Close() error`; `(*DB).SQL() *sql.DB`.
  - `store.OpenMemory(t testing.TB) *store.DB` em `internal/store/storetest.go` (helper para os testes dos outros pacotes; usa arquivo em `t.TempDir()`).
  - Tabelas de `0001_init.sql`:
    - `users(id INTEGER PK, email TEXT UNIQUE NOT NULL, password_hash TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'admin', totp_secret_enc TEXT NOT NULL, totp_last_step INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL)`
    - `recovery_codes(id INTEGER PK, user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE, code_hash TEXT NOT NULL, used_at INTEGER)`
    - `sessions(id TEXT PK, token_hash TEXT UNIQUE NOT NULL, user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE, csrf_token TEXT NOT NULL, ip TEXT NOT NULL, user_agent TEXT NOT NULL, created_at INTEGER NOT NULL, last_seen_at INTEGER NOT NULL, expires_at INTEGER NOT NULL)`
    - `audit_log(id INTEGER PK, user_id INTEGER, action TEXT NOT NULL, target TEXT NOT NULL DEFAULT '', ip TEXT NOT NULL DEFAULT '', metadata TEXT NOT NULL DEFAULT '{}', created_at INTEGER NOT NULL)` + índice em `created_at`
    - Datas em Unix segundos (UTC).

- [ ] **Step 1: Testes**

```go
func TestOpen_AppliesMigrations(t *testing.T)   // tabelas users, recovery_codes, sessions, audit_log existem; schema_migrations tem versão 1
func TestOpen_Idempotent(t *testing.T)          // abrir duas vezes o mesmo arquivo não falha e não duplica schema_migrations
func TestOpen_ForeignKeysOn(t *testing.T)       // PRAGMA foreign_keys == 1
```

- [ ] **Step 2: Rodar** — `go test ./internal/store/` → FAIL.
- [ ] **Step 3: Implementar.** Migrador próprio: arquivos `migrations/NNNN_*.sql` embutidos com `embed.FS`, aplicados em ordem dentro de transação, versão registrada em `schema_migrations(version INTEGER PK, applied_at INTEGER)`.
- [ ] **Step 4: Rodar** → PASS.
- [ ] **Step 5: Commit** — `feat(etapa-0): SQLite com migrações embutidas`

---

### Task 3: Chave mestra e criptografia de segredos

**Files:**
- Create: `internal/secrets/secrets.go`
- Test: `internal/secrets/secrets_test.go`

**Interfaces:**
- Produces:
  - `secrets.LoadOrCreateKey(path string, allowCreate bool) ([]byte, error)` — 32 bytes aleatórios; cria o arquivo com permissão 0600 se não existir **e** `allowCreate`; se não existir e `!allowCreate` → `secrets.ErrKeyMissing`; arquivo com tamanho diferente de 32 bytes → erro.
  - `secrets.NewBox(key []byte) (*Box, error)`; `(*Box).Encrypt(plain []byte) (string, error)` → base64url(nonce‖ciphertext) com AES-256-GCM; `(*Box).Decrypt(s string) ([]byte, error)`.

- [ ] **Step 1: Testes**

```go
func TestKey_CreateThenReload(t *testing.T)     // segunda chamada devolve os mesmos 32 bytes
func TestKey_Permissions(t *testing.T)          // modo 0600 (t.Skip em windows)
func TestKey_MissingNotAllowed(t *testing.T)    // errors.Is(err, secrets.ErrKeyMissing)
func TestBox_RoundTrip(t *testing.T)
func TestBox_TamperFails(t *testing.T)          // alterar 1 byte → erro
func TestBox_WrongKeyFails(t *testing.T)
func TestBox_NonceUnique(t *testing.T)          // Encrypt do mesmo texto duas vezes → saídas diferentes
```

- [ ] **Step 2: Rodar** → FAIL. **Step 3: Implementar.** **Step 4: Rodar** → PASS.
- [ ] **Step 5: Commit** — `feat(etapa-0): chave mestra e criptografia AES-256-GCM`

`main.go` (na Tarefa 6) chama `LoadOrCreateKey(path, allowCreate = (contagem de usuários == 0))`, para nunca gerar chave nova com dados já criptografados.

---

### Task 4: Senhas, TOTP, códigos de recuperação e repositório de usuários

**Files:**
- Create: `internal/auth/password.go`, `internal/auth/totp.go`, `internal/auth/recovery.go`, `internal/store/users.go`
- Test: `internal/auth/password_test.go`, `internal/auth/totp_test.go`, `internal/auth/recovery_test.go`, `internal/store/users_test.go`

**Interfaces:**
- Produces:
  - `auth.HashPassword(pw string) (string, error)` — argon2id, `m=65536 KiB, t=3, p=2`, salt 16 bytes, chave 32 bytes, formato PHC `$argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>`.
  - `auth.VerifyPassword(encoded, pw string) (bool, error)` — comparação em tempo constante.
  - `auth.ValidatePasswordPolicy(pw string) error` — mínimo 12 caracteres → `auth.ErrWeakPassword`.
  - `auth.NewTOTPSecret(email string) (secret, otpauthURL string, err error)` — issuer `"KoreGO"`, 6 dígitos, período 30s, SHA1.
  - `auth.CheckTOTP(secret, code string, now time.Time, lastStep int64) (step int64, ok bool)` — aceita passo atual ±1; recusa `step <= lastStep` (anti-replay).
  - `auth.GenerateRecoveryCodes() (plain []string, hashes []string, err error)` — 10 códigos `xxxxx-xxxxx` (base32 minúsculo sem ambíguos); hash = SHA-256 hex do código normalizado (minúsculo, sem espaços).
  - `auth.HashRecoveryCode(code string) string`
  - `store.User{ID int64; Email, PasswordHash, Role, TOTPSecretEnc string; TOTPLastStep int64; CreatedAt time.Time}`
  - `(*DB).CreateAdminIfNone(ctx, u User, recoveryHashes []string) (int64, error)` — numa transação `BEGIN IMMEDIATE`: se `COUNT(users) > 0` → `store.ErrSetupDone`; senão insere usuário e códigos.
  - `(*DB).CountUsers(ctx) (int, error)`, `UserByEmail(ctx, email) (User, error)` (e-mail normalizado minúsculo; não encontrado → `store.ErrNotFound`), `UserByID(ctx, id) (User, error)`
  - `(*DB).AdvanceTOTPStep(ctx, userID, step int64) (bool, error)` — `UPDATE ... SET totp_last_step=? WHERE id=? AND totp_last_step < ?`; retorna se atualizou.
  - `(*DB).UseRecoveryCode(ctx, userID int64, hash string) (bool, error)` — marca `used_at` se existir e não usado.

- [ ] **Step 1: Testes**

```go
func TestHashVerify(t *testing.T)               // senha correta true, errada false; hash começa com "$argon2id$v=19$m=65536,t=3,p=2$"
func TestPolicy(t *testing.T)                   // "curta" → ErrWeakPassword; 12 chars → nil
func TestCheckTOTP_ValidAndSkew(t *testing.T)   // código do passo atual, anterior e seguinte ok; dois passos atrás falha
func TestCheckTOTP_Replay(t *testing.T)         // lastStep == passo do código → ok=false
func TestRecoveryCodes(t *testing.T)            // 10 únicos, formato ^[a-z2-9]{5}-[a-z2-9]{5}$, HashRecoveryCode(" ABCDE-fghij ") == HashRecoveryCode("abcde-fghij")
func TestCreateAdminIfNone_Once(t *testing.T)   // segunda chamada → ErrSetupDone
func TestAdvanceTOTPStep(t *testing.T)          // 5 → true; 5 de novo → false; 4 → false
func TestUseRecoveryCode_SingleUse(t *testing.T)
```

- [ ] **Step 2–4:** rodar (FAIL) → implementar → rodar (PASS).
- [ ] **Step 5: Commit** — `feat(etapa-0): senhas argon2id, TOTP com anti-replay e códigos de recuperação`

---

### Task 5: Sessões, IP do cliente e limite de tentativas

**Files:**
- Create: `internal/store/sessions.go`, `internal/auth/sessions.go`, `internal/auth/ratelimit.go`, `internal/auth/clientip.go`
- Test: `internal/auth/sessions_test.go`, `internal/auth/ratelimit_test.go`, `internal/auth/clientip_test.go`

**Interfaces:**
- Produces:
  - `store.Session{ID, TokenHash string; UserID int64; CSRFToken, IP, UserAgent string; CreatedAt, LastSeenAt, ExpiresAt time.Time}`; métodos `CreateSession`, `SessionByTokenHash`, `TouchSession(id, at)`, `DeleteSession(id)`, `DeleteUserSession(userID int64, id string) (bool, error)`, `ListSessions(userID)`, `DeleteExpiredSessions(now) (int64, error)`.
  - `auth.SessionTTL = 7 * 24 * time.Hour`
  - `auth.Sessions{DB *store.DB; Now func() time.Time}`:
    - `Create(ctx, userID int64, ip, ua string) (token string, s store.Session, err error)` — token 32 bytes aleatórios base64url; guarda SHA-256 hex; CSRF 32 bytes base64url; ID 16 bytes hex.
    - `Validate(ctx, token string) (store.Session, error)` — inexistente ou expirada → `auth.ErrNoSession`; atualiza `last_seen_at` no máximo 1x por minuto.
  - `auth.Limiter` com `NewLimiter(max int, window, block time.Duration) *Limiter` (valores de produção: `5, time.Minute, 15*time.Minute`); `Blocked(ip string, now time.Time) bool`; `Fail(ip string, now time.Time)`; `Reset(ip string)`. Limpa entradas antigas a cada chamada de `Fail`.
  - `auth.ClientIP(r *http.Request) string` — usa o primeiro IP de `X-Forwarded-For` **somente** se `r.RemoteAddr` for loopback (127.0.0.0/8 ou ::1); caso contrário, o host de `RemoteAddr`.

- [ ] **Step 1: Testes**

```go
func TestSessions_CreateValidate(t *testing.T)  // token válido devolve a sessão; token diferente → ErrNoSession
func TestSessions_StoresOnlyHash(t *testing.T)  // a linha no banco não contém o token em texto
func TestSessions_Expired(t *testing.T)         // Now avançado 7d+1s → ErrNoSession
func TestLimiter_BlocksAfterFive(t *testing.T)  // 5 Fail em 1 min → Blocked true; 5 Fail espaçados de 61s → false
func TestLimiter_BlockLasts15Min(t *testing.T)  // bloqueado em t; em t+14m59s ainda; em t+15m01s não
func TestClientIP_IgnoresXFFFromRemote(t *testing.T)   // RemoteAddr "203.0.113.10:5000", XFF "1.2.3.4" → "203.0.113.10"
func TestClientIP_TrustsXFFFromLoopback(t *testing.T)  // RemoteAddr "127.0.0.1:5000", XFF "198.51.100.7, 10.0.0.1" → "198.51.100.7"
```

- [ ] **Step 2–4:** FAIL → implementar → PASS.
- [ ] **Step 5: Commit** — `feat(etapa-0): sessões, limite de tentativas e IP do cliente`

---

### Task 6: Fluxos de setup, login, logout, auditoria e montagem no `main`

**Files:**
- Create: `internal/auth/service.go`, `internal/audit/audit.go`, `internal/store/audit.go`, `internal/api/middleware.go`, `internal/api/setup.go`, `internal/api/auth.go`, `internal/api/audit.go`
- Modify: `internal/api/server.go`, `cmd/korego/main.go`
- Test: `internal/auth/service_test.go`, `internal/api/auth_test.go`

**Interfaces:**
- Consumes: Tarefas 2–5.
- Produces:
  - `audit.Entry{UserID *int64; Action, Target, IP string; Metadata map[string]any}`; `audit.Logger{DB *store.DB; Now func() time.Time}`; `(*Logger).Record(ctx, e Entry) error`; `(*DB).ListAudit(ctx, limit, beforeID int64) ([]store.AuditRow, error)`.
  - Ações de auditoria desta etapa: `auth.setup`, `auth.login`, `auth.login_failed`, `auth.logout`, `auth.session_revoked`.
  - `auth.Service{DB; Box *secrets.Box; Sessions *Sessions; Limiter *Limiter; Audit *audit.Logger; Now func() time.Time}`:
    - `NeedsSetup(ctx) (bool, error)`
    - `BeginSetup(ctx, email, password string) (SetupChallenge, error)` — `SetupChallenge{Token, Secret, OTPAuthURL string}`; guarda pendência **em memória** por 15 min (mapa protegido por mutex); senha já com hash; recusa se já houver usuário (`ErrSetupDone`).
    - `ConfirmSetup(ctx, token, code, ip, ua string) (LoginResult, []string, error)` — valida TOTP, chama `CreateAdminIfNone`, devolve códigos de recuperação em texto e cria sessão.
    - `Login(ctx, email, password, code, ip, ua string) (LoginResult, error)` — `code` aceita TOTP (6 dígitos) ou código de recuperação (`xxxxx-xxxxx`). Qualquer falha de e-mail, senha ou código → `ErrInvalidCredentials`. Com IP bloqueado → `ErrRateLimited`, sem checar a senha. Para e-mail inexistente, ainda executa `VerifyPassword` contra um hash fixo (tempo uniforme).
    - `Logout(ctx, sessionID string) error`
    - `LoginResult{Token, CSRFToken string; User store.User; ExpiresAt time.Time}`
  - Middlewares em `api`: `RequireSession` (lê o cookie `korego_session`, coloca `store.Session` e `store.User` no contexto; senão 401 `UNAUTHORIZED`) e `RequireCSRF` (métodos ≠ GET/HEAD/OPTIONS: `X-CSRF-Token` igual ao da sessão, comparação em tempo constante; senão 403 `CSRF_INVALID`).
  - Rotas:
    - `GET /api/setup/status` → `{"needsSetup": bool}`
    - `POST /api/setup` `{email,password}` → `{"token","secret","otpauthUrl"}`; com usuário existente → 409 `SETUP_ALREADY_DONE`; senha fraca → 400 `WEAK_PASSWORD`; e-mail inválido → 400 `VALIDATION_ERROR`
    - `POST /api/setup/confirm` `{token,code}` → define cookie, `{"user","csrfToken","recoveryCodes":[10]}`; token desconhecido/expirado → 400 `SETUP_TOKEN_INVALID`; código errado → 400 `INVALID_TOTP`
    - `POST /api/auth/login` `{email,password,code}` → cookie + `{"user","csrfToken"}`; 401 `INVALID_CREDENTIALS`; 429 `RATE_LIMITED`
    - `POST /api/auth/logout` (sessão + CSRF) → 204, cookie expirado
    - `GET /api/auth/me` (sessão) → `{"user","csrfToken"}`
    - `GET /api/auth/sessions` → lista com `current: bool`; `DELETE /api/auth/sessions/{id}` (CSRF) → 204
    - `GET /api/audit?limit=50&before=<id>` (sessão) → `{"items":[...],"nextBefore":id|null}`; `limit` máximo 200
  - JSON de usuário: `{"id","email","role","createdAt"}`. Nunca expor hash nem segredo TOTP.
  - Cookie: `Secure` quando `r.TLS != nil` ou (`ClientIP` confiável e `X-Forwarded-Proto: https`).
  - `main.go`: abre o banco em `<DataDir>/korego.db`, carrega a chave de `<ConfigDir>/master.key` (`allowCreate` = nenhum usuário), monta o `auth.Service` e roda uma goroutine que chama `DeleteExpiredSessions` a cada hora.

- [ ] **Step 1: Testes (service + HTTP com `httptest`)**

```go
func TestSetupFlow_HappyPath(t *testing.T)            // status true → setup → confirm com código válido → 10 códigos, cookie definido → status false
func TestSetup_ConcurrentConfirm_OneAdmin(t *testing.T) // 2 BeginSetup + 2 ConfirmSetup em goroutines → exatamente 1 sucesso, outro ErrSetupDone; CountUsers == 1
func TestSetup_AfterDone_409(t *testing.T)
func TestLogin_TOTPReplayRejected(t *testing.T)       // login ok com código C; novo login com o mesmo C no mesmo passo → 401
func TestLogin_RecoveryCodeSingleUse(t *testing.T)
func TestLogin_GenericError(t *testing.T)             // e-mail inexistente, senha errada e código errado → mesmo corpo 401 INVALID_CREDENTIALS
func TestLogin_RateLimited(t *testing.T)              // 5 falhas → 6ª tentativa com credenciais corretas → 429
func TestCSRF_Required(t *testing.T)                  // POST /api/auth/logout sem header → 403 CSRF_INVALID; com header → 204
func TestCookieAttributes(t *testing.T)               // HttpOnly, SameSite=Strict, Path=/, Max-Age 604800; Secure ausente em http simples
func TestAudit_RecordsLoginAndFailure(t *testing.T)   // GET /api/audit lista auth.login_failed e auth.login com IP
func TestMe_Unauthorized(t *testing.T)                // sem cookie → 401 UNAUTHORIZED
```

- [ ] **Step 2–4:** FAIL → implementar → `go test ./...` PASS.
- [ ] **Step 5: Teste manual rápido** — `KOREGO_DATA_DIR=./tmp KOREGO_CONFIG_DIR=./tmp go run ./cmd/korego` e `curl localhost:8000/api/setup/status` → `{"needsSetup":true}`.
- [ ] **Step 6: Commit** — `feat(etapa-0): setup do admin com 2FA, login, sessões e auditoria`

---

### Task 7: Fronteira `Node` e informações do host

**Files:**
- Create: `internal/node/node.go`, `internal/node/local.go`, `internal/node/nodetest/fake.go`, `internal/api/system.go`
- Modify: `internal/api/server.go`, `cmd/korego/main.go`
- Test: `internal/node/local_test.go`, `internal/api/system_test.go`

**Interfaces:**
- Produces:
  - ```go
    type HostInfo struct {
        Hostname, OS, Platform, PlatformVersion, Kernel, Arch string
        CPUs int
        MemoryTotalBytes, UptimeSeconds uint64
    }
    type Node interface {
        Info(ctx context.Context) (HostInfo, error)
    }
    ```
    As etapas 1 e 2 acrescentam `Metrics()`, `Docker()`, `PM2()`, `Proxy()` a esta interface.
  - `node.NewLocal() *node.Local` (gopsutil v4: `host.InfoWithContext`, `mem.VirtualMemoryWithContext`, `cpu.CountsWithContext(ctx, true)`).
  - `nodetest.Fake{InfoFn func(ctx) (node.HostInfo, error)}` implementando `node.Node`.
  - `GET /api/system` (sessão) → JSON de `HostInfo` em camelCase (`hostname`, `os`, `platform`, `platformVersion`, `kernel`, `arch`, `cpus`, `memoryTotalBytes`, `uptimeSeconds`); erro do node → 503 `NODE_UNAVAILABLE`.

- [ ] **Step 1: Testes** — `TestLocal_Info` (hostname não vazio, CPUs ≥ 1, memória > 0; roda em qualquer SO); `TestSystem_ReturnsInfo` e `TestSystem_NodeError503` com o fake.
- [ ] **Step 2–4:** FAIL → implementar → PASS.
- [ ] **Step 5: Commit** — `feat(etapa-0): fronteira Node com informações do host`

---

### Task 8: Hub de eventos e WebSocket

**Files:**
- Create: `internal/events/hub.go`, `internal/api/ws.go`
- Modify: `internal/api/server.go`, `cmd/korego/main.go`
- Test: `internal/events/hub_test.go`, `internal/api/ws_test.go`

**Interfaces:**
- Produces:
  - `events.Event{Topic string; Data any}`
  - `events.NewHub() *Hub`; `(*Hub).Publish(topic string, data any)` (nunca bloqueia); `(*Hub).Subscribe(topic string) (ch <-chan Event, cancel func())` — buffer 64; se cheio, o evento é **descartado** para aquele assinante.
  - `events.SourceFunc func(ctx context.Context, topic string, publish func(data any))`; `(*Hub).RegisterSource(prefix string, fn SourceFunc)` — ao surgir o primeiro assinante de um tópico com esse prefixo, roda `fn` numa goroutine; quando o último sai, cancela o `ctx`.
  - Fonte desta etapa: prefixo `system`, publica `{"uptimeSeconds": n}` a cada 5s (via `node.Info`).
  - `GET /api/ws` (sessão obrigatória):
    - recusa com 403 se o header `Origin` não for igual ao host da requisição (usar `websocket.AcceptOptions{OriginPatterns: ...}` vazio, que só aceita a mesma origem)
    - mensagens do cliente: `{"type":"subscribe","topic":"system"}` / `{"type":"unsubscribe","topic":"..."}`
    - tópico validado por `^[a-z][a-z-]*(:[A-Za-z0-9_.-]{1,128})?$`; inválido → `{"type":"error","code":"INVALID_TOPIC","topic":"..."}`
    - mensagens do servidor: `{"type":"event","topic":"...","data":...}`
    - ping a cada 30s; fecha a conexão quando a sessão expira (revalidar no ping)

- [ ] **Step 1: Testes**

```go
func TestHub_PublishSubscribe(t *testing.T)
func TestHub_SlowSubscriberDoesNotBlock(t *testing.T)  // assinante A nunca lê; 1000 Publish terminam em <100ms; assinante B recebe o último evento
func TestHub_SourceStartsAndStops(t *testing.T)        // 1º Subscribe inicia a fonte; 2º não reinicia; após cancelar os dois, ctx da fonte é cancelado
func TestWS_RequiresSession(t *testing.T)              // sem cookie → 401
func TestWS_RejectsForeignOrigin(t *testing.T)         // cookie válido + Origin "https://malicioso.exemplo.com" → handshake falha (403)
func TestWS_SubscribeReceivesEvent(t *testing.T)       // subscribe "system" → recebe {"type":"event","topic":"system"} (fake node, intervalo injetável)
func TestWS_InvalidTopic(t *testing.T)                 // topic "../x" → INVALID_TOPIC
```

- [ ] **Step 2–4:** FAIL → implementar (intervalo da fonte `system` configurável para os testes) → PASS.
- [ ] **Step 5: Commit** — `feat(etapa-0): hub de eventos e WebSocket autenticado`

---

### Task 9: Servir o frontend embutido, cabeçalhos de segurança e proxy de desenvolvimento

**Files:**
- Create: `internal/webui/embed.go`, `internal/webui/handler.go`, `internal/webui/devproxy.go`, `internal/webui/dist/.gitkeep`, `internal/api/headers.go`
- Modify: `.gitignore` (adicionar `!internal/webui/dist/.gitkeep`), `internal/api/server.go`, `cmd/korego/main.go`
- Test: `internal/webui/handler_test.go`, `internal/api/headers_test.go`

**Interfaces:**
- Produces:
  - `webui.Dist() fs.FS` (sub-FS `dist` do `//go:embed all:dist`)
  - `webui.NewHandler(fsys fs.FS) http.Handler` — serve arquivos estáticos do export (gerado com `trailingSlash: true`, logo `/login/` → `login/index.html`); caminho inexistente → `404.html` com status 404; `/_next/static/*` com `Cache-Control: public, max-age=31536000, immutable`; HTML com `Cache-Control: no-cache`. Se o FS só tiver `.gitkeep`, responde 503 com uma página simples "Frontend não compilado. Rode `make web`."
  - `webui.NewDevProxy(target string) (http.Handler, error)` — `httputil.ReverseProxy` para o servidor `next dev` (suporta upgrade de WebSocket do HMR). Ativado quando `Config.DevProxy` não está vazio.
  - `api.SecurityHeaders(next http.Handler) http.Handler` — `Content-Security-Policy: default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`, `X-Frame-Options: DENY`. Não aplicar o CSP no modo dev proxy.
  - Roteamento: `/api/*` → API; todo o resto → `webui`.

- [ ] **Step 1: Testes** — com `fstest.MapFS`: `TestServesIndex`, `TestTrailingSlashRoute` (`/login/` → conteúdo de `login/index.html`), `TestNotFoundPage404`, `TestStaticCacheHeaders`, `TestEmptyDist503`; `TestSecurityHeaders` (todos os 4 headers presentes numa resposta de `/api/health`).
- [ ] **Step 2–4:** FAIL → implementar → PASS.
- [ ] **Step 5: Commit** — `feat(etapa-0): frontend embutido, cabeçalhos de segurança e proxy de dev`

---

### Task 10: Projeto Next.js e sistema visual

**REQUIRED SKILLS:** invocar `frontend-design` antes de escrever componentes (direção: inspirado no Coolify, mais escuro, cantos 100% retos) e `web-design-guidelines` ao final para revisar.

**Files:**
- Create: `web/` (via `pnpm create next-app@latest web --ts --tailwind --app --src-dir --eslint --use-pnpm --no-turbopack --import-alias "@/*"`), `web/next.config.ts`, `web/src/app/globals.css`, `web/src/components/ui/*` (Button, Input, Label, Panel, Badge, Alert, Spinner), `web/src/app/styleguide/page.tsx`, `scripts/copy-web.mjs`, `web/vitest.config.ts`
- Modify: `Makefile` (alvo `web`: `cd web && pnpm install --frozen-lockfile && pnpm build && node ../scripts/copy-web.mjs`)
- Test: `web/src/components/ui/__tests__/button.test.tsx`, `web/scripts/no-rounded.test.ts`

**Interfaces:**
- Produces:
  - `next.config.ts`: em produção `output: 'export'`, `trailingSlash: true`, `images: { unoptimized: true }`. Sem `rewrites` (o Go faz o proxy em dev).
  - `scripts/copy-web.mjs`: apaga `internal/webui/dist/*` (exceto `.gitkeep`) e copia `web/out/` para lá. Funciona em Windows e Linux (só `node:fs`).
  - Tokens em `globals.css` (`@theme`): paleta escura definida com o `frontend-design`; `--radius-*` todos `0`.
  - Componentes em `@/components/ui` com variantes por props (`variant`, `size`), sem classes `rounded-*` (exceto `rounded-none`).
  - Página `/styleguide/` mostrando todos os componentes (útil para a revisão visual; fica fora do menu).

- [ ] **Step 1: Testes**

```ts
// button.test.tsx
it('renderiza variantes e fica desabilitado em loading', ...) // aria-busy="true" e disabled quando loading
// no-rounded.test.ts
it('nenhum arquivo em src/ usa rounded-* além de rounded-none', ...) // varre src/**/*.tsx com regex /\brounded-(?!none\b)/
```

- [ ] **Step 2: Rodar** — `cd web && pnpm vitest run` → FAIL.
- [ ] **Step 3: Implementar** (scaffold, tokens, componentes, styleguide).
- [ ] **Step 4: Verificar** — `pnpm vitest run && pnpm lint && pnpm build` PASS; `make web && go build ./... && go run ./cmd/korego`; abrir `http://localhost:8000/styleguide/` e conferir: fundo escuro, nenhum canto arredondado.
- [ ] **Step 5: Commit** — `feat(etapa-0): projeto Next.js e sistema visual escuro sem cantos arredondados`

---

### Task 11: Clientes de API e WebSocket no frontend

**Files:**
- Create: `web/src/lib/api.ts`, `web/src/lib/ws.ts`, `web/src/lib/auth-context.tsx`, `web/src/lib/query.tsx`
- Test: `web/src/lib/__tests__/api.test.ts`, `web/src/lib/__tests__/ws.test.ts`

**Interfaces:**
- Consumes: rotas das Tarefas 6–8.
- Produces:
  - `class ApiError extends Error { code: string; status: number }`
  - `api<T>(path: string, init?: { method?: string; body?: unknown }): Promise<T>` — `credentials: 'same-origin'`, JSON; envia `X-CSRF-Token` (guardado em memória por `setCsrfToken(t: string)`) em métodos ≠ GET; resposta de erro no formato da API vira `ApiError`; 401 dispara o evento `korego:unauthorized` em `window`.
  - `class LiveSocket { subscribe(topic: string, cb: (data: unknown) => void): () => void; status: 'connecting'|'open'|'closed'; onStatus(cb) }` — uma conexão compartilhada em `/api/ws`; reconexão com backoff exponencial (1s, 2s, 4s… máx. 30s, com jitter de ±20%); ao reconectar, reenvia todos os `subscribe` ativos.
  - Hook `useLive<T>(topic: string): T | undefined`.
  - `AuthProvider` + `useAuth(): { user, status: 'loading'|'setup'|'anonymous'|'authenticated', login(...), logout(), refresh() }` — no carregamento consulta `/api/setup/status` e `/api/auth/me`.
  - `QueryProvider` (TanStack Query, `retry` desligado para `ApiError` 4xx).

- [ ] **Step 1: Testes** (Vitest, `fetch` e `WebSocket` simulados)

```ts
it('lança ApiError com code e status', ...)
it('envia X-CSRF-Token só em métodos que alteram estado', ...)
it('dispara korego:unauthorized em 401', ...)
it('reassina os tópicos após reconectar', ...)
it('aumenta o intervalo de reconexão até 30s', ...)
```

- [ ] **Step 2–4:** FAIL → implementar → PASS.
- [ ] **Step 5: Commit** — `feat(etapa-0): clientes de API e WebSocket com reconexão`

---

### Task 12: Telas de setup, login, layout do painel, visão geral e segurança

**REQUIRED SKILLS:** `frontend-design` para as telas; `web-design-guidelines` para revisar antes do commit.

**Files:**
- Create: `web/src/app/setup/page.tsx`, `web/src/app/login/page.tsx`, `web/src/app/(painel)/layout.tsx`, `web/src/app/(painel)/page.tsx` (visão geral), `web/src/app/(painel)/configuracoes/seguranca/page.tsx`, `web/src/components/shell/*` (Sidebar, Topbar, LiveStatus), `web/src/app/not-found.tsx`
- Test: `web/src/app/__tests__/setup.test.tsx`, `web/src/app/__tests__/login.test.tsx`

**Interfaces:**
- Consumes: Tarefa 11.
- Comportamento:
  - **Redirecionamentos (client-side):** `status === 'setup'` → `/setup/`; `anonymous` → `/login/`; `authenticated` em `/setup/` ou `/login/` → `/`.
  - **Setup (3 passos):** (1) e-mail + senha + confirmação (mín. 12 caracteres, validação inline); (2) QR code do `otpauthUrl` (gerado no cliente com a lib `qrcode`) + segredo em texto para copiar + campo de 6 dígitos; (3) os 10 códigos de recuperação com "Copiar" e "Baixar .txt", e um checkbox "Guardei meus códigos" obrigatório antes de "Ir para o painel".
  - **Login:** e-mail, senha e código (aceita 6 dígitos ou `xxxxx-xxxxx`, com dica). Mensagens por `code`: `INVALID_CREDENTIALS` → "E-mail, senha ou código incorretos."; `RATE_LIMITED` → "Muitas tentativas. Tente novamente em 15 minutos."
  - **Layout:** barra lateral com itens Visão geral, Containers (desabilitado, "Etapa 1"), Projetos (desabilitado, "Etapa 2"), Domínios (desabilitado, "Etapa 2"), Configurações; topo com hostname, indicador do WebSocket (`LiveStatus`) e menu do usuário (Sair).
  - **Visão geral:** cartão com `HostInfo` (`GET /api/system`) e uptime ao vivo (`useLive('system')`), formatado como "3d 4h 12m".
  - **Configurações › Segurança:** sessões ativas (IP, navegador, último acesso, "esta sessão", botão Revogar) e os últimos eventos da auditoria com "Carregar mais" (paginação por `before`).
  - Todas as telas com estados de carregando, vazio e erro.

- [ ] **Step 1: Testes** — setup: avança apenas com senha ≥ 12 e confirmação igual; passo 3 bloqueia o botão até marcar o checkbox. Login: mostra a mensagem certa para `INVALID_CREDENTIALS` e `RATE_LIMITED`.
- [ ] **Step 2–4:** FAIL → implementar → `pnpm vitest run && pnpm lint && pnpm build` PASS.
- [ ] **Step 5: Revisão visual** — `make web && go run ./cmd/korego`; percorrer setup → login → visão geral → segurança; aplicar `web-design-guidelines` e corrigir os apontamentos.
- [ ] **Step 6: Commit** — `feat(etapa-0): telas de setup, login, painel e segurança`

---

### Task 13: Instalador, serviço systemd, releases e CI

**Files:**
- Create: `scripts/install.sh`, `scripts/korego.service`, `scripts/test-install.sh`, `.goreleaser.yaml`, `.github/workflows/ci.yml`, `.github/workflows/release.yml`

**Interfaces:**
- Produces:
  - `install.sh` (POSIX `sh`, `set -eu`):
    1. exige root; aceita só Ubuntu/Debian (`/etc/os-release`); arquitetura `x86_64` → `amd64`, `aarch64` → `arm64`.
    2. verifica Docker e `docker compose`; se faltarem, pergunta (ou instala direto com `KOREGO_YES=1`) via `https://get.docker.com`.
    3. informa (sem alterar nada) se 80/443 estão ocupadas e por qual processo (`ss -ltnp`); se 8000 estiver ocupada → aborta com mensagem.
    4. baixa `korego_<versão>_linux_<arch>.tar.gz` e `checksums.txt` da release (`KOREGO_VERSION`, padrão `latest`), confere SHA-256 e instala em `/usr/local/bin/korego`. `KOREGO_LOCAL_BINARY=/caminho` pula o download (testes).
    5. cria `/var/lib/korego` (0750) e `/etc/korego` (0700); escreve `/etc/korego/config.toml` só se não existir.
    6. instala `/etc/systemd/system/korego.service` e roda `systemctl enable --now korego`, exceto com `KOREGO_SKIP_SERVICE=1`.
    7. imprime `http://<IP público>:8000` (IP via `hostname -I`, primeiro endereço).
  - `korego.service`: `ExecStart=/usr/local/bin/korego serve --config /etc/korego/config.toml`, `Restart=on-failure`, `RestartSec=3`, `User=root` (precisa de Docker e `/proc`), `LimitNOFILE=65536`.
  - GoReleaser: `before.hooks` roda `make web`; builds `linux/amd64` e `linux/arm64` com `CGO_ENABLED=0` e `-ldflags "-s -w -X .../version.Version={{.Version}}"`; arquivo `korego_{{.Version}}_linux_{{.Arch}}.tar.gz`; `checksums.txt` (SHA-256); anexa `scripts/install.sh`.
  - `ci.yml` (push/PR): `go vet`, `go test ./...` (ubuntu e windows), `pnpm lint && pnpm vitest run && pnpm build`, `shellcheck scripts/*.sh`, `scripts/test-install.sh`.
  - `release.yml` (tag `v*`): GoReleaser.
  - `test-install.sh`: compila o binário linux, roda `install.sh` num container `ubuntu:24.04` com `KOREGO_LOCAL_BINARY`, `KOREGO_SKIP_SERVICE=1` e Docker simulado (`docker` falso no PATH), e confere: binário instalado e executável (`korego version`), diretórios com as permissões certas, `config.toml` criado, segunda execução não sobrescreve o `config.toml`.

- [ ] **Step 1: Escrever `test-install.sh`** com as verificações acima.
- [ ] **Step 2: Rodar** — `sh scripts/test-install.sh` → FAIL (install.sh não existe).
- [ ] **Step 3: Implementar** `install.sh`, `korego.service`, `.goreleaser.yaml` e os workflows.
- [ ] **Step 4: Verificar** — `shellcheck scripts/*.sh` sem avisos; `sh scripts/test-install.sh` PASS; `goreleaser release --snapshot --clean` gera os 2 arquivos e o `checksums.txt`.
- [ ] **Step 5: Commit** — `build(etapa-0): instalador, serviço systemd, GoReleaser e CI`

---

### Task 14: Autoatualização com rollback

**Files:**
- Create: `internal/update/check.go`, `internal/update/apply.go`, `internal/update/finish.go`, `internal/api/update.go`, `web/src/app/(painel)/configuracoes/atualizacao/page.tsx`
- Modify: `cmd/korego/main.go` (subcomando `update-finish`), `internal/api/server.go`
- Test: `internal/update/check_test.go`, `internal/update/apply_test.go`, `internal/update/finish_test.go`, `internal/api/update_test.go`

**Interfaces:**
- Produces:
  - `update.Release{Version, TarballURL, ChecksumsURL string}`
  - `update.Checker{BaseURL, Repo string; HTTP *http.Client}` (padrões `https://api.github.com`, `engviniciusbs/korego`); `(*Checker).Latest(ctx, arch string) (Release, error)`.
  - `update.Available(current, latest string) bool` — semver via `golang.org/x/mod/semver`; `current == "dev"` → sempre `false`.
  - `update.Apply(ctx, rel Release, exePath string, httpc *http.Client) error` — baixa o tarball, confere o SHA-256 contra `checksums.txt`, extrai `korego` para `exePath+".new"`, renomeia `exePath` → `exePath+".prev"` e `.new` → `exePath`. Checksum divergente → `update.ErrChecksum` e nada é trocado.
  - `update.Finisher{Restart func(ctx) error; Health func(ctx) error; Timeout time.Duration; ExePath string}`; `(*Finisher).Run(ctx) error` — reinicia, faz polling em `Health` até `Timeout` (30s); se falhar, restaura `.prev` e reinicia de novo, retornando `update.ErrRolledBack`.
  - Subcomando `korego update-finish` usa `Restart = exec.Command("systemctl", "restart", "korego")` e `Health = GET http://127.0.0.1:<porta>/api/health`. A API dispara esse subcomando de forma independente do serviço com `exec.Command("systemd-run", "--unit=korego-update", "--collect", exePath, "update-finish")`.
  - Rotas (sessão): `GET /api/update` → `{"current","latest","available"}` (cache de 1h); `POST /api/update` (CSRF) → 202 e grava `system.update` na auditoria; sem atualização → 409 `NO_UPDATE`.
  - Tela Configurações › Atualização: versão atual, última versão, botão "Atualizar" com confirmação, e reconexão automática após o reinício.

- [ ] **Step 1: Testes** (com `httptest.Server` simulando a API e os assets do GitHub)

```go
func TestAvailable(t *testing.T)                // v0.1.0→v0.2.0 true; iguais false; "dev" false
func TestLatest_PicksArchAsset(t *testing.T)
func TestApply_ReplacesAndKeepsPrev(t *testing.T)
func TestApply_ChecksumMismatch(t *testing.T)   // ErrChecksum; exePath intacto; sem .new sobrando
func TestFinish_HealthyNoRollback(t *testing.T)
func TestFinish_UnhealthyRollsBack(t *testing.T) // Health sempre falha → .prev restaurado, Restart chamado 2x, ErrRolledBack
func TestUpdateAPI_NoUpdate409(t *testing.T)
```

- [ ] **Step 2–4:** FAIL → implementar → PASS.
- [ ] **Step 5: Commit** — `feat(etapa-0): autoatualização com verificação de checksum e rollback`

---

### Task 15: Teste ponta a ponta e README

**Files:**
- Create: `web/e2e/fundacao.spec.ts`, `web/playwright.config.ts`, `README.md`
- Modify: `.github/workflows/ci.yml` (job `e2e`)

**Interfaces:**
- Consumes: tudo acima.
- Produces:
  - `playwright.config.ts` com `webServer` que roda o binário Go já compilado (`make web build`) com `KOREGO_DATA_DIR` e `KOREGO_CONFIG_DIR` em pasta temporária, porta `18000`.
  - Teste `fundacao.spec.ts`: abre `/` → redireciona para `/setup/` → cria admin (`admin@exemplo.com`) → lê o segredo exibido e gera o TOTP no teste (lib `otplib`) → guarda os códigos de recuperação → chega à visão geral e vê o hostname e o uptime → sai → faz login com um **código de recuperação** → abre Segurança e vê 1 sessão e os eventos `auth.setup`, `auth.logout` e `auth.login` na auditoria.
  - README: o que é o KoreGO, estado (etapa 0), instalação (`curl -fsSL https://github.com/engviniciusbs/korego/releases/latest/download/install.sh | sh`), desenvolvimento local (`pnpm dev` em `web/` + `KOREGO_DEV_PROXY=http://localhost:3000 go run ./cmd/korego`), testes e licença (definir com o autor antes de publicar a primeira release; deixar a seção "Licença: a definir").

- [ ] **Step 1: Escrever o teste e2e.**
- [ ] **Step 2: Rodar** — `cd web && pnpm playwright test` → deve passar se as tarefas anteriores estiverem corretas; corrigir o que falhar.
- [ ] **Step 3: Escrever o README** e o job `e2e` no CI.
- [ ] **Step 4: Verificar** — `make test && cd web && pnpm playwright test` PASS; CI verde no GitHub (`gh run watch`).
- [ ] **Step 5: Commit** — `test(etapa-0): fluxo ponta a ponta da fundação e README`

---

## Depois desta etapa

- Validar na VPS: publicar uma tag `v0.1.0`, rodar o instalador, criar o admin e conferir o painel em `http://<IP>:8000` (as portas 80/443 continuam com o nginx; nada é alterado).
- Escrever o plano da **Etapa 1 (Observar)**.
