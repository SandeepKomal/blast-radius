"use client";

import Link from "next/link";
import { useState } from "react";
import { ago, num } from "@/lib/format";
import type { Impacted, Level, PRFile, PRReport } from "@/lib/types";
import { useApi } from "@/lib/useApi";
import { CopyLink } from "./CopyLink";
import { Legend, PackMap } from "./PackMap";
import { ErrorState, Loading } from "./States";

const LEVEL_COLOR: Record<Level, string> = { high: "var(--high)", medium: "var(--medium)", low: "var(--low)" };
const LEVEL_TEXT: Record<Level, string> = {
  high: "Review carefully",
  medium: "Worth a close look",
  low: "Looks contained",
};

export function PRView({ owner, repo, number }: { owner: string; repo: string; number: number }) {
  const state = useApi<PRReport>(`/api/pr?owner=${encodeURIComponent(owner)}&repo=${encodeURIComponent(repo)}&number=${number}`);
  const ghUrl = `https://github.com/${owner}/${repo}/pull/${number}`;

  if (state.status === "loading") {
    return (
      <Loading
        title={`Mapping ${owner}/${repo} #${number}`}
        hint="Downloading the source and following every import. Large repositories can take up to a minute."
        elapsed={state.elapsed}
      />
    );
  }
  if (state.status === "error") return <ErrorState error={state.error} githubUrl={ghUrl} />;
  return <Report report={state.data} />;
}

function Report({ report }: { report: PRReport }) {
  const { repo, pr } = report;
  const [active, setActive] = useState<string | null>(null);
  const fileUrl = (path: string) => `https://github.com/${repo.owner}/${repo.repo}/blob/${pr.headSha}/${path.split("/").map(encodeURIComponent).join("/")}`;

  return (
    <>
      <header className="page-head">
        <div className="crumbs">
          {repo.avatar && <img className="avatar" src={repo.avatar} alt="" width={20} height={20} referrerPolicy="no-referrer" />}
          <Link href={`/${repo.owner}/${repo.repo}`}>
            {repo.owner}/<b style={{ color: "var(--text)" }}>{repo.repo}</b>
          </Link>
          <span className="sep">/</span>
          <span>Pull request</span>
        </div>
        <h1 className="page-title">
          {pr.title} <span className="num">#{pr.number}</span>
        </h1>
        <div className="meta-row">
          <span className={`pill pill-${pr.state}`}>{pr.draft && pr.state === "open" ? "Draft" : cap(pr.state)}</span>
          <span className="who">
            {pr.authorAvatar && <img className="avatar" src={pr.authorAvatar} alt="" width={20} height={20} referrerPolicy="no-referrer" />}
            <b style={{ color: "var(--text)", fontWeight: 550 }}>{pr.author}</b>
          </span>
          <span>
            <span className="branch" title={pr.head}>{pr.head}</span> → <span className="branch" title={pr.base}>{pr.base}</span>
          </span>
          <span className="mono">
            <span className="plus">+{num(pr.additions)}</span> <span className="minus">−{num(pr.deletions)}</span>
          </span>
          <span>{ago(pr.createdAt)}</span>
          <span className="head-actions">
            <CopyLink />
            <a className="btn btn-ghost" href={pr.url} target="_blank" rel="noopener noreferrer">
              Open on GitHub ↗
            </a>
          </span>
        </div>
      </header>

      {report.analysis.note && (
        <div className="notice" role="note">
          <span aria-hidden="true">⚠</span>
          <span>{report.analysis.note}</span>
        </div>
      )}

      <section className="overview">
        <div className="card card-pad">
          <h2>Blast score</h2>
          <div className="score">
            <ScoreRing score={report.score} level={report.level} />
            <div>
              <div className="level" style={{ color: LEVEL_COLOR[report.level] }}>{cap(report.level)} risk</div>
              <div className="label">{LEVEL_TEXT[report.level]}</div>
            </div>
          </div>
        </div>
        <div className="card card-pad">
          <h2>In short</h2>
          <ul className="summary">
            {report.summary.map((s, i) => (
              <li key={i}>
                <span>{withCode(s)}</span>
              </li>
            ))}
          </ul>
          <div className="stat-grid">
            <Stat v={num(report.files.length)} k="files changed" />
            <Stat v={num(report.impactedTotal)} k="files in the ripple" />
            <Stat v={num(report.tests.affected.length + report.tests.changed.length)} k="tests involved" />
            <Stat v={num(report.areas.filter((a) => a.changed > 0).length)} k="areas touched" />
          </div>
        </div>
      </section>

      <section className="report-grid">
        <div className="card map-card">
          <div className="map-head">
            <h2>Blast radius</h2>
            <span className="faint" style={{ fontSize: 12 }}>Click a folder to zoom · Esc to zoom out</span>
          </div>
          <PackMap map={report.map} edges={report.edges} active={active} onActive={setActive} fileUrl={fileUrl} />
          <Legend />
        </div>

        <div className="stack">
          <Risks report={report} />
          <ReviewOrder files={report.files} active={active} onActive={setActive} fileUrl={fileUrl} />
          <Ripple impacted={report.impacted} total={report.impactedTotal} onActive={setActive} />
          <Tests tests={report.tests} />
          <Areas areas={report.areas} />
        </div>
      </section>
    </>
  );
}

function ScoreRing({ score, level }: { score: number; level: Level }) {
  const r = 48;
  const c = 2 * Math.PI * r;
  return (
    <div className="score-ring" aria-label={`Blast score ${score} out of 100`}>
      <svg width="112" height="112" viewBox="0 0 112 112">
        <circle cx="56" cy="56" r={r} fill="none" stroke="rgba(255,255,255,0.07)" strokeWidth="8" />
        <circle
          cx="56"
          cy="56"
          r={r}
          fill="none"
          stroke={LEVEL_COLOR[level]}
          strokeWidth="8"
          strokeLinecap="round"
          strokeDasharray={`${(Math.max(score, 2) / 100) * c} ${c}`}
          style={{ filter: `drop-shadow(0 0 8px ${LEVEL_COLOR[level]})` }}
        />
      </svg>
      <div className="val">{score}</div>
    </div>
  );
}

function Stat({ v, k }: { v: string; k: string }) {
  return (
    <div className="stat">
      <div className="v">{v}</div>
      <div className="k">{k}</div>
    </div>
  );
}

function Risks({ report }: { report: PRReport }) {
  return (
    <div className="card card-pad">
      <h2>
        What to look at <span className="count">{report.risks.length}</span>
      </h2>
      {report.risks.length === 0 && <p className="muted" style={{ margin: 0 }}>Nothing stands out. This change looks contained.</p>}
      {report.risks.map((r) => (
        <div className="risk" key={r.id}>
          <span className={`badge badge-${r.level}`}>{r.level}</span>
          <div>
            <h3>{r.title}</h3>
            <p>{r.detail}</p>
            {r.files.length > 0 && (
              <details>
                <summary>
                  {r.files.length} file{r.files.length === 1 ? "" : "s"} ▾
                </summary>
                <ul className="path-list">
                  {r.files.map((f) => (
                    <li key={f} title={f}>{f}</li>
                  ))}
                </ul>
              </details>
            )}
          </div>
        </div>
      ))}
    </div>
  );
}

const ROLE_LABEL: Record<PRFile["role"], string> = {
  source: "code",
  test: "test",
  config: "config",
  deps: "deps",
  ci: "ci / infra",
  docs: "docs",
  data: "schema",
  asset: "asset",
};

function ReviewOrder({
  files,
  active,
  onActive,
  fileUrl,
}: {
  files: PRFile[];
  active: string | null;
  onActive: (p: string | null) => void;
  fileUrl: (p: string) => string;
}) {
  const [all, setAll] = useState(false);
  const shown = all ? files : files.slice(0, 12);
  return (
    <div className="card card-pad">
      <h2>
        Suggested review order <span className="count">{files.length}</span>
      </h2>
      <ol className="files">
        {shown.map((f, i) => (
          <li
            key={f.path}
            className={`file-row status-${f.status}${active === f.path ? " active" : ""}`}
            onMouseEnter={() => onActive(f.path)}
            onMouseLeave={() => onActive(null)}
          >
            <span className="idx">{i + 1}</span>
            <div className="name">
              <div className="path" title={f.path}>
                {f.status === "removed" ? (
                  <bdi>{f.path}</bdi>
                ) : (
                  <a href={fileUrl(f.path)} target="_blank" rel="noopener noreferrer">
                    <bdi>{f.path}</bdi>
                  </a>
                )}
              </div>
              <div className="sub">
                <span className="role">{ROLE_LABEL[f.role]}</span>
                <span style={{ display: "inline-flex", alignItems: "center", gap: 5 }}>
                  <i className="dot" style={{ background: f.color, width: 7, height: 7 }} /> {f.lang}
                </span>
                {f.status !== "modified" && <span>{f.status}</span>}
                {f.usedBy > 0 && <span>used by {f.usedBy} in this PR</span>}
                {f.reach > 0 && <span style={{ color: "var(--ripple1)" }}>reaches {f.reach}</span>}
              </div>
            </div>
            <span className="delta">
              <span className="plus">+{f.additions}</span>
              <span className="minus">−{f.deletions}</span>
            </span>
          </li>
        ))}
      </ol>
      {files.length > 12 && (
        <button className="btn btn-ghost more-btn" onClick={() => setAll(!all)}>
          {all ? "Show fewer" : `Show all ${files.length} files`}
        </button>
      )}
    </div>
  );
}

const DEPTH_TEXT = ["", "Imports the changed code directly", "Two steps away", "Three steps away"];
const DEPTH_COLOR = ["", "var(--ripple1)", "var(--ripple2)", "var(--ripple3)"];

function Ripple({ impacted, total, onActive }: { impacted: Impacted[]; total: number; onActive: (p: string | null) => void }) {
  const [all, setAll] = useState(false);
  if (total === 0) return null;
  const groups = ([1, 2, 3] as const).map((d) => impacted.filter((i) => i.depth === d)).filter((g) => g.length > 0);
  return (
    <div className="card card-pad">
      <h2>
        Ripple effect <span className="count">{num(total)}</span>
      </h2>
      {groups.map((g) => {
        const d = g[0].depth;
        const shown = all ? g : g.slice(0, 8);
        return (
          <div className="ripple-group" key={d}>
            <h3>
              <i className="dot" style={{ background: DEPTH_COLOR[d] }} />
              {DEPTH_TEXT[d]} <span className="faint">{g.length}</span>
            </h3>
            <ul className="path-list">
              {shown.map((i) => (
                <li key={i.path} title={`${i.path}\nvia ${i.via}`} onMouseEnter={() => onActive(i.path)} onMouseLeave={() => onActive(null)}>
                  {i.path}
                </li>
              ))}
            </ul>
          </div>
        );
      })}
      {impacted.length > 8 && (
        <button className="btn btn-ghost more-btn" onClick={() => setAll(!all)}>
          {all ? "Show fewer" : "Show all"}
        </button>
      )}
      {total > impacted.length && <p className="faint" style={{ fontSize: 12.5, margin: "10px 0 0" }}>Showing {num(impacted.length)} of {num(total)}.</p>}
    </div>
  );
}

function Tests({ tests }: { tests: PRReport["tests"] }) {
  if (tests.changed.length === 0 && tests.affected.length === 0) return null;
  return (
    <div className="card card-pad">
      <h2>Tests</h2>
      {tests.changed.length > 0 && (
        <>
          <div className="muted" style={{ fontSize: 13 }}>Changed in this PR</div>
          <ul className="path-list" style={{ marginBottom: 14 }}>
            {tests.changed.slice(0, 20).map((t) => <li key={t} title={t}>{t}</li>)}
          </ul>
        </>
      )}
      {tests.affected.length > 0 && (
        <>
          <div className="muted" style={{ fontSize: 13 }}>Existing tests that import the change. Run these.</div>
          <ul className="path-list">
            {tests.affected.slice(0, 20).map((t) => <li key={t} title={t}>{t}</li>)}
          </ul>
          {tests.affected.length > 20 && <p className="faint" style={{ fontSize: 12.5, margin: "8px 0 0" }}>and {tests.affected.length - 20} more</p>}
        </>
      )}
    </div>
  );
}

function Areas({ areas }: { areas: PRReport["areas"] }) {
  const top = areas.slice(0, 10);
  const max = Math.max(1, ...top.map((a) => a.changed + a.impacted));
  return (
    <div className="card card-pad">
      <h2>Areas</h2>
      <div className="areas">
        {top.map((a) => (
          <div className="area-row" key={a.name}>
            <span className="n" title={a.name}>{a.name}</span>
            <div className="bar" title={`${a.changed} changed, ${a.impacted} in the ripple`}>
              <span style={{ width: `${(a.changed / max) * 100}%`, background: "var(--changed)" }} />
              <span style={{ width: `${(a.impacted / max) * 100}%`, background: "var(--ripple1)", opacity: 0.7 }} />
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

function cap(s: string) {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

/** Renders `backticked` spans from the summary as code. */
function withCode(s: string) {
  return s.split(/(`[^`]+`)/).map((part, i) => (part.startsWith("`") ? <code key={i}>{part.slice(1, -1)}</code> : part));
}
