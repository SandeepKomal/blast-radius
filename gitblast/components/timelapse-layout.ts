// Turns time-lapse frames into circle layouts that can be blended into each other.

import { hierarchy, pack } from "d3-hierarchy";
import type { Frame } from "@/lib/types";

export interface Placed {
  key: string;
  x: number;
  y: number;
  r: number;
  depth: number;
  leaf: boolean;
  top: string;
  files: number;
}

interface Tree {
  name: string;
  path: string;
  value?: number;
  children?: Tree[];
}

/** Marks the files that sit directly in a folder that also has subfolders. */
export const HERE = "·";

function toTree(dirs: Record<string, number>): Tree {
  interface Build { path: string; own: number; kids: Map<string, Build> }
  const root: Build = { path: "", own: 0, kids: new Map() };
  for (const [key, count] of Object.entries(dirs)) {
    let node = root;
    if (key) {
      for (const part of key.split("/")) {
        let next = node.kids.get(part);
        if (!next) {
          next = { path: node.path ? `${node.path}/${part}` : part, own: 0, kids: new Map() };
          node.kids.set(part, next);
        }
        node = next;
      }
    }
    node.own += count;
  }
  const convert = (b: Build): Tree => {
    const name = b.path.slice(b.path.lastIndexOf("/") + 1) || "/";
    if (b.kids.size === 0) return { name, path: b.path, value: b.own };
    const children = [...b.kids.values()].map(convert);
    if (b.own > 0) children.push({ name: HERE, path: b.path ? `${b.path}/${HERE}` : HERE, value: b.own });
    return { name, path: b.path, children };
  };
  return convert(root);
}

/**
 * Lays out every frame in a unit square. Children are ordered by their size
 * in the final frame, so folders keep roughly the same place as they grow.
 */
export function layoutFrames(frames: Frame[]): Map<string, Placed>[] {
  const last = frames[frames.length - 1];
  const finalSize = new Map<string, number>();
  if (last) {
    hierarchy(toTree(last.dirs))
      .sum((d) => d.value ?? 0)
      .each((n) => finalSize.set(n.data.path, n.value ?? 0));
  }
  const maxFiles = Math.max(1, ...frames.map((f) => f.files));

  return frames.map((f) => {
    const h = hierarchy(toTree(f.dirs))
      .sum((d) => d.value ?? 0)
      .sort((a, b) => (finalSize.get(b.data.path) ?? 0) - (finalSize.get(a.data.path) ?? 0) || a.data.path.localeCompare(b.data.path));
    const laid = pack<Tree>().size([1, 1]).padding((n) => (n.depth === 0 ? 0.012 : 0.004))(h);
    const growth = 0.22 + 0.78 * Math.sqrt(f.files / maxFiles);
    const out = new Map<string, Placed>();
    for (const n of laid.descendants()) {
      if (n.depth === 0) continue;
      const top = n.data.path.split("/")[0];
      out.set(n.data.path, {
        key: n.data.path,
        x: 0.5 + (n.x - 0.5) * growth,
        y: 0.5 + (n.y - 0.5) * growth,
        r: n.r * growth,
        depth: n.depth,
        leaf: !n.children,
        top,
        files: n.value ?? 0,
      });
    }
    return out;
  });
}

/** Blends two layouts. New circles grow in place; vanishing ones shrink away. */
export function blend(a: Map<string, Placed>, b: Map<string, Placed>, k: number): (Placed & { fresh: number })[] {
  const out: (Placed & { fresh: number })[] = [];
  for (const [key, pb] of b) {
    const pa = a.get(key);
    if (pa) {
      out.push({ ...pb, x: pa.x + (pb.x - pa.x) * k, y: pa.y + (pb.y - pa.y) * k, r: pa.r + (pb.r - pa.r) * k, fresh: 0 });
    } else {
      out.push({ ...pb, r: pb.r * k, fresh: 1 - k });
    }
  }
  for (const [key, pa] of a) {
    if (!b.has(key)) out.push({ ...pa, r: pa.r * (1 - k), fresh: 0 });
  }
  return out.sort((p, q) => p.depth - q.depth || Number(p.leaf) - Number(q.leaf));
}

/**
 * One cool family (blues, indigos, violets, teals) of similar weight, so the
 * map reads as a single object rather than a box of crayons.
 */
export const PALETTE = ["#0a84ff", "#5e5ce6", "#40c8e0", "#9b6cf0", "#3d9bf5", "#7a78f2", "#2fb5c9", "#b48af5", "#5aa9ff", "#6f8cf7"];

/** Loose files in the repository root are not an area of their own, so they get a quiet slate. */
const ROOT_FILES = "#8b95a8";

/** Gives the biggest top-level folders (in the final frame) their own colour. */
export function topColors(final: Map<string, Placed> | undefined): Map<string, string> {
  const tops = [...(final?.values() ?? [])].filter((p) => p.depth === 1).sort((a, b) => b.files - a.files);
  const m = new Map<string, string>();
  let i = 0;
  for (const t of tops) m.set(t.top, t.top === HERE ? ROOT_FILES : PALETTE[i++ % PALETTE.length]);
  return m;
}
