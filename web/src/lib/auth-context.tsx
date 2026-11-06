"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { UNAUTHORIZED_EVENT, api, clearCsrfToken, setCsrfToken } from "./api";

export interface User {
  id: string;
  email: string;
  role: string;
  createdAt: string;
}

export type AuthStatus = "loading" | "setup" | "anonymous" | "authenticated";

interface Session {
  user: User;
  csrfToken: string;
}

/** Resultado de POST /api/setup/confirm. */
export interface SetupResult extends Session {
  recoveryCodes: string[];
}

export interface AuthValue {
  user: User | null;
  status: AuthStatus;
  login(email: string, password: string, code: string): Promise<void>;
  logout(): Promise<void>;
  refresh(): Promise<void>;
  /** Chamar após POST /api/setup/confirm: guarda o CSRF e autentica. */
  completeSetup(result: SetupResult): void;
}

const AuthContext = createContext<AuthValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [status, setStatus] = useState<AuthStatus>("loading");

  const applySession = useCallback((s: Session) => {
    setCsrfToken(s.csrfToken);
    setUser(s.user);
    setStatus("authenticated");
  }, []);

  const clear = useCallback(() => {
    clearCsrfToken();
    setUser(null);
    setStatus("anonymous");
  }, []);

  const refresh = useCallback(async () => {
    try {
      const { needsSetup } = await api<{ needsSetup: boolean }>("/api/setup/status");
      if (needsSetup) {
        clearCsrfToken();
        setUser(null);
        setStatus("setup");
        return;
      }
      applySession(await api<Session>("/api/auth/me"));
    } catch {
      // 401 ou erro inesperado: trata como anônimo (a tela de login exibirá falhas)
      clear();
    }
  }, [applySession, clear]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- carga inicial da sessão
    void refresh();
  }, [refresh]);

  useEffect(() => {
    const onUnauthorized = () => clear();
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
  }, [clear]);

  const login = useCallback(
    async (email: string, password: string, code: string) => {
      applySession(
        await api<Session>("/api/auth/login", {
          method: "POST",
          body: { email, password, code },
        }),
      );
    },
    [applySession],
  );

  const logout = useCallback(async () => {
    try {
      await api<void>("/api/auth/logout", { method: "POST" });
    } finally {
      clear();
    }
  }, [clear]);

  const completeSetup = useCallback(
    (r: SetupResult) => applySession(r),
    [applySession],
  );

  const value = useMemo<AuthValue>(
    () => ({ user, status, login, logout, refresh, completeSetup }),
    [user, status, login, logout, refresh, completeSetup],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth deve ser usado dentro de <AuthProvider>");
  return ctx;
}
