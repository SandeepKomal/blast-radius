import { NextResponse } from "next/server";
import { GitHubError } from "./github";
import { allow, clientKey } from "./ratelimit";
import type { ApiError } from "./types";

export function fail(code: string, message: string, status: number, retryAfter?: number) {
  const body: ApiError = { error: { code, message, retryAfter } };
  const headers: Record<string, string> = { "Cache-Control": "no-store" };
  if (retryAfter) headers["Retry-After"] = String(retryAfter);
  return NextResponse.json(body, { status, headers });
}

/** Runs an analysis with rate limiting and turns failures into friendly JSON. */
export async function respond<T>(req: Request, cacheSeconds: number, work: () => Promise<T>) {
  const gate = allow(clientKey(req.headers));
  if (!gate.ok) return fail("rate_limited", "You're going a bit fast. Try again in a few minutes.", 429, gate.retryAfter);
  try {
    const data = await work();
    return NextResponse.json(data, {
      headers: { "Cache-Control": `public, max-age=60, s-maxage=${cacheSeconds}, stale-while-revalidate=${cacheSeconds * 4}` },
    });
  } catch (err) {
    if (err instanceof GitHubError) return fail(err.code, err.message, err.status, err.retryAfter);
    console.error("analysis failed", err);
    return fail("internal", "Something went wrong on our side. Try again in a moment.", 500);
  }
}
