import { describe, expect, it } from "vitest";
import { blend, HERE, layoutFrames } from "@/components/timelapse-layout";
import { folderKey, milestones } from "@/lib/timelapse";
import type { Frame } from "@/lib/types";

function frame(index: number, dirs: Record<string, number>, langs: Record<string, number> = {}): Frame {
  const files = Object.values(dirs).reduce((a, b) => a + b, 0);
  return { sha: `s${index}`, date: "2024-01-01T00:00:00Z", author: "a", message: "m", index, files, bytes: 0, dirs, langs };
}

describe("folderKey", () => {
  it("cuts directories to a depth", () => {
    expect(folderKey("README.md", 2)).toBe("");
    expect(folderKey("a/b/c/d.ts", 2)).toBe("a/b");
    expect(folderKey("a/d.ts", 3)).toBe("a");
  });
});

describe("milestones", () => {
  it("notes new folders, new languages and big jumps", () => {
    const frames = [
      frame(1, { "": 60 }, { Markdown: 60 }),
      frame(2, { "": 60, src: 80 }, { Markdown: 60, Go: 80 }),
      frame(3, { "": 60, src: 20 }, { Markdown: 60, Go: 20 }),
    ];
    const labels = milestones(frames).map((m) => m.label);
    expect(labels).toContain("src/ appears");
    expect(labels).toContain("First Go code");
    expect(labels).toContain("Grows 133%");
    expect(labels.some((l) => l.startsWith("Big clean-up"))).toBe(true);
  });
});

describe("layout", () => {
  const frames = [frame(1, { "": 2, src: 3 }), frame(2, { "": 2, src: 3, "src/lib": 4, docs: 5 })];
  const layouts = layoutFrames(frames);

  it("separates files sitting next to subfolders", () => {
    expect(layouts[1].has(`src/${HERE}`)).toBe(true);
    expect(layouts[1].get("src")?.leaf).toBe(false);
    expect(layouts[0].get("src")?.leaf).toBe(true);
  });

  it("grows the whole map as the repository grows", () => {
    const span = (m: Map<string, { r: number; depth: number }>) => Math.max(...[...m.values()].filter((p) => p.depth === 1).map((p) => p.r));
    expect(span(layouts[1])).toBeGreaterThan(span(layouts[0]) * 0.9);
  });

  it("blends new circles in from nothing", () => {
    const mid = blend(layouts[0], layouts[1], 0.5);
    const docs = mid.find((p) => p.key === "docs");
    expect(docs?.fresh).toBeCloseTo(0.5);
    expect(docs?.r).toBeCloseTo((layouts[1].get("docs")?.r ?? 0) * 0.5);
  });
});
