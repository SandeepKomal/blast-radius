"use client";

import { useRouter } from "next/navigation";
import { useState, type FormEvent } from "react";
import { parseInput, routePath } from "@/lib/route";

export function UrlBox({ compact = false, autoFocus = false }: { compact?: boolean; autoFocus?: boolean }) {
  const router = useRouter();
  const [value, setValue] = useState("");
  const [error, setError] = useState("");

  function submit(e: FormEvent) {
    e.preventDefault();
    const route = parseInput(value);
    if (!route) {
      setError("Paste a GitHub link, or type owner/repo or owner/repo#123.");
      return;
    }
    setError("");
    router.push(routePath(route));
  }

  return (
    <form onSubmit={submit} role="search">
      <div className={`urlbox${compact ? " compact" : ""}`}>
        <label htmlFor={compact ? "q-compact" : "q"} className="sr-only">
          GitHub repository or pull request
        </label>
        <input
          id={compact ? "q-compact" : "q"}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder={compact ? "owner/repo or a GitHub link" : "https://github.com/owner/repo/pull/123"}
          autoFocus={autoFocus}
          autoComplete="off"
          spellCheck={false}
        />
        <button type="submit" className={`btn btn-primary${compact ? " btn-sm" : ""}`}>
          {compact ? "Go" : "Show me"}
        </button>
      </div>
      {error && !compact && <div className="form-error">{error}</div>}
    </form>
  );
}
