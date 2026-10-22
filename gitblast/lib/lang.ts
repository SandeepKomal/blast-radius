// Language detection by file name, with colours close to the ones developers
// already associate with each language. Shared by the server and the browser.

export interface Lang {
  name: string;
  color: string;
}

const L = (name: string, color: string): Lang => ({ name, color });

const BY_EXT: Record<string, Lang> = {};
function add(lang: Lang, ...exts: string[]) {
  for (const e of exts) BY_EXT[e] = lang;
}

add(L("TypeScript", "#3178c6"), "ts", "tsx", "mts", "cts");
add(L("JavaScript", "#f1e05a"), "js", "jsx", "mjs", "cjs");
add(L("Python", "#3572a5"), "py", "pyi");
add(L("Go", "#00add8"), "go");
add(L("Rust", "#dea584"), "rs");
add(L("Java", "#b07219"), "java");
add(L("Kotlin", "#a97bff"), "kt", "kts");
add(L("Scala", "#c22d40"), "scala");
add(L("Swift", "#f05138"), "swift");
add(L("C", "#8a8f98"), "c", "h");
add(L("C++", "#f34b7d"), "cc", "cpp", "cxx", "hpp", "hh", "hxx");
add(L("C#", "#178600"), "cs");
add(L("Ruby", "#cc342d"), "rb");
add(L("PHP", "#4f5d95"), "php");
add(L("Dart", "#00b4ab"), "dart");
add(L("Elixir", "#6e4a7e"), "ex", "exs");
add(L("Haskell", "#5e5086"), "hs");
add(L("Lua", "#000080"), "lua");
add(L("Shell", "#89e051"), "sh", "bash", "zsh", "fish");
add(L("Vue", "#41b883"), "vue");
add(L("Svelte", "#ff3e00"), "svelte");
add(L("HTML", "#e34c26"), "html", "htm");
add(L("CSS", "#663399"), "css", "scss", "sass", "less");
add(L("Markdown", "#5b6170"), "md", "mdx", "rst", "txt");
add(L("Config", "#6b7280"), "json", "yaml", "yml", "toml", "ini", "xml", "lock");
add(L("Terraform", "#844fba"), "tf", "hcl");
add(L("SQL", "#e38c00"), "sql");
add(L("Image", "#3f3f46"), "png", "jpg", "jpeg", "gif", "svg", "ico", "webp");

const OTHER = L("Other", "#52525b");

export function langOf(path: string): Lang {
  const base = path.slice(path.lastIndexOf("/") + 1).toLowerCase();
  if (base === "dockerfile" || base.endsWith(".dockerfile")) return L("Dockerfile", "#384d54");
  if (base === "makefile") return L("Makefile", "#427819");
  const dot = base.lastIndexOf(".");
  if (dot < 0) return OTHER;
  return BY_EXT[base.slice(dot + 1)] ?? OTHER;
}

/** Extensions whose imports gitblast follows when building the dependency graph. */
export const SOURCE_EXTS = new Set([
  "ts", "tsx", "mts", "cts", "js", "jsx", "mjs", "cjs", "vue", "svelte",
  "py", "go", "rs", "java", "kt", "kts", "scala", "rb", "php", "dart",
  "c", "h", "cc", "cpp", "cxx", "hpp", "hh", "hxx", "css", "scss", "sass", "less",
]);

export function extOf(path: string): string {
  const base = path.slice(path.lastIndexOf("/") + 1);
  const dot = base.lastIndexOf(".");
  return dot < 0 ? "" : base.slice(dot + 1).toLowerCase();
}

const TEST_RE =
  /(^|\/)(__tests__|tests?|spec|specs|e2e|testdata|fixtures)\/|(\.|_|-)(test|spec)\.[a-z0-9]+$|(^|\/)test_[^/]+\.py$|_test\.go$|Tests?\.(java|kt|cs|swift)$/i;

export function isTest(path: string): boolean {
  return TEST_RE.test(path);
}
