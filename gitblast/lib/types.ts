// The JSON the API returns and the pages render. Shared by server and browser.

export type FileStatus = "added" | "modified" | "removed" | "renamed";
export type Role = "source" | "test" | "config" | "deps" | "ci" | "docs" | "data" | "asset";
export type Level = "high" | "medium" | "low";

/** A node of the circle map. Leaves carry a value; `state` marks PR involvement. */
export interface MapNode {
  name: string;
  path: string;
  value?: number;
  /** Number of files this leaf stands for (a folder leaf stands for many). */
  files?: number;
  state?: "changed" | "added" | "removed" | "ripple1" | "ripple2" | "ripple3";
  color?: string;
  children?: MapNode[];
}

export interface RepoSummary {
  owner: string;
  repo: string;
  description: string | null;
  stars: number;
  avatar: string;
}

export interface PRFile {
  path: string;
  previous?: string;
  status: FileStatus;
  additions: number;
  deletions: number;
  role: Role;
  lang: string;
  color: string;
  /** Files in this PR that import this one (directly or not). Used for review order. */
  usedBy: number;
  /** Files outside the PR that this one reaches. */
  reach: number;
}

export interface Impacted {
  path: string;
  depth: 1 | 2 | 3;
  /** The changed file this one depends on. */
  via: string;
  test: boolean;
}

export interface Risk {
  id: string;
  level: Level;
  title: string;
  detail: string;
  files: string[];
}

export interface PRReport {
  repo: RepoSummary;
  pr: {
    number: number;
    title: string;
    author: string;
    authorAvatar: string;
    state: "open" | "closed" | "merged";
    draft: boolean;
    base: string;
    head: string;
    headSha: string;
    createdAt: string;
    url: string;
    additions: number;
    deletions: number;
    commits: number;
    changedFiles: number;
  };
  score: number;
  level: Level;
  summary: string[];
  files: PRFile[];
  impacted: Impacted[];
  impactedTotal: number;
  tests: { changed: string[]; affected: string[] };
  risks: Risk[];
  areas: { name: string; changed: number; impacted: number }[];
  map: MapNode;
  edges: [string, string][];
  analysis: { complete: boolean; filesScanned: number; note?: string };
  generatedAt: string;
}

export interface Frame {
  sha: string;
  date: string;
  author: string;
  avatar?: string;
  message: string;
  /** 1-based position of this commit in the branch history. */
  index: number;
  files: number;
  bytes: number;
  /** Folder (to a fixed depth) → number of files in it and below. */
  dirs: Record<string, number>;
  /** Language name → number of files. */
  langs: Record<string, number>;
}

export interface Timelapse {
  repo: RepoSummary & {
    defaultBranch: string;
    createdAt: string;
    language: string | null;
    license: string | null;
    forks: number;
  };
  totalCommits: number;
  frames: Frame[];
  milestones: { frame: number; label: string }[];
  contributors: { login: string; avatar: string; contributions: number }[];
  langColors: Record<string, string>;
  depth: number;
  partial: boolean;
  generatedAt: string;
}

export interface PullSummary {
  number: number;
  title: string;
  author: string;
  avatar: string;
  updatedAt: string;
  draft: boolean;
}

export interface ApiError {
  error: { code: string; message: string; retryAfter?: number };
}
