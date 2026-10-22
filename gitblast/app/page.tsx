import Link from "next/link";
import { SiteFooter, SiteHeader } from "@/components/SiteChrome";
import { UrlBox } from "@/components/UrlBox";

const REPO_EXAMPLES = ["facebook/react", "vercel/next.js", "golang/go"];
const PR_EXAMPLES = ["sandeepkomal/blast-radius/pull/1"];

export default function Home() {
  return (
    <>
      <SiteHeader search={false} />
      <main>
        <section className="hero wrap">
          <div className="eyebrow gradient-text">gitblast</div>
          <h1>Change one word. See your code differently.</h1>
          <p className="lede">
            Swap github for gitblast in any link. Two tools, no install and no sign-in: the blast radius of a pull
            request, and a time-lapse of how a repository grew.
          </p>

          <div className="swap" aria-label="Replace github with gitblast in the address">
            <span className="faint">https://git</span>
            <span className="slot">
              <span className="old">hub</span>
              <span className="new gradient-text" aria-hidden="true">
                blast
              </span>
            </span>
            <span className="faint">.com/</span>
            <span>owner/repo</span>
          </div>

          <Tools />
        </section>

        <section className="band band-alt">
          <div className="wrap">
            <div className="band-head">
              <h2>What each tool shows you.</h2>
              <p>Both work on any public repository and read the code itself, so every result can be traced back.</p>
            </div>
            <div className="features">
              <article className="card feature">
                <div className="kicker">/owner/repo/pull/123</div>
                <h3>Blast radius.</h3>
                <p>
                  Every changed file on a map of the whole repository, with the code that imports it rippling outwards.
                  Plus the tests that cover it, a risk score and the best order to review it in.
                </p>
                <div className="art">
                  <BlastArt />
                </div>
              </article>
              <article className="card feature">
                <div className="kicker">/owner/repo</div>
                <h3>Time-lapse.</h3>
                <p>
                  Watch the codebase grow from its first commit to today. New folders appear, languages arrive, and big
                  rewrites show up as sudden changes in shape.
                </p>
                <div className="art">
                  <TimelapseArt />
                </div>
              </article>
            </div>
          </div>
        </section>

        <section className="band" id="how">
          <div className="wrap">
            <div className="band-head">
              <h2>No guessing. No AI.</h2>
              <p>gitblast reads code the way your editor does, so every line on the map can be traced back to an import.</p>
            </div>
            <div className="steps">
              <div className="card step band-alt">
                <div className="n gradient-text">1</div>
                <h4>Reads the source.</h4>
                <p>It downloads the public source at the pull request&apos;s latest commit and keeps it in memory only.</p>
              </div>
              <div className="card step band-alt">
                <div className="n gradient-text">2</div>
                <h4>Follows the imports.</h4>
                <p>TypeScript, JavaScript, Python, Go, Rust, Java, Kotlin, Ruby, PHP, Dart, C and C++.</p>
              </div>
              <div className="card step band-alt">
                <div className="n gradient-text">3</div>
                <h4>Walks outwards.</h4>
                <p>Up to three steps through everything that imports the change, then it flags what needs a closer look.</p>
              </div>
            </div>
          </div>
        </section>

        <section className="band band-alt closing">
          <div className="wrap">
            <div className="band-head" style={{ marginBottom: 40 }}>
              <h2>Try it on any repository.</h2>
              <p>Paste a link below, or just change github to gitblast in your address bar.</p>
            </div>
            <Tools compact />
          </div>
        </section>
      </main>
      <SiteFooter />
    </>
  );
}

/** The two tools side by side, each with its own search box. */
function Tools({ compact = false }: { compact?: boolean }) {
  return (
    <div className={`tools${compact ? " tools-compact" : ""}`}>
      <div className="card tool">
        <div className="tool-icon" style={{ background: "var(--blast)" }} aria-hidden="true">
          <BlastIcon />
        </div>
        <h2>Blast radius</h2>
        <p>For a pull request: what it changes, what imports the changed code and what to review first.</p>
        <UrlBox mode="pr" placeholder="github.com/owner/repo/pull/123" cta="Show blast radius" />
        {!compact && (
          <div className="examples">
            <span className="faint">Try</span>
            {PR_EXAMPLES.map((e) => (
              <Link key={e} href={`/${e}`}>
                {e.replace("/pull/", "#")}
              </Link>
            ))}
          </div>
        )}
      </div>
      <div className="card tool">
        <div className="tool-icon" style={{ background: "linear-gradient(135deg, #40c8e0, #0a84ff 55%, #5e5ce6)" }} aria-hidden="true">
          <TimelapseIcon />
        </div>
        <h2>Time-lapse</h2>
        <p>For a repository: watch it grow from the first commit to today, folder by folder.</p>
        <UrlBox mode="repo" placeholder="github.com/owner/repo" cta="Play time-lapse" />
        {!compact && (
          <div className="examples">
            <span className="faint">Try</span>
            {REPO_EXAMPLES.map((e) => (
              <Link key={e} href={`/${e}`}>
                {e}
              </Link>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function BlastIcon() {
  return (
    <svg viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="#fff" strokeWidth="1.8">
      <circle cx="12" cy="12" r="3" fill="#fff" stroke="none" />
      <circle cx="12" cy="12" r="6.5" strokeOpacity="0.8" />
      <circle cx="12" cy="12" r="10" strokeOpacity="0.45" />
    </svg>
  );
}

function TimelapseIcon() {
  return (
    <svg viewBox="0 0 24 24" width="24" height="24" fill="#fff">
      <circle cx="5" cy="15" r="2" fillOpacity="0.6" />
      <circle cx="11" cy="13" r="3.2" fillOpacity="0.8" />
      <circle cx="18.5" cy="10.5" r="4.5" />
    </svg>
  );
}

function BlastArt() {
  const rings = [
    { r: 104, c: "var(--line)" },
    { r: 74, c: "var(--ripple2)" },
    { r: 46, c: "var(--ripple1)" },
  ];
  const dots = [
    [160, 125, 9, "var(--changed)"], [184, 108, 6, "var(--changed)"], [146, 102, 5, "var(--added)"],
    [212, 146, 5, "var(--ripple1)"], [122, 146, 5, "var(--ripple1)"], [168, 166, 4.5, "var(--ripple1)"],
    [232, 100, 4, "var(--ripple2)"], [96, 108, 4, "var(--ripple2)"], [196, 192, 4, "var(--ripple2)"],
    [112, 184, 4, "var(--ripple2)"], [252, 158, 3.5, "var(--ripple3)"], [70, 156, 3.5, "var(--ripple3)"],
  ] as const;
  return (
    <svg viewBox="0 0 320 250" width="100%" height="100%" aria-hidden="true">
      {rings.map((r) => (
        <circle key={r.r} cx="160" cy="125" r={r.r} fill="none" style={{ stroke: r.c }} strokeOpacity="0.6" strokeDasharray="2 5" />
      ))}
      {dots.slice(3).map(([x, y], i) => (
        <path key={i} d={`M${x} ${y} Q160 125 165 115`} fill="none" style={{ stroke: "var(--ripple1)" }} strokeOpacity="0.35" />
      ))}
      {dots.map(([x, y, r, c], i) => (
        <circle key={i} cx={x} cy={y} r={r} style={{ fill: c }} />
      ))}
    </svg>
  );
}

function TimelapseArt() {
  const steps = [
    { x: 58, r: 15 },
    { x: 128, r: 28 },
    { x: 226, r: 52 },
  ];
  return (
    <svg viewBox="0 0 320 250" width="100%" height="100%" aria-hidden="true">
      <line x1="20" y1="222" x2="300" y2="222" style={{ stroke: "var(--line)" }} />
      {steps.map((s, i) => (
        <g key={i} opacity={0.4 + i * 0.3}>
          <circle cx={s.x} cy={130} r={s.r} style={{ fill: "var(--map-folder)", stroke: "var(--map-folder-line)" }} />
          <circle cx={s.x - s.r * 0.35} cy={130 - s.r * 0.2} r={s.r * 0.42} fill="#0a84ff" />
          <circle cx={s.x + s.r * 0.42} cy={130 + s.r * 0.18} r={s.r * 0.32} fill="#5e5ce6" />
          {i > 0 && <circle cx={s.x + s.r * 0.05} cy={130 + s.r * 0.62} r={s.r * 0.22} fill="#40c8e0" />}
          <circle cx={s.x} cy={222} r="3.5" style={{ fill: i === 2 ? "var(--text)" : "var(--line)" }} />
        </g>
      ))}
    </svg>
  );
}
