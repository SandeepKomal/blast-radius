"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useMemo, useRef, useState, type FormEvent, type PointerEvent as ReactPointerEvent } from "react";
import { bytes, compact, day, monthYear, num } from "@/lib/format";
import type { Frame, PullSummary, Timelapse } from "@/lib/types";
import { useApi } from "@/lib/useApi";
import { CopyLink } from "./CopyLink";
import { ErrorState, Loading } from "./States";
import { blend, HERE, layoutFrames, topColors, type Placed } from "./timelapse-layout";

const SPEEDS = [0.5, 1, 2, 4];
const FRAME_MS = 900;

export function TimelapseView({ owner, repo }: { owner: string; repo: string }) {
  const q = `owner=${encodeURIComponent(owner)}&repo=${encodeURIComponent(repo)}`;
  const state = useApi<Timelapse>(`/api/timelapse?${q}`);
  if (state.status === "loading") {
    return (
      <Loading
        title={`Rewinding ${owner}/${repo}`}
        hint="Sampling its history from the first commit to today. Repositories with long histories take a little longer."
        elapsed={state.elapsed}
      />
    );
  }
  if (state.status === "error") return <ErrorState error={state.error} githubUrl={`https://github.com/${owner}/${repo}`} />;
  return <Player data={state.data} />;
}

function Player({ data }: { data: Timelapse }) {
  const { repo, frames } = data;
  const layouts = useMemo(() => layoutFrames(frames), [frames]);
  const colors = useMemo(() => topColors(layouts[layouts.length - 1]), [layouts]);
  const last = Math.max(0, frames.length - 1);

  const canvasRef = useRef<HTMLCanvasElement>(null);
  const tRef = useRef(0);
  const playingRef = useRef(false);
  const speedRef = useRef(1);
  const drawnRef = useRef<(Placed & { fresh: number })[]>([]);
  const [t, setT] = useState(0);
  const [playing, setPlaying] = useState(false);
  const [speed, setSpeed] = useState(1);
  const [tip, setTip] = useState<{ x: number; y: number; p: Placed } | null>(null);

  const current = frames[Math.min(last, Math.round(t))];

  function seek(v: number) {
    tRef.current = Math.max(0, Math.min(last, v));
    setT(tRef.current);
  }
  function play(on: boolean) {
    if (on && tRef.current >= last) seek(0);
    playingRef.current = on;
    setPlaying(on);
  }

  // Start playing once, unless the viewer prefers less motion.
  useEffect(() => {
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches || last === 0) {
      seek(last);
    } else {
      const id = setTimeout(() => play(true), 450);
      return () => clearTimeout(id);
    }
    // Runs once per data set; play and seek only touch refs and setters.
  }, [data]);

  // Drawing loop. It runs every animation frame but hands React a coarser
  // position, so the panels re-render about twenty times a second at most.
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;
    const font = getComputedStyle(document.body).getPropertyValue("--font-geist-mono") || "monospace";
    let raf = 0;
    let prev = performance.now();
    let shown = -1;

    const draw = (now: number) => {
      const dt = now - prev;
      prev = now;
      if (playingRef.current) {
        tRef.current = Math.min(last, tRef.current + (dt / FRAME_MS) * speedRef.current);
        const coarse = Math.round(tRef.current * 20) / 20;
        if (coarse !== shown) {
          shown = coarse;
          setT(coarse);
        }
        if (tRef.current >= last) {
          playingRef.current = false;
          setPlaying(false);
        }
      }

      const dpr = Math.min(2, window.devicePixelRatio || 1);
      const w = canvas.clientWidth;
      const h = canvas.clientHeight;
      if (canvas.width !== Math.round(w * dpr) || canvas.height !== Math.round(h * dpr)) {
        canvas.width = Math.round(w * dpr);
        canvas.height = Math.round(h * dpr);
      }
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      ctx.clearRect(0, 0, w, h);

      const a = Math.floor(tRef.current);
      const b = Math.min(last, a + 1);
      const raw = tRef.current - a;
      const k = raw * raw * (3 - 2 * raw);
      const nodes = layouts.length ? blend(layouts[a], layouts[b], b === a ? 0 : k) : [];
      drawnRef.current = nodes;

      const { size, ox, oy } = stageGeometry(w, h);

      for (const n of nodes) {
        const r = n.r * size;
        if (r < 0.4) continue;
        const x = ox + n.x * size;
        const y = oy + n.y * size;
        const color = colors.get(n.top) ?? "#6b7280";
        ctx.beginPath();
        ctx.arc(x, y, r, 0, Math.PI * 2);
        if (n.leaf) {
          ctx.fillStyle = withAlpha(color, n.key.endsWith(HERE) ? 0.45 : 0.8);
          if (n.fresh > 0.05) {
            ctx.shadowColor = color;
            ctx.shadowBlur = 24 * n.fresh;
          }
          ctx.fill();
          ctx.shadowBlur = 0;
        } else {
          ctx.fillStyle = withAlpha(color, n.depth === 1 ? 0.06 : 0.035);
          ctx.fill();
          ctx.strokeStyle = n.depth === 1 ? withAlpha(color, 0.35) : "rgba(255,255,255,0.08)";
          ctx.lineWidth = 1;
          ctx.stroke();
        }
      }

      ctx.font = `500 11.5px ${font}`;
      ctx.textAlign = "center";
      for (const n of nodes) {
        if (n.depth !== 1) continue;
        const r = n.r * size;
        if (r < 30) continue;
        const label = n.key === HERE ? "(root files)" : `${n.key}/`;
        const max = Math.floor((r * 1.6) / 7);
        const text = label.length > max ? `${label.slice(0, Math.max(1, max - 1))}…` : label;
        const x = ox + n.x * size;
        const y = oy + n.y * size - r + (n.leaf ? r + 4 : 16);
        ctx.fillStyle = "rgba(7,8,11,0.55)";
        const tw = ctx.measureText(text).width;
        ctx.fillRect(x - tw / 2 - 4, y - 10, tw + 8, 15);
        ctx.fillStyle = "rgba(238,240,244,0.85)";
        ctx.fillText(text, x, y + 1);
      }

      raf = requestAnimationFrame(draw);
    };
    raf = requestAnimationFrame(draw);
    return () => cancelAnimationFrame(raf);
  }, [layouts, colors, last]);

  // Keyboard: space plays or pauses, arrows step.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement;
      if (target.closest("input, textarea, select, [contenteditable]")) return;
      if (e.key === " ") {
        e.preventDefault();
        play(!playingRef.current);
      } else if (e.key === "ArrowRight") {
        play(false);
        seek(Math.floor(tRef.current) + 1);
      } else if (e.key === "ArrowLeft") {
        play(false);
        seek(Math.ceil(tRef.current) - 1);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [last]);

  function hover(e: React.MouseEvent<HTMLCanvasElement>) {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const box = canvas.getBoundingClientRect();
    const px = e.clientX - box.left;
    const py = e.clientY - box.top;
    const { size, ox, oy } = stageGeometry(box.width, box.height);
    let hit: Placed | null = null;
    for (const n of drawnRef.current) {
      const dx = ox + n.x * size - px;
      const dy = oy + n.y * size - py;
      if (dx * dx + dy * dy <= (n.r * size) ** 2 && (!hit || n.depth >= hit.depth)) hit = n;
    }
    setTip(hit ? { x: px, y: py, p: hit } : null);
  }

  const tops = useMemo(() => countFolders(current), [current]);

  return (
    <>
      <header className="page-head">
        <div className="crumbs">
          {repo.avatar && <img className="avatar" src={repo.avatar} alt="" width={20} height={20} referrerPolicy="no-referrer" />}
          <span>{repo.owner}</span>
          <span className="sep">/</span>
          <b style={{ color: "var(--text)" }}>{repo.repo}</b>
        </div>
        <h1 className="page-title">How {repo.repo} grew</h1>
        <div className="meta-row">
          {repo.description && <span style={{ flexBasis: "100%", color: "var(--muted)" }}>{repo.description}</span>}
          <span>★ {compact(repo.stars)}</span>
          <span>{compact(repo.forks)} forks</span>
          {repo.language && <span>{repo.language}</span>}
          {repo.license && <span>{repo.license}</span>}
          <span>{num(data.totalCommits)} commits since {monthYear(repo.createdAt)}</span>
          <span className="head-actions">
            <CopyLink />
            <a className="btn btn-ghost" href={`https://github.com/${repo.owner}/${repo.repo}`} target="_blank" rel="noopener noreferrer">
              Open on GitHub ↗
            </a>
          </span>
        </div>
      </header>

      {data.partial && (
        <div className="notice" role="note">
          <span aria-hidden="true">⚠</span>
          <span>Some points in this history were too large to read in full, so a few frames are approximate.</span>
        </div>
      )}

      <section className="tl-grid">
        <div className="card stage">
          <canvas
            ref={canvasRef}
            onMouseMove={hover}
            onMouseLeave={() => setTip(null)}
            aria-label={`Animated map of ${repo.repo}'s folders over time`}
            role="img"
          />
          {current && (
            <div className="stage-caption">
              <div>
                <div className="stage-date">{monthYear(current.date)}</div>
                <div className="stage-sub">
                  commit {num(current.index)} of {num(data.totalCommits)}
                </div>
              </div>
              <div className="stage-commit">
                <div className="msg">{current.message}</div>
                <div className="by">
                  {current.avatar && <img className="avatar" src={current.avatar} alt="" width={16} height={16} referrerPolicy="no-referrer" />}
                  {current.author} · <span className="mono">{current.sha.slice(0, 7)}</span>
                </div>
              </div>
            </div>
          )}
          {tip && (
            <div className="tooltip" style={{ left: tip.x, top: tip.y }}>
              <div className="p">{tip.p.key === HERE ? "(files in the root)" : tip.p.key.endsWith(`/${HERE}`) ? `${tip.p.key.slice(0, -2)}/ (files directly here)` : `${tip.p.key}/`}</div>
              <div className="s">{num(tip.p.files)} files</div>
            </div>
          )}
          <div className="controls">
            <button className="btn btn-primary btn-icon" onClick={() => play(!playing)} aria-label={playing ? "Pause" : "Play"}>
              {playing ? <PauseIcon /> : <PlayIcon />}
            </button>
            <Scrubber t={t} last={last} milestones={data.milestones} onSeek={(v) => { play(false); seek(v); }} />
            <button
              className="btn btn-ghost speed"
              onClick={() => {
                const next = SPEEDS[(SPEEDS.indexOf(speed) + 1) % SPEEDS.length];
                speedRef.current = next;
                setSpeed(next);
              }}
              aria-label="Playback speed"
            >
              {speed}×
            </button>
          </div>
        </div>

        <div className="stack">
          <div className="card card-pad">
            <h2>At this point</h2>
            <div className="counter-grid">
              <div className="stat"><div className="v">{num(current?.files ?? 0)}</div><div className="k">files</div></div>
              <div className="stat"><div className="v">{bytes(current?.bytes ?? 0)}</div><div className="k">of code and assets</div></div>
              <div className="stat"><div className="v">{num(tops)}</div><div className="k">top-level folders</div></div>
              <div className="stat"><div className="v">{day(current?.date ?? "")}</div><div className="k">date</div></div>
            </div>
          </div>
          {current && <Languages frame={current} colors={data.langColors} />}
          {data.milestones.length > 0 && (
            <div className="card card-pad">
              <h2>Moments</h2>
              <ul className="milestones">
                {data.milestones.map((m, i) => (
                  <li key={i}>
                    <button className={m.frame <= t ? "past" : ""} onClick={() => { play(false); seek(m.frame); }}>
                      <i className="dot" style={{ background: m.frame <= t ? "var(--ripple1)" : "var(--faint)" }} />
                      {m.label}
                      <span className="when">{monthYear(frames[m.frame]?.date ?? "")}</span>
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          )}
          {data.contributors.length > 0 && (
            <div className="card card-pad">
              <h2>Top contributors</h2>
              <div className="people">
                {data.contributors.map((c) => (
                  <a key={c.login} href={`https://github.com/${c.login}`} target="_blank" rel="noopener noreferrer" title={`${c.login} · ${num(c.contributions)} commits`}>
                    <img className="avatar" src={c.avatar} alt={c.login} width={36} height={36} referrerPolicy="no-referrer" />
                  </a>
                ))}
              </div>
            </div>
          )}
        </div>
      </section>

      <PullRequests owner={repo.owner} repo={repo.repo} />
    </>
  );
}

function Scrubber({
  t,
  last,
  milestones,
  onSeek,
}: {
  t: number;
  last: number;
  milestones: Timelapse["milestones"];
  onSeek: (t: number) => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const pct = last === 0 ? 100 : (t / last) * 100;

  function at(e: ReactPointerEvent<HTMLDivElement>) {
    const box = ref.current?.getBoundingClientRect();
    if (!box) return;
    onSeek(((e.clientX - box.left) / box.width) * last);
  }

  return (
    <div
      ref={ref}
      className="scrubber"
      role="slider"
      tabIndex={0}
      aria-label="Position in history"
      aria-valuemin={0}
      aria-valuemax={last}
      aria-valuenow={Math.round(t)}
      onPointerDown={(e) => {
        e.currentTarget.setPointerCapture(e.pointerId);
        at(e);
      }}
      onPointerMove={(e) => e.buttons === 1 && at(e)}
    >
      <div className="track" />
      <div className="fill" style={{ width: `${pct}%` }} />
      {milestones.map((m, i) => (
        <span key={i} className="tick" style={{ left: `${last === 0 ? 0 : (m.frame / last) * 100}%` }} />
      ))}
      <div className="knob" style={{ left: `${pct}%` }} />
    </div>
  );
}

function Languages({ frame, colors }: { frame: Frame; colors: Record<string, string> }) {
  const entries = Object.entries(frame.langs).filter(([name]) => name !== "Other" && name !== "Image");
  const total = entries.reduce((s, [, n]) => s + n, 0) || 1;
  const sorted = entries.sort((a, b) => b[1] - a[1]);
  const top = sorted.slice(0, 7);
  const rest = sorted.slice(7).reduce((s, [, n]) => s + n, 0);
  if (rest > 0) top.push(["Everything else", rest]);
  return (
    <div className="card card-pad">
      <h2>Languages</h2>
      <div className="langbar">
        {top.map(([name, n]) => (
          <span key={name} style={{ flexGrow: n, background: colors[name] ?? "#52525b" }} title={name} />
        ))}
      </div>
      <div className="lang-list">
        {top.map(([name, n]) => (
          <div key={name}>
            <i className="dot" style={{ background: colors[name] ?? "#52525b" }} />
            <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{name}</span>
            <span className="pct">{((n / total) * 100).toFixed(n / total < 0.1 ? 1 : 0)}%</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function PullRequests({ owner, repo }: { owner: string; repo: string }) {
  const router = useRouter();
  const state = useApi<PullSummary[]>(`/api/pulls?owner=${encodeURIComponent(owner)}&repo=${encodeURIComponent(repo)}`);
  const [n, setN] = useState("");

  function jump(e: FormEvent) {
    e.preventDefault();
    const v = Number(n.replace(/^#/, ""));
    if (Number.isInteger(v) && v > 0) router.push(`/${owner}/${repo}/pull/${v}`);
  }

  return (
    <section className="section" style={{ marginTop: 48 }}>
      <div className="card card-pad">
        <h2>See the blast radius of a pull request</h2>
        {state.status === "loading" && <p className="faint" style={{ margin: 0 }}>Loading open pull requests…</p>}
        {state.status === "error" && <p className="faint" style={{ margin: 0 }}>Couldn&apos;t load open pull requests.</p>}
        {state.status === "ready" && state.data.length === 0 && <p className="faint" style={{ margin: 0 }}>No open pull requests right now.</p>}
        {state.status === "ready" && state.data.length > 0 && (
          <ul className="pulls">
            {state.data.map((p) => (
              <li key={p.number}>
                <Link href={`/${owner}/${repo}/pull/${p.number}`}>
                  {p.avatar ? <img className="avatar" src={p.avatar} alt="" width={22} height={22} referrerPolicy="no-referrer" /> : <span />}
                  <span className="t">
                    <span className="faint mono">#{p.number}</span> {p.title}
                    {p.draft && <span className="pill" style={{ marginLeft: 8 }}>Draft</span>}
                  </span>
                  <span className="go">Blast it →</span>
                </Link>
              </li>
            ))}
          </ul>
        )}
        <form className="pr-jump" onSubmit={jump}>
          <div className="urlbox compact" style={{ flex: 1, maxWidth: 320 }}>
            <label htmlFor="pr-number" className="sr-only">Pull request number</label>
            <input id="pr-number" value={n} onChange={(e) => setN(e.target.value)} placeholder="Any PR number, e.g. 1234" inputMode="numeric" />
            <button type="submit" className="btn btn-ghost">Go</button>
          </div>
        </form>
      </div>
    </section>
  );
}

/** Where the unit-square layout sits on the canvas, leaving room for the caption. */
function stageGeometry(w: number, h: number) {
  const size = Math.min(w, h) * 0.86;
  return { size, ox: (w - size) / 2, oy: (h - size) / 2 + h * 0.05 };
}

function countFolders(f: Frame | undefined): number {
  if (!f) return 0;
  return new Set(Object.keys(f.dirs).map((d) => d.split("/")[0]).filter(Boolean)).size;
}

function withAlpha(hex: string, a: number): string {
  const v = parseInt(hex.slice(1), 16);
  return `rgba(${(v >> 16) & 255},${(v >> 8) & 255},${v & 255},${a})`;
}

function PlayIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true">
      <path d="M3 1.8v10.4a.6.6 0 0 0 .9.5l8.4-5.2a.6.6 0 0 0 0-1L3.9 1.3a.6.6 0 0 0-.9.5z" fill="currentColor" />
    </svg>
  );
}

function PauseIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true">
      <rect x="2.5" y="1.5" width="3.2" height="11" rx="1" fill="currentColor" />
      <rect x="8.3" y="1.5" width="3.2" height="11" rx="1" fill="currentColor" />
    </svg>
  );
}
