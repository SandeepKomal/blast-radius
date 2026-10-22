import type { Metadata } from "next";
import { SiteFooter, SiteHeader } from "@/components/SiteChrome";
import { SITE } from "@/lib/site";

export const metadata: Metadata = { title: "Privacy and opt-out" };

export default function Privacy() {
  return (
    <>
      <SiteHeader />
      <main className="wrap" style={{ paddingBottom: 96 }}>
        <article className="prose">
          <h1>Privacy and opt-out</h1>
          <p>The short version: gitblast looks only at public repositories, keeps no code and doesn&apos;t track you.</p>

          <h2>What gitblast reads</h2>
          <p>
            Only public repositories, through GitHub&apos;s public API and source downloads. Private repositories are
            refused even when GitHub could serve them. There is no sign-in, so gitblast never acts on your behalf.
          </p>

          <h2>What it keeps</h2>
          <p>
            Source code is read in memory to find imports and thrown away when the analysis finishes. Only the result
            (file paths, counts and the dependency map) is cached for a few hours so that popular links stay fast. Commit
            messages, usernames and avatars come from GitHub as they are publicly shown there. Email addresses are never
            stored or shown.
          </p>

          <h2>What it doesn&apos;t do</h2>
          <ul>
            <li>No cookies, no analytics, no tracking pixels, no advertising.</li>
            <li>No AI model is sent your code. Every result comes from parsing the code directly.</li>
            <li>Nothing is written to GitHub. gitblast only reads.</li>
          </ul>

          <h2 id="opt-out">Opting out</h2>
          <p>
            If you own a repository and don&apos;t want it shown here, add the topic <code>gitblast-optout</code> to it on
            GitHub. gitblast checks for that topic before every analysis. You can also{" "}
            <a href={SITE.contact} target="_blank" rel="noopener noreferrer">
              open an issue
            </a>{" "}
            to have a whole account excluded.
          </p>

          <h2>Trademarks</h2>
          <p>
            GitHub is a trademark of GitHub, Inc. gitblast is an independent open-source project and is not affiliated
            with, sponsored by or endorsed by GitHub.
          </p>
        </article>
      </main>
      <SiteFooter />
    </>
  );
}
