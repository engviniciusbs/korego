// Validadores que espelham as regras do servidor (internal/deploy/validate.go e
// internal/domains/validate.go) para mostrar o erro antes de enviar. O
// servidor continua sendo a autoridade.

const slugRe = /^[a-z0-9][a-z0-9-]{0,62}$/;
const repoRe = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/;
const branchRe = /^[A-Za-z0-9._/-]{1,255}$/;
const serviceRe = /^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$/;
const envNameRe = /^[A-Za-z_][A-Za-z0-9_]{0,127}$/;
const hostRe = /^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$/;
const imgHostRe = /^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$/;
const imgCompRe = /^[a-z0-9]+(?:(?:\.|_|__|-+)[a-z0-9]+)*$/;
const imgTagRe = /^\w[\w.-]{0,127}$/;
const imgDigestRe = /^sha256:[a-f0-9]{64}$/;
const labelRe = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

const byteLen = (s: string) => new TextEncoder().encode(s).length;

export function validSlug(s: string): boolean {
  return slugRe.test(s);
}

export function validRepo(s: string): boolean {
  if (!repoRe.test(s)) return false;
  const [owner, name] = s.split("/");
  return owner !== "." && owner !== ".." && name !== "." && name !== "..";
}

/** Só https://host/caminho, sem usuário/senha, sem "@" nem barra invertida. */
export function validGitUrl(s: string): boolean {
  if (s === "" || /[ \t\r\n\0\\@]/.test(s)) return false;
  const m = /^https:\/\/([^/?#]*)/i.exec(s);
  if (!m) return false;
  try {
    const u = new URL(s);
    if (u.protocol !== "https:" || u.username || u.password || !u.host) return false;
  } catch {
    return false;
  }
  const authority = m[1];
  const i = authority.lastIndexOf(":");
  let host = authority;
  if (i >= 0) {
    if (!/^\d*$/.test(authority.slice(i + 1))) return false;
    host = authority.slice(0, i);
  }
  return hostRe.test(host);
}

export function validBranch(s: string): boolean {
  return (
    branchRe.test(s) &&
    !s.includes("..") &&
    !s.startsWith("-") &&
    !s.startsWith("/") &&
    !s.endsWith("/") &&
    !s.endsWith(".lock")
  );
}

/** Caminho relativo, sem "..", dentro do clone. */
export function validRelPath(s: string): boolean {
  if (s === "" || byteLen(s) > 512 || /[\0\\:]/.test(s) || s.startsWith("/") || s.startsWith("-")) {
    return false;
  }
  return !s.split("/").includes("..");
}

export function validServiceName(s: string): boolean {
  return serviceRe.test(s);
}

export function validEnvName(s: string): boolean {
  return envNameRe.test(s);
}

export function validPort(p: number): boolean {
  return Number.isInteger(p) && p >= 1 && p <= 65535;
}

/** [registro[:porta]/]caminho[:tag][@sha256:hex]. */
export function validImageRef(s: string): boolean {
  if (s === "" || byteLen(s) > 255) return false;
  const at = s.indexOf("@");
  if (at >= 0) {
    if (!imgDigestRe.test(s.slice(at + 1))) return false;
    s = s.slice(0, at);
  }
  const parts = s.split("/");
  const last = parts[parts.length - 1];
  const colon = last.lastIndexOf(":");
  if (colon >= 0) {
    if (!imgTagRe.test(last.slice(colon + 1))) return false;
    parts[parts.length - 1] = last.slice(0, colon);
  }
  if (parts.length > 1) {
    const first = parts[0];
    if (/[.:]/.test(first) || first === "localhost") {
      if (!imgHostRe.test(first)) return false;
      parts.shift();
    }
  }
  return parts.every((c) => imgCompRe.test(c));
}

function isIPv4(s: string): boolean {
  const p = s.split(".");
  return p.length === 4 && p.every((x) => /^(0|[1-9]\d{0,2})$/.test(x) && Number(x) <= 255);
}

function isIPv6(s: string): boolean {
  if (!s.includes(":") || !/^[0-9a-fA-F:.]+$/.test(s)) return false;
  try {
    new URL(`http://[${s}]`);
    return true;
  } catch {
    return false;
  }
}

/** Minúsculo, total ≤ 253, ao menos dois rótulos válidos; IP não é hostname. */
export function validHostname(s: string): boolean {
  if (s === "" || s.length > 253 || isIPv4(s) || isIPv6(s)) return false;
  const labels = s.split(".");
  return labels.length >= 2 && labels.every((l) => labelRe.test(l));
}

/** Como net.SplitHostPort: "host:porta" ou "[v6]:porta". */
function splitHostPort(s: string): [string, string] | null {
  if (s.startsWith("[")) {
    const end = s.indexOf("]");
    if (end < 0 || s[end + 1] !== ":") return null;
    return [s.slice(1, end), s.slice(end + 2)];
  }
  const i = s.lastIndexOf(":");
  if (i < 0) return null;
  const host = s.slice(0, i);
  if (host.includes(":") || host.includes("[") || s.includes("]")) return null;
  return [host, s.slice(i + 1)];
}

/** Endereço manual host:porta: host IP, localhost ou hostname, porta 1-65535. */
export function validAddress(s: string): boolean {
  const hp = splitHostPort(s);
  if (!hp) return false;
  const [host, port] = hp;
  if (host === "" || port === "" || !/^[0-9]+$/.test(port)) return false;
  const p = Number(port);
  if (p < 1 || p > 65535) return false;
  return host === "localhost" || isIPv4(host) || isIPv6(host) || validHostname(host);
}

const pathSegRe = /^[a-z0-9._~-]+$/;
const headerNameRe = /^[A-Za-z0-9!#$%&'*+.^_`|~-]+$/;

/** Espelha proxy.validPath: "/" ou "/seg/.../", segmentos em [a-z0-9._~-]. */
export function validRoutePath(p: string): boolean {
  if (p === "/") return true;
  if (p.length < 3 || !p.startsWith("/") || !p.endsWith("/")) return false;
  return p
    .slice(1, -1)
    .split("/")
    .every((seg) => pathSegRe.test(seg));
}

/** Espelha proxy.ValidImmutablePrefix: um caminho de rota diferente de "/". */
export function validImmutablePrefix(p: string): boolean {
  return p !== "/" && validRoutePath(p);
}

/** Limpa um caminho absoluto como path.Clean do Go. */
function cleanPath(p: string): string {
  const out: string[] = [];
  for (const seg of p.split("/")) {
    if (seg === "" || seg === ".") continue;
    if (seg === "..") out.pop();
    else out.push(seg);
  }
  return "/" + out.join("/");
}

/** Espelha proxy.ValidStaticRoot: absoluto e, depois de limpo, em /var/www. */
export function validStaticRoot(root: string): boolean {
  if (!root.startsWith("/")) return false;
  const c = cleanPath(root);
  return c === "/var/www" || c.startsWith("/var/www/");
}

/** Espelha proxy.ValidHeaderName (token HTTP). */
export function validHeaderName(n: string): boolean {
  return headerNameRe.test(n);
}
