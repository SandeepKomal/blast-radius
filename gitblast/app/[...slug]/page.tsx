import type { Metadata } from "next";
import { notFound, redirect } from "next/navigation";
import { PRView } from "@/components/PRView";
import { SiteFooter, SiteHeader } from "@/components/SiteChrome";
import { TimelapseView } from "@/components/TimelapseView";
import { parseSegments, routePath } from "@/lib/route";

interface Props {
  params: Promise<{ slug: string[] }>;
}

export async function generateMetadata({ params }: Props): Promise<Metadata> {
  const route = parseSegments((await params).slug);
  if (!route) return {};
  const name = `${route.owner}/${route.repo}`;
  const path = routePath(route);
  const images = [{ url: `/api/og?path=${encodeURIComponent(path)}`, width: 1200, height: 630 }];
  const meta =
    route.kind === "pr"
      ? {
          title: `${name} #${route.number}`,
          description: `The blast radius of pull request #${route.number} in ${name}: what it changes, what imports it and what to review first.`,
        }
      : { title: `${name} time-lapse`, description: `Watch ${name} grow from its first commit to today.` };
  return {
    ...meta,
    alternates: { canonical: path },
    openGraph: { ...meta, url: path, images },
    twitter: { card: "summary_large_image", ...meta, images },
  };
}

export default async function Page({ params }: Props) {
  const { slug } = await params;
  const route = parseSegments(slug);
  if (!route) notFound();
  const canonical = routePath(route);
  if (`/${slug.map(decodeSafe).join("/")}` !== canonical) redirect(canonical);

  return (
    <>
      <SiteHeader />
      <main className="wrap">
        {route.kind === "pr" ? (
          <PRView owner={route.owner} repo={route.repo} number={route.number} />
        ) : (
          <TimelapseView owner={route.owner} repo={route.repo} />
        )}
      </main>
      <SiteFooter />
    </>
  );
}

function decodeSafe(s: string): string {
  try {
    return decodeURIComponent(s);
  } catch {
    return s;
  }
}
