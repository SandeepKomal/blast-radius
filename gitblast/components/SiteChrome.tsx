import Link from "next/link";
import { SITE } from "@/lib/site";
import { LogoMark } from "./Logo";
import { UrlBox } from "./UrlBox";

export function SiteHeader({ search = true }: { search?: boolean }) {
  return (
    <header className="site-header">
      <div className="wrap">
        <Link href="/" className="logo" aria-label="gitblast home">
          <LogoMark />
          <span>gitblast</span>
        </Link>
        {search && (
          <div className="header-search">
            <UrlBox compact />
          </div>
        )}
        <nav className="header-links" style={search ? undefined : { marginLeft: "auto" }}>
          <Link href="/#how">How it works</Link>
          <a href={SITE.source} target="_blank" rel="noopener noreferrer">
            Source
          </a>
        </nav>
      </div>
    </header>
  );
}

export function SiteFooter() {
  return (
    <footer className="site-footer">
      <div className="wrap">
        <div>
          gitblast is open source under the MIT licence. It is not affiliated with or endorsed by GitHub, Inc.
        </div>
        <nav>
          <Link href="/privacy">Privacy</Link>
          <Link href="/privacy#opt-out">Opt out</Link>
          <a href={SITE.source} target="_blank" rel="noopener noreferrer">
            Source
          </a>
        </nav>
      </div>
    </footer>
  );
}
