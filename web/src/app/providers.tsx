"use client";

import type { ReactNode } from "react";
import { AuthProvider } from "@/lib/auth-context";
import { QueryProvider } from "@/lib/query";

export function Providers({ children }: { children: ReactNode }) {
  return (
    <QueryProvider>
      <AuthProvider>{children}</AuthProvider>
    </QueryProvider>
  );
}
