import Link from "next/link";
import { SiteFooter, SiteHeader } from "@/components/SiteChrome";

export default function NotFound() {
  return (
    <>
      <SiteHeader />
      <main className="alt">
        <div className="wrap">
        <div className="card error-box">
          <h1>That isn&apos;t a GitHub link</h1>
          <p>
            gitblast understands <code>/owner/repo</code> and <code>/owner/repo/pull/123</code>. Change <code>github</code>{" "}
            to <code>gitblast</code> in any repository or pull request link.
          </p>
          <div className="actions">
            <Link href="/" className="btn btn-primary">
              Go to gitblast
            </Link>
          </div>
        </div>
        </div>
      </main>
      <SiteFooter />
    </>
  );
}
