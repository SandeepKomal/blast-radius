import { ImageResponse } from "next/og";
import { parseSegments } from "@/lib/route";

const size = { width: 1200, height: 630 };

// The social preview card. It only uses the path, so it never calls GitHub.
export function GET(req: Request) {
  const path = new URL(req.url).searchParams.get("path") ?? "";
  const route = parseSegments(path.split("/"));
  const name = route ? `${route.owner}/${route.repo}` : "gitblast";
  const kicker = route?.kind === "pr" ? `Pull request #${route.number}` : "Repository time-lapse";
  const line = route?.kind === "pr" ? "What it changes, what imports it, what to review first." : "Watch it grow from the first commit to today.";

  return new ImageResponse(
    (
      <div
        style={{
          width: "100%",
          height: "100%",
          display: "flex",
          flexDirection: "column",
          justifyContent: "space-between",
          padding: 72,
          background: "radial-gradient(circle at 85% 20%, rgba(255,90,54,0.35), transparent 45%), #07080b",
          color: "#eef0f4",
          fontFamily: "sans-serif",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 18, fontSize: 34, fontWeight: 600 }}>
          <div style={{ width: 40, height: 40, borderRadius: 20, background: "linear-gradient(135deg,#ffb020,#ff5a36,#ff2e7e)" }} />
          gitblast
        </div>
        <div style={{ display: "flex", flexDirection: "column", gap: 18 }}>
          <div style={{ fontSize: 30, color: "#ff8a5c" }}>{kicker}</div>
          <div style={{ fontSize: name.length > 30 ? 60 : 76, fontWeight: 700, letterSpacing: -2 }}>{name}</div>
          <div style={{ fontSize: 30, color: "#9097a8" }}>{line}</div>
        </div>
      </div>
    ),
    { ...size, headers: { "Cache-Control": "public, max-age=86400, s-maxage=604800, immutable" } },
  );
}
