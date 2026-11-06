export class ApiError extends Error {
  code: string;
  status: number;

  constructor(code: string, message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.code = code;
    this.status = status;
  }
}

export const UNAUTHORIZED_EVENT = "korego:unauthorized";

let csrfToken = "";

export function setCsrfToken(t: string): void {
  csrfToken = t;
}

export function clearCsrfToken(): void {
  csrfToken = "";
}

export interface ApiInit {
  method?: string;
  body?: unknown;
}

export async function api<T>(path: string, init: ApiInit = {}): Promise<T> {
  const method = (init.method ?? "GET").toUpperCase();
  const headers: Record<string, string> = {};
  if (method !== "GET" && method !== "HEAD" && csrfToken) {
    headers["X-CSRF-Token"] = csrfToken;
  }
  let body: string | undefined;
  if (init.body !== undefined) {
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(init.body);
  }

  let res: Response;
  try {
    res = await fetch(path, { method, headers, body, credentials: "same-origin" });
  } catch {
    throw new ApiError("NETWORK_ERROR", "Falha de rede", 0);
  }

  const text = await res.text();

  if (!res.ok) {
    let code = `HTTP_${res.status}`;
    let message = res.statusText || code;
    try {
      const parsed = JSON.parse(text);
      if (parsed?.error?.code) {
        code = String(parsed.error.code);
        message = String(parsed.error.message ?? message);
      }
    } catch {
      // corpo não-JSON: mantém HTTP_<status>
    }
    if (res.status === 401 && typeof window !== "undefined") {
      window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
    }
    throw new ApiError(code, message, res.status);
  }

  if (res.status === 204 || text === "") return undefined as T;
  return JSON.parse(text) as T;
}
