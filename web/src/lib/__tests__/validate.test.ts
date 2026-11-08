import { describe, expect, it } from "vitest";
import {
  validAddress,
  validBranch,
  validEnvName,
  validGitUrl,
  validHostname,
  validImageRef,
  validPort,
  validRelPath,
  validRepo,
  validServiceName,
  validSlug,
} from "../validate";

// Tabelas espelhando internal/deploy/validate_test.go e internal/domains/validate_test.go.
function check(name: string, f: (s: string) => boolean, ok: string[], bad: string[]) {
  it(name, () => {
    for (const s of ok) expect(f(s), `${name} deveria aceitar ${JSON.stringify(s)}`).toBe(true);
    for (const s of bad) expect(f(s), `${name} deveria recusar ${JSON.stringify(s)}`).toBe(false);
  });
}

describe("validadores (espelham o servidor)", () => {
  check(
    "slug",
    validSlug,
    ["blog", "a", "meu-app-1", "a".repeat(63)],
    ["", "-a", "Blog", "a_b", "a b", "../x", "a".repeat(64)],
  );
  check(
    "repo",
    validRepo,
    ["owner/app", "o.w-n_1/r.e-p_o"],
    ["", "owner", "a/b/c", "../x", "./x", "a/..", "a/.", "a b/c", "-"],
  );
  check(
    "gitURL",
    validGitUrl,
    ["https://github.com/o/r.git", "https://git.exemplo.com/x"],
    [
      "",
      "http://github.com/o/r",
      "ssh://git@github.com/o/r",
      "git@github.com:o/r",
      "https://user:pass@github.com/o/r",
      "https://user@github.com/o/r",
      "https://github.com/o /r",
      "file:///etc",
      "https://",
      "-https://x.com",
      "ext::sh -c x",
      "https://x.com/a\\b",
      "https:x.com",
    ],
  );
  check(
    "branch",
    validBranch,
    ["main", "feature/x-1", "v1.2.3", "rel_1"],
    ["", "-branch", "/abs", "a/", "main.lock", "a..b", "a b", "a;b", "$x", "a".repeat(256)],
  );
  check(
    "relpath",
    validRelPath,
    ["Dockerfile", "docker/Dockerfile.prod", "./compose.yml"],
    ["", "../x", "a/../../x", "/abs", "-f", "a\\b", "C:/x", "a/..", "a\x00b"],
  );
  check(
    "service",
    validServiceName,
    ["web", "web.1", "A_b-c"],
    ["", "-a", "a b", ".a", "a".repeat(64)],
  );
  check(
    "env",
    validEnvName,
    ["A", "_X", "DATABASE_URL", "a1"],
    ["", "1A", "A-B", "A B", "A=B", "A".repeat(129)],
  );
  check(
    "image",
    validImageRef,
    [
      "nginx",
      "nginx:1-alpine",
      "library/nginx",
      "ghcr.io/owner/app:v1.2.3",
      "registry.exemplo.com:5000/team/app@sha256:" + "a".repeat(64),
      "localhost:5000/app",
      "nginx:latest@sha256:" + "0".repeat(64),
      "a/b__c/d-e",
    ],
    [
      "",
      "Imagem:latest",
      "nginx:",
      "-nginx",
      "nginx latest",
      "a/../b",
      "nginx@sha256:abc",
      "nginx:" + "a".repeat(129),
      "ghcr.io//a",
      "a/",
      "/a",
      "nginx;rm",
      "nginx:-x",
    ],
  );

  const long = ("a".repeat(63) + ".").repeat(3) + "a".repeat(62); // 254
  const ok253 = ("a".repeat(63) + ".").repeat(3) + "a".repeat(61);
  check(
    "hostname",
    validHostname,
    ["exemplo.com", "app.exemplo.com", "a-b.exemplo.com.br", "app.korego.localhost", ok253],
    ["Exemplo.com", "exemplo", "-a.com", "a..com", long, "a_b.com", "", "a-.com", "203.0.113.10", "exemplo.com."],
  );
  check(
    "address",
    validAddress,
    ["127.0.0.1:5000", "localhost:3000", "[::1]:80", "api.interna:8080", "localhost:65535", "exemplo.com:1"],
    [
      "127.0.0.1",
      ":80",
      "x:0",
      "x:70000",
      "a b:1",
      "localhost:+80",
      "localhost:",
      "localhost:0",
      "localhost:70000",
      "Exemplo.com:80",
      "http://x:80",
    ],
  );

  it("porta", () => {
    for (const p of [1, 80, 65535]) expect(validPort(p)).toBe(true);
    for (const p of [0, -1, 65536, 1.5, NaN]) expect(validPort(p)).toBe(false);
  });
});
