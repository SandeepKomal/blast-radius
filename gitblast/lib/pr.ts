// Builds the blast-radius report for one pull request: what it changes, what
// imports the changed code, which tests cover it and what deserves a closer look.

import { api, getRepo, tarball, type RepoMeta } from "./github";
import { extractImports, indexFiles, isManifest, noteManifest, resolveImports, dir } from "./imports";
import { extOf, isTest, langOf, SOURCE_EXTS } from "./lang";
import { readTarGz } from "./tar";
import type { Impacted, Level, MapNode, PRFile, PRReport, Risk, Role } from "./types";

interface RawPull {
  number: number;
  title: string;
  user: { login: string; avatar_url: string } | null;
  state: "open" | "closed";
  merged_at: string | null;
  draft?: boolean;
  base: { ref: string; sha: string };
  head: { ref: string; sha: string; repo: { full_name: string } | null };
  created_at: string;
  html_url: string;
  additions: number;
  deletions: number;
  commits: number;
  changed_files: number;
}

interface RawFile {
  filename: string;
  previous_filename?: string;
  status: "added" | "removed" | "modified" | "renamed" | "copied" | "changed" | "unchanged";
  additions: number;
  deletions: number;
  patch?: string;
}

const MAX_FILE_PAGES = 30; // GitHub lists at most 3,000 files per pull request.
const MAX_SOURCE_BYTES = 768 * 1024;
const MAX_DEPTH = 3;
const MAX_IMPACTED = 2000;
const MAX_MAP_FILES = 600;
const MAX_EDGES = 900;

export async function analyzePR(owner: string, repo: string, number: number): Promise<PRReport> {
  const meta = await getRepo(owner, repo);
  const { data: pull } = await api<RawPull>(`/repos/${meta.owner}/${meta.repo}/pulls/${number}`);

  const rawFiles: RawFile[] = [];
  for (let page = 1; page <= MAX_FILE_PAGES; page++) {
    const { data } = await api<RawFile[]>(`/repos/${meta.owner}/${meta.repo}/pulls/${number}/files?per_page=100&page=${page}`);
    rawFiles.push(...data);
    if (data.length < 100) break;
  }

  const scan = await scanSource(meta, pull, rawFiles);
  return buildReport(meta, pull, rawFiles, scan);
}

export interface SourceScan {
  paths: string[];
  imports: Map<string, string[]>;
  manifests: Map<string, string>;
  complete: boolean;
  failed: boolean;
}

async function scanSource(meta: RepoMeta, pull: RawPull, files: RawFile[]): Promise<SourceScan> {
  const scan: SourceScan = { paths: [], imports: new Map(), manifests: new Map(), complete: true, failed: false };
  let stream: ReadableStream<Uint8Array>;
  try {
    stream = await tarball(meta.owner, meta.repo, pull.head.sha);
  } catch {
    try {
      stream = await tarball(meta.owner, meta.repo, `refs/pull/${pull.number}/head`);
    } catch {
      return { ...scan, complete: false, failed: true };
    }
  }
  // Large repositories are read up to the limits; the report says so.
  const { truncated } = await readTarGz(
    stream,
    { maxCompressed: 150 * 1024 * 1024, maxUnpacked: 700 * 1024 * 1024, maxEntries: 150_000 },
    (path, size) => (SOURCE_EXTS.has(extOf(path)) && size <= MAX_SOURCE_BYTES) || (isManifest(path) && size <= 256 * 1024),
    (f) => {
      scan.paths.push(f.path);
      if (!f.content || f.content.includes(0)) return;
      const text = f.content.toString("utf8");
      if (isManifest(f.path)) scan.manifests.set(f.path, text);
      else scan.imports.set(f.path, extractImports(f.path, text));
    },
  );
  scan.complete = !truncated;
  // Files the PR touches always belong in the map, even past a size limit.
  if (truncated) {
    const seen = new Set(scan.paths);
    for (const f of files) if (f.status !== "removed" && !seen.has(f.filename)) scan.paths.push(f.filename);
  }
  return scan;
}

export function buildReport(meta: RepoMeta, pull: RawPull, rawFiles: RawFile[], scan: SourceScan): PRReport {
  const removed = new Set<string>();
  for (const f of rawFiles) {
    if (f.status === "removed") removed.add(f.filename);
    if (f.status === "renamed" && f.previous_filename) removed.add(f.previous_filename);
  }
  const idx = indexFiles(scan.paths, removed);
  for (const [p, text] of scan.manifests) noteManifest(idx, p, text);

  // importer → imported, and the reverse.
  const deps = new Map<string, string[]>();
  const users = new Map<string, string[]>();
  for (const [file, specs] of scan.imports) {
    const targets = resolveImports(idx, file, specs);
    deps.set(file, targets);
    for (const t of targets) {
      const u = users.get(t);
      if (u) u.push(file);
      else users.set(t, [file]);
    }
  }

  const present = rawFiles.filter((f) => f.status !== "removed").map((f) => f.filename);
  const changed = new Set(present);

  // Walk outwards from the changed files through everything that imports them.
  const impacted = new Map<string, Impacted>();
  let frontier = present.map((p) => ({ path: p, via: p }));
  for (let depth = 1 as 1 | 2 | 3; depth <= MAX_DEPTH && frontier.length > 0; depth++) {
    const next: { path: string; via: string }[] = [];
    for (const { path, via } of frontier) {
      for (const user of users.get(path) ?? []) {
        if (changed.has(user) || impacted.has(user) || removed.has(user)) continue;
        if (impacted.size >= MAX_IMPACTED) break;
        impacted.set(user, { path: user, depth, via, test: isTest(user) });
        next.push({ path: user, via });
      }
    }
    frontier = next;
  }

  // Files still importing something this PR deletes.
  const dangling = new Map<string, string[]>();
  for (const gone of removed) {
    for (const user of users.get(gone) ?? []) {
      if (removed.has(user)) continue;
      const list = dangling.get(gone) ?? [];
      list.push(user);
      dangling.set(gone, list);
    }
  }

  const files = rawFiles.map((f) => toFile(f, changed, users, impacted));
  const order = reviewOrder(files);

  const tests = {
    changed: files.filter((f) => f.role === "test").map((f) => f.path),
    affected: [...impacted.values()].filter((i) => i.test).map((i) => i.path),
  };

  const risks = findRisks(rawFiles, files, impacted, dangling, tests, scan);
  const score = Math.min(100, risks.reduce((s, r) => s + (r.level === "high" ? 26 : r.level === "medium" ? 11 : 4), 0));
  const level: Level = score >= 50 ? "high" : score >= 20 ? "medium" : "low";

  const areas = areaCounts(files, impacted);
  const impactedList = [...impacted.values()].sort((a, b) => a.depth - b.depth || a.path.localeCompare(b.path));
  const { map, edges } = buildMap(scan.paths, files, impactedList, deps, removed);

  const additions = pull.additions;
  const deletions = pull.deletions;
  const state = pull.merged_at ? "merged" : pull.state;

  return {
    repo: { owner: meta.owner, repo: meta.repo, description: meta.description, stars: meta.stars, avatar: meta.ownerAvatar },
    pr: {
      number: pull.number,
      title: pull.title,
      author: pull.user?.login ?? "ghost",
      authorAvatar: pull.user?.avatar_url ?? "",
      state,
      draft: Boolean(pull.draft),
      base: pull.base.ref,
      head: pull.head.ref,
      headSha: pull.head.sha,
      createdAt: pull.created_at,
      url: pull.html_url,
      additions,
      deletions,
      commits: pull.commits,
      changedFiles: pull.changed_files,
    },
    score,
    level,
    summary: summarize(files, areas, impacted, tests, risks, scan),
    files: order,
    impacted: impactedList.slice(0, 400),
    impactedTotal: impacted.size,
    tests: { changed: tests.changed, affected: tests.affected.slice(0, 200) },
    risks,
    areas,
    map,
    edges,
    analysis: {
      complete: scan.complete && !scan.failed,
      filesScanned: scan.imports.size,
      note: scan.failed
        ? "GitHub didn't let us download the source, so the ripple effect is missing. The changed files are still shown."
        : scan.complete
          ? undefined
          : "This repository is very large, so only part of it was read. The ripple effect may be bigger than shown.",
    },
    generatedAt: new Date().toISOString(),
  };
}

// ---------------------------------------------------------------------------

export function roleOf(path: string): Role {
  const p = path.toLowerCase();
  const b = p.slice(p.lastIndexOf("/") + 1);
  if (isTest(path)) return "test";
  if (/^\.github\/workflows\/|(^|\/)(\.gitlab-ci\.yml|jenkinsfile|azure-pipelines\.yml|\.circleci\/|dockerfile|docker-compose[^/]*\.ya?ml|compose\.ya?ml)$|\.tf$|(^|\/)(charts|helm|k8s|kubernetes|deploy|deployment|infra)\//.test(p)) return "ci";
  if (/^(package\.json|package-lock\.json|yarn\.lock|pnpm-lock\.yaml|bun\.lockb?|go\.mod|go\.sum|cargo\.toml|cargo\.lock|gemfile|gemfile\.lock|pyproject\.toml|poetry\.lock|pipfile|pipfile\.lock|uv\.lock|composer\.json|composer\.lock|pom\.xml|build\.gradle(\.kts)?|requirements[^/]*\.txt|pubspec\.ya?ml|mix\.exs|mix\.lock)$/.test(b)) return "deps";
  if (/(^|\/)(migrations?|migrate|schema)\/|\.sql$|(^|\/)schema\.(prisma|graphql|rb)$/.test(p)) return "data";
  if (/\.(md|mdx|rst|txt|adoc)$|^docs?\//.test(p) || /^(license|changelog|readme)/.test(b)) return "docs";
  if (/\.(png|jpe?g|gif|svg|ico|webp|woff2?|ttf|mp4|lottie)$/.test(p)) return "asset";
  if (/\.(json|ya?ml|toml|ini|conf|cfg|env|properties)$|(^|\/)\.env|\.config\.[a-z]+$|^\.[^/]+rc(\.[a-z]+)?$/.test(b)) return "config";
  return "source";
}

function toFile(f: RawFile, changed: Set<string>, users: Map<string, string[]>, impacted: Map<string, Impacted>): PRFile {
  const status = f.status === "removed" ? "removed" : f.status === "added" || f.status === "copied" ? "added" : f.status === "renamed" ? "renamed" : "modified";
  const lang = langOf(f.filename);
  let reach = 0;
  for (const i of impacted.values()) if (i.via === f.filename) reach++;
  return {
    path: f.filename,
    previous: f.previous_filename,
    status,
    additions: f.additions,
    deletions: f.deletions,
    role: roleOf(f.filename),
    lang: lang.name,
    color: lang.color,
    usedBy: status === "removed" ? 0 : usersWithin(users, f.filename, changed),
    reach,
  };
}

/** How many files inside the PR import `path`, directly or through each other. */
function usersWithin(users: Map<string, string[]>, path: string, within: Set<string>): number {
  const seen = new Set<string>([path]);
  const stack = [path];
  while (stack.length) {
    const cur = stack.pop() as string;
    for (const u of users.get(cur) ?? []) {
      if (within.has(u) && !seen.has(u)) {
        seen.add(u);
        stack.push(u);
      }
    }
  }
  return seen.size - 1;
}

const ROLE_ORDER: Record<Role, number> = { data: 0, source: 1, test: 3, deps: 4, ci: 5, config: 6, docs: 7, asset: 8 };

/** Foundations first: files the rest of the PR builds on, then their users, then tests and the rest. */
export function reviewOrder(files: PRFile[]): PRFile[] {
  return [...files].sort(
    (a, b) =>
      ROLE_ORDER[a.role] - ROLE_ORDER[b.role] ||
      b.usedBy - a.usedBy ||
      b.reach - a.reach ||
      b.additions + b.deletions - (a.additions + a.deletions) ||
      a.path.localeCompare(b.path),
  );
}

const SECURITY = /(^|[/_.-])(auth\w*|login|logout|session|token|oauth|jwt|saml|sso|crypt\w*|password|passwd|secret|permission|rbac|acl|csrf|xss|sanitiz\w*|security|credential|signup|signin)([/_.-]|$)/i;

function findRisks(
  raw: RawFile[],
  files: PRFile[],
  impacted: Map<string, Impacted>,
  dangling: Map<string, string[]>,
  tests: { changed: string[]; affected: string[] },
  scan: SourceScan,
): Risk[] {
  const risks: Risk[] = [];
  const add = (r: Risk) => risks.push({ ...r, files: r.files.slice(0, 30) });

  if (dangling.size > 0) {
    const users = [...new Set([...dangling.values()].flat())];
    add({
      id: "dangling",
      level: "high",
      title: "Deleted files are still imported",
      detail: `${plural(users.length, "file")} still seem${users.length === 1 ? "s" : ""} to import ${plural(dangling.size, "file")} this PR deletes or renames. That usually breaks the build.`,
      files: users,
    });
  }

  const security = files.filter((f) => SECURITY.test(f.path) && f.role !== "docs" && f.role !== "test");
  if (security.length > 0) {
    add({
      id: "security",
      level: security.some((f) => f.role === "source") ? "high" : "medium",
      title: "Touches security-sensitive code",
      detail: "Authentication, sessions, tokens, permissions or cryptography. Worth a reviewer who knows that area.",
      files: security.map((f) => f.path),
    });
  }

  const data = files.filter((f) => f.role === "data");
  if (data.length > 0) {
    add({
      id: "data",
      level: "high",
      title: "Changes the database schema or migrations",
      detail: "Migrations are hard to undo once they run in production. Check they are backwards compatible.",
      files: data.map((f) => f.path),
    });
  }

  const exported = raw.filter((f) => f.patch && removesExport(f.filename, f.patch)).map((f) => f.filename);
  if (exported.length > 0) {
    add({
      id: "api",
      level: "medium",
      title: "Changes or removes exported code",
      detail: "Exported functions, types or classes were edited or removed. Callers outside this repository may break.",
      files: exported,
    });
  }

  const ci = files.filter((f) => f.role === "ci");
  if (ci.length > 0) {
    add({
      id: "ci",
      level: "medium",
      title: "Changes CI, containers or infrastructure",
      detail: "Pipelines, Dockerfiles, Terraform or Kubernetes files. These change how code is built or deployed.",
      files: ci.map((f) => f.path),
    });
  }

  const deps = files.filter((f) => f.role === "deps");
  if (deps.length > 0) {
    add({
      id: "deps",
      level: "medium",
      title: "Changes dependencies",
      detail: "New or updated packages bring in code nobody on the project wrote. Check what changed in the lockfile.",
      files: deps.map((f) => f.path),
    });
  }

  const source = files.filter((f) => f.role === "source" && f.status !== "removed" && SOURCE_EXTS.has(extOf(f.path)));
  if (source.length > 0 && tests.changed.length === 0) {
    add({
      id: "untested",
      level: tests.affected.length === 0 && scan.imports.size > 0 ? "medium" : "low",
      title: "No tests changed",
      detail:
        tests.affected.length > 0
          ? `The code changed but no tests did. ${plural(tests.affected.length, "existing test")} import${tests.affected.length === 1 ? "s" : ""} it, so run those at least.`
          : "The code changed but no tests did, and no existing test imports it.",
      files: source.map((f) => f.path),
    });
  }

  if (impacted.size >= 20) {
    add({
      id: "wide",
      level: impacted.size >= 100 ? "high" : "medium",
      title: "Wide ripple effect",
      detail: `${plural(impacted.size, "other file")} import the changed code, directly or indirectly.`,
      files: [...impacted.values()].filter((i) => i.depth === 1).map((i) => i.path),
    });
  }

  const lines = raw.reduce((s, f) => s + f.additions + f.deletions, 0);
  if (lines > 1000 || raw.length > 50) {
    add({
      id: "size",
      level: lines > 3000 ? "medium" : "low",
      title: "Large pull request",
      detail: `${lines.toLocaleString("en-US")} lines across ${plural(raw.length, "file")}. Large PRs get shallower reviews; consider splitting it.`,
      files: [],
    });
  }

  const order: Record<Level, number> = { high: 0, medium: 1, low: 2 };
  return risks.sort((a, b) => order[a.level] - order[b.level]);
}

const EXPORT_LINE: Record<string, RegExp> = {
  js: /^-\s*export\s|^-\s*module\.exports/,
  py: /^-(def|class|async def)\s+[A-Za-z]\w*/,
  go: /^-(func(\s+\([^)]*\))?\s+[A-Z]|type\s+[A-Z]|var\s+[A-Z]|const\s+[A-Z])/,
  rs: /^-\s*pub\s+(fn|struct|enum|trait|type|const|mod)\s/,
  jvm: /^-\s*public\s/,
};

function removesExport(path: string, patch: string): boolean {
  const ext = extOf(path);
  const re =
    ["ts", "tsx", "js", "jsx", "mjs", "cjs", "mts", "cts"].includes(ext) ? EXPORT_LINE.js
      : ext === "py" ? EXPORT_LINE.py
        : ext === "go" ? EXPORT_LINE.go
          : ext === "rs" ? EXPORT_LINE.rs
            : ["java", "kt", "scala", "cs"].includes(ext) ? EXPORT_LINE.jvm
              : null;
  if (!re || isTest(path)) return false;
  return patch.split("\n").some((line) => !line.startsWith("---") && re.test(line));
}

/** Groups paths into areas: the top folder, or two levels for monorepo layouts. */
export function areaOf(path: string): string {
  const parts = path.split("/");
  if (parts.length === 1) return "(root)";
  if (/^(src|packages|apps|libs|lib|internal|pkg|crates|services|modules|components|cmd)$/.test(parts[0]) && parts.length > 2) {
    return `${parts[0]}/${parts[1]}`;
  }
  return parts[0];
}

function areaCounts(files: PRFile[], impacted: Map<string, Impacted>) {
  const m = new Map<string, { name: string; changed: number; impacted: number }>();
  const bump = (p: string, key: "changed" | "impacted") => {
    const a = areaOf(p);
    const e = m.get(a) ?? { name: a, changed: 0, impacted: 0 };
    e[key]++;
    m.set(a, e);
  };
  for (const f of files) bump(f.path, "changed");
  for (const i of impacted.keys()) bump(i, "impacted");
  return [...m.values()].sort((a, b) => b.changed - a.changed || b.impacted - a.impacted);
}

function summarize(
  files: PRFile[],
  areas: { name: string; changed: number }[],
  impacted: Map<string, Impacted>,
  tests: { changed: string[]; affected: string[] },
  risks: Risk[],
  scan: SourceScan,
): string[] {
  const out: string[] = [];
  const touched = areas.filter((a) => a.changed > 0).map((a) => a.name);
  const where = touched.length <= 3 ? list(touched.map((a) => `\`${a}\``)) : `${list(touched.slice(0, 3).map((a) => `\`${a}\``))} and ${touched.length - 3} more`;
  const counts = (["added", "modified", "renamed", "removed"] as const)
    .map((s) => [s, files.filter((f) => f.status === s).length] as const)
    .filter(([, n]) => n > 0)
    .map(([s, n]) => `${n} ${s}`);
  out.push(`Changes ${plural(files.length, "file")} (${counts.join(", ")}) in ${where}.`);

  if (scan.failed) {
    out.push("The ripple effect couldn't be worked out because the source couldn't be downloaded.");
  } else if (impacted.size === 0) {
    out.push(scan.imports.size > 0 ? "Nothing else in the repository imports the changed code, so the blast radius stays inside this PR." : "None of the changed files are code gitblast can follow imports for.");
  } else {
    const direct = [...impacted.values()].filter((i) => i.depth === 1).length;
    const t = tests.affected.length;
    out.push(
      `The change reaches ${plural(impacted.size, "more file")} through imports (${direct} directly)${t > 0 ? `, including ${plural(t, "test")}` : ""}.`,
    );
  }

  const top = risks.find((r) => r.level === "high") ?? risks.find((r) => r.level === "medium");
  if (top) out.push(`Biggest concern: ${top.title}.`);
  else out.push("Nothing stands out as risky.");
  return out;
}

// ---------------------------------------------------------------------------
// The circle map: the whole repository as nested folders, with PR files drawn
// individually and everything else folded into its folder.

function buildMap(
  allPaths: string[],
  files: PRFile[],
  impacted: Impacted[],
  deps: Map<string, string[]>,
  removed: Set<string>,
): { map: MapNode; edges: [string, string][] } {
  const state = new Map<string, MapNode["state"]>();
  for (const f of files) state.set(f.path, f.status === "removed" ? "removed" : f.status === "added" ? "added" : "changed");
  for (const i of impacted) {
    if (state.size >= MAX_MAP_FILES) break;
    state.set(i.path, i.depth === 1 ? "ripple1" : i.depth === 2 ? "ripple2" : "ripple3");
  }

  const total = Math.max(allPaths.length, 1);
  // Highlighted files get a minimum size so they stay visible in big repositories.
  const weight = (s: MapNode["state"]) =>
    Math.max(1, total * (s === "ripple2" || s === "ripple3" ? 0.0012 : s?.startsWith("ripple") ? 0.002 : 0.004));

  interface Dir { name: string; path: string; dirs: Map<string, Dir>; loose: number; total: number; marked: string[] }
  const root: Dir = { name: "", path: "", dirs: new Map(), loose: 0, total: 0, marked: [] };
  const getDir = (p: string): Dir => {
    let d = root;
    if (!p) return d;
    let acc = "";
    for (const part of p.split("/")) {
      acc = acc ? `${acc}/${part}` : part;
      let next = d.dirs.get(part);
      if (!next) {
        next = { name: part, path: acc, dirs: new Map(), loose: 0, total: 0, marked: [] };
        d.dirs.set(part, next);
      }
      d = next;
    }
    return d;
  };
  const all = new Set(allPaths);
  for (const p of removed) all.add(p);
  for (const p of all) {
    const d = getDir(dir(p));
    if (state.has(p)) d.marked.push(p);
    else d.loose++;
  }
  const count = (d: Dir): number => {
    d.total = d.loose + d.marked.length;
    for (const c of d.dirs.values()) d.total += count(c);
    return d.total;
  };
  count(root);
  const hasMarked = (d: Dir): boolean => d.marked.length > 0 || [...d.dirs.values()].some(hasMarked);

  const toNode = (d: Dir, depth: number): MapNode => {
    const children: MapNode[] = [];
    let folded = d.loose;
    for (const c of [...d.dirs.values()].sort((a, b) => b.total - a.total)) {
      if (hasMarked(c)) children.push(toNode(c, depth + 1));
      else if (depth < 2) children.push({ name: c.name, path: c.path, value: c.total, files: c.total });
      else folded += c.total;
    }
    for (const m of d.marked) {
      const s = state.get(m);
      const lang = langOf(m);
      children.push({ name: m.slice(m.lastIndexOf("/") + 1), path: m, value: weight(s), files: 1, state: s, color: lang.color });
    }
    if (folded > 0) children.push({ name: "other files", path: `${d.path}/…`, value: folded, files: folded });
    return { name: d.name || "/", path: d.path, children };
  };
  const map = toNode(root, 0);

  const edges: [string, string][] = [];
  for (const i of impacted) {
    if (edges.length >= MAX_EDGES) break;
    if (!state.has(i.path)) continue;
    for (const t of deps.get(i.path) ?? []) {
      if (state.has(t) && t !== i.path) edges.push([i.path, t]);
    }
  }
  return { map, edges };
}

function plural(n: number, word: string): string {
  return `${n.toLocaleString("en-US")} ${word}${n === 1 ? "" : "s"}`;
}

function list(items: string[]): string {
  if (items.length <= 1) return items.join("");
  return `${items.slice(0, -1).join(", ")} and ${items[items.length - 1]}`;
}

