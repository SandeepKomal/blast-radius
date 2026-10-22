import { describe, expect, it } from "vitest";
import { extractImports } from "@/lib/imports";
import { areaOf, buildReport, roleOf, type SourceScan } from "@/lib/pr";

type Args = Parameters<typeof buildReport>;

const meta: Args[0] = {
  owner: "acme",
  repo: "shop",
  fullName: "acme/shop",
  description: null,
  defaultBranch: "main",
  stars: 1,
  forks: 0,
  language: "TypeScript",
  createdAt: "2020-01-01T00:00:00Z",
  pushedAt: "2024-01-01T00:00:00Z",
  sizeKb: 10,
  license: "MIT",
  archived: false,
  fork: false,
  ownerAvatar: "",
};

const pull: Args[1] = {
  number: 7,
  title: "Rework sessions",
  user: { login: "dev", avatar_url: "" },
  state: "open",
  merged_at: null,
  draft: false,
  base: { ref: "main", sha: "b" },
  head: { ref: "feat", sha: "h", repo: null },
  created_at: "2024-01-01T00:00:00Z",
  html_url: "https://github.com/acme/shop/pull/7",
  additions: 30,
  deletions: 12,
  commits: 2,
  changed_files: 3,
};

// session.ts is changed; api.ts imports it; page.tsx imports api.ts; a test imports api.ts.
// legacy.ts was deleted but old.ts still imports it.
const source: Record<string, string> = {
  "src/auth/session.ts": "export const s = 1;",
  "src/api.ts": "import { s } from './auth/session';",
  "src/page.tsx": "import './api';",
  "src/api.test.ts": "import './api';",
  "src/old.ts": "import './legacy';",
  "src/unrelated.ts": "export {};",
  "README.md": "",
};

function scan(): SourceScan {
  const imports = new Map<string, string[]>();
  for (const [p, t] of Object.entries(source)) if (p.endsWith(".ts") || p.endsWith(".tsx")) imports.set(p, extractImports(p, t));
  return { paths: Object.keys(source), imports, manifests: new Map(), complete: true, failed: false };
}

const files: Args[2] = [
  { filename: "src/auth/session.ts", status: "modified", additions: 20, deletions: 10, patch: "-export function old() {}\n+export const s = 1;" },
  { filename: "src/legacy.ts", status: "removed", additions: 0, deletions: 2 },
  { filename: "README.md", status: "modified", additions: 10, deletions: 0 },
];

describe("buildReport", () => {
  const report = buildReport(meta, pull, files, scan());

  it("walks the ripple through importers, with depth", () => {
    const byPath = Object.fromEntries(report.impacted.map((i) => [i.path, i]));
    expect(byPath["src/api.ts"]).toMatchObject({ depth: 1, via: "src/auth/session.ts" });
    expect(byPath["src/page.tsx"]).toMatchObject({ depth: 2 });
    expect(byPath["src/api.test.ts"]).toMatchObject({ depth: 2, test: true });
    expect(byPath["src/unrelated.ts"]).toBeUndefined();
    expect(report.impactedTotal).toBe(3);
    expect(report.tests.affected).toEqual(["src/api.test.ts"]);
  });

  it("flags the risks it can see", () => {
    const ids = report.risks.map((r) => r.id);
    expect(ids).toContain("dangling");
    expect(ids).toContain("security");
    expect(ids).toContain("api");
    expect(ids).toContain("untested");
    expect(report.risks.find((r) => r.id === "dangling")?.files).toEqual(["src/old.ts"]);
    expect(report.level).toBe("high");
  });

  it("orders code before docs", () => {
    expect(report.files.map((f) => f.path)).toEqual(["src/auth/session.ts", "src/legacy.ts", "README.md"]);
  });

  it("draws every highlighted file on the map", () => {
    const states = new Map<string, string | undefined>();
    const walk = (n: typeof report.map) => {
      if (n.state) states.set(n.path, n.state);
      n.children?.forEach(walk);
    };
    walk(report.map);
    expect(states.get("src/auth/session.ts")).toBe("changed");
    expect(states.get("src/legacy.ts")).toBe("removed");
    expect(states.get("src/api.ts")).toBe("ripple1");
    expect(report.edges).toContainEqual(["src/api.ts", "src/auth/session.ts"]);
  });

  it("explains itself in plain sentences", () => {
    expect(report.summary[0]).toBe("Changes 3 files (2 modified, 1 removed) in `src`, `src/auth` and `(root)`.");
    expect(report.summary[1]).toBe("The change reaches 3 more files through imports (1 directly), including 1 test.");
  });

  it("says so when the source couldn't be read", () => {
    const r = buildReport(meta, pull, files, { paths: [], imports: new Map(), manifests: new Map(), complete: false, failed: true });
    expect(r.analysis.complete).toBe(false);
    expect(r.analysis.note).toMatch(/download/);
    expect(r.impactedTotal).toBe(0);
  });
});

describe("classification", () => {
  it("assigns roles", () => {
    expect(roleOf(".github/workflows/ci.yml")).toBe("ci");
    expect(roleOf("infra/main.tf")).toBe("ci");
    expect(roleOf("package-lock.json")).toBe("deps");
    expect(roleOf("db/migrations/001_init.sql")).toBe("data");
    expect(roleOf("src/foo_test.go")).toBe("test");
    expect(roleOf("docs/intro.md")).toBe("docs");
    expect(roleOf("tsconfig.json")).toBe("config");
    expect(roleOf("src/index.ts")).toBe("source");
  });

  it("groups monorepo paths into areas", () => {
    expect(areaOf("packages/ui/src/button.tsx")).toBe("packages/ui");
    expect(areaOf("docs/a.md")).toBe("docs");
    expect(areaOf("README.md")).toBe("(root)");
  });
});
