"use client";

import { useRouter } from "next/navigation";
import { useId, useState, type FormEvent } from "react";
import { parseInput, routePath } from "@/lib/route";

type Mode = "any" | "pr" | "repo";

const HINT: Record<Mode, string> = {
  any: "Paste a GitHub link, or type owner/repo or owner/repo#123.",
  pr: "Paste a pull request link, or type owner/repo#123.",
  repo: "Paste a repository link, or type owner/repo.",
};

/**
 * A search box that turns whatever is pasted into a gitblast page. `pr` only
 * accepts pull requests; `repo` opens the time-lapse even for a PR link.
 */
export function UrlBox({
  mode = "any",
  compact = false,
  autoFocus = false,
  placeholder,
  cta,
}: {
  mode?: Mode;
  compact?: boolean;
  autoFocus?: boolean;
  placeholder?: string;
  cta?: string;
}) {
  const router = useRouter();
  const id = useId();
  const [value, setValue] = useState("");
  const [error, setError] = useState("");

  function submit(e: FormEvent) {
    e.preventDefault();
    let route = parseInput(value);
    if (route && mode === "pr" && route.kind !== "pr") route = null;
    if (route && mode === "repo" && route.kind === "pr") route = { kind: "repo", owner: route.owner, repo: route.repo };
    if (!route) {
      setError(HINT[mode]);
      return;
    }
    setError("");
    router.push(routePath(route));
  }

  return (
    <form onSubmit={submit} role="search">
      <div className={`urlbox${compact ? " compact" : ""}`}>
        <label htmlFor={id} className="sr-only">
          {mode === "pr" ? "GitHub pull request" : mode === "repo" ? "GitHub repository" : "GitHub repository or pull request"}
        </label>
        <input
          id={id}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder={placeholder ?? (compact ? "owner/repo or a GitHub link" : "https://github.com/owner/repo")}
          autoFocus={autoFocus}
          autoComplete="off"
          spellCheck={false}
          aria-invalid={error ? true : undefined}
        />
        <button type="submit" className={`btn btn-primary${compact ? " btn-sm" : ""}`}>
          {cta ?? (compact ? "Go" : "Show me")}
        </button>
      </div>
      {error && !compact && <div className="form-error">{error}</div>}
    </form>
  );
}
