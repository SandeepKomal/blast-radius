// The only place gitblast talks to GitHub. Every request is read-only, carries
// an identifying User-Agent and has a timeout.

import { validOwner, validRepo } from "./route";

export type ErrorCode = "not_found" | "rate_limited" | "unavailable" | "too_large" | "upstream";

export class GitHubError extends Error {
  constructor(
    public code: ErrorCode,
    message: string,
    public status = 502,
    public retryAfter?: number,
  ) {
    super(message);
  }
}

const API = "https://api.github.com";
const UA = "gitblast/0.1 (open-source repository visualiser)";

function headers(accept = "application/vnd.github+json"): HeadersInit {
  const h: Record<string, string> = {
    Accept: accept,
    "User-Agent": UA,
    "X-GitHub-Api-Version": "2022-11-28",
  };
  const token = process.env.GITHUB_TOKEN;
  if (token) h.Authorization = `Bearer ${token}`;
  return h;
}

export async function api<T>(path: string, timeoutMs = 15_000): Promise<{ data: T; headers: Headers }> {
  let res: Response;
  try {
    res = await fetch(API + path, { headers: headers(), signal: AbortSignal.timeout(timeoutMs), cache: "no-store" });
  } catch {
    throw new GitHubError("upstream", "GitHub didn't answer in time. Try again in a moment.");
  }
  if (res.ok) return { data: (await res.json()) as T, headers: res.headers };

  if ((res.status === 403 || res.status === 429) && res.headers.get("x-ratelimit-remaining") === "0") {
    const reset = Number(res.headers.get("x-ratelimit-reset") ?? 0) * 1000;
    const retry = Math.max(60, Math.ceil((reset - Date.now()) / 1000));
    throw new GitHubError("rate_limited", "gitblast is popular right now and has used its GitHub quota.", 429, retry);
  }
  if (res.status === 404 || res.status === 451) {
    throw new GitHubError("not_found", "That repository or pull request doesn't exist, or isn't public.", 404);
  }
  if (res.status === 409) throw new GitHubError("not_found", "This repository is empty.", 404);
  if (res.status === 403 && res.headers.get("retry-after")) {
    throw new GitHubError("rate_limited", "GitHub asked us to slow down.", 429, Number(res.headers.get("retry-after")));
  }
  throw new GitHubError("upstream", `GitHub answered ${res.status}. Try again in a moment.`);
}

/** Streams a source tarball. The caller enforces size limits while reading. */
export async function tarball(owner: string, repo: string, ref: string): Promise<ReadableStream<Uint8Array>> {
  const url = `https://codeload.github.com/${owner}/${repo}/tar.gz/${encodeURIComponent(ref).replace(/%2F/g, "/")}`;
  let res: Response;
  try {
    res = await fetch(url, { headers: { "User-Agent": UA }, signal: AbortSignal.timeout(40_000), cache: "no-store" });
  } catch {
    throw new GitHubError("upstream", "Downloading the source from GitHub timed out.");
  }
  if (!res.ok || !res.body) {
    throw new GitHubError(res.status === 404 ? "not_found" : "upstream", `Couldn't download the source (${res.status}).`);
  }
  return res.body;
}

export interface RepoMeta {
  owner: string;
  repo: string;
  fullName: string;
  description: string | null;
  defaultBranch: string;
  stars: number;
  forks: number;
  language: string | null;
  createdAt: string;
  pushedAt: string;
  sizeKb: number;
  license: string | null;
  archived: boolean;
  fork: boolean;
  ownerAvatar: string;
}

interface RawRepo {
  name: string;
  full_name: string;
  private: boolean;
  visibility?: string;
  description: string | null;
  default_branch: string;
  stargazers_count: number;
  forks_count: number;
  language: string | null;
  created_at: string;
  pushed_at: string;
  size: number;
  license: { spdx_id: string | null } | null;
  archived: boolean;
  fork: boolean;
  topics?: string[];
  owner: { login: string; avatar_url: string };
}

export function blocked(owner: string, repo: string): boolean {
  const list = (process.env.GITBLAST_BLOCKLIST ?? "")
    .split(",")
    .map((s) => s.trim().toLowerCase())
    .filter(Boolean);
  const o = owner.toLowerCase();
  return list.includes(o) || list.includes(`${o}/${repo.toLowerCase()}`);
}

/**
 * Loads a repository and refuses anything that isn't public, is on the
 * blocklist, or carries the `gitblast-optout` topic. Every analysis starts here.
 */
export async function getRepo(owner: string, repo: string): Promise<RepoMeta> {
  if (!validOwner(owner) || !validRepo(repo)) throw new GitHubError("not_found", "That isn't a valid repository name.", 404);
  if (blocked(owner, repo)) {
    throw new GitHubError("unavailable", "The owner of this repository asked not to be shown on gitblast.", 403);
  }
  const { data: r } = await api<RawRepo>(`/repos/${owner}/${repo}`);
  if (r.private || (r.visibility && r.visibility !== "public")) {
    throw new GitHubError("not_found", "That repository doesn't exist, or isn't public.", 404);
  }
  if (r.topics?.includes("gitblast-optout") || blocked(r.owner.login, r.name)) {
    throw new GitHubError("unavailable", "The owner of this repository asked not to be shown on gitblast.", 403);
  }
  return {
    owner: r.owner.login,
    repo: r.name,
    fullName: r.full_name,
    description: r.description,
    defaultBranch: r.default_branch,
    stars: r.stargazers_count,
    forks: r.forks_count,
    language: r.language,
    createdAt: r.created_at,
    pushedAt: r.pushed_at,
    sizeKb: r.size,
    license: r.license?.spdx_id && r.license.spdx_id !== "NOASSERTION" ? r.license.spdx_id : null,
    archived: r.archived,
    fork: r.fork,
    ownerAvatar: r.owner.avatar_url,
  };
}

/** Runs `fn` over `items` with at most `limit` in flight. */
export async function mapLimit<T, R>(items: T[], limit: number, fn: (item: T, i: number) => Promise<R>): Promise<R[]> {
  const out = new Array<R>(items.length);
  let next = 0;
  async function worker() {
    while (next < items.length) {
      const i = next++;
      out[i] = await fn(items[i], i);
    }
  }
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, worker));
  return out;
}
