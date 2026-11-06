import { describe, expect, it } from "vitest";
import { canRemove, groupContainers, type Container } from "../containers";

const mk = (name: string, project: string, state = "running"): Container => ({
  id: name.padEnd(12, "0"),
  name,
  image: "img",
  state,
  status: "",
  health: "",
  createdAt: "",
  project,
  service: "",
  managed: false,
  ports: [],
});

describe("groupContainers", () => {
  it("projetos em ordem e Avulsos por último", () => {
    const groups = groupContainers([
      mk("solo", ""),
      mk("z-web", "zeta"),
      mk("b-db", "alfa"),
      mk("a-app", "alfa"),
    ]);
    expect(groups.map((g) => g.label)).toEqual(["alfa", "zeta", "Avulsos"]);
    expect(groups[2].project).toBeNull();
    expect(groups[0].project).toBe("alfa");
    expect(groups[0].containers.map((c) => c.name)).toEqual(["a-app", "b-db"]);
  });

  it("lista vazia não gera grupos", () => {
    expect(groupContainers([])).toEqual([]);
  });
});

describe("canRemove", () => {
  it("só exited, created e dead", () => {
    for (const s of ["exited", "created", "dead"]) expect(canRemove(mk("a", "", s))).toBe(true);
    for (const s of ["running", "paused", "restarting", "removing"])
      expect(canRemove(mk("a", "", s))).toBe(false);
  });
});
