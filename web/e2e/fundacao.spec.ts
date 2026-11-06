import { readFileSync } from "node:fs";
import { hostname } from "node:os";
import path from "node:path";
import { expect, test } from "@playwright/test";
import { generate } from "otplib";
import { EMAIL, PASSWORD, saveAdmin } from "./helpers";

test("fundação: setup, painel, logout, login com código de recuperação e auditoria", async ({ page }) => {
  // / -> /setup/ (nenhum usuário ainda)
  await page.goto("/");
  await expect(page).toHaveURL(/\/setup\/$/);

  // Passo 1: conta
  const setupToken = readFileSync(
    path.join(process.env.KOREGO_E2E_DIR!, "config", "setup-token"),
    "utf8",
  ).trim();
  await page.getByLabel("Token de setup").fill(setupToken);
  await page.getByLabel("E-mail").fill(EMAIL);
  await page.getByLabel(/^Senha/).fill(PASSWORD);
  await page.getByLabel("Confirmar senha").fill(PASSWORD);
  await page.getByRole("button", { name: "Continuar" }).click();

  // Passo 2: segredo TOTP exibido na página
  const secretEl = page.locator("code.select-all");
  await expect(secretEl).toBeVisible();
  const secret = ((await secretEl.textContent()) ?? "").replace(/\s+/g, "");
  expect(secret).not.toBe("");
  await page.getByLabel("Código do autenticador").fill(await generate({ secret }));
  await page.getByRole("button", { name: "Confirmar" }).click();

  // Passo 3: códigos de recuperação
  const codeItems = page.getByRole("list", { name: "Códigos de recuperação" }).getByRole("listitem");
  await expect(codeItems).toHaveCount(10);
  const recoveryCodes = (await codeItems.allTextContents())
    .map((t) => t.match(/[a-z0-9]{5}-[a-z0-9]{5}/i)?.[0])
    .filter((c): c is string => Boolean(c));
  expect(recoveryCodes).toHaveLength(10);
  // O primeiro código é consumido abaixo; os demais ficam para os outros specs.
  saveAdmin({ email: EMAIL, password: PASSWORD, recoveryCodes: recoveryCodes.slice(1) });
  await page.getByLabel("Guardei meus códigos").check();
  await page.getByRole("button", { name: "Ir para o painel" }).click();

  // Visão geral: hostname e uptime
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole("heading", { name: "Visão geral" })).toBeVisible();
  await expect(page.locator("dd", { hasText: hostname() })).toBeVisible();
  await expect(page.getByText("Tempo ligado")).toBeVisible();
  await expect(page.locator("p.font-mono").filter({ hasText: /^(\d+d )?(\d+h )?\d+m$/ })).toBeVisible();

  // Sair
  await page.getByRole("button", { name: new RegExp(EMAIL) }).click();
  await page.getByRole("button", { name: "Sair" }).click();
  await expect(page).toHaveURL(/\/login\/$/);

  // Login com um código de recuperação
  await page.getByLabel("E-mail").fill(EMAIL);
  await page.getByLabel(/^Senha/).fill(PASSWORD);
  await page.getByLabel("Código").fill(recoveryCodes[0]);
  await page.getByRole("button", { name: "Entrar" }).click();
  await expect(page.getByRole("heading", { name: "Visão geral" })).toBeVisible();

  // Segurança: 1 sessão e eventos de auditoria
  await page.goto("/configuracoes/seguranca/");
  await expect(page.getByRole("heading", { name: "Segurança" })).toBeVisible();
  await expect(page.getByText("Esta sessão", { exact: true })).toHaveCount(1);
  await expect(page.locator("table").first().locator("tbody tr")).toHaveCount(1);
  const audit = page.locator("table").nth(1);
  await expect(audit.getByText("Configuração inicial")).toBeVisible();
  await expect(audit.getByText("Logout")).toBeVisible();
  await expect(audit.getByText("Login", { exact: true })).toBeVisible();
});
