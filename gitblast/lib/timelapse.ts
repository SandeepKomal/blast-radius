// Samples a repository's history at evenly spaced commits and records the
// shape of the tree at each one, so the page can animate how it grew.

import { api, getRepo, GitHubError, mapLimit } from "./github";
import { langOf } from "./lang";
import type { Frame, Timelapse } from "./types";

interface RawCommit {
  sha: string;
  commit: {
    message: string;
    author: { name: string; date: string } | null;
    committer: { date: string } | null;
    tree: { sha: string };
  };
  author: { login: string; avatar_url: string } | null;
}

interface RawTree {
  tree: { path: string; type: "blob" | "tree" | "commit"; size?: number }[];
  truncated: boolean;
}

interface RawContributor {
  login?: string;
  avatar_url?: string;
  contributions: number;
  type: string;
}

/** Folders deeper than this are folded into their parent, to keep frames small. */
const DEPTHS = [3, 2, 1];
const MAX_KEYS = 900;

export async function analyzeTimelapse(owner: string, repo: string): Promise<Timelapse> {
  const meta = await getRepo(owner, repo);
  const base = `/repos/${meta.owner}/${meta.repo}`;
  const branch = encodeURIComponent(meta.defaultBranch);

  // With one commit per page, the last page number is the commit count.
  const first = await api<RawCommit[]>(`${base}/commits?sha=${branch}&per_page=1`);
  const last = first.headers.get("link")?.match(/[?&]page=(\d+)>;\s*rel="last"/);
  const total = last ? Number(last[1]) : first.data.length;
  if (total === 0) throw new GitHubError("not_found", "This repository has no commits yet.", 404);

  const latest = first.data[0];
  const latestTree = await api<RawTree>(`${base}/git/trees/${latest.commit.tree.sha}?recursive=1`, 25_000);
  const size = latestTree.data.tree.length;
  const frameCount = Math.min(total, size > 40_000 ? 14 : size > 15_000 ? 22 : 36);

  // Page k (newest = 1) of a one-per-page listing is the k-th newest commit.
  const pages: number[] = [];
  for (let i = 0; i < frameCount; i++) {
    const fromOldest = frameCount === 1 ? 0 : Math.round((i * (total - 1)) / (frameCount - 1));
    pages.push(total - fromOldest);
  }
  const unique = [...new Set(pages)];

  const commits = await mapLimit(unique, 6, async (page) => {
    if (page === 1) return latest;
    try {
      const { data } = await api<RawCommit[]>(`${base}/commits?sha=${branch}&per_page=1&page=${page}`);
      return data[0] ?? null;
    } catch {
      return null;
    }
  });

  let partial = latestTree.data.truncated;
  const trees = await mapLimit(commits, 4, async (c) => {
    if (!c) return null;
    if (c.sha === latest.sha) return latestTree.data;
    try {
      return (await api<RawTree>(`${base}/git/trees/${c.commit.tree.sha}?recursive=1`, 25_000)).data;
    } catch {
      return null;
    }
  });

  const depth = pickDepth(latestTree.data);
  const frames: Frame[] = [];
  const langColors: Record<string, string> = {};
  for (let i = 0; i < commits.length; i++) {
    const c = commits[i];
    const t = trees[i];
    if (!c || !t) {
      partial = true;
      continue;
    }
    if (t.truncated) partial = true;
    frames.push(toFrame(c, t, total - unique[i] + 1, depth, langColors));
  }
  frames.sort((a, b) => a.index - b.index);

  let contributors: Timelapse["contributors"] = [];
  try {
    const { data } = await api<RawContributor[]>(`${base}/contributors?per_page=12`);
    contributors = data
      .filter((c) => c.type !== "Bot" && c.login && c.avatar_url)
      .slice(0, 10)
      .map((c) => ({ login: c.login as string, avatar: c.avatar_url as string, contributions: c.contributions }));
  } catch {
    // Very large repositories can't list contributors; the page copes without.
  }

  return {
    repo: {
      owner: meta.owner,
      repo: meta.repo,
      description: meta.description,
      stars: meta.stars,
      avatar: meta.ownerAvatar,
      defaultBranch: meta.defaultBranch,
      createdAt: meta.createdAt,
      language: meta.language,
      license: meta.license,
      forks: meta.forks,
    },
    totalCommits: total,
    frames,
    milestones: milestones(frames),
    contributors,
    langColors,
    depth,
    partial,
    generatedAt: new Date().toISOString(),
  };
}

function pickDepth(tree: RawTree): number {
  for (const d of DEPTHS) {
    const keys = new Set<string>();
    for (const e of tree.tree) if (e.type === "blob") keys.add(folderKey(e.path, d));
    if (keys.size <= MAX_KEYS) return d;
  }
  return 1;
}

/** The folder a file is counted under: its directory cut to `depth` levels. */
export function folderKey(path: string, depth: number): string {
  const parts = path.split("/");
  parts.pop();
  if (parts.length === 0) return "";
  return parts.slice(0, depth).join("/");
}

function toFrame(c: RawCommit, t: RawTree, index: number, depth: number, colors: Record<string, string>): Frame {
  const dirs: Record<string, number> = {};
  const langs: Record<string, number> = {};
  let files = 0;
  let bytes = 0;
  for (const e of t.tree) {
    if (e.type !== "blob") continue;
    files++;
    bytes += e.size ?? 0;
    const key = folderKey(e.path, depth);
    dirs[key] = (dirs[key] ?? 0) + 1;
    const lang = langOf(e.path);
    langs[lang.name] = (langs[lang.name] ?? 0) + 1;
    colors[lang.name] = lang.color;
  }
  return {
    sha: c.sha,
    date: c.commit.author?.date ?? c.commit.committer?.date ?? "",
    author: c.author?.login ?? c.commit.author?.name ?? "unknown",
    avatar: c.author?.avatar_url,
    message: c.commit.message.split("\n")[0].slice(0, 140),
    index,
    files,
    bytes,
    dirs,
    langs,
  };
}

const QUIET_LANGS = new Set(["Other", "Config", "Markdown", "Image"]);

export function milestones(frames: Frame[]): { frame: number; label: string }[] {
  const out: { frame: number; label: string }[] = [];
  const seenTop = new Set<string>();
  const seenLang = new Set<string>();
  frames.forEach((f, i) => {
    const tops = new Set(Object.keys(f.dirs).map((d) => d.split("/")[0]).filter(Boolean));
    const langs = Object.keys(f.langs).filter((l) => !QUIET_LANGS.has(l) && f.langs[l] >= 3);
    if (i > 0) {
      const newTops = [...tops].filter((t) => !seenTop.has(t));
      if (newTops.length > 0 && newTops.length <= 3) out.push({ frame: i, label: `${newTops.map((t) => `${t}/`).join(", ")} appears` });
      for (const l of langs) if (!seenLang.has(l)) out.push({ frame: i, label: `First ${l} code` });
      const prev = frames[i - 1];
      if (prev.files > 50 && f.files > prev.files * 1.6) out.push({ frame: i, label: `Grows ${Math.round((f.files / prev.files - 1) * 100)}%` });
      if (prev.files > 50 && f.files < prev.files * 0.7) out.push({ frame: i, label: `Big clean-up (−${Math.round((1 - f.files / prev.files) * 100)}%)` });
    }
    tops.forEach((t) => seenTop.add(t));
    langs.forEach((l) => seenLang.add(l));
  });
  return out.slice(0, 14);
}
