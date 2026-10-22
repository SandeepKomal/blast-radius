import Link from "next/link";
import { SiteFooter, SiteHeader } from "@/components/SiteChrome";
import { UrlBox } from "@/components/UrlBox";

const EXAMPLES = ["facebook/react", "vercel/next.js", "golang/go", "rust-lang/rust", "sandeepkomal/blast-radius"];

export default function Home() {
  return (
    <>
      <SiteHeader search={false} />
      <main className="wrap">
        <section className="hero">
          <div className="eyebrow">
            <span className="dot" style={{ background: "var(--accent)" }} /> Free, open source, no sign-in
          </div>
          <h1>
            See what a pull request <span className="gradient-text">really touches</span>
          </h1>
          <p className="lede">
            Change one word in any GitHub link. gitblast maps the blast radius of a pull request and replays how a
            repository grew, commit by commit.
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
            <span>owner/repo/pull/123</span>
          </div>

          <div className="hero-input">
            <UrlBox autoFocus />
            <div className="examples">
              {EXAMPLES.map((e) => (
                <Link key={e} href={`/${e}`}>
                  {e}
                </Link>
              ))}
            </div>
          </div>
        </section>

        <section className="features">
          <article className="card feature">
            <div className="art">
              <BlastArt />
            </div>
            <h3>Pull request blast radius</h3>
            <p>
              <code>/owner/repo/pull/123</code>. Every changed file on a map of the whole repository, with the code that
              imports it rippling outwards. You also get the tests that cover the change, a risk score and a suggested
              order for reading the files.
            </p>
          </article>
          <article className="card feature">
            <div className="art">
              <TimelapseArt />
            </div>
            <h3>Repository time-lapse</h3>
            <p>
              <code>/owner/repo</code>. Watch the codebase grow from the first commit to today. Folders appear, languages
              arrive and big rewrites show up as sudden changes in the shape.
            </p>
          </article>
        </section>

        <section className="section" id="how">
          <h2>How it works</h2>
          <p>gitblast reads code the same way your editor does. It doesn&apos;t guess, and no AI looks at your code.</p>
          <div className="steps">
            <div className="card step">
              <div className="n">01</div>
              <h4>Reads the source</h4>
              <p>It downloads the public source at the pull request&apos;s latest commit and keeps it in memory only.</p>
            </div>
            <div className="card step">
              <div className="n">02</div>
              <h4>Follows the imports</h4>
              <p>
                It resolves imports in TypeScript, JavaScript, Python, Go, Rust, Java, Kotlin, Ruby, PHP, Dart, C and C++
                to build the dependency graph.
              </p>
            </div>
            <div className="card step">
              <div className="n">03</div>
              <h4>Walks outwards</h4>
              <p>
                It starts from the changed files and walks up to three steps through everything that imports them, then
                flags what deserves a closer look.
              </p>
            </div>
          </div>
        </section>
      </main>
      <SiteFooter />
    </>
  );
}

function BlastArt() {
  const rings = [
    { r: 92, c: "rgba(255,255,255,0.06)" },
    { r: 66, c: "rgba(255,217,138,0.18)" },
    { r: 42, c: "rgba(255,176,32,0.35)" },
  ];
  const dots = [
    [150, 110, 7, "var(--changed)"], [170, 96, 5, "var(--changed)"], [140, 92, 4, "var(--added)"],
    [196, 130, 4, "var(--ripple1)"], [118, 128, 4, "var(--ripple1)"], [160, 144, 3.5, "var(--ripple1)"],
    [214, 92, 3, "var(--ripple2)"], [96, 100, 3, "var(--ripple2)"], [182, 168, 3, "var(--ripple2)"],
    [110, 160, 3, "var(--ripple2)"], [232, 140, 2.5, "var(--ripple3)"], [80, 140, 2.5, "var(--ripple3)"],
  ] as const;
  return (
    <svg viewBox="0 0 300 220" width="100%" height="100%" aria-hidden="true">
      {rings.map((r) => (
        <circle key={r.r} cx="150" cy="115" r={r.r} fill="none" stroke={r.c} strokeDasharray="2 4" />
      ))}
      {dots.slice(3).map(([x, y], i) => (
        <path key={i} d={`M${x} ${y} Q150 115 155 105`} stroke="rgba(255,176,32,0.25)" fill="none" />
      ))}
      {dots.map(([x, y, r, c], i) => (
        <circle key={i} cx={x} cy={y} r={r} fill={c} />
      ))}
    </svg>
  );
}

function TimelapseArt() {
  const steps = [
    { x: 50, r: 14 },
    { x: 115, r: 26 },
    { x: 205, r: 46 },
  ];
  return (
    <svg viewBox="0 0 300 220" width="100%" height="100%" aria-hidden="true">
      <line x1="20" y1="190" x2="280" y2="190" stroke="rgba(255,255,255,0.1)" />
      {steps.map((s, i) => (
        <g key={i} opacity={0.45 + i * 0.27}>
          <circle cx={s.x} cy={110} r={s.r} fill="rgba(255,255,255,0.03)" stroke="rgba(255,255,255,0.14)" />
          <circle cx={s.x - s.r * 0.35} cy={110 - s.r * 0.2} r={s.r * 0.42} fill="#3178c6" opacity="0.8" />
          <circle cx={s.x + s.r * 0.42} cy={110 + s.r * 0.18} r={s.r * 0.32} fill="#ff5a36" opacity="0.85" />
          {i > 0 && <circle cx={s.x + s.r * 0.05} cy={110 + s.r * 0.62} r={s.r * 0.22} fill="#2ee6a8" opacity="0.8" />}
          <circle cx={s.x} cy={190} r="3" fill={i === 2 ? "#fff" : "rgba(255,255,255,0.4)"} />
        </g>
      ))}
    </svg>
  );
}
