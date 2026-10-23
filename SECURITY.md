# Security policy

blast-radius sits between an AI agent and your infrastructure, so a bypass is
a security bug. Please report it privately.

## Reporting a vulnerability

Use [GitHub's private vulnerability reporting](https://github.com/SandeepKomal/blast-radius/security/advisories/new).
Please don't open a public issue for anything that lets a destructive command
through without approval.

A useful report includes:

- the exact command (or tool call) and the directory it ran in
- what blast-radius decided (`blast-radius check --json '<command>'` prints it)
- what you expected it to decide
- your OS, shell, blast-radius version and the tool versions involved

You can expect an acknowledgement within 3 days and a fix or a plan within
14 days. Fixed bypasses are credited in the release notes unless you'd rather
stay anonymous.

## What counts as a vulnerability

- A command that destroys or replaces resources, and that blast-radius should
  recognise, passing without approval under the default policy.
- A way for the agent to switch blast-radius off, or loosen its policy,
  without a human approving it.
- A failure (crash, timeout, bad input) that results in a pass instead of
  "ask" or a block.

- A secret (password, token, key) that `audit_redact` should mask reaching
  the activity log, or the dashboard serving the log to anything other than
  the local machine.

Things blast-radius documents as out of scope are not vulnerabilities, but
are still welcome as feature requests: see [Limitations](README.md#limitations).

## Supply chain

- No third-party Go dependencies; the binary is built from this repository
  and the Go standard library.
- Release archives are built by GitHub Actions with GoReleaser, with a
  `checksums.txt` and signed build provenance. Verify before installing:
  `gh attestation verify <archive> --repo SandeepKomal/blast-radius`.
- Every GitHub Action is pinned to a full commit SHA and updated by
  Dependabot. Workflows default to read-only tokens and don't keep
  credentials in the checkout.
- CI runs CodeQL, govulncheck, a gitleaks scan of the whole history, and
  OpenSSF Scorecard.

## The activity log

The activity log is a local file with mode 600 that can contain commands,
folders and the requests typed to the agent, with personal data and secrets
masked by default. Treat it like a shell history: keep it on encrypted
disks, don't attach it to public issues, and use `blast-radius audit forget`
to remove entries. [docs/privacy.md](docs/privacy.md) lists everything it can
contain.

## Supported versions

Security fixes go into the latest release. Please upgrade before reporting.
