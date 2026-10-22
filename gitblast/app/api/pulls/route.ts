import { TTLCache } from "@/lib/cache";
import { api, getRepo } from "@/lib/github";
import { fail, respond } from "@/lib/http";
import { validOwner, validRepo } from "@/lib/route";
import type { PullSummary } from "@/lib/types";

export const runtime = "nodejs";

const cache = new TTLCache<PullSummary[]>(300);

interface RawPull {
  number: number;
  title: string;
  user: { login: string; avatar_url: string } | null;
  updated_at: string;
  draft?: boolean;
}

export async function GET(req: Request) {
  const q = new URL(req.url).searchParams;
  const owner = q.get("owner") ?? "";
  const repo = q.get("repo") ?? "";
  if (!validOwner(owner) || !validRepo(repo)) return fail("bad_request", "That isn't a valid repository.", 400);
  const key = `${owner}/${repo}`.toLowerCase();
  return respond(req, 300, () =>
    cache.get(key, 5 * 60_000, async () => {
      const meta = await getRepo(owner, repo);
      const { data } = await api<RawPull[]>(`/repos/${meta.owner}/${meta.repo}/pulls?state=open&sort=updated&direction=desc&per_page=8`);
      return data.map((p) => ({
        number: p.number,
        title: p.title,
        author: p.user?.login ?? "ghost",
        avatar: p.user?.avatar_url ?? "",
        updatedAt: p.updated_at,
        draft: Boolean(p.draft),
      }));
    }),
  );
}
