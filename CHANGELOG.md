# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[semantic versioning](https://semver.org/).

## [Unreleased]

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
- `blast-radius init --user` turns on the audit log and `audit_all`.
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

[Unreleased]: https://github.com/SandeepKomal/blast-radius/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/SandeepKomal/blast-radius/releases/tag/v0.1.0
