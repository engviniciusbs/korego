import type { NextConfig } from "next";

// Em produção o app vira um export estático embutido no binário Go.
// Em dev o Go faz proxy para o `next dev`, por isso não há rewrites aqui.
const isProd = process.env.NODE_ENV === "production";

const nextConfig: NextConfig = {
  ...(isProd ? { output: "export" as const } : {}),
  trailingSlash: true,
  images: { unoptimized: true },
  turbopack: {
    rules: {
      "*.css": {
        loaders: ["@tailwindcss/turbopack"],
        as: "*.css",
      },
    },
  },
};

export default nextConfig;
