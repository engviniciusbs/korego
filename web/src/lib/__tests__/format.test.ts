import { describe, expect, it } from "vitest";
import {
  describeUserAgent,
  formatBytes,
  formatDateTime,
  formatGiB,
  formatPercent,
  formatRate,
  formatUptime,
} from "../format";

describe("formatUptime", () => {
  it("formata dias, horas e minutos", () => {
    expect(formatUptime(3 * 86400 + 4 * 3600 + 12 * 60 + 59)).toBe("3d 4h 12m");
  });

  it("omite unidades maiores zeradas", () => {
    expect(formatUptime(4 * 3600 + 5 * 60)).toBe("4h 5m");
    expect(formatUptime(42 * 60)).toBe("42m");
  });

  it("mantém zeros intermediários", () => {
    expect(formatUptime(2 * 86400 + 7 * 60)).toBe("2d 0h 7m");
  });

  it("menos de um minuto vira 0m; valores inválidos também", () => {
    expect(formatUptime(30)).toBe("0m");
    expect(formatUptime(-5)).toBe("0m");
    expect(formatUptime(Number.NaN)).toBe("0m");
  });
});

describe("formatGiB", () => {
  it("converte bytes para GiB com uma casa decimal em pt-BR", () => {
    expect(formatGiB(8 * 1024 ** 3)).toBe("8,0 GiB");
    expect(formatGiB(1.5 * 1024 ** 3)).toBe("1,5 GiB");
  });
});

describe("formatDateTime", () => {
  it("formata no padrão pt-BR", () => {
    expect(formatDateTime("2026-11-03T14:05:00Z", "UTC")).toBe("03/11/2026, 14:05");
  });

  it("devolve o texto original para datas inválidas", () => {
    expect(formatDateTime("nope")).toBe("nope");
  });
});

describe("describeUserAgent", () => {
  it("identifica navegador e sistema", () => {
    expect(
      describeUserAgent(
        "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
      ),
    ).toBe("Chrome · Windows");
    expect(
      describeUserAgent("Mozilla/5.0 (X11; Linux x86_64; rv:132.0) Gecko/20100101 Firefox/132.0"),
    ).toBe("Firefox · Linux");
    expect(
      describeUserAgent(
        "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15",
      ),
    ).toBe("Safari · macOS");
    expect(describeUserAgent("Mozilla/5.0 Chrome/130 Edg/130.0")).toBe("Edge");
  });

  it("usa o texto bruto ou um rótulo quando não reconhece", () => {
    expect(describeUserAgent("curl/8.4.0")).toBe("curl/8.4.0");
    expect(describeUserAgent("")).toBe("Desconhecido");
  });
});

describe("formatBytes, formatRate e formatPercent", () => {
  it("formatBytes usa unidades binárias com 1 casa", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1536)).toBe("1,5 KiB");
    expect(formatBytes(5 * 1024 ** 3)).toBe("5,0 GiB");
    expect(formatBytes(2 * 1024 ** 4)).toBe("2,0 TiB");
  });

  it("formatRate e formatPercent em pt-BR", () => {
    expect(formatRate(1.5 * 1024 ** 2)).toBe("1,5 MiB/s");
    expect(formatPercent(12.34)).toBe("12,3%");
    expect(formatPercent(0)).toBe("0,0%");
  });
});
