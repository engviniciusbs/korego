import { readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { expect, type Page } from "@playwright/test";

/** Credenciais do administrador criado pelo spec de fundação. */
export const EMAIL = "admin@exemplo.com";
export const PASSWORD = "senha-de-teste-muito-longa-123";

interface AdminFile {
  email: string;
  password: string;
  recoveryCodes: string[];
}

const adminFile = () => path.join(process.env.KOREGO_E2E_DIR!, "e2e-admin.json");

/** Grava as credenciais e os códigos de recuperação ainda não usados. */
export function saveAdmin(admin: AdminFile): void {
  writeFileSync(adminFile(), JSON.stringify(admin), "utf8");
}

/**
 * Entra com senha + um código de recuperação (cada um vale uma vez só).
 * Evita reaproveitar um TOTP dentro da mesma janela de 30 s.
 */
export async function loginWithRecoveryCode(page: Page): Promise<void> {
  const admin = JSON.parse(readFileSync(adminFile(), "utf8")) as AdminFile;
  const code = admin.recoveryCodes.shift();
  if (!code) throw new Error("sem códigos de recuperação disponíveis");
  writeFileSync(adminFile(), JSON.stringify(admin), "utf8");

  await page.goto("/login/");
  await page.getByLabel("E-mail").fill(admin.email);
  await page.getByLabel(/^Senha/).fill(admin.password);
  await page.getByLabel("Código").fill(code);
  await page.getByRole("button", { name: "Entrar" }).click();
  await expect(page.getByRole("heading", { name: "Visão geral" })).toBeVisible();
}
