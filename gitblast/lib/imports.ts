// Finds which files import which, across the common languages, using the
// repository layout to resolve each import. It is a static, best-effort
// reading: an import that cannot be matched to a file in the repository is
// ignored, and dynamic or generated imports are not seen.

import { extOf } from "./lang";

export interface RepoFiles {
  files: Set<string>;
  /** Files that existed before the change but were deleted by it. */
  removed: Set<string>;
  byBase: Map<string, string[]>;
  byDir: Map<string, string[]>;
  /** go.mod module path → directory holding the go.mod ("" for the root). */
  goModules: { module: string; dir: string }[];
  /** package.json "name" → directory, for workspaces in monorepos. */
  packages: Map<string, string>;
}

export function indexFiles(paths: Iterable<string>, removed: Iterable<string> = []): RepoFiles {
  const idx: RepoFiles = {
    files: new Set(),
    removed: new Set(removed),
    byBase: new Map(),
    byDir: new Map(),
    goModules: [],
    packages: new Map(),
  };
  for (const p of [...paths, ...idx.removed]) {
    if (idx.files.has(p)) continue;
    idx.files.add(p);
    push(idx.byBase, base(p), p);
    push(idx.byDir, dir(p), p);
  }
  return idx;
}

/** Reads the manifests that change how imports resolve (go.mod, package.json). */
export function noteManifest(idx: RepoFiles, path: string, text: string): void {
  const b = base(path);
  if (b === "go.mod") {
    const m = text.match(/^\s*module\s+(\S+)/m);
    if (m) idx.goModules.push({ module: m[1].replace(/^"|"$/g, ""), dir: dir(path) });
  } else if (b === "package.json") {
    try {
      const name = (JSON.parse(text) as { name?: unknown }).name;
      if (typeof name === "string" && name) idx.packages.set(name, dir(path));
    } catch {
      // Not every package.json is valid JSON (templates, fixtures); skip it.
    }
  }
}

export function isManifest(path: string): boolean {
  const b = base(path);
  return b === "go.mod" || b === "package.json";
}

// ---------------------------------------------------------------------------
// Extraction: raw import strings, by language.

const JS_FROM = /(?:^|[\s;}])(?:import|export)\s[^'"`;]*?\sfrom\s*['"]([^'"\n]+)['"]/g;
const JS_BARE = /(?:^|[\s;])import\s*['"]([^'"\n]+)['"]/g;
const JS_CALL = /(?:\bimport|\brequire)\s*\(\s*['"]([^'"\n]+)['"]\s*\)/g;
const CSS_IMPORT = /@(?:import|use|forward)\s+(?:url\()?\s*['"]([^'"\n]+)['"]/g;
const PY_FROM = /^[ \t]*from[ \t]+([.\w]+)[ \t]+import[ \t]+(?:\(([^)]*)\)|([^\n#]+))/gm;
const PY_IMPORT = /^[ \t]*import[ \t]+([\w., \t]+)/gm;
const GO_BLOCK = /^import\s*\(([\s\S]*?)\)/gm;
const GO_SINGLE = /^import\s+(?:[\w.]+\s+)?"([^"]+)"/gm;
const GO_STRING = /"([^"\n]+)"/g;
const RS_MOD = /^\s*(?:pub(?:\([^)]*\))?\s+)?mod\s+(\w+)\s*;/gm;
const RS_USE = /^\s*(?:pub(?:\([^)]*\))?\s+)?use\s+crate::([\w:]+)/gm;
const JVM_IMPORT = /^\s*import\s+(?:static\s+)?([\w.]+?)(\.\*)?\s*;?\s*$/gm;
const RB_REQUIRE = /^\s*require(_relative)?\s*\(?\s*['"]([^'"\n]+)['"]/gm;
const PHP_USE = /^\s*use\s+([\w\\]+)(?:\s+as\s+\w+)?\s*;/gm;
const PHP_INCLUDE = /\b(?:require|include)(?:_once)?\s*\(?\s*(?:__DIR__\s*\.\s*)?['"]([^'"\n]+)['"]/g;
const DART_IMPORT = /^\s*(?:import|export|part)\s+['"]([^'"\n]+)['"]/gm;
const C_INCLUDE = /^\s*#\s*include\s+"([^"\n]+)"/gm;

function all(re: RegExp, text: string, group = 1): string[] {
  const out: string[] = [];
  re.lastIndex = 0;
  for (let m = re.exec(text); m; m = re.exec(text)) out.push(m[group]);
  return out;
}

/** Returns the import specifiers in a file, in a form `resolve` understands. */
export function extractImports(path: string, text: string): string[] {
  const ext = extOf(path);
  switch (ext) {
    case "ts": case "tsx": case "mts": case "cts": case "js": case "jsx":
    case "mjs": case "cjs": case "vue": case "svelte":
      return [...all(JS_FROM, text), ...all(JS_BARE, text), ...all(JS_CALL, text)];
    case "css": case "scss": case "sass": case "less":
      return all(CSS_IMPORT, text);
    case "py": {
      const out: string[] = [];
      PY_FROM.lastIndex = 0;
      for (let m = PY_FROM.exec(text); m; m = PY_FROM.exec(text)) {
        const names = (m[2] ?? m[3]).split(",").map((s) => s.trim().split(/\s+/)[0]);
        for (const n of names) if (n && n !== "*") out.push(`${m[1]}|${n}`);
        out.push(m[1]);
      }
      for (const list of all(PY_IMPORT, text)) {
        for (const part of list.split(",")) {
          const mod = part.trim().split(/\s+/)[0];
          if (mod) out.push(mod);
        }
      }
      return out;
    }
    case "go": {
      const out = all(GO_SINGLE, text);
      for (const block of all(GO_BLOCK, text)) out.push(...all(GO_STRING, block));
      return out;
    }
    case "rs":
      return [...all(RS_MOD, text).map((m) => `mod:${m}`), ...all(RS_USE, text).map((u) => `crate:${u}`)];
    case "java": case "kt": case "kts": case "scala": {
      const out: string[] = [];
      JVM_IMPORT.lastIndex = 0;
      for (let m = JVM_IMPORT.exec(text); m; m = JVM_IMPORT.exec(text)) out.push(m[2] ? `${m[1]}.*` : m[1]);
      return out;
    }
    case "rb": {
      const out: string[] = [];
      RB_REQUIRE.lastIndex = 0;
      for (let m = RB_REQUIRE.exec(text); m; m = RB_REQUIRE.exec(text)) out.push(m[1] ? `./${m[2]}` : m[2]);
      return out;
    }
    case "php":
      return [...all(PHP_USE, text).map((u) => `use:${u}`), ...all(PHP_INCLUDE, text)];
    case "dart":
      return all(DART_IMPORT, text).filter((s) => !s.startsWith("dart:"));
    case "c": case "h": case "cc": case "cpp": case "cxx": case "hpp": case "hh": case "hxx":
      return all(C_INCLUDE, text);
    default:
      return [];
  }
}

// ---------------------------------------------------------------------------
// Resolution: import strings → files in the repository.

const JS_EXTS = ["ts", "tsx", "js", "jsx", "mjs", "cjs", "mts", "cts", "vue", "svelte", "d.ts"];
const CSS_EXTS = ["css", "scss", "sass", "less"];

export function resolveImports(idx: RepoFiles, from: string, specs: string[]): string[] {
  const out = new Set<string>();
  const ext = extOf(from);
  for (const spec of specs) {
    for (const target of resolveOne(idx, from, ext, spec)) {
      if (target !== from) out.add(target);
    }
  }
  return [...out];
}

function resolveOne(idx: RepoFiles, from: string, ext: string, spec: string): string[] {
  const here = dir(from);
  switch (ext) {
    case "ts": case "tsx": case "mts": case "cts": case "js": case "jsx":
    case "mjs": case "cjs": case "vue": case "svelte":
      return opt(resolveJs(idx, here, spec));
    case "css": case "scss": case "sass": case "less": {
      if (/^(https?:)?\/\//.test(spec)) return [];
      const p = join(here, spec.replace(/^~/, ""));
      const name = base(p);
      return opt(
        firstFile(idx, [p, ...CSS_EXTS.map((e) => `${p}.${e}`), ...CSS_EXTS.map((e) => join(dir(p), `_${name}.${e}`))]),
      );
    }
    case "py":
      return opt(resolvePython(idx, here, spec));
    case "go":
      return resolveGo(idx, spec);
    case "rs":
      return opt(resolveRust(idx, from, spec));
    case "java": case "kt": case "kts": case "scala":
      return resolveJvm(idx, spec);
    case "rb":
      if (spec.startsWith("./")) return opt(firstFile(idx, withExt(join(here, spec), ["rb"])));
      return opt(firstFile(idx, withExt(`lib/${spec}`, ["rb"])) ?? suffixUnique(idx, `${spec}.rb`));
    case "php": {
      if (spec.startsWith("use:")) {
        const parts = spec.slice(4).split("\\");
        for (let i = 0; i < parts.length - 1; i++) {
          const hit = suffixUnique(idx, `${parts.slice(i).join("/")}.php`);
          if (hit) return [hit];
        }
        return [];
      }
      return opt(firstFile(idx, [join(here, spec)]));
    }
    case "dart": {
      const pkg = spec.match(/^package:[^/]+\/(.+)$/);
      if (pkg) return opt(suffixUnique(idx, `lib/${pkg[1]}`));
      return opt(firstFile(idx, [join(here, spec)]));
    }
    case "c": case "h": case "cc": case "cpp": case "cxx": case "hpp": case "hh": case "hxx":
      return opt(firstFile(idx, [join(here, spec)]) ?? suffixUnique(idx, spec));
    default:
      return [];
  }
}

function resolveJs(idx: RepoFiles, here: string, spec: string): string | null {
  const clean = spec.split("?")[0];
  let p: string | null = null;
  if (clean.startsWith("./") || clean.startsWith("../") || clean === "." || clean === "..") p = join(here, clean);
  else if (clean.startsWith("/")) p = clean.slice(1);
  else if (clean.startsWith("@/") || clean.startsWith("~/")) {
    const rest = clean.slice(2);
    return jsCandidates(idx, `src/${rest}`) ?? jsCandidates(idx, rest) ?? jsCandidates(idx, `app/${rest}`);
  } else {
    // A bare import may point at a workspace package in the same monorepo.
    let best = "";
    for (const name of idx.packages.keys()) {
      if ((clean === name || clean.startsWith(`${name}/`)) && name.length > best.length) best = name;
    }
    if (!best) return null;
    const pkgDir = idx.packages.get(best) ?? "";
    const sub = clean.slice(best.length).replace(/^\//, "");
    if (sub) return jsCandidates(idx, join(pkgDir, sub)) ?? jsCandidates(idx, join(pkgDir, `src/${sub}`));
    return jsCandidates(idx, join(pkgDir, "src/index")) ?? jsCandidates(idx, join(pkgDir, "index")) ??
      jsCandidates(idx, join(pkgDir, "lib/index"));
  }
  return jsCandidates(idx, p);
}

function jsCandidates(idx: RepoFiles, p: string): string | null {
  const c = [p, ...JS_EXTS.map((e) => `${p}.${e}`), ...JS_EXTS.map((e) => `${p}/index.${e}`)];
  // ESM TypeScript imports "./x.js" for a file named x.ts.
  const esm = p.match(/^(.*)\.(m|c)?jsx?$/);
  if (esm) c.push(`${esm[1]}.ts`, `${esm[1]}.tsx`, `${esm[1]}.mts`, `${esm[1]}.cts`);
  return firstFile(idx, c);
}

function resolvePython(idx: RepoFiles, here: string, spec: string): string | null {
  const [mod, name] = spec.split("|");
  const dots = mod.match(/^\.*/)?.[0].length ?? 0;
  const rest = mod.slice(dots);
  const parts = rest ? rest.split(".") : [];
  if (name) parts.push(name);
  const path = parts.join("/");
  if (dots > 0) {
    let d = here;
    for (let i = 1; i < dots; i++) d = dir(d);
    if (!path) return null;
    return firstFile(idx, [`${join(d, path)}.py`, `${join(d, path)}/__init__.py`]);
  }
  if (!path) return null;
  return suffixUnique(idx, `${path}.py`) ?? suffixUnique(idx, `${path}/__init__.py`);
}

function resolveGo(idx: RepoFiles, spec: string): string[] {
  for (const { module, dir: modDir } of idx.goModules) {
    if (spec !== module && !spec.startsWith(`${module}/`)) continue;
    const d = join(modDir, spec.slice(module.length).replace(/^\//, ""));
    return (idx.byDir.get(d) ?? []).filter((f) => f.endsWith(".go") && !f.endsWith("_test.go"));
  }
  return [];
}

function resolveRust(idx: RepoFiles, from: string, spec: string): string | null {
  if (spec.startsWith("mod:")) {
    const name = spec.slice(4);
    const b = base(from);
    const modDir = /^(mod|lib|main)\.rs$/.test(b) ? dir(from) : join(dir(from), b.replace(/\.rs$/, ""));
    return firstFile(idx, [join(modDir, `${name}.rs`), join(modDir, `${name}/mod.rs`)]);
  }
  // crate::a::b::c → the longest prefix that names a module file under src/.
  let root = dir(from);
  while (root && base(root) !== "src") root = dir(root);
  if (base(root) !== "src") root = "src";
  const parts = spec.slice(6).split("::");
  for (let n = parts.length; n > 0; n--) {
    const p = join(root, parts.slice(0, n).join("/"));
    const hit = firstFile(idx, [`${p}.rs`, `${p}/mod.rs`]);
    if (hit) return hit;
  }
  return null;
}

function resolveJvm(idx: RepoFiles, spec: string): string[] {
  const exts = ["java", "kt", "scala"];
  if (spec.endsWith(".*")) {
    const d = spec.slice(0, -2).replace(/\./g, "/");
    for (const [dirPath, files] of idx.byDir) {
      if (dirPath === d || dirPath.endsWith(`/${d}`)) return files.filter((f) => exts.includes(extOf(f))).slice(0, 50);
    }
    return [];
  }
  const parts = spec.split(".");
  // Try the full name, then without the last part (a static import of a member).
  for (const n of [parts.length, parts.length - 1]) {
    if (n < 2) break;
    const p = parts.slice(0, n).join("/");
    for (const e of exts) {
      const hit = suffixUnique(idx, `${p}.${e}`);
      if (hit) return [hit];
    }
  }
  return [];
}

// ---------------------------------------------------------------------------

function opt(s: string | null): string[] {
  return s ? [s] : [];
}

function firstFile(idx: RepoFiles, candidates: string[]): string | null {
  for (const c of candidates) if (idx.files.has(c)) return c;
  return null;
}

function withExt(p: string, exts: string[]): string[] {
  return [p, ...exts.map((e) => `${p}.${e}`)];
}

/** The shortest file whose path ends with `suffix`, if that is unambiguous enough. */
function suffixUnique(idx: RepoFiles, suffix: string): string | null {
  const cands = idx.byBase.get(base(suffix));
  if (!cands) return null;
  let best: string | null = null;
  for (const c of cands) {
    if (c === suffix || c.endsWith(`/${suffix}`)) {
      if (best === null || c.length < best.length) best = c;
    }
  }
  return best;
}

function push<K, V>(m: Map<K, V[]>, k: K, v: V) {
  const a = m.get(k);
  if (a) a.push(v);
  else m.set(k, [v]);
}

export function dir(p: string): string {
  const i = p.lastIndexOf("/");
  return i < 0 ? "" : p.slice(0, i);
}

export function base(p: string): string {
  return p.slice(p.lastIndexOf("/") + 1);
}

/** Joins and normalises a relative path; never climbs above the repository root. */
export function join(a: string, b: string): string {
  const out: string[] = [];
  for (const part of `${a}/${b}`.split("/")) {
    if (part === "" || part === ".") continue;
    if (part === "..") out.pop();
    else out.push(part);
  }
  return out.join("/");
}
