"use client";

import { hierarchy, pack, type HierarchyCircularNode } from "d3-hierarchy";
import { useEffect, useMemo, useRef, useState, type MouseEvent } from "react";
import type { MapNode } from "@/lib/types";

const SIZE = 1000;

export const STATE_COLOR: Record<NonNullable<MapNode["state"]>, string> = {
  changed: "var(--changed)",
  added: "var(--added)",
  removed: "var(--removed)",
  ripple1: "var(--ripple1)",
  ripple2: "var(--ripple2)",
  ripple3: "var(--ripple3)",
};

const STATE_LABEL: Record<NonNullable<MapNode["state"]>, string> = {
  changed: "Changed in this PR",
  added: "Added in this PR",
  removed: "Deleted in this PR",
  ripple1: "Imports the change directly",
  ripple2: "Two steps from the change",
  ripple3: "Three steps from the change",
};

type Circle = HierarchyCircularNode<MapNode>;
interface View { x: number; y: number; r: number }

export function PackMap({
  map,
  edges,
  active,
  onActive,
  fileUrl,
}: {
  map: MapNode;
  edges: [string, string][];
  active: string | null;
  onActive: (path: string | null) => void;
  fileUrl: (path: string) => string;
}) {
  const root = useMemo(() => {
    const h = hierarchy(map)
      .sum((d) => (d.children ? 0 : d.value ?? 0))
      .sort((a, b) => (b.value ?? 0) - (a.value ?? 0));
    return pack<MapNode>().size([SIZE, SIZE]).padding((d) => (d.depth === 0 ? 8 : 3))(h);
  }, [map]);

  const byPath = useMemo(() => {
    const m = new Map<string, Circle>();
    for (const n of root.descendants()) m.set(n.data.path, n);
    return m;
  }, [root]);

  const full: View = { x: SIZE / 2, y: SIZE / 2, r: SIZE / 2 };
  const [focus, setFocus] = useState<Circle>(root);
  const [view, setView] = useState<View>(full);
  const viewRef = useRef(view);
  viewRef.current = view;
  const [tip, setTip] = useState<{ x: number; y: number; node: Circle } | null>(null);
  const wrapRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setFocus(root);
    setView({ x: SIZE / 2, y: SIZE / 2, r: SIZE / 2 });
  }, [root]);

  function zoomTo(n: Circle) {
    setFocus(n);
    const from = viewRef.current;
    const to = { x: n.x, y: n.y, r: n.r * 1.04 };
    const start = performance.now();
    const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    const dur = reduce ? 0 : 650;
    const step = (now: number) => {
      const t = dur === 0 ? 1 : Math.min(1, (now - start) / dur);
      const k = t < 0.5 ? 4 * t * t * t : 1 - (-2 * t + 2) ** 3 / 2;
      setView({ x: from.x + (to.x - from.x) * k, y: from.y + (to.y - from.y) * k, r: from.r + (to.r - from.r) * k });
      if (t < 1) requestAnimationFrame(step);
    };
    requestAnimationFrame(step);
  }

  const focusRef = useRef(focus);
  focusRef.current = focus;
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const parent = focusRef.current.parent;
      if (e.key === "Escape" && parent) zoomTo(parent);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // zoomTo only reads refs, so registering once is enough.
  }, []);

  const scale = SIZE / 2 / view.r;
  const vb = `${view.x - view.r} ${view.y - view.r} ${view.r * 2} ${view.r * 2}`;

  const activeEdges = new Set<number>();
  if (active) edges.forEach(([a, b], i) => (a === active || b === active) && activeEdges.add(i));

  function showTip(e: MouseEvent, node: Circle) {
    const box = wrapRef.current?.getBoundingClientRect();
    if (!box) return;
    setTip({ x: e.clientX - box.left, y: e.clientY - box.top, node });
    if (node.data.state) onActive(node.data.path);
  }

  const nodes = root.descendants();
  const labelled = nodes.filter(
    (n) => n.children && n.depth === focus.depth + 1 && n.r * scale > 70 && isInside(n, focus),
  );

  return (
    <div className="map-wrap" ref={wrapRef} onMouseLeave={() => { setTip(null); onActive(null); }}>
      <svg viewBox={vb} role="img" aria-label="Map of the repository with the files this pull request changes and the files that import them">
        <rect x={view.x - view.r} y={view.y - view.r} width={view.r * 2} height={view.r * 2} fill="transparent" onClick={() => focus.parent && zoomTo(focus.parent)} />
        {nodes.map((n, i) => {
          if (n.depth === 0) return null;
          const d = n.data;
          if (n.children) {
            return (
              <circle
                key={i}
                cx={n.x}
                cy={n.y}
                r={n.r}
                vectorEffect="non-scaling-stroke"
                style={{ fill: "var(--map-folder)", stroke: "var(--map-folder-line)", cursor: "zoom-in" }}
                onClick={(e) => { e.stopPropagation(); zoomTo(n === focus && n.parent ? n.parent : n); }}
                onMouseMove={(e) => showTip(e, n)}
              />
            );
          }
          const s = d.state;
          const isActive = active === d.path;
          return (
            <circle
              key={i}
              cx={n.x}
              cy={n.y}
              r={Math.max(n.r, 2.2 / scale)}
              strokeWidth={isActive ? 2.5 : 1.5}
              strokeDasharray={s === "removed" ? "3 3" : undefined}
              vectorEffect="non-scaling-stroke"
              opacity={active && !isActive && s ? 0.45 : 1}
              style={{
                fill: s ? (s === "removed" ? "transparent" : STATE_COLOR[s]) : "var(--map-rest)",
                stroke: s === "removed" ? STATE_COLOR.removed : isActive ? "var(--map-edge-active)" : "none",
                cursor: s && s !== "removed" ? "pointer" : "default",
                transition: "opacity .2s",
              }}
              onMouseMove={(e) => showTip(e, n)}
              onClick={(e) => {
                e.stopPropagation();
                if (s && s !== "removed") window.open(fileUrl(d.path), "_blank", "noopener,noreferrer");
                else if (n.parent && n.parent !== focus) zoomTo(n.parent);
              }}
            />
          );
        })}
        <g pointerEvents="none">
          {edges.map(([a, b], i) => {
            const p = byPath.get(a);
            const q = byPath.get(b);
            if (!p || !q) return null;
            const mx = (p.x + q.x) / 2 + (q.y - p.y) * 0.18;
            const my = (p.y + q.y) / 2 - (q.x - p.x) * 0.18;
            const on = activeEdges.has(i);
            return (
              <path
                key={i}
                d={`M${p.x} ${p.y} Q${mx} ${my} ${q.x} ${q.y}`}
                fill="none"
                style={{ stroke: on ? "var(--map-edge-active)" : "var(--map-edge)" }}
                strokeOpacity={on ? 0.85 : active ? 0.08 : 0.3}
                strokeWidth={on ? 1.6 : 1}
                vectorEffect="non-scaling-stroke"
              />
            );
          })}
        </g>
        <g pointerEvents="none">
          {labelled.map((n, i) => (
            <text
              key={i}
              x={n.x}
              y={n.y - n.r + 34 / scale}
              textAnchor="middle"
              fontSize={21 / scale}
              style={{ fill: "var(--map-label)", fontFamily: "var(--font)", fontWeight: 500 }}
            >
              {truncate(n.data.name, Math.floor((n.r * scale) / 8))}
            </text>
          ))}
        </g>
      </svg>
      {focus.depth > 0 && (
        <button className="btn btn-ghost btn-sm map-back" onClick={() => zoomTo(root)}>
          ← {focus.ancestors().reverse().slice(1).map((a) => a.data.name).join("/")}
        </button>
      )}
      {tip && (
        <div className="tooltip" style={{ left: tip.x, top: tip.y }}>
          <div className="p">{tip.node.data.path === "" ? "/" : tip.node.data.path.replace(/\/…$/, "/")}</div>
          <div className="s">
            {tip.node.data.state
              ? STATE_LABEL[tip.node.data.state]
              : tip.node.children
                ? `${countFiles(tip.node)} files · click to zoom`
                : `${tip.node.data.files ?? 0} file${tip.node.data.files === 1 ? "" : "s"} not affected`}
          </div>
        </div>
      )}
    </div>
  );
}

function countFiles(n: Circle): string {
  let total = 0;
  for (const l of n.leaves()) total += l.data.files ?? 0;
  return total.toLocaleString("en-US");
}

function isInside(n: Circle, focus: Circle): boolean {
  for (let p: Circle | null = n; p; p = p.parent) if (p === focus) return true;
  return false;
}

function truncate(s: string, max: number): string {
  return s.length <= max ? s : `${s.slice(0, Math.max(1, max - 1))}…`;
}

export function Legend() {
  const items: [NonNullable<MapNode["state"]>, string][] = [
    ["changed", "Changed"],
    ["added", "Added"],
    ["removed", "Deleted"],
    ["ripple1", "Imports it"],
    ["ripple2", "2 steps away"],
    ["ripple3", "3 steps away"],
  ];
  return (
    <div className="legend">
      {items.map(([s, label]) => (
        <span key={s}>
          <i
            className="dot"
            style={s === "removed" ? { border: "1.5px dashed var(--removed)" } : { background: STATE_COLOR[s] }}
          />
          {label}
        </span>
      ))}
      <span>
        <i className="dot" style={{ background: "var(--map-rest)" }} />
        Unaffected
      </span>
    </div>
  );
}
