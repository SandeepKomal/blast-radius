import Link from "next/link";
import type { ApiError } from "@/lib/types";

export function Loading({ title, hint, elapsed }: { title: string; hint: string; elapsed: number }) {
  return (
    <div className="loading" role="status" aria-live="polite">
      <div>
        <div className="pulse">
          <span />
          <span />
          <span />
        </div>
        <h2>{title}</h2>
        <p>{hint}</p>
        <div className="elapsed">{elapsed > 0 ? `${elapsed}s` : " "}</div>
      </div>
    </div>
  );
}

const TITLES: Record<string, string> = {
  not_found: "Nothing here",
  rate_limited: "Taking a breather",
  unavailable: "Not available",
  bad_request: "That link doesn't look right",
};

export function ErrorState({ error, githubUrl }: { error: ApiError["error"]; githubUrl?: string }) {
  const minutes = error.retryAfter ? Math.ceil(error.retryAfter / 60) : 0;
  return (
    <div className="card error-box">
      <h1>{TITLES[error.code] ?? "Something went wrong"}</h1>
      <p>
        {error.message}
        {minutes > 0 && ` Try again in about ${minutes} minute${minutes === 1 ? "" : "s"}.`}
      </p>
      <div className="actions">
        <Link href="/" className="btn btn-primary">
          Back to gitblast
        </Link>
        {githubUrl && error.code !== "unavailable" && (
          <a href={githubUrl} className="btn btn-ghost" target="_blank" rel="noopener noreferrer">
            Open on GitHub
          </a>
        )}
      </div>
    </div>
  );
}
