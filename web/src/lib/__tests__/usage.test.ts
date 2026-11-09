import { describe, expect, test } from "vitest";
import { keepNewerUsage, mergeUsageLive, type UsageOwner, type UsageSnapshot } from "../usage";

const owner = (key: string, over: Partial<UsageOwner> = {}): UsageOwner => ({
  key,
  kind: "service",
  label: key,
  cpuPercent: 1,
  memBytes: 100,
  procCount: 1,
  truncated: false,
  processes: [{ pid: 1, name: key, cmd: key, user: "root", cpuPercent: 1, memBytes: 100 }],
  ...over,
});

const live = (o: UsageOwner) => {
  const { processes: _p, truncated: _t, ...rest } = o;
  void _p;
  void _t;
  return rest;
};

describe("mergeUsageLive", () => {
  const prev: UsageSnapshot = {
    time: "2026-11-08T12:00:00Z",
    dockerOk: true,
    owners: [owner("a", { truncated: true }), owner("b")],
  };

  test("mantém processos de quem continua, remove quem saiu e cria o novo sem processos", () => {
    const next = mergeUsageLive(prev, {
      time: "2026-11-08T12:00:05Z",
      dockerOk: false,
      owners: [live(owner("a", { cpuPercent: 42 })), live(owner("c"))],
    });

    expect(next.time).toBe("2026-11-08T12:00:05Z");
    expect(next.dockerOk).toBe(false);
    expect(next.owners.map((o) => o.key)).toEqual(["a", "c"]);

    const a = next.owners[0];
    expect(a.cpuPercent).toBe(42);
    expect(a.truncated).toBe(true);
    expect(a.processes).toEqual(prev.owners[0].processes);

    const c = next.owners[1];
    expect(c.processes).toEqual([]);
    expect(c.truncated).toBe(false);
  });

  test("sem retrato anterior, todos entram sem processos", () => {
    const next = mergeUsageLive(undefined, {
      time: "2026-11-08T12:00:05Z",
      dockerOk: true,
      owners: [live(owner("a"))],
    });
    expect(next.owners[0].processes).toEqual([]);
  });
});

describe("keepNewerUsage", () => {
  const fetched: UsageSnapshot = {
    time: "2026-11-08T12:00:00Z",
    dockerOk: true,
    owners: [owner("a", { cpuPercent: 10 }), owner("old")],
  };

  test("sem cache, fica a resposta REST", () => {
    expect(keepNewerUsage(undefined, fetched)).toBe(fetched);
  });

  test("REST mais nova que o cache substitui o cache", () => {
    const cached: UsageSnapshot = { time: "2026-11-07T23:59:55Z", dockerOk: false, owners: [] };
    expect(keepNewerUsage(cached, fetched)).toBe(fetched);
  });

  test("REST atrasada não sobrescreve o tópico mais novo, mas traz os processos", () => {
    const cached: UsageSnapshot = {
      time: "2026-11-08T12:00:05Z",
      dockerOk: false,
      owners: [owner("a", { cpuPercent: 77, processes: [], truncated: false }), owner("novo", { processes: [] })],
    };
    const out = keepNewerUsage(cached, { ...fetched, owners: [owner("a", { cpuPercent: 10, truncated: true }), owner("old")] });

    expect(out.time).toBe("2026-11-08T12:00:05Z");
    expect(out.dockerOk).toBe(false);
    expect(out.owners.map((o) => o.key)).toEqual(["a", "novo"]);
    expect(out.owners[0].cpuPercent).toBe(77);
    expect(out.owners[0].processes).toEqual(fetched.owners[0].processes);
    expect(out.owners[0].truncated).toBe(true);
    expect(out.owners[1].processes).toEqual([]);
  });
});
