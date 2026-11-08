import { execFileSync } from "node:child_process";
import { expect, test, type APIRequestContext, type Locator, type Page } from "@playwright/test";
import { loginWithRecoveryCode } from "./helpers";

const PROJECT = "e2e";
const SLUG = "site-e2e";
const IMAGE = "nginx:1-alpine";
const HOST = "site-e2e.korego.localhost";
/** Caddy escuta na rede do host: a rota é escolhida pelo cabeçalho Host. */
const PROXY_URL = "http://127.0.0.1/";
const NGINX_PAGE = "Welcome to nginx!";

function docker(...args: string[]): string {
  return execFileSync("docker", args, { encoding: "utf8", timeout: 120_000, stdio: ["ignore", "pipe", "pipe"] });
}

function dockerAvailable(): boolean {
  try {
    docker("version");
    return true;
  } catch {
    return false;
  }
}

const hasDocker = dockerAvailable();
// O Caddy usa network_mode host, que o Docker Desktop (Windows/macOS) não
// oferece de verdade. Fora do CI pula sem Docker ou fora de Linux; no CI o
// spec é obrigatório e falha em vez de pular.
test.skip(
  !process.env.CI && (!hasDocker || process.platform !== "linux"),
  "publicar precisa de Linux com Docker e as portas 80/443 livres: spec pulado",
);

function tryDocker(...args: string[]): string {
  try {
    return docker(...args);
  } catch {
    return "";
  }
}

const ids = (out: string) => out.split(/\s+/).filter(Boolean);

test.afterAll(() => {
  test.setTimeout(120_000);
  if (!hasDocker) return;
  tryDocker("rm", "-f", "korego-caddy");
  const containers = ids(tryDocker("ps", "-aq", "--filter", `name=korego-${SLUG}-`));
  if (containers.length) tryDocker("rm", "-f", ...containers);
  const images = ids(tryDocker("images", "-q", `korego/${SLUG}`));
  if (images.length) tryDocker("rmi", "-f", ...[...new Set(images)]);
});

interface ProxyStatus {
  state: string;
  process?: string;
  message?: string;
}

/**
 * Espera o proxy ficar `active`. Sem ele o deploy falha ("proxy inativo").
 * "Tentar novamente" (POST /api/proxy/retry) força um Ensure sem esperar a
 * volta de 30 s da reconciliação. Portas 80/443 ocupadas falham na hora, com o
 * nome do processo.
 */
async function waitProxyActive(page: Page): Promise<void> {
  const me = await page.request.get("/api/auth/me");
  expect(me.ok()).toBe(true);
  const { csrfToken } = (await me.json()) as { csrfToken: string };
  let last: ProxyStatus = { state: "desconhecido" };
  let lastRetry = 0;
  await expect
    .poll(
      async () => {
        const r = await page.request.get("/api/proxy");
        last = (await r.json()) as ProxyStatus;
        if (last.state !== "active" && last.state !== "blocked" && Date.now() - lastRetry > 10_000) {
          lastRetry = Date.now();
          const retry = await page.request.post("/api/proxy/retry", {
            headers: { "X-CSRF-Token": csrfToken },
            timeout: 150_000,
          });
          if (retry.ok()) last = (await retry.json()) as ProxyStatus;
        }
        // O valor inteiro aparece na falha por tempo esgotado.
        return JSON.stringify(last);
      },
      { timeout: 150_000, intervals: [1_000, 2_000, 3_000], message: "proxy não ficou ativo" },
    )
    .toMatch(/"state":"(active|blocked)"/);
  if (last.state === "blocked") {
    throw new Error(`proxy bloqueado: portas 80/443 em uso por ${last.process || "outro processo"}. ${last.message ?? ""}`);
  }
}

/**
 * Uma requisição pelo Caddy. Só vale como ok com 200 E a página do nginx: sem
 * rota para o Host, o Caddy responde 200 com corpo vazio. `desc` traz o status
 * (ou o erro de conexão) e um trecho do corpo, para a mensagem de falha.
 */
async function probe(request: APIRequestContext): Promise<{ ok: boolean; desc: string }> {
  try {
    const r = await request.get(PROXY_URL, { headers: { Host: HOST }, maxRedirects: 0, timeout: 10_000 });
    const body = await r.text();
    return {
      ok: r.status() === 200 && body.includes(NGINX_PAGE),
      desc: `${r.status()} ${JSON.stringify(body.slice(0, 120))}`,
    };
  } catch (err) {
    return { ok: false, desc: err instanceof Error ? err.message.split("\n")[0] : String(err) };
  }
}

/** Status de um deploy no cabeçalho do log (ao vivo); espera ele terminar e exige "No ar". */
async function expectDeployLive(page: Page, id: number, timeout: number): Promise<Locator> {
  const section = page.getByRole("region", { name: `Log do deploy #${id}` });
  const badge = section.locator('[aria-live="polite"]');
  await expect(badge).toHaveText(/No ar|Falhou|Cancelado/, { timeout });
  const status = (await badge.textContent())?.trim();
  if (status !== "No ar") {
    const log = await section.getByRole("region", { name: `Saída do deploy #${id}` }).textContent();
    throw new Error(`deploy #${id} terminou como "${status}":\n${log}`);
  }
  return section;
}

/** Célula de status da linha do deploy na aba Deploys. */
const statusCell = (page: Page, id: number) =>
  page.getByRole("row", { name: `Deploy #${id}`, exact: true }).getByRole("cell").nth(1);

async function deployNow(page: Page, appId: number): Promise<number> {
  const resp = page.waitForResponse(
    (r) => r.request().method() === "POST" && r.url().endsWith(`/api/apps/${appId}/deploys`),
  );
  await page.getByRole("button", { name: "Deploy agora" }).click();
  const r = await resp;
  expect(r.status(), await r.text()).toBe(202);
  return ((await r.json()) as { deploy: { id: number } }).deploy.id;
}

test("publicar: projeto, app por imagem, deploy, domínio, deploy sem queda, rollback e auditoria", async ({
  page,
  request,
}) => {
  test.setTimeout(6 * 60_000);
  await loginWithRecoveryCode(page);

  // 1. Projeto
  await page.goto("/projetos/");
  await expect(page.getByRole("heading", { name: "Projetos", level: 1 })).toBeVisible();
  await page.getByRole("button", { name: "Novo projeto" }).click();
  const projDialog = page.getByRole("dialog", { name: "Novo projeto" });
  await projDialog.getByLabel(/^Nome/).fill(PROJECT);
  await projDialog.getByRole("button", { name: "Criar projeto" }).click();
  await expect(projDialog).toBeHidden();
  await page.getByRole("link", { name: PROJECT, exact: true }).click();
  await expect(page.getByRole("heading", { name: PROJECT, level: 1 })).toBeVisible();

  // 2. Aplicação por imagem
  await page.getByRole("link", { name: "Nova aplicação" }).click();
  await expect(page.getByRole("heading", { name: "Nova aplicação" })).toBeVisible();
  await page.getByText("Imagem pronta de um registro").click();
  await expect(page.getByRole("radio", { name: /^Imagem/ })).toBeChecked();
  await page.getByRole("textbox", { name: /^Imagem/ }).fill(IMAGE);
  await page.getByRole("textbox", { name: /^Nome/ }).fill(SLUG);
  await page.getByRole("spinbutton", { name: /^Porta interna/ }).fill("80");
  await page.getByRole("button", { name: "Criar aplicação" }).click();
  await expect(page).toHaveURL(/\/aplicacoes\/detalhe\/\?id=\d+/);
  await expect(page.getByRole("heading", { name: SLUG, level: 1 })).toBeVisible();
  const appId = Number(new URL(page.url()).searchParams.get("id"));
  const appTab = (aba: string) => `/aplicacoes/detalhe/?id=${appId}&aba=${aba}`;

  // Sem o proxy ativo o deploy falha ao trocar a rota.
  await waitProxyActive(page);

  const d1 = await deployNow(page, appId);
  await expect(page.getByRole("tab", { name: "Deploys" })).toHaveAttribute("aria-selected", "true");
  await page.getByRole("link", { name: `Ver log do deploy #${d1}` }).click();
  await expect(page).toHaveURL(new RegExp(`/deploys/detalhe/\\?id=${d1}`));
  const log1 = await expectDeployLive(page, d1, 120_000);
  const out1 = log1.getByRole("region", { name: `Saída do deploy #${d1}` });
  await expect(out1).toContainText("==> Baixando a imagem");
  // Saída real do pull: a linha final do Docker, com a imagem já local (CI) ou baixada agora.
  await expect(out1).toContainText(/Status: (Image is up to date|Downloaded newer image) for nginx:1-alpine/);

  // 3. Domínio, sem forçar HTTPS
  await page.goto("/dominios/");
  await page.getByRole("button", { name: "Adicionar domínio" }).click();
  const domDialog = page.getByRole("dialog", { name: "Adicionar domínio" });
  await domDialog.getByRole("textbox", { name: /^Hostname/ }).fill(HOST);
  await expect(domDialog.getByRole("tab", { name: "Aplicação" })).toHaveAttribute("aria-selected", "true");
  const appSelect = domDialog.getByRole("combobox", { name: /^Aplicação/ });
  await expect(appSelect.locator("option", { hasText: SLUG })).toHaveCount(1);
  await appSelect.selectOption({ label: SLUG });
  await domDialog.getByRole("checkbox", { name: "Forçar HTTPS" }).uncheck();
  const saved = page.waitForResponse((r) => r.request().method() === "POST" && r.url().endsWith("/api/domains"));
  await domDialog.getByRole("button", { name: "Salvar" }).click();
  const savedResp = await saved;
  expect(savedResp.status(), await savedResp.text()).toBe(201);
  // Com o DNS sem apontar para o servidor, o diálogo fica aberto com o aviso e "Fechar".
  await expect(async () => {
    const close = domDialog.getByRole("button", { name: "Fechar" });
    if (await close.isVisible()) await close.click();
    await expect(domDialog).toBeHidden({ timeout: 1_000 });
  }).toPass({ timeout: 30_000 });
  await expect(page.getByRole("link", { name: new RegExp(HOST.replace(/\./g, "\\.")) })).toBeVisible();

  // 4. Pelo Caddy, a página padrão do nginx
  await expect
    .poll(async () => {
      const r = await probe(request);
      return r.ok ? "ok" : r.desc;
    }, { timeout: 60_000, intervals: [500, 1_000, 2_000] })
    .toBe("ok");

  // 5. Segundo deploy com um laço de requisições pelo proxy: toda resposta é 200 com a página do nginx.
  // 3 requisições sobrepostas a cada ~100 ms estreitam a janela em que um 502 curto escaparia.
  await page.goto(appTab("visao"));
  await expect(page.getByRole("heading", { name: SLUG, level: 1 })).toBeVisible();
  let stop = false;
  const results: { ok: boolean; desc: string }[] = [];
  const loop = (async () => {
    while (!stop || results.length < 10) {
      results.push(...(await Promise.all([probe(request), probe(request), probe(request)])));
      await new Promise((r) => setTimeout(r, 100));
    }
  })();
  let d2 = 0;
  try {
    d2 = await deployNow(page, appId);
    await expect(page.getByRole("tab", { name: "Deploys" })).toHaveAttribute("aria-selected", "true");
    await expect(statusCell(page, d2)).toHaveText(/No ar|Falhou|Cancelado/, { timeout: 120_000 });
    // Mais algumas requisições depois da troca (container anterior já removido).
    await new Promise((r) => setTimeout(r, 1_000));
  } finally {
    stop = true;
    await loop;
  }
  await expect(statusCell(page, d2)).toHaveText("No ar");
  const failures = results.filter((r) => !r.ok).map((r) => r.desc);
  expect(failures, `respostas sem a página do nginx durante a troca (${results.length} requisições)`).toEqual([]);
  expect(results.length).toBeGreaterThanOrEqual(10);

  // 6. O primeiro foi substituído
  await expect(statusCell(page, d1)).toHaveText("Substituído", { timeout: 30_000 });

  // 7. Rollback para o primeiro
  const row1 = page.getByRole("row", { name: `Deploy #${d1}`, exact: true });
  await row1.getByRole("button", { name: `Voltar para esta versão (deploy #${d1})` }).click();
  const rbDialog = page.getByRole("dialog", { name: `Voltar para o deploy #${d1}?` });
  const rbResp = page.waitForResponse(
    (r) => r.request().method() === "POST" && r.url().endsWith(`/api/apps/${appId}/rollback`),
  );
  await rbDialog.getByRole("button", { name: "Voltar para esta versão", exact: true }).click();
  const rb = await rbResp;
  expect(rb.status(), await rb.text()).toBe(202);
  const d3 = ((await rb.json()) as { deploy: { id: number } }).deploy.id;
  await expect(page).toHaveURL(new RegExp(`/deploys/detalhe/\\?id=${d3}`));
  await expectDeployLive(page, d3, 120_000);
  const after = await probe(request);
  expect(after.ok, `depois do rollback: ${after.desc}`).toBe(true);
  await page.goto(appTab("deploys"));
  await expect(statusCell(page, d3)).toHaveText("No ar");
  await expect(statusCell(page, d2)).toHaveText("Substituído", { timeout: 30_000 });

  // 8. Auditoria
  await page.goto("/configuracoes/seguranca/");
  const audit = page.getByRole("region", { name: "Auditoria" });
  for (const action of ["deploy.create", "deploy.rollback", "domain.create"]) {
    await expect(audit.getByText(action, { exact: true }).first()).toBeVisible();
  }

  // 9. Limpeza pela interface: domínio e aplicação
  await page.goto("/dominios/");
  await page.getByRole("button", { name: `Remover ${HOST}` }).click();
  const rmDomain = page.getByRole("dialog", { name: "Remover domínio?" });
  await rmDomain.getByRole("button", { name: "Remover", exact: true }).click();
  await expect(rmDomain).toBeHidden();
  await expect(page.getByText("Nenhum domínio cadastrado.")).toBeVisible();

  await page.goto(appTab("configuracoes"));
  await page.getByRole("button", { name: "Remover aplicação" }).click();
  const rmApp = page.getByRole("dialog", { name: "Remover aplicação?" });
  await rmApp.getByRole("textbox", { name: /para confirmar/ }).fill(SLUG);
  await rmApp.getByRole("button", { name: "Remover", exact: true }).click();
  await expect(page).toHaveURL(/\/projetos\/detalhe\/\?id=\d+/, { timeout: 60_000 });
  await expect(page.getByText("Nenhuma aplicação neste projeto.")).toBeVisible();
});
