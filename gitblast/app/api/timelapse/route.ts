import { TTLCache } from "@/lib/cache";
import { fail, respond } from "@/lib/http";
import { validOwner, validRepo } from "@/lib/route";
import { analyzeTimelapse } from "@/lib/timelapse";
import type { Timelapse } from "@/lib/types";

export const runtime = "nodejs";
export const maxDuration = 60;

const cache = new TTLCache<Timelapse>(100);

export async function GET(req: Request) {
  const q = new URL(req.url).searchParams;
  const owner = q.get("owner") ?? "";
  const repo = q.get("repo") ?? "";
  if (!validOwner(owner) || !validRepo(repo)) return fail("bad_request", "That isn't a valid repository.", 400);
  const key = `${owner}/${repo}`.toLowerCase();
  return respond(req, 6 * 3600, () => cache.get(key, 6 * 3600_000, () => analyzeTimelapse(owner, repo)));
}
