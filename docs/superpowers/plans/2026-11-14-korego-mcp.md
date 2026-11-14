# KoreGO MCP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an agent (Claude Code on the user's laptop) operate KoreGO through an MCP server embedded in the binary at `/mcp`, authenticated by named API tokens, with irreversible actions gated by a fresh 2FA code.

**Architecture:** API tokens live in a new table and are validated by a `RequireToken` middleware that only guards `/mcp`. The authenticated REST routes are split into a reusable "core" that is mounted twice: behind the session cookie + CSRF (as today) under `/api`, and as an in-process router that MCP tools dispatch into with the token's identity in the context. That way validation, audit and the 2FA gate stay in the existing handlers. The MCP layer is the official Go SDK in stateless Streamable HTTP mode. Tool inputs reuse the REST input structs, so the schemas cannot drift from the API.

**Tech Stack:** Go 1.27, chi, SQLite (existing store), `github.com/modelcontextprotocol/go-sdk` (v1.8.0 or the latest stable v1), Next.js panel (existing `web/`).

**Spec:** `docs/superpowers/specs/2026-11-14-korego-mcp-design.md`

## Global Constraints

- Go is not on PATH on this machine. Run Go through Docker from the repo root: `docker run --rm -v "$PWD":/src -w /src -v korego-gocache:/root/go golang:1.27 go test ./... -count=1`. Below, `GO test …` means that prefix. Web: `cd web && pnpm test` / `pnpm lint` / `pnpm build`.
- Token format: `kg_` + 32 random bytes, base64url without padding. Only the SHA-256 (hex) is stored. Shown once.
- Token name: 1..64 chars after trim, unique among non-revoked tokens of the user. Expiry choices: never / 30 / 90 days; default 90.
- Bearer is accepted **only on `/mcp`**. `/api/*` keeps cookie + CSRF and ignores `Authorization`.
- Bearer transport: accepted when `requestIsHTTPS(r)`, or when the peer is loopback **and** no `X-Forwarded-For`, `X-Forwarded-Proto`, `X-Forwarded-Host` or `Forwarded` header is present. Otherwise 403 `TOKEN_REQUIRES_HTTPS`, message: "Tokens de API só funcionam via HTTPS. Configure o domínio do painel em Configurações → Painel."
- Token limiter key space: `"token:" + ip`, separate from login. It covers both bad tokens and wrong agent codes.
- `last_used_at`/`last_used_ip` are written at most once per minute per token.
- 2FA-gated routes for token requests: `DELETE /apps/{id}`, `DELETE /projects/{id}`, `DELETE /domains/{id}`, `DELETE /certificates/{id}`, `DELETE /containers/{id}`, `POST /proxy/takeover`, `POST /proxy/rollback`, `POST /update`, `PUT /settings/panel/domain`, `PUT /settings/panel/public-port`, `DELETE /github`.
- Error codes: reuse `CODE_REQUIRED` (403), `INVALID_CODE` (403), `RATE_LIMITED` (429). New: `INVALID_TOKEN` (401), `TOKEN_REQUIRES_HTTPS` (403), `TOKEN_NAME_TAKEN` (409), `TOKEN_NOT_FOUND` (404).
- Audit metadata on token requests: `{"via":"token","token":"<name>","tokenId":N}` merged into the entry's metadata. Audit actions for tokens: `api_token.create`, `api_token.revoke`.
- Token management, sessions, logout, `/auth/me`, setup and login are never reachable from a token.
- Secrets never come back through MCP: env values, certificate keys, TOTP secrets, GitHub App keys. The REST handlers already enforce this; tools must not add fields.
- User-facing text in pt-BR. Tool names in English snake_case, tool descriptions in pt-BR.
- UI: dark theme only, no rounded corners, follow the existing `configuracoes/*` pages. Use the `frontend-design` skill before building the page and `web-design-guidelines` at the end.

## Review Focus

1. **Two deletions in a row with the same code.** The existing TOTP replay protection rejects the second one. The agent must get a message telling it to ask for the next code, not just "Código inválido." Covered in Task 6 (`TestMCP_GatedReplayHint`).
2. **A token revoked while Claude Code stays connected.** Stateless MCP checks the token on every request, so the next tool call must fail with `INVALID_TOKEN`. Covered in Task 4 (`TestRequireToken_RevokedMidway`).
3. **Bearer header variants.** `bearer kg_…` (lowercase scheme) and extra spaces are accepted. A token in the query string (`/mcp?token=`) is never accepted. Covered in Task 4.
4. **A panel reached at `http://IP:8000` from the internet.** Creating a token is refused with the HTTPS hint, before any code is consumed. Covered in Task 5 (`TestTokens_CreateRefusedOverPlainHTTP`).
5. **`deploy_wait` with a huge timeout or an agent that disconnects.** The timeout is clamped to 120 s, and context cancellation returns promptly instead of leaking a goroutine. Covered in Task 6 (`TestMCP_DeployWaitClampAndCancel`).

---

### Task 1: API token store

**Files:**
- Create: `internal/store/migrations/0006_api_tokens.sql`
- Create: `internal/store/tokens.go`
- Test: `internal/store/tokens_test.go`

**Interfaces:**
- Produces:
  ```go
  type APIToken struct {
      ID, UserID  int64
      Name        string
      TokenHash   string
      Prefix      string     // first 10 chars of the secret ("kg_" + 7)
      CreatedAt   time.Time
      LastUsedAt  *time.Time
      LastUsedIP  string
      ExpiresAt   *time.Time // nil = never
      RevokedAt   *time.Time
  }
  func (d *DB) CreateAPIToken(ctx context.Context, t APIToken) (int64, error) // ErrConflict on duplicate active name
  func (d *DB) APITokenByHash(ctx context.Context, hash string) (APIToken, error) // ErrNotFound
  func (d *DB) ListAPITokens(ctx context.Context, userID int64) ([]APIToken, error) // non-revoked, newest first
  func (d *DB) RevokeAPIToken(ctx context.Context, userID, id int64, at time.Time) (APIToken, error) // ErrNotFound if missing or already revoked
  func (d *DB) TouchAPIToken(ctx context.Context, id int64, at time.Time, ip string) error
  ```

- [ ] **Step 1: Write the failing tests** in `tokens_test.go`, using the existing store test helper (`storetest.go`):
  - `TestAPIToken_CreateAndLookup`: create, then `APITokenByHash` returns the same fields, with `LastUsedAt`, `ExpiresAt` and `RevokedAt` nil when unset.
  - `TestAPIToken_DuplicateActiveName`: a second active token with the same name gives `ErrConflict`. After revoking the first, the name can be reused.
  - `TestAPIToken_Revoke`: revoke sets `RevokedAt`, the token disappears from `ListAPITokens`, a second revoke gives `ErrNotFound`, and a revoke under another `userID` gives `ErrNotFound`.
  - `TestAPIToken_Touch`: `LastUsedAt` and `LastUsedIP` are updated.
  - `TestAPIToken_UserCascade`: deleting the user removes the tokens (FK cascade).
- [ ] **Step 2:** `GO test ./internal/store/ -run APIToken -count=1` → FAIL (undefined).
- [ ] **Step 3:** Migration: table `api_tokens`, with the columns from the spec (§3.1) as unix-seconds integers, nullable for `last_used_at`, `expires_at` and `revoked_at`. `token_hash` is UNIQUE. Add a partial unique index `ON api_tokens(user_id, name) WHERE revoked_at IS NULL`. Implement the methods in `tokens.go`, following the scan and error style of `sessions.go` (`isUnique` → `ErrConflict`).
- [ ] **Step 4:** `GO test ./internal/store/ -count=1` → PASS. `GO vet ./...` clean.
- [ ] **Step 5:** Commit `feat(mcp): tabela e store de tokens de API`.

### Task 2: Token service and token-aware audit

**Files:**
- Create: `internal/auth/tokens.go`, `internal/auth/tokens_test.go`
- Modify: `internal/audit/audit.go` (`Record`)
- Test: `internal/audit/audit_test.go` (create it if missing)

**Interfaces:**
- Consumes: Task 1 store methods.
- Produces:
  ```go
  // package auth
  var ErrNoToken = errors.New("token inválido")
  type Tokens struct { DB *store.DB; Now func() time.Time }
  func (t *Tokens) Issue(ctx context.Context, userID int64, name string, ttl time.Duration) (secret string, tok store.APIToken, err error) // ttl 0 = never; ErrInvalidTokenName for empty/>64 after trim; store.ErrConflict passes through
  var ErrInvalidTokenName = errors.New("nome de token inválido")
  func (t *Tokens) Validate(ctx context.Context, secret, ip string) (store.APIToken, error) // ErrNoToken: malformed, unknown, revoked, expired
  // package audit
  type TokenRef struct { ID int64; Name string }
  func WithToken(ctx context.Context, ref TokenRef) context.Context
  func TokenFrom(ctx context.Context) (TokenRef, bool)
  ```

- [ ] **Step 1: Write the failing tests.**
  - `TestTokens_IssueFormat`: the secret matches `^kg_[A-Za-z0-9_-]{43}$`. `tok.Prefix == secret[:10]`. The stored hash is `hex(sha256(secret))`, and the secret itself is stored nowhere.
  - `TestTokens_ValidateRejects`: malformed (`"abc"`, `"kg_"`), unknown, revoked, and expired (with `Now` past `ExpiresAt`) all give `ErrNoToken`.
  - `TestTokens_TouchThrottled`: two `Validate` calls 30 s apart write `LastUsedAt` once. At 61 s it is written again, with the new IP.
  - `TestTokens_IssueNameRules`: `"  "` and 65 chars give `ErrInvalidTokenName`. The name is stored trimmed.
  - `TestRecord_MergesTokenMetadata` (audit): with `WithToken(ctx, {7,"notebook"})`, the stored metadata has `via=token`, `token=notebook`, `tokenId=7` plus the entry's own keys. The caller's `Metadata` map is not mutated. Without a token, the metadata is unchanged.
- [ ] **Step 2:** `GO test ./internal/auth/ ./internal/audit/ -count=1` → FAIL.
- [ ] **Step 3:** Implement. Use `crypto/rand` and `base64.RawURLEncoding`. `Validate` compares by hash lookup (the hash is the index, so no timing leak on the secret) and touches when `LastUsedAt` is nil or ≥ 1 min old. `Record` copies `e.Metadata` before merging.
- [ ] **Step 4:** `GO test ./internal/auth/ ./internal/audit/ -count=1` → PASS.
- [ ] **Step 5:** Commit `feat(mcp): emissão e validação de tokens, auditoria com o token`.

### Task 3: Split the authenticated routes into a reusable core (no behaviour change)

**Files:**
- Modify: `internal/api/server.go`, `internal/api/proxy_migration.go` (`confirmCode`), `internal/api/settings.go`, `internal/api/deploys.go`
- Test: the existing `internal/api/*_test.go` (must stay green), plus `internal/api/server_test.go`

**Interfaces:**
- Produces (package `api`, unexported, used by Tasks 4–6):
  ```go
  type routes struct { /* every handler value built once in NewRouter (ghH, updateH, wsH… are stateful: build once) */ }
  func (rt *routes) mountCore(r chi.Router)      // every authenticated route EXCEPT: /auth/me, /auth/sessions*, /auth/logout, /ws, /containers/{id}/logs/download, /tokens*
  func (rt *routes) mountSessionOnly(r chi.Router) // the excluded ones above
  func confirmCode(svc *auth.Service, w http.ResponseWriter, r *http.Request, userID int64, ip string, rec func(map[string]any)) bool // moved from the migrationHandlers method, same behaviour
  type panelDomainInput struct{ … }   // the anonymous struct of settings.setDomain, now named
  type panelPortInput struct{ … }     // settings.setPublicPort
  type rollbackInput struct{ … }      // deploys.rollback
  ```
- `NewRouter` keeps its signature. Inside `/api`, the authenticated group becomes `r.Group(func(r){ r.Use(RequireSession(d.Auth), RequireCSRF); rt.mountSessionOnly(r); rt.mountCore(r) })`.

- [ ] **Step 1:** Add `TestRouter_CoreExcludesSessionOnly` to `server_test.go`. Build a chi router, call only `rt.mountCore` on it, and assert that `GET /auth/me`, `GET /auth/sessions`, `POST /auth/logout`, `GET /ws` and `GET /tokens` answer 404 there, while `GET /system` answers 200. Expose a test hook if needed (`export_test.go`: `func CoreForTest(d Deps) http.Handler`).
- [ ] **Step 2:** `GO test ./internal/api/ -count=1` → the new test FAILS and every other test passes.
- [ ] **Step 3:** Refactor as described. Keep route registration order and the `if d.X != nil` conditions identical.
- [ ] **Step 4:** `GO test ./... -count=1` → PASS (the whole suite, untouched otherwise).
- [ ] **Step 5:** Commit `refactor(api): rotas autenticadas num núcleo reutilizável`.

### Task 4: `RequireToken`, the in-process dispatcher, the 2FA gate and audit gaps

**Files:**
- Create: `internal/api/tokenauth.go`, `internal/api/dispatch.go`
- Modify: `internal/api/server.go` (`Deps.Tokens *auth.Tokens`, `Deps.TokenLimiter *auth.Limiter`; gate wrappers in `mountCore`), `internal/api/proxy_migration.go`, `internal/api/proxy.go` (audit `proxy.retry`), `cmd/korego/main.go` (build `auth.Tokens` and `auth.NewLimiter(5, time.Minute, 15*time.Minute)` for tokens)
- Test: `internal/api/tokenauth_test.go`

**Interfaces:**
- Consumes: Task 2 (`auth.Tokens`, `audit.WithToken`), Task 3 (`routes`, `confirmCode`).
- Produces:
  ```go
  // RequireToken validates the Bearer token and puts in ctx: sessionKey = store.Session{ID: "token:<id>", UserID: tok.UserID}, userKey, audit.WithToken, and tokenIdentityKey.
  func RequireToken(tokens *auth.Tokens, db *store.DB, lim *auth.Limiter) func(http.Handler) http.Handler
  type tokenIdentity struct { Token store.APIToken; User store.User; RemoteAddr string; Header http.Header /* only X-Forwarded-For, X-Forwarded-Proto, X-Real-IP, CF-Connecting-IP, User-Agent */ }
  func tokenTransportOK(r *http.Request) bool
  // dispatch runs one request through the core router in-process.
  type dispatcher struct{ core http.Handler }
  func (d dispatcher) do(ctx context.Context, id tokenIdentity, method, path string, query url.Values, body any, code string) (status int, resp []byte)
  // requireCodeForToken: no-op for cookie requests; for token requests needs header X-Korego-Code, verified with auth.Service.VerifyCode and the token limiter (key "token:"+ip); on success marks ctx so confirmCode returns true without reading the body.
  func requireCodeForToken(svc *auth.Service, lim *auth.Limiter) func(http.Handler) http.Handler
  ```
- `dispatcher.do` builds the request with `http.NewRequestWithContext`, sets `RemoteAddr` and the copied headers from `id`, and sets `X-Korego-Code` when `code != ""`. It re-applies the identity to the context (session, user and audit token), serves the request through `core` with an `httptest.ResponseRecorder`, and marshals `body` only when it is non-nil.

- [ ] **Step 1: Write the failing tests** in `tokenauth_test.go`. Use the `env` helpers from `auth_test.go` (`newEnvDeps`, `withRemote`, `withHeader`) and mount `RequireToken` in front of a probe handler through an export_test hook.
  - `TestRequireToken_Valid`: `Authorization: Bearer <secret>` over a request with `r.TLS` set → the probe sees the user and `audit.TokenFrom`.
  - `TestRequireToken_SchemeVariants`: `bearer <s>` and `Bearer   <s>` → ok. `?token=<s>` without the header → 401 `INVALID_TOKEN`.
  - `TestRequireToken_Transport`: plain HTTP from `203.0.113.5:1` → 403 `TOKEN_REQUIRES_HTTPS`. From `127.0.0.1:1` with no forwarding headers → ok. From `127.0.0.1:1` with `X-Forwarded-Proto: http` → 403. From `127.0.0.1:1` with `X-Forwarded-Proto: https` → ok.
  - `TestRequireToken_RevokedMidway`: ok, then `RevokeAPIToken`, then the next request → 401 `INVALID_TOKEN`.
  - `TestRequireToken_LimiterSeparate`: 5 bad tokens from one IP → the 6th gives 429 `RATE_LIMITED`. A panel login from the same IP is **not** blocked.
  - `TestAPI_IgnoresBearer`: `GET /api/system` with a valid Bearer and no cookie → 401 `UNAUTHORIZED`.
  - `TestGate_TokenNeedsCode`: through `dispatcher.do` as a token, `DELETE /api/projects/{id}` without a code → 403 `CODE_REQUIRED`, with a wrong code → 403 `INVALID_CODE`, with a valid TOTP → 204, and reusing the same TOTP on another delete → 403 `INVALID_CODE`. Run the same table for every gated route in Global Constraints and assert `CODE_REQUIRED` without a code.
  - `TestGate_CookieUnchanged`: as a cookie session, `DELETE /api/projects/{id}` with no code → 204, and `POST /api/proxy/takeover` without a code is still `CODE_REQUIRED`.
  - `TestGate_TakeoverTokenUsesHeader`: as a token with a valid header code → takeover proceeds, and the body is not required.
  - `TestAudit_TokenLabel`: a token creates a project → the audit row metadata has `via=token` and `token=<name>`.
  - `TestAudit_ProxyRetry`: `POST /proxy/retry` writes an audit row with action `proxy.retry`.
- [ ] **Step 2:** `GO test ./internal/api/ -count=1` → the new tests FAIL.
- [ ] **Step 3:** Implement. The gate wraps exactly the routes listed in Global Constraints (`r.With(gate).Delete(...)`). In `confirmCode`, return true early when the ctx is marked as confirmed. Audit every gate refusal like `confirmCode` does (stage `code_required` / `invalid_code` / `rate_limited`, action `api_token.code`). While here, look for any other state-changing core route without an audit call and add one, following the `record()` pattern. List what you added in the commit body.
- [ ] **Step 4:** `GO test ./... -count=1` → PASS.
- [ ] **Step 5:** Commit `feat(mcp): autenticação por token, despacho interno e 2FA nas ações irreversíveis`.

### Task 5: Token management REST for the panel

**Files:**
- Create: `internal/api/tokens.go`
- Modify: `internal/api/server.go` (`mountSessionOnly`)
- Test: `internal/api/tokens_test.go`

**Interfaces:**
- Consumes: Task 2 (`Tokens.Issue`), Task 3 (`confirmCode`, `mountSessionOnly`), Task 4 (`tokenTransportOK`).
- Produces (cookie session + CSRF only):
  - `GET /api/tokens` → `{items: [{id, name, prefix, createdAt, lastUsedAt, lastUsedIp, expiresAt}]}`
  - `POST /api/tokens` with body `{name, expiresInDays: 0|30|90, code}` → 201 `{token: "kg_…", item: {…}}`
  - `DELETE /api/tokens/{id}` → 204

- [ ] **Step 1: Write the failing tests.**
  - `TestTokens_CreateListRevoke`: create with a valid code → 201 with the token. The list has the item and no secret or hash field. Revoke → 204, after which the list is empty and an audit row exists for `api_token.create` and for `api_token.revoke`, with the name and no secret.
  - `TestTokens_CreateNeedsCode`: missing code → `CODE_REQUIRED`, wrong code → `INVALID_CODE` (the login limiter applies; this is a human in the panel).
  - `TestTokens_CreateRefusedOverPlainHTTP`: from `203.0.113.5:1` without TLS → 403 `TOKEN_REQUIRES_HTTPS`, and the TOTP code is **not** consumed (the same code works on a following HTTPS request).
  - `TestTokens_Validation`: `expiresInDays: 7` → 400 `VALIDATION_ERROR`, empty name → 400, duplicate name → 409 `TOKEN_NAME_TAKEN`, revoking an unknown id → 404 `TOKEN_NOT_FOUND`.
  - `TestTokens_NotReachableByToken`: through `dispatcher.do` as a token, `GET /api/tokens` → 404.
- [ ] **Step 2:** `GO test ./internal/api/ -run Tokens -count=1` → FAIL.
- [ ] **Step 3:** Implement. Check transport first, then the code, then issue.
- [ ] **Step 4:** `GO test ./... -count=1` → PASS.
- [ ] **Step 5:** Commit `feat(mcp): criar, listar e revogar tokens pelo painel`.

### Task 6: MCP server at `/mcp` with every tool

**Files:**
- Create: `internal/api/mcp.go` (server, instructions, result mapping), `internal/api/mcp_tools.go` (tool table)
- Modify: `internal/api/server.go` (mount `/mcp` at the top-level router, outside `/api`), `go.mod`/`go.sum`
- Test: `internal/api/mcp_test.go`

**Interfaces:**
- Consumes: Task 4 (`RequireToken`, `dispatcher`, `tokenIdentity`), the REST input types (`projectInput`, `AppInput`, `envInput`, `domains.DomainInput`, `certInput`, `panelDomainInput`, `panelPortInput`, `rollbackInput`, the GitHub manifest body).
- Produces: `func newMCPHandler(d dispatcher, version string) http.Handler`, mounted as `r.With(RequireToken(...)).Handle("/mcp", h)`. Only when `d.Tokens != nil`.
- Wiring: `mcp.NewServer(&mcp.Implementation{Name: "korego", Version: version}, &mcp.ServerOptions{Instructions: mcpInstructions})` and `mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true})`. The identity reaches the tools through the SDK's bearer path: wrap the handler in the SDK `auth.RequireBearerToken` with a verifier that reads the `tokenIdentity` that `RequireToken` already put in the request context and returns `TokenInfo{UserID, Expiration, Extra: {"identity": id}}`. Tools read `req.Extra.TokenInfo`. If the SDK version propagates the request context to tool handlers, reading the identity from ctx is also fine; the test in Step 1 decides.
- Tool inputs: `{id?: int64, code?: string, body?: <REST input type>, …query fields}`, as one small struct per tool shape, generic where possible (`type withBody[T any] struct{ ID int64 \`json:"id,omitempty"\`; Code string \`json:"code,omitempty"\`; Body T \`json:"body"\` }`). Only `body` is sent as the request body, because `decodeJSON` rejects unknown fields. Gated tools declare `code` as required in the schema description. Non-gated tools have no `code` field.
- Result mapping: 2xx → text content with the JSON response (and `StructuredContent` when the SDK version supports it cleanly). Non-2xx → `IsError: true`, text `"<CODE> (HTTP n): <message>"`. On `CODE_REQUIRED`, append: "Peça ao usuário o código de 6 dígitos do aplicativo autenticador e chame de novo com `code`. Nunca invente nem reutilize um código." On `INVALID_CODE`, append: "Cada código vale uma vez; se o usuário acabou de usar este, peça o próximo."
- `mcpInstructions` (pt-BR, ≤ 25 lines): the publishing flow from spec §1 (github_repos → project/app create → app_env_set → deploy_start → deploy_wait/deploy_log → domain_create → domain_dns → proxy_status), the rule that DNS must point to the server before an `auto` certificate is issued, the 2FA rule, that env values are write-only, and that connecting the GitHub App is done in the panel.

Tool table (method, `/api` path, input, gate):

| Tool | Route | Input |
|---|---|---|
| `system_info` | GET /system | — |
| `metrics_current` / `metrics_history` | GET /metrics/current, /metrics/history | `range` |
| `usage_current` / `usage_history` / `usage_at` | GET /usage, /usage/history, /usage/at | query params as in `usage.go` |
| `pm2_list` | GET /pm2 | — |
| `audit_list` | GET /audit | `limit`, `before` |
| `container_list` / `container_get` / `container_logs` | GET /containers, /containers/{id}, /containers/{id}/logs | `id`, `tail` (default 200, max 1000) |
| `container_start` / `stop` / `restart` | POST /containers/{id}/… | `id` |
| `container_remove` | DELETE /containers/{id} | `id`, `code` (gate) |
| `github_status` / `github_repos` | GET /github, /github/repos | — |
| `github_disconnect` | DELETE /github | `code` (gate) |
| `project_list` / `project_create` / `project_update` | GET/POST /projects, PUT /projects/{id} | `projectInput` |
| `project_delete` | DELETE /projects/{id} | `code` (gate) |
| `app_list` / `app_get` / `app_create` / `app_update` | /apps… | `AppInput` |
| `app_delete` | DELETE /apps/{id} | `code` (gate) |
| `app_env_names` / `app_env_set` | GET/PUT /apps/{id}/env | `envInput` |
| `deploy_start` / `deploy_list` | POST/GET /apps/{id}/deploys | `id` (app) |
| `deploy_get` / `deploy_log` / `deploy_cancel` | /deploys/{id}… | `id`, `from` |
| `deploy_rollback` | POST /apps/{id}/rollback | `rollbackInput` |
| `deploy_wait` | polls GET /deploys/{id} every 2 s, then GET /deploys/{id}/log | `id`, `timeoutSeconds` (default 60, clamped to 1..120); returns `{status, timedOut, lastLines}` with the last 50 lines |
| `domain_list` / `domain_create` / `domain_update` / `domain_dns` | /domains… | `domains.DomainInput` (update is partial, as REST) |
| `domain_delete` | DELETE /domains/{id} | `code` (gate) |
| `proxy_status` / `proxy_retry` / `proxy_trial_check` | GET /proxy, POST /proxy/retry, GET /proxy/trial-check | — |
| `proxy_takeover` / `proxy_rollback` | POST /proxy/takeover, /proxy/rollback | `code` (gate) |
| `certificate_list` / `certificate_create` | GET/POST /certificates | `certInput` |
| `certificate_delete` | DELETE /certificates/{id} | `code` (gate) |
| `panel_settings` | GET /settings/panel | — |
| `panel_set_domain` / `panel_set_public_port` | PUT /settings/panel/… | `panelDomainInput` / `panelPortInput`, `code` (gate) |
| `update_status` / `update_apply` | GET/POST /update | `code` on apply (gate) |

Register a tool only when its route exists in the core (same `Deps` nil checks), so `tools/list` never offers a tool that would answer 404.

- [ ] **Step 1: Write the failing tests** in `mcp_test.go`. Use the SDK client (`mcp.NewClient` + `StreamableClientTransport`) against an `httptest.NewTLSServer` that serves `NewRouter`. Put the Bearer header in the client's `http.Client` transport, and use the existing fakes for docker, deploy runner and proxy from the other api tests.
  - `TestMCP_ListTools`: every tool from the table is listed (with the full deps). The `domain_create` input schema contains `body.hostname` and `body.routes`. `project_delete` has `code`, and `project_list` has no `code`.
  - `TestMCP_Unauthorized`: no token → HTTP 401 and the client fails to initialize.
  - `TestMCP_PublishScenario`: as a token: `project_create` → `app_create` (git URL source) → `app_env_set` → `deploy_start` → `deploy_wait` reaches a terminal status → `domain_create` → `domain_dns` → `proxy_status` lists the hostname. Every call has `IsError == false`. The audit rows for the writes carry `token=<name>`. `app_env_names` returns names, and no value appears anywhere in the responses.
  - `TestMCP_ErrorCarriesCode`: `domain_create` with an invalid route → `IsError`, and the text starts with the KoreGO code (e.g. `INVALID_PATH`).
  - `TestMCP_GatedReplayHint`: `project_delete` without `code` → text contains `CODE_REQUIRED` and "Peça ao usuário". With a valid TOTP → ok. A second `project_delete` reusing that TOTP → `INVALID_CODE` plus the "peça o próximo" hint.
  - `TestMCP_DeployWaitClampAndCancel`: `timeoutSeconds: 9999` is treated as 120 (assert via an injectable clock or sleep func in the tool, not real time). Cancelling the client context returns within 1 s.
  - `TestMCP_NoSessionOnlyTools`: no tool name contains `token` or `session`.
- [ ] **Step 2:** `GO test ./internal/api/ -run MCP -count=1` → FAIL.
- [ ] **Step 3:** `GO get github.com/modelcontextprotocol/go-sdk@v1.8.0` (or the latest stable v1, after checking its README for the exact `AddTool`, `StreamableHTTPOptions` and `auth.RequireBearerToken` signatures). Then implement.
- [ ] **Step 4:** `GO test ./... -count=1` → PASS. Also run `GO vet ./...` and `GO build ./cmd/korego`.
- [ ] **Step 5:** Commit `feat(mcp): servidor MCP em /mcp com as ferramentas do painel`.

### Task 7: Panel page, audit label and README

**Files:**
- Create: `web/src/app/(painel)/configuracoes/tokens/page.tsx`, `web/src/lib/tokens.ts`, a test under `web/src/app/__tests__/` or `web/src/lib/__tests__/`, following the existing tests
- Modify: `web/src/components/shell/sidebar.tsx` (add `{ label: "Tokens de API", href: "/configuracoes/tokens/" }` after "Segurança"), `web/src/app/(painel)/configuracoes/seguranca/page.tsx` (`auditDetail`), `README.md`

**Interfaces:**
- Consumes: Task 5 REST.

- [ ] **Step 1: Write the failing tests** (vitest + testing-library, as the existing ones):
  - The list renders name, prefix, last use ("nunca" when null) and expiry ("nunca" when null).
  - Create dialog: name, expiry select (90 dias by default, 30 dias, Nunca), 2FA code. On success it shows the token once with a copy button and the line `claude mcp add --transport http korego ${location.origin}/mcp --header "Authorization: Bearer <token>"`. Closing the dialog drops the token from memory, so reopening does not show it.
  - A `TOKEN_REQUIRES_HTTPS` error shows the API message and a link to `/configuracoes/painel/`.
  - Revoke asks for confirmation, then refetches.
  - `auditDetail` returns `token <name>` when `metadata.via === "token"`.
- [ ] **Step 2:** `cd web && pnpm test` → FAIL.
- [ ] **Step 3:** Load `frontend-design`, then build the page following `configuracoes/seguranca/page.tsx` (Panel, Button, query and mutation patterns). Finish with `web-design-guidelines`.
- [ ] **Step 4:** `cd web && pnpm test && pnpm lint && pnpm build` → PASS. Then `make web`, or the equivalent `node ../scripts/copy-web.mjs`, so the embedded dist is updated the way previous stages did.
- [ ] **Step 5:** README: a new section "Agentes (MCP)" with what it is, the requirement that the panel be on HTTPS (panel domain), how to create a token, the `claude mcp add` line, the list of 2FA-gated actions, what is panel-only (tokens, sessions, connecting the GitHub App), and how to revoke. Update the "Estado" paragraph.
- [ ] **Step 6:** Commit `feat(mcp): tela de tokens de API, rótulo na auditoria e README`.

### Task 8: End-to-end check against a real binary

**Files:** none new (or a script under `scripts/` if the e2e for Etapa 3 has a pattern there; follow it).

- [ ] **Step 1:** Build the binary (`GO build -o bin/korego ./cmd/korego`), then run it in a `golang:1.27` (or Debian) container with the data dir in a temp volume. Complete the setup through the API, and create a token through `POST /api/tokens` over loopback.
- [ ] **Step 2:** From the host, run `claude mcp add --transport http korego-e2e http://127.0.0.1:<port>/mcp --header "Authorization: Bearer …"` (loopback is allowed) and check that `claude mcp list` shows it connected. Call `system_info` and `project_create`, then `project_delete` without a code and confirm `CODE_REQUIRED` comes back.
- [ ] **Step 3:** Remove the e2e MCP entry (`claude mcp remove korego-e2e`). Write down the commands and results in the PR description.
