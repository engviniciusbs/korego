import { execFileSync } from "node:child_process";
import { expect, test } from "@playwright/test";
import { loginWithRecoveryCode } from "./helpers";

const NAME = "korego-e2e";
const PROJECT = "e2e";

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
test.skip(!hasDocker, "Docker indisponível: spec de containers pulado");

function removeContainer(): void {
  try {
    docker("rm", "-f", NAME);
  } catch {
    // não existia
  }
}

test.beforeAll(() => {
  if (!hasDocker) return;
  removeContainer();
  docker(
    "run", "-d", "--name", NAME,
    "--label", `com.docker.compose.project=${PROJECT}`,
    "busybox", "sh", "-c",
    "i=0; while true; do i=$((i+1)); echo linha-$i; sleep 1; done",
  );
});

test.afterAll(() => {
  if (hasDocker) removeContainer();
});

test("containers: observar, ver logs, parar, remover e auditar", async ({ page }) => {
  await loginWithRecoveryCode(page);

  // Visão geral: CPU com valor numérico e seletor de período com 7d
  const cpu = page.getByRole("region", { name: "CPU" }).first();
  await expect(cpu).toContainText(/\d/);
  await expect(page.getByRole("tab", { name: "7d" })).toBeVisible();
  await page.getByRole("tab", { name: "7d" }).click();
  await expect(page.getByRole("tab", { name: "7d" })).toHaveAttribute("aria-selected", "true");

  // Lista: grupo "e2e" com o container marcado como externo
  await page.goto("/containers/");
  const group = page.getByRole("region", { name: /^e2e/ });
  await expect(group).toBeVisible();
  const row = group.getByRole("row", { name: new RegExp(NAME) });
  await expect(row.getByText("externo", { exact: true })).toBeVisible();

  // Detalhe
  await row.getByRole("link", { name: NAME }).click();
  await expect(page).toHaveURL(/\/containers\/detalhe\/\?id=/);
  await page.getByRole("tab", { name: "Logs" }).click();
  const output = page.getByRole("region", { name: `Saída de ${NAME}` });
  await expect(output).toContainText("linha-");
  const maxLine = async () => {
    const text = (await output.textContent()) ?? "";
    return Math.max(0, ...[...text.matchAll(/linha-(\d+)/g)].map((m) => Number(m[1])));
  };
  const before = await maxLine();
  await expect.poll(maxLine, { timeout: 5_000 }).toBeGreaterThan(before);

  // Parar, com o aviso de container externo
  await page.getByRole("button", { name: "Parar", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText("Este container não foi criado pelo KoreGO")).toBeVisible();
  await dialog.getByRole("button", { name: "Parar", exact: true }).click();
  // O sh do busybox ignora SIGTERM como PID 1: o Docker espera ~10 s antes do kill.
  await expect(dialog).toBeHidden({ timeout: 30_000 });
  await expect.poll(() => docker("inspect", "-f", "{{.State.Status}}", NAME).trim(), { timeout: 30_000 }).toBe("exited");

  // Volta à lista: estado exited e remoção
  await page.goto("/containers/");
  const row2 = page.getByRole("region", { name: /^e2e/ }).getByRole("row", { name: new RegExp(NAME) });
  await expect(row2.getByText("exited", { exact: true })).toBeVisible();
  await row2.getByRole("button", { name: "Remover" }).click();
  const dialog2 = page.getByRole("dialog");
  await expect(dialog2.getByText("Este container não foi criado pelo KoreGO")).toBeVisible();
  await dialog2.getByRole("button", { name: "Remover", exact: true }).click();
  await expect(dialog2).toBeHidden();
  await expect(page.getByRole("link", { name: NAME })).toHaveCount(0);

  // Segurança: auditoria das duas ações
  await page.goto("/configuracoes/seguranca/");
  const audit = page.locator("table").nth(1);
  await expect(audit.getByText("container.stop")).toBeVisible();
  await expect(audit.getByText("container.remove")).toBeVisible();
});
