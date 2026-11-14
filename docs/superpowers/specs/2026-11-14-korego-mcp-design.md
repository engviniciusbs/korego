# KoreGO MCP: agents operate KoreGO (design spec)

- **Date:** 2026-11-14
- **Status:** approved in conversation, awaiting review of this document
- **Base:** KoreGO after Etapa 3 (`main` at 253ad43)

## 1. Intent

The user wants AI agents, typically **Claude Code running on their own computer**, to operate KoreGO end to end without opening the panel. The guiding scenario: inside a website project that is already on GitHub, the user says "put this on my server" and the agent:

1. finds the repository among those the GitHub App can see;
2. creates the app (project, deploy type, branch), sets env vars;
3. triggers a deploy and follows the log to the end; on failure it reads the log, fixes the code, pushes, and retries;
4. adds the domain, checks DNS, and confirms that HTTPS answered.

The user asked for "basically everything": the agent should be able to do anything the panel does, and read everything the panel shows.

### Success criteria
1. From a fresh Claude Code session, with one `claude mcp add` line, the agent completes the scenario above without the user touching the panel.
2. Every change the agent makes shows up in the audit log, attributed to the named token that did it (reads are not audited; see 3.4).
3. No irreversible action happens without the user typing a fresh 2FA code into the chat.
4. A token can be revoked from the panel and stops working on the next request.

### Goals
- MCP server **embedded in the KoreGO binary**, served at `/mcp`.
- **API tokens**: created in the panel, named, revocable, audited.
- Tools covering the panel's features (section 5).
- Irreversible actions require a fresh TOTP or recovery code passed by the agent (section 4).

### Non-goals
- Deploying from a local folder or uploading files. The site always comes from git (GitHub App or git URL), as today.
- Per-token permission scopes (read-only / publish / admin). One user, one level; the 2FA gate is the brake.
- Multiple users or roles.
- A separately installed client (`korego mcp` stdio, npm package).
- Live streams (WebSocket metrics, follow-mode logs). Agents poll.

### Anti-goals (failure even if it works)
- An agent can mint, see or extend tokens, or disable 2FA. Token and session management stay panel-only.
- A token travels over plain HTTP across the internet.
- MCP tools re-implement validation or business rules, so that the agent can do something the panel would reject (or the reverse).
- Secrets come back through the MCP: env var values, certificate private keys, TOTP secrets, GitHub App keys.

## 2. Approaches

How the MCP is built (decision by the user):

| | Approach | Trade-off |
|---|---|---|
| **A (chosen)** | Embedded in KoreGO, Streamable HTTP at `/mcp`, auth by `Authorization: Bearer` | Nothing to install on the laptop; versioned with the server so tools never drift from the API; one binary. Exposes `/mcp` on the internet like the panel already is, so it needs HTTPS. |
| B | `korego mcp` subcommand on the laptop (stdio), calling the REST API with the token | Works without HTTPS on the panel, but needs macOS/Windows builds, install and upgrades on every machine, and can lag behind the server. |
| C | Separate npm package (TypeScript) | Easy `npx` install, but another language, repo and release cycle in a single-binary Go project. |

How much power the agent gets (decision by the user):

| | Approach | Trade-off |
|---|---|---|
| 1 | Full-power token | Simplest; a confused agent can delete production with nothing in the way. |
| **2 (chosen)** | Full power, but irreversible actions require a fresh 2FA code the agent asks the user for | "Everything" stays available; the brake sits only where mistakes can't be undone; reuses the existing `confirmCode` mechanism (`internal/api/proxy_migration.go:97`). |
| 3 | Per-token scopes | More flexible, but more to build and configure; overkill for one user and one agent today. |

Connection, as the user will type it:

```
claude mcp add --transport http korego https://painel.exemplo.com/mcp \
  --header "Authorization: Bearer kg_..."
```

## 3. API tokens

### 3.1 Model
New table (migration `0006_api_tokens.sql`):

- `id`, `user_id` (FK users, cascade), `name` (1..64 chars, unique among non-revoked tokens), `token_hash` (SHA-256 of the secret, unique), `prefix` (first chars shown in the panel to tell tokens apart), `created_at`, `last_used_at`, `last_used_ip`, `expires_at` (nullable = never), `revoked_at` (nullable).
- Token format: `kg_` + 32 random bytes, base64url. Shown **once** at creation, never stored in clear.

### 3.2 Panel
New section **Configurações → Tokens de API**:
- List: name, prefix, created, last used (time and IP), expires, and a revoke button.
- **Criar token**: name, expiry (never / 30 / 90 days; default 90), and the user's **current 2FA code** (creating a token is as sensitive as logging in). The result screen shows the token once, plus the ready-to-copy `claude mcp add ...` line built from the panel's public URL.
- Revoke takes effect on the next request. Revoking is audited.

### 3.3 Authentication
- New middleware `RequireToken`: reads `Authorization: Bearer kg_...`, looks up the hash, rejects revoked or expired tokens, and puts the token's user in the context **the same way `RequireSession` does**, plus the token itself (id and name) so handlers and audit can see it.
- **Tokens are accepted only on `/mcp`** (builder's call, settled in review). `/api/*` keeps `RequireSession` + CSRF exactly as today; MCP tools reach the handlers in-process (5.2) with the token's identity in the context, so no route needs to accept Bearer from outside and no router regrouping is needed. Token management, sessions, logout, setup and login are simply not exposed as tools.
- **HTTPS only**: a Bearer token is accepted only when `requestIsHTTPS` is true (it already trusts `X-Forwarded-Proto` only from a loopback peer, i.e. KoreGO's own Caddy), **or** the peer is loopback and the request carries no `X-Forwarded-*` headers (an agent on the server itself, or an SSH tunnel). A request proxied by Caddy over plain HTTP is refused. Otherwise the answer is 403 `TOKEN_REQUIRES_HTTPS`, and the message tells the user to set up the panel domain.
- The panel refuses to **create** a token when the panel itself is being used over plain HTTP from a non-loopback peer (the secret would cross the internet in clear); it shows the same hint to set up the panel domain.
- Failed token lookups and wrong 2FA codes on token requests use a **separate limiter key space** (e.g. `token:<ip>`), not the login one, so an agent stuck on a stale token cannot lock the user out of panel login from the same IP.
- `last_used_at`/`last_used_ip` are updated at most once a minute per token, to keep writes low.

### 3.4 Audit
Scope: the actions the panel already audits, plus any **state-changing** route that is not audited today (e.g. `POST /proxy/retry`), which gains an audit entry. Reads are not audited; `last_used_at`/`last_used_ip` show token activity. The shared audit helpers (`record()` in `domains.go`, `containers.audit`, `proxy_migration`, `update.go`, and the others that read `CurrentSession`) learn to read the token from the context.

Every audited action from a token request records `{"via":"token","token":"<name>","tokenId":N}` in metadata, so the audit page can show "Claude Code do notebook" instead of only the user. The panel's audit list shows this label.

## 4. 2FA-gated actions

For **token** requests, these actions require a `code` (fresh TOTP or recovery code), validated with the existing `auth.Service.VerifyCode`, rate limiter and last-step replay protection, exactly like `confirmCode` does today:

| Action | Why |
|---|---|
| Delete app, project, domain, certificate | Irreversible; takes a site down |
| Remove container | Irreversible |
| Proxy takeover / rollback | Already gated today, for every caller |
| Apply KoreGO update | Restarts the server |
| Change the panel domain or public port | Can lock the user (and the agent) out |
| Disconnect GitHub | Breaks every auto-deploy |

Panel (cookie) requests keep today's behaviour: they rely on the confirmation dialog, except takeover/rollback, which already require a code.

Flow seen by the agent: the tool is called without `code` → the result is an error `CODE_REQUIRED` with a message telling the agent to ask the user for the 6-digit code of their authenticator app, and that it must not invent or reuse one → the agent asks, the user types it, the agent calls again with `code`. A wrong code returns the existing `INVALID_CODE`, and the token limiter (3.3) applies.

Not gated (reversible or routine): create/update anything, deploy, cancel deploy, rollback to the previous image, set env vars, start/stop/restart containers, proxy retry.

## 5. MCP server

### 5.1 Transport and SDK
- The official Go SDK (`github.com/modelcontextprotocol/go-sdk`), Streamable HTTP transport, mounted at `/mcp` on the same listener and router as the API, behind `RequireToken`. Stateless mode is preferred if the SDK supports it, since there are no subscriptions.
- `/mcp` accepts **only** Bearer tokens, never the session cookie.
- Server `instructions` (sent at initialize) give agents a short guide in pt-BR: the publishing flow of section 1, that DNS must point to the server before an automatic certificate is issued, how the 2FA gate works, and that env var values are write-only.

### 5.2 How tools run
Each tool is a thin adapter that **calls the existing REST handler in-process**: it builds an `http.Request` for the matching route with the authenticated context (user and token), serves it through the API router, and turns the JSON response, or `{code, message}` error, into the tool result. This keeps validation, audit, rate limits and the 2FA gate in one place. Error results carry the KoreGO error `code` and its pt-BR message, so the agent can act on them.

### 5.3 Tools
Names in English snake_case, descriptions in pt-BR. Inputs mirror the REST bodies. `(2FA)` marks the `code` parameter of section 4.

**Overview and reading**
- `system_info`: version, host, uptime, Docker/PM2 availability.
- `metrics_current`, `metrics_history(range: 1h|24h|7d)`.
- `usage_current`, `usage_history(owner, range)`, `usage_at(time)`.
- `pm2_list`.
- `audit_list(filters as the REST route)`.

**Containers**
- `container_list`, `container_get(id)`, `container_logs(id, tail, since)`: snapshot only, capped.
- `container_start|stop|restart(id)`, `container_remove(id, code)` (2FA).

**GitHub**
- `github_status`, `github_repos`.
- `github_disconnect(code)` (2FA). Connecting the GitHub App stays in the panel: it is a browser flow.

**Projects and apps**
- `project_list|create|update`, `project_delete(id, code)` (2FA).
- `app_list|get|create|update`, `app_delete(id, code)` (2FA).
- `app_env_names(id)`, `app_env_set(id, vars)`: values are write-only, exactly as in REST.

**Deploys**
- `deploy_start(appId)`, `deploy_list(appId)`, `deploy_get(id)`, `deploy_cancel(id)`, `deploy_rollback(appId, deployId)` (mirrors the REST body).
- `deploy_log(id, from)`: same paging as `GET /deploys/{id}/log`.
- `deploy_wait(id, timeoutSeconds ≤ 120)`: blocks until the deploy reaches a terminal status or the timeout, then returns the status plus the last log lines. It exists so the agent does not have to loop on `deploy_get`.

**Domains and proxy**
- `domain_list`, `domain_create`, `domain_update` (partial, like the REST PUT), `domain_delete(id, code)` (2FA).
- `domain_dns(id)`: the existing DNS check.
- `proxy_status`: includes per-domain certificate and trial results; this is how the agent confirms "HTTPS answered".
- `proxy_retry`, `proxy_trial_check`, `proxy_takeover(code)` and `proxy_rollback(code)` (2FA).

**Certificates**
- `certificate_list`, `certificate_create(pem | path)`, `certificate_delete(id, code)` (2FA). The private key never comes back.

**Panel settings and update**
- `panel_settings`, `panel_set_domain(…, code)` and `panel_set_public_port(…, code)` (2FA).
- `update_status`, `update_apply(code)` (2FA).

Deliberately absent: token management, sessions, logout, setup, login, WebSocket, the log download.

## 6. Testing
- Token store: create, hash lookup, expiry, revoke, unique name among active tokens.
- `RequireToken`: valid / revoked / expired / malformed / plain HTTP from non-loopback → 403 / Caddy-proxied plain HTTP → 403 / direct loopback HTTP ok; `/api/*` rejects Bearer (unchanged behaviour).
- Wrong tokens and wrong agent codes do not count against panel login from the same IP.
- 2FA gate: each gated route without `code` → `CODE_REQUIRED`; wrong → `CODE_INVALID` plus limiter; right → proceeds; replayed code rejected; cookie requests unaffected (except takeover/rollback, as today).
- Audit metadata carries the token name.
- MCP: an in-process client (SDK client against an `httptest` server) lists the tools, runs the scenario of section 1 against the existing fakes (docker, deploy runner, proxy), and checks that error results carry the KoreGO code.
- Panel: token page (create with code, shown once, revoke), audit label.

## 7. Calls settled after review (not discussed with the user)
- Tokens only on `/mcp`, not on `/api/*`.
- Loopback exception limited to direct, unproxied requests; token creation refused over plain public HTTP.
- Audit covers changes, not reads.
- Separate limiter key space for token auth and agent 2FA codes.

## 8. Left to the builder
- Exact SDK version and the stateless / stateful choice.
- JSON schemas of the tool inputs, derived from the REST DTOs.
- Exact log caps for `container_logs` and `deploy_wait`.
- README section ("Agentes (MCP)") with the setup steps and the list of 2FA-gated actions.
