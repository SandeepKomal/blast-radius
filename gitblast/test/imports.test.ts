import { describe, expect, it } from "vitest";
import { extractImports, indexFiles, noteManifest, resolveImports } from "@/lib/imports";

function resolve(files: string[], from: string, text: string, manifests: Record<string, string> = {}) {
  const idx = indexFiles(files);
  for (const [p, t] of Object.entries(manifests)) noteManifest(idx, p, t);
  return resolveImports(idx, from, extractImports(from, text)).sort();
}

describe("imports", () => {
  it("resolves TypeScript and JavaScript imports", () => {
    const files = ["src/app.ts", "src/util/index.ts", "src/lib/a.ts", "src/lib/b.tsx", "src/c.ts", "src/d.js"];
    const text = `
      import x from "./util";
      import { y } from './lib/a.js';
      export * from "./lib/b";
      const z = await import("./c");
      const w = require('./d');
      import "react";
    `;
    expect(resolve(files, "src/app.ts", text)).toEqual(["src/c.ts", "src/d.js", "src/lib/a.ts", "src/lib/b.tsx", "src/util/index.ts"]);
  });

  it("resolves workspace packages and @/ aliases", () => {
    const files = ["apps/web/page.tsx", "packages/ui/src/index.ts", "packages/ui/package.json", "apps/web/src/lib/x.ts"];
    const text = `import { Button } from "@acme/ui"; import { x } from "@/lib/x";`;
    expect(resolve(files, "apps/web/page.tsx", text, { "packages/ui/package.json": '{"name":"@acme/ui"}' })).toEqual([
      "packages/ui/src/index.ts",
    ]);
    expect(resolve(["src/a.ts", "src/lib/x.ts"], "src/a.ts", `import { x } from "@/lib/x";`)).toEqual(["src/lib/x.ts"]);
  });

  it("resolves Python absolute and relative imports", () => {
    const files = ["pkg/__init__.py", "pkg/core.py", "pkg/sub/helpers.py", "pkg/sub/__init__.py", "app.py"];
    const text = "import os\nfrom pkg.core import run\nfrom pkg.sub import helpers\n";
    expect(resolve(files, "app.py", text)).toEqual(["pkg/core.py", "pkg/sub/__init__.py", "pkg/sub/helpers.py"]);
    expect(resolve(files, "pkg/sub/helpers.py", "from ..core import run\nfrom . import (\n  thing,\n)\n")).toEqual(["pkg/core.py"]);
  });

  it("resolves Go packages through go.mod", () => {
    const files = ["go.mod", "main.go", "internal/shell/parse.go", "internal/shell/lex.go", "internal/shell/parse_test.go"];
    const text = 'package main\n\nimport (\n\t"fmt"\n\tsh "example.com/tool/internal/shell"\n)\n';
    expect(resolve(files, "main.go", text, { "go.mod": "module example.com/tool\n\ngo 1.24\n" })).toEqual([
      "internal/shell/lex.go",
      "internal/shell/parse.go",
    ]);
  });

  it("resolves Rust modules and crate paths", () => {
    const files = ["src/main.rs", "src/net/mod.rs", "src/net/http.rs", "src/util.rs"];
    expect(resolve(files, "src/main.rs", "mod net;\nmod util;\n")).toEqual(["src/net/mod.rs", "src/util.rs"]);
    expect(resolve(files, "src/util.rs", "use crate::net::http::Client;\n")).toEqual(["src/net/http.rs"]);
  });

  it("resolves Java, Ruby, PHP and C includes", () => {
    expect(resolve(["src/main/java/com/acme/App.java", "src/main/java/com/acme/util/Strings.java"], "src/main/java/com/acme/App.java", "import com.acme.util.Strings;\nimport java.util.List;\n")).toEqual([
      "src/main/java/com/acme/util/Strings.java",
    ]);
    expect(resolve(["lib/a.rb", "lib/b.rb"], "lib/a.rb", "require_relative 'b'\nrequire 'json'\n")).toEqual(["lib/b.rb"]);
    expect(resolve(["src/Http/Kernel.php", "src/Http/Request.php"], "src/Http/Kernel.php", "use App\\Http\\Request;\n")).toEqual(["src/Http/Request.php"]);
    expect(resolve(["src/a.c", "src/a.h", "include/b.h"], "src/a.c", '#include "a.h"\n#include "b.h"\n#include <stdio.h>\n')).toEqual(["include/b.h", "src/a.h"]);
  });
});
