import { expect, test, type Page } from "@playwright/test";
import { loginWithRecoveryCode } from "./helpers";

/**
 * Editor de domínio: rotas por caminho e cabeçalho de resposta. Só grava e
 * relê pelo painel (o tráfego real pelo Caddy é coberto pela integração), então
 * roda em qualquer sistema. Os endereços não precisam existir.
 */
const HOST = "rotas-e2e.korego.localhost";
const ROOT_ADDR = "127.0.0.1:5000";
const API_ADDR = "127.0.0.1:5001";

const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\/]/g, "\\$&");

/** Remove o domínio pela API se ele existir (limpeza mesmo com falha no meio). */
async function removeDomain(page: Page): Promise<void> {
  const me = await page.request.get("/api/auth/me");
  if (!me.ok()) return;
  const { csrfToken } = (await me.json()) as { csrfToken: string };
  const list = await page.request.get("/api/domains");
  if (!list.ok()) return;
  const { domains } = (await list.json()) as { domains: { id: number; hostname: string }[] };
  for (const d of domains.filter((x) => x.hostname === HOST)) {
    await page.request.delete(`/api/domains/${d.id}`, { headers: { "X-CSRF-Token": csrfToken } });
  }
}

test("domínios: cria com rotas /api/ e /, cabeçalho X-Teste, lista e reabre no editor", async ({ page }) => {
  await loginWithRecoveryCode(page);
  try {
    await page.goto("/dominios/");
    await page.getByRole("button", { name: "Adicionar domínio" }).click();
    const dialog = page.getByRole("dialog", { name: "Adicionar domínio" });
    await dialog.getByRole("textbox", { name: /^Hostname/ }).fill(HOST);

    // Rota "/" → endereço.
    await dialog.getByRole("tab", { name: "Endereço" }).click();
    await dialog.getByRole("textbox", { name: /host:porta/ }).fill(ROOT_ADDR);

    // Rota "/api/" → outro endereço.
    await dialog.getByRole("button", { name: "Adicionar rota por caminho" }).click();
    await dialog.getByRole("textbox", { name: "Caminho" }).fill("/api/");
    const api = dialog.getByRole("group", { name: /^Rota \d+ \(\/api\/\)$/ });
    await api.getByRole("tab", { name: "Endereço" }).click();
    await api.getByRole("textbox", { name: /host:porta/ }).fill(API_ADDR);

    // Cabeçalho de resposta na aba Segurança.
    await dialog.getByRole("tab", { name: "Segurança" }).click();
    await dialog.getByRole("button", { name: "Adicionar cabeçalho" }).click();
    await dialog.getByRole("textbox", { name: "Nome do cabeçalho 1" }).fill("X-Teste");
    await dialog.getByRole("textbox", { name: "Valor do cabeçalho 1" }).fill("1");

    const saved = page.waitForResponse((r) => r.request().method() === "POST" && r.url().endsWith("/api/domains"));
    await dialog.getByRole("button", { name: "Salvar" }).click();
    const resp = await saved;
    expect(resp.status(), await resp.text()).toBe(201);
    const sent = resp.request().postDataJSON() as {
      routes: { path: string; targetType: string; address?: string }[];
      headers: { name: string; value: string }[];
    };
    expect(sent.routes.map((r) => [r.path, r.targetType, r.address])).toEqual([
      ["/api/", "address", API_ADDR],
      ["/", "address", ROOT_ADDR],
    ]);
    expect(sent.headers).toEqual([{ name: "X-Teste", value: "1" }]);

    // Com o DNS sem apontar para o servidor, o diálogo fica aberto com o aviso e "Fechar".
    await expect(async () => {
      const close = dialog.getByRole("button", { name: "Fechar" });
      if (await close.isVisible()) await close.click();
      await expect(dialog).toBeHidden({ timeout: 1_000 });
    }).toPass({ timeout: 30_000 });

    // Na lista: o hostname, o destino da rota "/" e a rota extra.
    const row = page
      .getByRole("row")
      .filter({ has: page.getByRole("link", { name: new RegExp(escapeRe(HOST)) }) });
    await expect(row).toHaveCount(1);
    await expect(row).toContainText(ROOT_ADDR);
    await expect(row).toContainText("+1 rota");

    // Reabre no editor com as duas rotas e o cabeçalho.
    await row.getByRole("button", { name: `Editar ${HOST}` }).click();
    const edit = page.getByRole("dialog", { name: "Editar domínio" });
    const groups = edit.getByRole("group", { name: /^Rota \d+ \(/ });
    await expect(groups).toHaveCount(2);
    const apiGroup = edit.getByRole("group", { name: "Rota 1 (/api/)" });
    const rootGroup = edit.getByRole("group", { name: "Rota 2 (/)" });
    await expect(apiGroup.getByRole("tab", { name: "Endereço" })).toHaveAttribute("aria-selected", "true");
    await expect(apiGroup.getByRole("textbox", { name: /host:porta/ })).toHaveValue(API_ADDR);
    await expect(rootGroup.getByRole("tab", { name: "Endereço" })).toHaveAttribute("aria-selected", "true");
    await expect(rootGroup.getByRole("textbox", { name: /host:porta/ })).toHaveValue(ROOT_ADDR);
    await edit.getByRole("tab", { name: "Segurança" }).click();
    await expect(edit.getByRole("textbox", { name: "Nome do cabeçalho 1" })).toHaveValue("X-Teste");
    await expect(edit.getByRole("textbox", { name: "Valor do cabeçalho 1" })).toHaveValue("1");
    await edit.getByRole("button", { name: "Cancelar" }).click();
    await expect(edit).toBeHidden();

    // Limpeza pela interface (o spec publicar espera a lista vazia no fim).
    await page.getByRole("button", { name: `Remover ${HOST}` }).click();
    const rm = page.getByRole("dialog", { name: "Remover domínio?" });
    await rm.getByRole("button", { name: "Remover", exact: true }).click();
    await expect(rm).toBeHidden();
    await expect(row).toHaveCount(0);
  } finally {
    await removeDomain(page);
  }
});
