# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[semantic versioning](https://semver.org/).

## [Unreleased]

### Changed

- The dashboard's Overview opens with a radar of every risky command (blocked
  in the inner ring, asked in the middle, allowed outside; the angle is when
  it ran; select a blip for details) and a plain-words status (Action needed,
  Shields up, All clear) with the next step to take.
- <kbd>Ctrl</kbd>+<kbd>K</kbd>, <kbd>⌘</kbd>+<kbd>K</kbd> or <kbd>/</kbd>
  opens a palette to jump to a page or find any command.
- README screenshots refreshed.

### Added

- The dashboard shows every AI agent under its own name, not only Claude
  Code and Codex, with a **By agent** chart on the Overview and an agent
  filter on the Activity page.
- `blast-radius check --agent NAME [--session ID]` records a command as run
  by any agent, so agents without a built-in hook appear in the dashboard.
- The Codex hook records the model, and Codex's `SessionStart`,
  `UserPromptSubmit` and `PostToolUse` events, like Claude Code's.
- `blast-radius install --agent codex|gemini|copilot` installs the hook for
  Codex, Gemini CLI and GitHub Copilot CLI, with the absolute path of the
  binary; `uninstall --agent` removes it, and `doctor` and the dashboard's
  Setup page check it.
- Hooks for Gemini CLI (`hook gemini`) and GitHub Copilot CLI
  (`hook copilot`), checking shell commands and file edits.
- The Codex hook checks `apply_patch` file edits.
- End-to-end tests run the real Claude Code, Codex, Gemini CLI and Copilot
  CLI, with the hooks `install` writes, against a mock model, and check
  `check --agent` for agents without a built-in hook.
- The Setup page finds paranoid-sre in the skill folders of Codex, Gemini
  CLI and Copilot CLI (`~/.agents/skills` and each agent's own), not only
  Claude Code's.

### Fixed

- A config file with anything after its closing `}`, such as rules pasted
  below it, is now an error. Before, everything after the first `}` was
  ignored without a warning, so those rules silently didn't apply.
- Claude Code's session, request and result records had no
  `agent_platform`, unlike every other agent's.
- Codex hooks did nothing until trusted in Codex's `/hooks`, and Codex
  skipped them without a warning. `install --agent codex` and `doctor` now
  say so, and the README explains it.
- When the Codex hook couldn't write its answer, it exited 2 without a
  reason on stderr, which Codex ignores; the command then ran.

### Security

- The browser tests and the agent CLIs used by the end-to-end tests are
  installed with `npm ci` from committed lockfiles, so they are pinned by
  hash, and Dependabot updates them (OpenSSF Scorecard Pinned-Dependencies).
- Self-protection covers every supported agent: commands and file edits
  that change Codex, Gemini CLI or Copilot hook settings need approval.
- Self-protection could be bypassed with a path written differently:
  `~/.codex/./hooks.json`, `~/.Claude/settings.json`, a relative path after
  `cd`, a trailing dot on Windows, or removing the whole folder. Paths are
  now normalised and resolved against the command's directory, and folders
  holding a hook count as the hook.
- The release build no longer uses the GitHub Actions cache, so a poisoned
  cache can't reach a release binary (zizmor cache-poisoning).
- `blast-radius init --user` creates the user config readable only by its
  owner (600, folder 700): it can name production accounts and clusters.

## [0.2.1] - 2026-10-23

### Fixed

- `blast-radius doctor` no longer writes its self-test commands to the
  activity log, where the dashboard showed them as if an agent had run
  them.

### Security

- The release workflow is read-only by default; only the release job can
  write (OpenSSF Scorecard Token-Permissions).

## [0.2.0] - 2026-10-23

### Added

- `blast-radius ui`: a dashboard on `127.0.0.1` with KPIs, decisions over time, breakdowns by
  environment and tool, the previews still running, a searchable activity table with details for
  each command, a setup checklist for blast-radius and paranoid-sre, and the rules in effect. It
  reads the audit log and updates live.
- The dashboard shows which agent and model ran each command, the request it was working on,
  and what happened after the check: ran, failed, not run, or waiting for your answer.
  `blast-radius install` adds hooks for `SessionStart`, `UserPromptSubmit`, `PostToolUse`,
  `PostToolUseFailure` and `PermissionDenied` that only record this; run it again to get them.
- `audit_all` also records commands with nothing risky in them.
- paranoid-sre in the dashboard: a Tasks page shows each request as a story (skill loaded,
  checked where it is, previewed, made a way back, the risky command, the decision, the
  result), and the Overview has a Careful mode card. The hook records which skills the agent
  loads (by name only).
- Privacy controls for India's DPDP Act: personal data (emails, Indian phone numbers, PAN,
  Aadhaar, card numbers, IP addresses) and secrets are masked before they are logged
  (`audit_redact`, on by default); `audit_prompts: false` stops recording requests;
  `audit_retention_days` deletes old entries; `blast-radius audit prune`, `export` and
  `forget` answer retention, access and erasure requests. See `docs/privacy.md`.
- `blast-radius init --user` turns on the audit log and `audit_all`, and keeps entries for
  90 days.
- `CLAUDE.md` with the rules for agents working in this repository.

### Security

- Every GitHub Action is pinned to a commit SHA, checkouts no longer keep credentials, and the
  terragrunt download in the end-to-end tests is checked against its SHA-256.
- CI scans the full history for secrets with gitleaks, and OpenSSF Scorecard runs weekly.
- AWS commands are matched against the account of the active profile, read offline from the AWS
  config file (`aws_account_id`, `sso_account_id`, `role_arn`, `source_profile`) or
  `AWS_ACCOUNT_ID`, so an environment can protect a whole AWS account whatever the profile is
  called.

## [0.1.0] - 2026-10-19

First release.

### Added

- Claude Code PreToolUse hook for Bash, PowerShell and file edits.
- Previews for `terraform`/`tofu` apply and destroy, `terragrunt`, `kubectl delete`, `kubectl apply`
  and `helm uninstall`.
- Recognition of destructive `helm`, `pulumi`, `aws`, `gcloud`, `gsutil` and `az` commands.
- State tracking across chained commands: `cd`, `export`, `terraform workspace select`,
  `kubectl config use-context`, `kubectx`, `pulumi stack select`, `gcloud config set project`,
  `az account set`, PowerShell `$env:` assignments.
- The active gcloud configuration and default Azure subscription are read from the CLIs' own
  config files, so production is recognised without flags.
- Policy with environments, protected resources, extra stateful types and aliases.
  Project config files can only make rules stricter.
- Commands nested in `cmd /c`, `Invoke-Expression` and `ssh host '...'` are checked.
- Self-protection: commands and edits that would switch blast-radius off need approval.
- `doctor`, `init`, `check --json` and an optional audit log. `doctor` warns when Claude Code
  is too old for fail-closed hooks, or when the hook runs a different blast-radius binary.
- Fail-closed behaviour: hook errors, timeouts, bad input and config errors never pass.
- Release archives for Linux, macOS and Windows with signed build provenance.

[Unreleased]: https://github.com/SandeepKomal/blast-radius/compare/v0.2.1...HEAD
[0.2.1]: https://github.com/SandeepKomal/blast-radius/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/SandeepKomal/blast-radius/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/SandeepKomal/blast-radius/releases/tag/v0.1.0
