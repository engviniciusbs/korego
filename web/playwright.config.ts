import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { defineConfig } from "@playwright/test";

const PORT = 18000;
const repoRoot = path.resolve(__dirname, "..");
const binary = path.join(repoRoot, "bin", process.platform === "win32" ? "korego.exe" : "korego");

// Pasta temporária nova a cada execução. O config é carregado também nos
// workers, que herdam esta variável de ambiente do processo principal.
const stateDir = (process.env.KOREGO_E2E_DIR ??= mkdtempSync(path.join(tmpdir(), "korego-e2e-")));

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  projects: [
    { name: "fundacao", testMatch: /fundacao\.spec\.ts/ },
    { name: "containers", testMatch: /containers\.spec\.ts/, dependencies: ["fundacao"] },
  ],
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: {
    baseURL: `http://localhost:${PORT}`,
    channel: "chrome",
    trace: "retain-on-failure",
  },
  webServer: {
    command: `"${binary}"`,
    cwd: repoRoot,
    url: `http://localhost:${PORT}/api/health`,
    reuseExistingServer: false,
    timeout: 30_000,
    env: {
      KOREGO_LISTEN: `:${PORT}`,
      KOREGO_DATA_DIR: path.join(stateDir, "data"),
      KOREGO_CONFIG_DIR: path.join(stateDir, "config"),
    },
  },
});
