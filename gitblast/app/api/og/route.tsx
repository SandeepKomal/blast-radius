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
          padding: 80,
          background: "#fbfbfd",
          color: "#1d1d1f",
          fontFamily: "sans-serif",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 14, fontSize: 32, fontWeight: 600 }}>
          <div style={{ width: 34, height: 34, borderRadius: 17, background: "linear-gradient(90deg,#ff9500,#ff3b30,#ff2d55)" }} />
          gitblast
        </div>
        <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
          <div
            style={{
              fontSize: 32,
              fontWeight: 600,
              backgroundImage: "linear-gradient(90deg,#ff9500,#ff3b30,#ff2d55)",
              backgroundClip: "text",
              color: "transparent",
            }}
          >
            {kicker}
          </div>
          <div style={{ fontSize: name.length > 30 ? 64 : 84, fontWeight: 700, letterSpacing: -3 }}>{name}</div>
          <div style={{ fontSize: 32, color: "#6e6e73" }}>{line}</div>
        </div>
      </div>
    ),
    { ...size, headers: { "Cache-Control": "public, max-age=86400, s-maxage=604800, immutable" } },
  );
}
