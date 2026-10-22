import Link from "next/link";
import { SiteFooter, SiteHeader } from "@/components/SiteChrome";
import { UrlBox } from "@/components/UrlBox";

const EXAMPLES = ["facebook/react", "vercel/next.js", "golang/go", "rust-lang/rust", "sandeepkomal/blast-radius"];

export default function Home() {
  return (
    <>
      <SiteHeader search={false} />
      <main>
        <section className="hero wrap">
          <div className="eyebrow gradient-text">gitblast</div>
          <h1>See what a pull request really touches.</h1>
          <p className="lede">
            Change one word in any GitHub link. Get the blast radius of a pull request, or watch a repository grow from
            its first commit.
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

        <section className="band band-alt">
          <div className="wrap">
            <div className="band-head">
              <h2>Two ways to see a repository.</h2>
              <p>Both work on any public repository, with nothing to install and no sign-in.</p>
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
            <div className="band-head" style={{ marginBottom: 32 }}>
              <h2>Try it on any repository.</h2>
              <p>Paste a link, or just change github to gitblast in your address bar.</p>
            </div>
            <div className="hero-input">
              <UrlBox />
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
