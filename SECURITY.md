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

Things blast-radius documents as out of scope are not vulnerabilities, but
are still welcome as feature requests: see [Limitations](README.md#limitations).

## Supported versions

Security fixes go into the latest release. Please upgrade before reporting.
