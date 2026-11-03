// Copia o export estático do Next (web/out) para internal/webui/dist,
// onde o binário Go o embute. Usa só node:fs para rodar em Windows e Linux.
import { cpSync, existsSync, mkdirSync, readdirSync, rmSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const src = join(root, "web", "out");
const dest = join(root, "internal", "webui", "dist");

if (!existsSync(join(src, "index.html"))) {
  console.error(`copy-web: ${src} não contém index.html. Rode \`pnpm build\` em web/ antes.`);
  process.exit(1);
}

mkdirSync(dest, { recursive: true });
for (const name of readdirSync(dest)) {
  if (name === ".gitkeep") continue;
  rmSync(join(dest, name), { recursive: true, force: true });
}
cpSync(src, dest, { recursive: true });

console.log(`copy-web: ${src} -> ${dest}`);
