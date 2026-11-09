# KoreGO Etapa 3 (advanced proxy and nginx migration) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:**
- A domain gets path routes, static folders, custom certificates, a "Cloudflare only" mode, response headers and compression.
- Caddy runs in a trial mode while another server holds 80/443.
- The user can take the ports over from nginx with automatic rollback, and later return to nginx.

**Architecture:**
- `node/proxy` keeps owning the Caddy JSON and the container. `BuildConfig` takes `[]Site` plus a `Listen` mode instead of `[]Route`.
- `domains` turns DB rows into sites and reconciles them.
- A new `certs` package validates and stores PEMs, which are encrypted with the existing `secrets.Box`.
- A new `cloudflare` package keeps the IP ranges up to date.
- A new `migrate` package runs takeover and rollback through a `ServiceManager` interface (systemd in production).
- The frontend extends the existing domain dialog and proxy banner.

**Tech Stack:**
- Go 1.27 with CGO off, chi, modernc sqlite, the Caddy 2.11 JSON config, the Docker SDK, testify.
- Next.js static export, TanStack Query, Vitest, Playwright.

**Spec:** `docs/superpowers/specs/2026-11-09-korego-etapa-3-proxy-design.md`

## Global Constraints

- Comments, error messages and UI copy are in Portuguese. Docs, specs and plans contain no real domains or IPs; use `exemplo.com`, `loja.exemplo.net` and `203.0.113.10`.
- Routes: 1..20 per domain. `path` is `/` or a prefix that starts and ends with `/` (`[a-z0-9._~-]` segments). The `/` route is required. Duplicates are rejected. Matching uses `path*`, longest prefix first, with `/` last.
- Static `root` must be absolute and, after `filepath.Clean`, equal `/var/www` or start with `/var/www/`. Caddy sees it through a `/var/www:/var/www:ro` bind mount.
- With `spa`, `try_files` is `{path}`, `/index.html`. `/index.html`, including the fallback, gets `Cache-Control: no-cache`. `immutablePrefix` gets `Cache-Control: public, max-age=31536000, immutable`.
- `maxBodyBytes` 0 means unlimited; above the limit the response is 413 (`request_body.max_size`). `noBuffering` means `flush_interval: -1`.
- Headers: at most 30 per domain. The name must match `^[A-Za-z0-9!#$%&'*+.^_`|~-]+$`. The value must not contain `\r` or `\n`.
  - The security preset is exactly:
    - `Strict-Transport-Security: max-age=31536000`
    - `X-Content-Type-Options: nosniff`
    - `Referrer-Policy: strict-origin-when-cross-origin`
    - `Permissions-Policy: camera=(), microphone=(), geolocation=()`
- Always: remove the `Server` response header, and send `X-Real-IP: {http.request.remote.host}` upstream for `app` and `address` targets. When `cloudflareOnly` is set, send `{client_ip}` instead.
- Compression (`encode` with zstd and gzip): default on for new domains, off for migrated rows.
- `cloudflareOnly`:
  - A peer outside the CF ranges gets `403` on `:80` and `:443`.
  - Server `trusted_proxies` is static with the CF ranges, and `client_ip_headers` is `["CF-Connecting-IP"]`.
  - In trial mode `127.0.0.1/32` and `::1/128` are also allowed.
- CF ranges: embedded fallback, refreshed every 24 h from `https://www.cloudflare.com/ips-v4` and `https://www.cloudflare.com/ips-v6`.
  - A fetched list is accepted only if it has at least 10 valid IPv4 CIDRs and at least 1 valid IPv6 CIDR.
  - The last good list is persisted in setting `cloudflare.ranges` (JSON).
- Custom certificates:
  - Reject when the chain does not parse, when the key does not match the leaf, when the leaf does not cover every hostname of the domain (including the `www` pair), or when it is expired.
  - The full PEM is stored encrypted in `certificates.pem_enc` and sent inline via `tls.certificates.load_pem`.
  - The key is never returned by any API.
  - The panel shows an expiry warning 30 days before `notAfter`.
- Listen modes:
  - `normal` = `:80` and `:443`.
  - `trial` = `127.0.0.1:18080` and `127.0.0.1:18443`.
  - Proxy state `trial` replaces `blocked` whenever another process holds 80/443.
- Takeover and rollback:
  - Supported port owners are the systemd units `nginx` and `apache2` only.
  - Both require a fresh TOTP or recovery code in the request body (`code`).
  - Bind retry lasts up to 5 s. `auto` certificates get up to 60 s to issue during verification.
  - Settings: `proxy.migrated_from` and `proxy.migrated_at` (unix).
- Error codes:
  - 400: `INVALID_ROUTE`, `INVALID_PATH`, `INVALID_ROOT`, `INVALID_HEADER`, `INVALID_CERT`, `CERT_MISMATCH`, `CERT_EXPIRED`, `CERT_IN_USE`.
  - 409: `PROXY_NOT_TRIAL`, `UNSUPPORTED_PORT_OWNER`.
  - 403: `CODE_REQUIRED` and `INVALID_CODE`.
- Every create, update or delete of a domain or certificate, plus every takeover and rollback, is audited.
- UI: dark theme only, no rounded corners. Use the `frontend-design` skill before building and `web-design-guidelines` at the end.
- Env (Windows dev): Go is at `D:\sdk\go`; prefix bash commands with `export PATH="/d/sdk/go/bin:/d/go/bin:$PATH"`. There is no `make`. Linux-only checks run in a `golang:1.27` container or in CI.

## Decisions

- **D1.** `proxy.Apply(ctx, sites, mode)` and `InSync(ctx, sites, mode)` replace the route-based signatures, and `proxy.Route` is deleted. `Ensure` returns `Status{State, Mode}`: `StateTrial` when the ports are foreign, or `StateActive` with `Mode=normal`.
- **D2.** The container spec version is the label `korego.caddy-spec=2`.
  - `Ensure` recreates the container when the label is missing or lower. It stops and removes the container, then runs it again with the same volumes and the new bind.
  - It recreates only when the state is not normal-active, meaning Caddy is not serving 80/443. Otherwise it logs "recriação pendente" and retries on a later tick.
- **D3.** Takeover progress is published on hub topic `proxy-migration` as `{step, status: running|ok|failed, message}`. The final event has `done: true` and `result: ok|rolled_back|failed`. `GET /api/proxy` also returns the last result.
- **D4.** `auth.Service.VerifyCode(ctx, userID, code) (bool, error)` is exported and wraps `checkCode`. API handlers get the user from the session context, as other handlers do.
- **D5.** Trial check and post-takeover verification use one function, `migrate.Check(ctx, sites, addr)`. For each site and route path, it does a GET over TLS to `addr` with SNI = host and `InsecureSkipVerify` (it reports the leaf instead).
  - **Pass:** the handshake succeeds and the status is below 500.
  - **Exceptions:** an `auto` site in trial is "pending" and does not fail. A `cloudflareOnly` site is checked from loopback, which trial allows.
  - For static routes the request path is `/`.

## Review Focus

1. **A deploy swaps the `current` symlink while Caddy serves.** Requests keep working, and new files are served without reloading Caddy. Test: Task 10 integration swaps the symlink and asserts the new content.
2. **A takeover where nginx stops but Caddy fails to bind**, for example because another process grabbed 443. nginx must be running again, Caddy must be back in trial, and the result must be `rolled_back`. Test: Task 7 unit, bind failure.
3. **Malicious or sloppy input in the editor.** Cases: `root` with `..` or `/var/www-evil`, a header value with CRLF, a path like `/api` without the trailing slash or `//`, a pasted PEM containing only the cert or only the key. Each gets a clear 400 with the field code, and nothing is applied. Tests: Task 5 and Task 6 validation tables.
4. **An existing v0.4.0 install upgrades.** The domains keep serving with no manual step, and the panel domain still works. Test: Task 1 migration test and Task 2 legacy golden.
5. **The CF ranges refresh returns an HTML error page or a truncated list.** The old list stays, so legitimate traffic is not 403'd. Test: Task 4.

---

### Task 1: Store — certificates, routes and domain options

**Files:**
- Create: `internal/store/migrations/0005_proxy.sql`, `internal/store/certificates.go`, `internal/store/certificates_test.go`
- Modify: `internal/store/domains.go` (+ test), `internal/store/migrate_test.go` (version 5)

**Interfaces:**
- Produces:
  - `store.Domain` gains:
    - `TLSMode string` (`auto|custom`)
    - `CertID *int64`
    - `CloudflareOnly bool`
    - `Headers []Header` (`Header{Name, Value string}`, stored as JSON in `headers_json`)
    - `Compression bool`
    - `Routes []DomainRoute`
  - `DomainRoute{ID, DomainID int64; Position int; Path, TargetType string; AppID *int64; Address string; MaxBodyBytes int64; NoBuffering bool; Root string; SPA bool; ImmutablePrefix string}`. Target types: `app|address|static`.
  - `CreateDomain` and `UpdateDomain` write the domain and replace its routes in one transaction. `ListDomains` and `DomainByID` load the routes ordered by `position`.
  - `Certificate{ID int64; Name, Issuer string; Names []string; NotAfter time.Time; Fingerprint, PEMEnc string; CreatedAt time.Time}`, with methods:
    - `CreateCertificate(ctx, Certificate) (int64, error)`
    - `ListCertificates(ctx) ([]Certificate, error)`
    - `CertificateByID(ctx, id)`
    - `DeleteCertificate(ctx, id)`: returns `ErrCertInUse` when any domain references it.
    - `DomainsUsingCert(ctx, id) ([]string, error)`
- DDL (0005):
  - `certificates(id PK, name TEXT NOT NULL, issuer, names_json, not_after INTEGER, fingerprint TEXT UNIQUE, pem_enc TEXT NOT NULL, created_at)`
  - `domain_routes(id PK, domain_id REFERENCES domains(id) ON DELETE CASCADE, position, path, target_type, app_id REFERENCES apps(id), address DEFAULT '', max_body_bytes DEFAULT 0, no_buffering DEFAULT 0, root DEFAULT '', spa DEFAULT 0, immutable_prefix DEFAULT '', UNIQUE(domain_id, path))`
  - `ALTER TABLE domains ADD` `tls_mode TEXT NOT NULL DEFAULT 'auto'`, `cert_id INTEGER REFERENCES certificates(id)`, `cloudflare_only INTEGER NOT NULL DEFAULT 0`, `headers_json TEXT NOT NULL DEFAULT '[]'`, `compression INTEGER NOT NULL DEFAULT 0`
  - Then `INSERT INTO domain_routes(domain_id, position, path, target_type, app_id, address) SELECT id, 0, '/', target_type, app_id, address FROM domains`. The old `target_type`, `app_id` and `address` columns stay, unused after this; drop them in a later version.

- [ ] **Step 1: Tests**
  - `TestMigrate_0005_ConvertsDomainsToRootRoute`: run migrations up to 4, insert one app domain and one address domain, run 5, then assert each has exactly one route `/` with the same target and `compression=false`.
  - `TestDomains_RoutesRoundTripOrdered`
  - `TestDomains_UpdateReplacesRoutes`
  - `TestDomains_DeleteCascadesRoutes`
  - `TestCertificates_CRUDAndInUse`: deleting a certificate that is referenced returns `ErrCertInUse`, and `DomainsUsingCert` lists the hostnames.
- [ ] **Step 2:** Run `go test ./internal/store/... -count=1` and confirm it FAILS.
- [ ] **Step 3:** Implement the migration and the store methods.
- [ ] **Step 4:** Run `go test ./internal/store/... -count=1` and confirm it PASSES. `go vet ./...` must be clean.
- [ ] **Step 5:** Commit: `feat(etapa-3): rotas, certificados e opções de domínio no banco`

### Task 2: Proxy config — sites, path routes, address options, headers, compression

**Files:**
- Modify: `internal/node/proxy/proxy.go`, `caddyconfig.go`, `caddyconfig_test.go`; `testdata/` goldens
- Modify (callers, compile only): `internal/domains/routes.go`, `reconcile.go`, `internal/node/proxy/proxytest` fake
  - Callers keep the current behaviour by building a single-`/`-route `Site` per domain until Task 6.

**Interfaces:**
- Produces in `proxy`:
  ```go
  type Listen string // "normal" | "trial"
  const (ListenNormal Listen = "normal"; ListenTrial Listen = "trial")
  type Site struct {
      Host, WWWRedirect string; ForceHTTPS bool
      Routes []SiteRoute
      TLS SiteTLS            // Mode "auto"|"custom"; CertPEM string (chain+key, only when custom)
      CloudflareOnly bool
      Headers []Header       // Header{Name, Value string}
      Compression bool
  }
  type SiteRoute struct {
      Path string            // "/" or "/x/"
      Upstream string        // set for app/address targets
      MaxBodyBytes int64; NoBuffering bool
      Static *StaticTarget   // set for static targets
  }
  type StaticTarget struct{ Root string; SPA bool; ImmutablePrefix string }
  func BuildConfig(sites []Site, mode Listen, cf []string) ([]byte, error) // cf = CF CIDRs
  ```
  `Proxy.Apply(ctx, sites, mode)` and `InSync(ctx, sites, mode)` per D1. `Route` is removed.
- In this task `Static`, the custom TLS mode, `CloudflareOnly`, `cf` and `ListenTrial` may return `fmt.Errorf("não suportado ainda")`. Task 3 adds them.

- [ ] **Step 1: Tests**
  - `TestBuildConfig_LegacySingleRouteGolden`: the v0.4.0 golden inputs as sites. The only diffs from the old goldens are `Server` removal (`headers` handler with `response.delete: ["Server"]`) and `X-Real-IP` in `header_up`. Update the goldens.
  - `TestBuildConfig_PathRoutesLongestFirst`: given `/`, `/api/` and `/api/v2/` in any order, the subroutes are ordered `/api/v2/*`, `/api/*`, `/`.
  - `TestBuildConfig_AddressOptions`: `maxBodyBytes=1048576` produces a `request_body` handler with `max_size: 1048576` before `reverse_proxy`. `noBuffering` produces `flush_interval: -1`.
  - `TestBuildConfig_HeadersAndCompression`: domain headers produce a `headers` handler with `response.set`. Compression produces `encode` with zstd and gzip.
  - `TestBuildConfig_WWWRedirectStillWorks`
  - `TestBuildConfig_Deterministic`
- [ ] **Step 2:** Run `go test ./internal/node/proxy/... -count=1` and confirm it FAILS.
- [ ] **Step 3:** Implement. Each host route holds a `subroute` handler: headers and encode first, then the path routes, each with `match.path`.
- [ ] **Step 4:** Run `go test ./... -count=1` and confirm it PASSES. The domains tests must still pass through the compile shim.
- [ ] **Step 5:** Commit: `feat(etapa-3): sites com rotas por caminho, cabeçalhos e compressão no Caddy`

### Task 3: Proxy config — static folders, custom TLS, Cloudflare-only, trial listen

**Files:**
- Modify: `internal/node/proxy/caddyconfig.go`, `caddyconfig_test.go`, `testdata/`

**Interfaces:**
- Consumes: Task 2 types.
- Produces: `BuildConfig` handles every field of `Site`, plus `ListenTrial` and `cf`.

- [ ] **Step 1: Tests.** Goldens use example names only.
  - `TestBuildConfig_StaticSPAGolden`, modelled on `app.exemplo.com`:
    - `file_server` with `root: /var/www/app/current`;
    - `rewrite` via `try_files` `{path}` and `/index.html`;
    - `Cache-Control: no-cache` on `/index.html`, including the fallback;
    - immutable cache on `/assets/*`;
    - CSP header.
  - `TestBuildConfig_CustomTLS`:
    - `apps.tls.certificates.load_pem` has `{certificate, key}` split from `CertPEM`;
    - the host is excluded from automation policies;
    - two sites sharing one PEM produce a single entry.
  - `TestBuildConfig_CloudflareOnly`:
    - on both servers, the first route for the host is a `remote_ip` not-in-ranges matcher answering `static_response` 403;
    - the servers have `trusted_proxies` (static, CF ranges) and `client_ip_headers: ["CF-Connecting-IP"]`;
    - `X-Real-IP` uses `{http.vars.client_ip}`.
  - `TestBuildConfig_TrialListen`:
    - the listen addresses are `127.0.0.1:18080` and `127.0.0.1:18443`;
    - the CF allow list also includes `127.0.0.1/32` and `::1/128`;
    - `auto` hosts keep their automation policy.
  - `TestBuildConfig_RejectsBadInput`: a static root outside `/var/www`, a custom TLS PEM with no key, an empty `cf` when `CloudflareOnly` is set.
- [ ] **Step 2:** Confirm the tests FAIL.
- [ ] **Step 3:** Implement. The exact Caddy JSON shapes come from the Caddy 2.11 JSON docs: `http.handlers.file_server`, `rewrite`, `headers`, `request_body`, `tls.certificates.load_pem`, `servers.trusted_proxies` with the `static` source, `client_ip_headers`, and the `remote_ip` matcher. Cite the docs in comments, as the file already does.
- [ ] **Step 4:** Run `go test ./internal/node/proxy/... -count=1` and confirm it PASSES. Then validate one generated config against a real Caddy:
  `docker run --rm -v <tmp>/c.json:/c.json caddy:2.11.9-alpine caddy validate --config /c.json`
  It must print `Valid configuration`. Use a self-signed PEM generated in the test.
- [ ] **Step 5:** Commit: `feat(etapa-3): estático, certificado próprio, só Cloudflare e modo ensaio no Caddy`

### Task 4: Caddy container v2 and trial state; Cloudflare ranges

**Files:**
- Create: `internal/cloudflare/ranges.go`, `ranges_test.go`, `ranges_default.go` (embedded lists)
- Modify: `internal/node/proxy/caddy.go` (+ test), `internal/node/docker/docker.go` and `run.go` (`RunSpec.Binds []BindMount{Source, Target string; ReadOnly bool}`, and `Container.Labels map[string]string` if missing), the `dockertest` fake

**Interfaces:**
- Produces:
  - `cloudflare.Ranges` with `New(db *store.DB, client *http.Client)`, `Get() []string` (v4 and v6), and `Run(ctx)` (refresh now, then every 24 h).
  - `proxy.Status.Mode Listen`, and `StateTrial`.
  - `Caddy.Ensure`:
    - ports held by a foreign process → container running and state `trial`, with `Process` set;
    - ports free or held by Caddy → `active` with mode normal;
    - label `korego.caddy-spec=2`, with recreation per D2;
    - bind `/var/www:/var/www:ro`, creating `/var/www` on the host with mode 0755 if it is missing.

- [ ] **Step 1: Tests**
  - `TestRanges_Refresh`, a table using an `httptest` server:
    - a good list is accepted and persisted;
    - an HTML body is ignored;
    - 5 IPv4 CIDRs are ignored;
    - a 500 is ignored;
    - on startup with a stored list, the stored list is used;
    - with no store, the embedded list is used.
  - `TestEnsure_ForeignPortsGivesTrial`
  - `TestEnsure_RecreatesOldSpecWhenNotServing`
  - `TestEnsure_DoesNotRecreateWhileServingNormal`
  - `TestEnsure_RunSpecHasWwwBindAndLabel`
- [ ] **Step 2:** Run the tests and confirm they FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test ./... -count=1` and confirm it PASSES.
- [ ] **Step 5:** Commit: `feat(etapa-3): container do Caddy com /var/www, estado de ensaio e faixas da Cloudflare`

### Task 5: Certificates service and API

**Files:**
- Create: `internal/certs/certs.go`, `certs_test.go`, `internal/api/certificates.go`, `certificates_test.go`
- Modify: `internal/api/server.go` (routes, `Deps.Certs`), `cmd/korego/main.go`

**Interfaces:**
- Consumes: Task 1 store, `secrets.Box`.
- Produces:
  - `certs.Parse(pem []byte, now time.Time) (Info, error)`, where `Info{Issuer string; Names []string; NotAfter time.Time; Fingerprint string}`. Errors are `ErrInvalidCert`, `ErrKeyMismatch` and `ErrExpired`.
  - `certs.Covers(names []string, host string) bool`, with wildcard support for one label.
  - `certs.Service{DB, Box, Now}` with:
    - `Add(ctx, name string, pem []byte) (store.Certificate, error)`
    - `AddFromPath(ctx, name, path string)`: reads the file, max 1 MB.
    - `List(ctx) ([]View, error)`, where `View` has `usedBy`.
    - `Delete(ctx, id)`
    - `PEM(ctx, id) (string, error)`: decrypts, for the domains service only.
  - API:
    - `GET /api/certificates`
    - `POST /api/certificates` with body `{name, pem}` or `{name, path}`
    - `DELETE /api/certificates/{id}`
  - JSON: `{id, name, issuer, names, notAfter, fingerprint, usedBy, expiresSoon}`, where `expiresSoon` means `notAfter` is within 30 days.

- [ ] **Step 1: Tests.** Generate self-signed certificates in a test helper.
  - `TestParse`, a table: valid chain and key; key from another certificate → `ErrKeyMismatch`; only the certificate → `ErrInvalidCert`; only the key → `ErrInvalidCert`; garbage → `ErrInvalidCert`; expired → `ErrExpired`; a wildcard with `Covers("a.exemplo.com")` true and `Covers("a.b.exemplo.com")` false.
  - `TestAPI_CertificateNeverReturnsKey`: in every response, assert that `PRIVATE KEY` does not appear anywhere in the JSON.
  - `TestAPI_DeleteInUse409`: expects 400 `CERT_IN_USE`.
  - `TestAPI_AddFromPath`
  - Assert that every write is audited.
- [ ] **Step 2:** Run the tests and confirm they FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test ./... -count=1` and confirm it PASSES.
- [ ] **Step 5:** Commit: `feat(etapa-3): certificados próprios cifrados com API`

### Task 6: Domains service — new input, validation, sites, reconcile with mode

**Files:**
- Modify: `internal/domains/domains.go`, `validate.go`, `routes.go` (rename to `sites.go`), `reconcile.go`, `panel.go`, tests; `internal/api/domains.go` (+ test); `cmd/korego/main.go`

**Interfaces:**
- Consumes:
  - Task 1 store
  - Tasks 2 and 3 `proxy.Site`
  - Task 4 `proxy.Status.Mode` and `cloudflare.Ranges`
  - Task 5 `certs.Service.PEM` and `certs.Covers`
- Produces:
  - `DomainInput` adds `Routes []RouteInput`, `TLS{Mode string; CertID *int64}`, `CloudflareOnly bool`, `Headers []store.Header`, `Compression *bool` (nil means the default, true).
    - Old-style input with no `Routes` maps to one `/` route built from `TargetType`, `AppID` and `Address`.
  - `Service.Sites(ctx) ([]proxy.Site, error)` replaces `Routes`. It keeps the current rules: the panel first, bad rows skipped with a log line, and app routes without a port skipped. A custom certificate that fails to decrypt skips that domain.
  - `Reconcile`:
    - applies the sites with `st.Mode`;
    - in `trial`, also applies (in trial mode) instead of returning early;
    - passes `Ranges.Get()` to the proxy.
    - `Service` gains the fields `Certs *certs.Service` and `Ranges interface{ Get() []string }`.
  - `DomainView` adds `routes`, `tls` (`{mode, certId, certName, notAfter, expiresSoon}`), `cloudflareOnly`, `headers` and `compression`.
  - The panel domain (`is_panel`) accepts `tls`, `cloudflareOnly`, `headers` and `compression` via `PUT /api/domains/{id}`. Its routes stay fixed: one `/` route to the panel port, and a `routes` field sent for the panel domain is rejected with `INVALID_ROUTE`. Test: `TestPanelDomain_CustomCertAndCloudflareOnly`.

- [ ] **Step 1: Tests**
  - `TestValidate_Routes`, a table with these cases and expected codes:
    - no `/` → `INVALID_ROUTE`;
    - `/api` → `INVALID_PATH`;
    - `//` → `INVALID_PATH`;
    - a duplicate path → `INVALID_ROUTE`;
    - 21 routes → `INVALID_ROUTE`;
    - root `/var/www/../etc` → `INVALID_ROOT`;
    - root `/var/www-evil` → `INVALID_ROOT`;
    - root `relative` → `INVALID_ROOT`;
    - a header value with `\r\n` → `INVALID_HEADER`;
    - a header name with a space → `INVALID_HEADER`;
    - 31 headers → `INVALID_HEADER`;
    - custom TLS without a certificate → `INVALID_CERT`;
    - a certificate that does not cover the `www` pair → `CERT_MISMATCH`.
  - `TestSites_OldStyleInputIsSingleRoute`
  - `TestSites_CustomCertIncludedAndBadCertSkipped`
  - `TestReconcile_TrialAppliesTrialMode`
  - `TestAPI_DomainCreateWithRoutesAndAudit`
  - `TestAPI_OldStyleBodyStillWorks`
- [ ] **Step 2:** Run the tests and confirm they FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test ./... -count=1` and confirm it PASSES. Also run `go test -race ./internal/domains/... -count=1` in the Linux container.
- [ ] **Step 5:** Commit: `feat(etapa-3): domínios com rotas, certificado, só Cloudflare e cabeçalhos`

### Task 7: Migration — trial check, takeover, rollback

**Files:**
- Create: `internal/migrate/migrate.go`, `check.go`, `systemd.go` (Linux) with a stub for other platforms, and tests; `internal/api/proxy_migration.go` (+ test)
- Modify: `internal/auth/service.go` (`VerifyCode`, D4), `internal/api/server.go`, `internal/api/domains.go` (`GET /api/proxy` adds `mode`, `migratedFrom`, `migratedAt` and `lastMigration`), `cmd/korego/main.go`

**Interfaces:**
- Consumes: the domains service (`Sites`, `Reconcile`, `Status`), the proxy (`Apply` with mode, `Ensure`), the store settings, and the hub.
- Produces:
  - `migrate.ServiceManager` interface: `Stop(ctx, unit)`, `Start(ctx, unit)`, `Enable(ctx, unit)`, `Disable(ctx, unit)`, `Active(ctx, unit) (bool, error)`, `Exists(ctx, unit) bool`, and `UnitForProcess(process string) (string, bool)`. The last one maps `"nginx (pid N)"` to `nginx` and apache to `apache2`; anything else returns false.
    - The systemd implementation shells out to `systemctl` with a 15 s timeout per call.
  - `migrate.Check(ctx, sites []proxy.Site, addr string, trial bool) []SiteCheck`, where `SiteCheck{Host string; OK, Pending bool; Paths []PathCheck; Leaf CertSummary; Message string}`.
  - `migrate.Engine{Domains, Proxy, Systemd ServiceManager, DB, Hub, Now}`:
    - `Takeover(ctx) Result`
    - `Rollback(ctx) Result`

    `Result{Outcome: "ok"|"rolled_back"|"failed"; Message string}`. Steps follow spec §4.2 and §4.3, with events per D3.
  - API:
    - `GET /api/proxy/trial-check`, cached for 10 s.
    - `POST /api/proxy/takeover` with body `{code}`; returns 202 and runs the job in a goroutine.
    - `POST /api/proxy/rollback` with body `{code}`.
    - 409 when the state is wrong. Only one migration job runs at a time; otherwise 409 `MIGRATION_RUNNING`.

- [ ] **Step 1: Tests.** Use fake ServiceManager, proxy and check.
  - `TestTakeover_Success`: the call order is stop → apply normal → check → disable. The settings are written, the result is ok, and the events are emitted in order.
  - `TestTakeover_BindFailsRollsBack`: apply normal errors for more than 5 s. Expect apply trial → start → result `rolled_back`, nginx active, and `proxy.migrated_from` not set.
  - `TestTakeover_VerifyFailsRollsBack`
  - `TestTakeover_RollbackStartFailsReportsFailed`
  - `TestTakeover_UnsupportedOwner409`
  - `TestTakeover_NotTrial409`
  - `TestTakeover_AutoCertWaitsUpTo60s`: use a fake clock.
  - `TestRollback_EnablesAndStartsUnit`
  - `TestAPI_TakeoverRequiresCode`: no code → 403 `CODE_REQUIRED`; wrong code → 403 `INVALID_CODE`; the action is audited.
  - `TestCheck_ReportsStatusAndLeaf`: an `httptest` TLS server.
- [ ] **Step 2:** Run the tests and confirm they FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test ./... -count=1` and confirm it PASSES. Run the race detector in the Linux container for `./internal/migrate/...`.
- [ ] **Step 5:** Commit: `feat(etapa-3): ensaio, assumir as portas com volta automática e voltar para o servidor anterior`

### Task 8: Frontend — domain editor and certificates

**Files:**
- Modify: `web/src/components/domains/domain-dialog.tsx` (tabs Rotas/Certificado/Segurança; split into `route-list.tsx`, `tls-tab.tsx`, `security-tab.tsx` under `web/src/components/domains/`), `web/src/app/(painel)/dominios/page.tsx` (badges, Certificados section), `web/src/lib/domains.ts`
- Create: `web/src/lib/certificates.ts`, `web/src/components/domains/certificates-panel.tsx`, tests under `__tests__/`

**Interfaces:**
- Consumes: the Task 5 and Task 6 JSON.
- Produces: the TS types `DomainRoute`, `DomainTLS`, `Certificate`, and hooks `useCertificates()`, `useAddCertificate()`, `useDeleteCertificate()`.

- [ ] **Step 1: Vitest tests**
  - "adiciona, reordena e remove rotas; '/' é obrigatória"
  - "opções aparecem conforme o tipo (endereço: limite e sem buffer; estático: pasta, SPA e cache imutável)"
  - "modelo segurança preenche os 4 cabeçalhos exatos"
  - "certificado próprio: escolher existente ou adicionar colando PEM; a resposta nunca mostra chave"
  - "erros do servidor aparecem no campo certo (INVALID_ROOT, CERT_MISMATCH)"
  - "lista mostra selos Certificado próprio, Só Cloudflare e aviso de vencimento"
  - "domínio simples continua com uma rota '/' e parece o formulário de antes"
- [ ] **Step 2:** Run the tests and confirm they FAIL.
- [ ] **Step 3:** Implement. Use the `frontend-design` skill first, then review against `web-design-guidelines`.
- [ ] **Step 4:** From `web/`, run `pnpm vitest run && pnpm lint && pnpm build` and confirm it PASSES.
- [ ] **Step 5:** Commit: `feat(etapa-3): editor de domínio com rotas, certificado e segurança`

### Task 9: Frontend — trial banner, takeover and rollback

**Files:**
- Modify: `web/src/components/domains/proxy-banner.tsx`, `web/src/lib/domains.ts`
- Create: `web/src/components/domains/takeover-dialog.tsx`, tests

**Interfaces:**
- Consumes: the Task 7 API and the `proxy-migration` topic.

- [ ] **Step 1: Vitest tests**
  - "ensaio mostra processo, checklist por domínio (ok, pendente, falha) e botão Assumir as portas"
  - "Assumir pede código 2FA, mostra passos ao vivo e termina em sucesso"
  - "resultado rolled_back mostra 'voltou para o nginx: <motivo>'"
  - "migrado mostra 'Migrado do nginx em <data>' e Voltar para o nginx"
  - "Assumir desabilitado quando algum domínio falha no ensaio"
- [ ] **Step 2:** Run the tests and confirm they FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `pnpm vitest run && pnpm lint && pnpm build` and confirm it PASSES.
- [ ] **Step 5:** Commit: `feat(etapa-3): ensaio e troca de servidor no painel`

### Task 10: Integration, e2e, README

**Files:**
- Create: `internal/migrate/migrate_integration_test.go` (`//go:build integration && linux`), `internal/node/proxy/static_integration_test.go`
- Modify: `.github/workflows/ci.yml` (the integration job adds `./internal/migrate/...`), `web/e2e/` (a domains spec step), `README.md` (Etapa 3 section, plus a migration guide replacing the v1 spec §12 runbook text, with example names only)

**Interfaces:**
- Integration:
  - Real Caddy. A real `nginx:alpine` container on the host network holds 80/443. A test `ServiceManager` maps `nginx` to stop/start of that container.
  - Flow:
    1. Ensure gives `trial`.
    2. Apply a static site and a proxied site.
    3. `Check` on 18443 passes.
    4. `Takeover` → ok, and `:443` serves both.
    5. `Rollback` → nginx answers on 80 again.
- Static (Review Focus #1): a site rooted at `/var/www/korego-it/current`, a symlink. Request a file, swap the symlink, request again and get the new content.
- Custom TLS: the handshake on 443 presents the test certificate's fingerprint.
- e2e: create a domain with routes `/api/` → address and `/` → address, plus the header `X-Teste: 1`. Assert the list shows it and the editor reopens with both routes.

- [ ] **Step 1:** Write the tests.
- [ ] **Step 2:** Run locally whatever runs: Linux container with `--network host --pid host --cgroupns host` and the docker socket, as in the Etapa 2 notes. Report what ran.
- [ ] **Step 3:** Update CI and the README.
- [ ] **Step 4:** Run `go test ./...` and `pnpm vitest run`. The controller pushes; CI must be green with the new tests executed.
- [ ] **Step 5:** Commit: `test(etapa-3): integração com Caddy e nginx reais, e2e e README`
