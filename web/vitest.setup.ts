import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Espelha `trailingSlash: true` do next.config: o <Link> lê esta variável em tempo de execução.
process.env.__NEXT_TRAILING_SLASH = "true";

afterEach(() => cleanup());
