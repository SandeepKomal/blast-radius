"use client";

import { useEffect, useState } from "react";
import type { ApiError } from "./types";

export type ApiState<T> =
  | { status: "loading"; elapsed: number }
  | { status: "error"; error: ApiError["error"] }
  | { status: "ready"; data: T };

export function useApi<T>(url: string | null): ApiState<T> {
  const [state, setState] = useState<ApiState<T>>({ status: "loading", elapsed: 0 });

  useEffect(() => {
    if (!url) return;
    const ctrl = new AbortController();
    const started = Date.now();
    setState({ status: "loading", elapsed: 0 });
    const timer = setInterval(() => {
      setState((s) => (s.status === "loading" ? { status: "loading", elapsed: Math.floor((Date.now() - started) / 1000) } : s));
    }, 1000);

    fetch(url, { signal: ctrl.signal })
      .then(async (res) => {
        const body = (await res.json().catch(() => null)) as (T & Partial<ApiError>) | null;
        if (!res.ok || !body || body.error) {
          setState({
            status: "error",
            error: body?.error ?? { code: "internal", message: "Something went wrong. Try again in a moment." },
          });
        } else {
          setState({ status: "ready", data: body });
        }
      })
      .catch((err: unknown) => {
        if ((err as Error).name === "AbortError") return;
        setState({ status: "error", error: { code: "network", message: "Couldn't reach gitblast. Check your connection." } });
      })
      .finally(() => clearInterval(timer));

    return () => {
      ctrl.abort();
      clearInterval(timer);
    };
  }, [url]);

  return state;
}
