import { describe, expect, it } from "vitest";
import { parseInput, parseSegments, routePath } from "@/lib/route";

describe("parseSegments", () => {
  it("reads repositories and pull requests", () => {
    expect(parseSegments(["vercel", "next.js"])).toEqual({ kind: "repo", owner: "vercel", repo: "next.js" });
    expect(parseSegments(["a", "b", "pull", "42", "files"])).toEqual({ kind: "pr", owner: "a", repo: "b", number: 42 });
  });

  it("treats any other GitHub page of a repository as the repository", () => {
    expect(parseSegments(["a", "b", "tree", "main", "src"])).toEqual({ kind: "repo", owner: "a", repo: "b" });
    expect(parseSegments(["a", "b.git"])).toEqual({ kind: "repo", owner: "a", repo: "b" });
  });

  it("accepts a full GitHub link pasted after the domain", () => {
    expect(parseSegments(["https:", "", "github.com", "a", "b", "pull", "7"])).toEqual({ kind: "pr", owner: "a", repo: "b", number: 7 });
    expect(parseSegments(["github.com", "a", "b"])).toEqual({ kind: "repo", owner: "a", repo: "b" });
  });

  it("rejects names GitHub would not allow", () => {
    expect(parseSegments(["a"])).toBeNull();
    expect(parseSegments(["-a", "b"])).toBeNull();
    expect(parseSegments(["a", ".."])).toBeNull();
    expect(parseSegments(["a", "b c"])).toBeNull();
    expect(parseSegments(["a", "b", "pull", "0"])).toEqual({ kind: "repo", owner: "a", repo: "b" });
  });
});

describe("parseInput", () => {
  it("understands what people paste", () => {
    expect(parseInput("https://github.com/a/b/pull/12?diff=split")).toEqual({ kind: "pr", owner: "a", repo: "b", number: 12 });
    expect(parseInput("  a/b#9 ")).toEqual({ kind: "pr", owner: "a", repo: "b", number: 9 });
    expect(parseInput("gitblast.com/a/b")).toEqual({ kind: "repo", owner: "a", repo: "b" });
    expect(parseInput("a/b/issues#readme")).toEqual({ kind: "repo", owner: "a", repo: "b" });
    expect(parseInput("hello")).toBeNull();
  });

  it("round-trips through routePath", () => {
    expect(routePath({ kind: "pr", owner: "a", repo: "b", number: 3 })).toBe("/a/b/pull/3");
  });
});
