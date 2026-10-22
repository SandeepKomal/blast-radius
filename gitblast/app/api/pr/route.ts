import { TTLCache } from "@/lib/cache";
import { fail, respond } from "@/lib/http";
import { analyzePR } from "@/lib/pr";
import { validOwner, validRepo } from "@/lib/route";
import type { PRReport } from "@/lib/types";

export const runtime = "nodejs";
export const maxDuration = 60;

const cache = new TTLCache<PRReport>(200);

export async function GET(req: Request) {
  const q = new URL(req.url).searchParams;
  const owner = q.get("owner") ?? "";
  const repo = q.get("repo") ?? "";
  const number = Number(q.get("number"));
  if (!validOwner(owner) || !validRepo(repo) || !Number.isInteger(number) || number < 1 || number > 1e9) {
    return fail("bad_request", "That isn't a valid pull request.", 400);
  }
  const key = `${owner}/${repo}#${number}`.toLowerCase();
  return respond(req, 300, () => cache.get(key, 5 * 60_000, () => analyzePR(owner, repo, number)));
}
