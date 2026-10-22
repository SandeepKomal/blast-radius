// Turns the path after gitblast.com/ (or anything a person pastes) into a view.
// Shared by the server and the browser, so it must stay free of Node APIs.

export type Route =
  | { kind: "repo"; owner: string; repo: string }
  | { kind: "pr"; owner: string; repo: string; number: number };

const OWNER = /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$/;
const REPO = /^[A-Za-z0-9._-]{1,100}$/;

export function validOwner(s: string): boolean {
  return OWNER.test(s);
}

export function validRepo(s: string): boolean {
  return REPO.test(s) && s !== "." && s !== "..";
}

/** Parses path segments such as ["vercel", "next.js", "pull", "123", "files"]. */
export function parseSegments(raw: string[]): Route | null {
  let segs = raw.map((s) => safeDecode(s)).filter((s) => s.length > 0);
  // Someone typed gitblast.com/ in front of a full GitHub link. Browsers fold
  // "https://" into "https:" plus an empty segment, which the filter drops.
  if (segs[0] === "https:" || segs[0] === "http:") segs = segs.slice(1);
  if (segs[0] === "github.com" || segs[0] === "www.github.com") segs = segs.slice(1);
  if (segs.length < 2) return null;

  const owner = segs[0];
  const repo = segs[1].replace(/\.git$/, "");
  if (!validOwner(owner) || !validRepo(repo)) return null;

  if (segs[2] === "pull" || segs[2] === "pulls") {
    if (segs[3] && /^\d{1,9}$/.test(segs[3])) {
      const number = Number(segs[3]);
      if (number > 0) return { kind: "pr", owner, repo, number };
    }
  }
  return { kind: "repo", owner, repo };
}

/** Parses free text from the search box: a URL, owner/repo, or owner/repo#123. */
export function parseInput(text: string): Route | null {
  let t = text.trim();
  if (!t) return null;
  t = t.replace(/^(https?:\/\/)?(www\.)?(github|gitblast)\.com\//i, "");
  t = t.split(/[?]/)[0];
  const hash = t.match(/^([^#\s]+)#(\d+)$/);
  if (hash) t = `${hash[1]}/pull/${hash[2]}`;
  t = t.split("#")[0];
  return parseSegments(t.split("/"));
}

export function routePath(r: Route): string {
  const base = `/${r.owner}/${r.repo}`;
  return r.kind === "pr" ? `${base}/pull/${r.number}` : base;
}

export function githubUrl(r: Route): string {
  return `https://github.com${routePath(r)}`;
}

function safeDecode(s: string): string {
  try {
    return decodeURIComponent(s);
  } catch {
    return s;
  }
}
