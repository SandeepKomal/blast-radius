# CLAUDE.md

Guidance for Claude (and other agents) working in this repository.

## What this is

blast-radius is a hook for Claude Code, Codex, Gemini CLI and GitHub Copilot
CLI, written in Go, that previews what an infrastructure command would destroy
and asks or blocks before the agent runs it. `blast-radius ui` is a local dashboard over its activity log. It pairs
with [paranoid-sre](https://github.com/SandeepKomal/paranoid-sre), a skill that
makes the agent careful before it reaches the hook.

## Commands

```bash
make test        # go test -race ./...
make lint        # golangci-lint, including the e2e build tag
make e2e         # needs terraform or tofu; kubectl tests need BLAST_RADIUS_E2E_KUBE=1;
                 # agent tests need BLAST_RADIUS_E2E_{CLAUDE,CODEX,GEMINI,COPILOT}=<cli>
make fuzz        # fuzz the shell parser
go build -o blast-radius . && ./blast-radius ui   # run the dashboard
```

Run `make test` and `make lint` before every commit. CI also runs the tests
on macOS and Windows: use `filepath`, `policy.UserConfigPath()` and the
`isolate(t)` test helper rather than hard-coded paths or `XDG_*` variables.

## Layout

| Path | What it does |
| --- | --- |
| `main.go` | CLI entry, `evaluate()` (analyse a request and apply the policy) |
| `hook.go` | `hook claude`: PreToolUse decisions; other events go to `record.go` |
| `codex_hook.go`, `agent_hooks.go` | `hook codex`, `hook gemini`, `hook copilot`: the same decisions in each agent's wire format |
| `record.go` | Recording-only hook events: sessions, prompts, results, skills |
| `audit.go`, `auditcmd.go` | Writing the audit log; `audit prune / forget / export` |
| `install.go`, `doctor.go` | Editing Claude Code settings; health checks and `init` templates |
| `ui.go`, `ui/` | Dashboard server (loopback only, read-only) and its static page |
| `internal/shell` | Bash and PowerShell parsing |
| `internal/analyze` | Per-tool analysis and previews (terraform, kubectl, helm, cloud CLIs) |
| `internal/policy` | Config loading and decisions |
| `internal/redact` | Masking personal data and secrets before they are logged |
| `internal/render` | Text output for the hook and `check` |
| `docs/` | `how-it-works.md`, `configuration.md`, `privacy.md`, screenshots |

## Rules that must not be broken

- **Fail closed.** If something can't be checked, the answer is "ask", never
  "pass" (a denial, for agents whose hooks can't ask). A crash, timeout, bad input or config error must not let a command
  through. Every new code path needs a test proving this.
- **Previews are read-only.** Nothing blast-radius runs may change state or
  take locks.
- **Standard library only.** No new Go dependencies. The dashboard loads
  nothing from the internet: no CDNs, fonts or analytics.
- **Recording hooks never print and never block.** Output on
  `UserPromptSubmit` or `SessionStart` reaches Claude's context. Only the
  `PreToolUse` hook has `onFailure: "block"`.
- **Privacy by default.** New fields in the audit log must be listed in
  `docs/privacy.md`, pass through `internal/redact` when they can hold free
  text, and be erasable with `audit forget`. Audit settings are read from the
  user config only, never from a repository's `.blast-radius.json`.
- **A repository config can only make rules stricter.**
- **Never weaken self-protection**: editing blast-radius's own settings or
  uninstalling it needs a human's approval.

## Dashboard conventions

- Visual style: plain and calm, in the manner of Apple's, Anthropic's and
  OpenAI's own interfaces. Solid surfaces with hairline borders and soft
  shadows, the system font, sentence-case labels, generous spacing, and
  color only where it carries meaning (the outcome colors and the brand
  accent). No glass or backdrop blur, glows, gradients, decorative grids,
  neon, or uppercase "HUD" labels. Motion only where it says something (a
  preview running, a command waiting), and none under
  `prefers-reduced-motion`.
- The Overview opens with the radar (each risky command as a blip: blocked
  in the inner ring, asked in the middle, allowed outside; the angle is when
  it ran) and a plain-words status with the next step. Ctrl+K, Cmd+K or `/`
  opens the command palette. Keep every view reachable from the keyboard.
- Build DOM with the `h()` / `s()` helpers and `textContent`; never
  `innerHTML`. Everything shown comes from commands an agent wrote.
- The CSP forbids inline styles: set sizes through `el.style` in JS or CSS
  classes, not `style="..."` attributes.
- Outcome colours (blue allowed, amber asked, red blocked) were checked for
  colour-vision deficiency in both themes. Each outcome also has its own icon
  shape; keep both.
- After UI changes, check light, dark and 390px width in a browser. To
  refresh the README screenshots, run the dashboard against sample data and
  capture each view at 1440x900, device scale 2, into `docs/screenshots/`.

## Releases

1. Move the `Unreleased` entries in `CHANGELOG.md` under `## [X.Y.Z] - date`.
2. Merge to `main`, then push a tag `vX.Y.Z`, or run the **release**
   workflow from the Actions tab with the version (`X.Y.Z`). It tests, tags
   `main` once every check has passed, builds with GoReleaser, and attests
   build provenance.

## Commits and docs

- One focused change per commit, with a message that says why.
- Update `CHANGELOG.md` (Unreleased), the README and `docs/` in the same
  change as the behaviour.
- New tools: follow "Adding a tool" in `CONTRIBUTING.md` and add a row to the
  README coverage table.
