# Privacy and India's DPDP Act

blast-radius runs on your computer. It sends nothing to the blast-radius
project, to Anthropic or to anyone else. What it keeps is one file, the
**activity log**, and only if you turn it on. This page lists everything that
file can contain, the controls you have over it, and a checklist that maps
India's **Digital Personal Data Protection Act, 2023** (DPDP Act) and the
**DPDP Rules, 2025** to blast-radius features and to the things your
organisation still has to do.

> This page explains how the software behaves. It is not legal advice. The
> DPDP Rules phase in over 18 months from their notification in November
> 2025; check the current notified text, and your own legal team, before
> relying on any item here.

## Who is who

| DPDP term | Who that is when you use blast-radius |
| --- | --- |
| **Data Principal** | The people the log is about: the engineers whose AI agent ran commands, and anyone named in their requests. |
| **Data Fiduciary** | The organisation (or person) that runs Claude Code with blast-radius and decides to keep the activity log. Usually your employer. |
| **Data Processor** | None. The blast-radius project never receives any data: there is no server, telemetry or update check. |
| Not covered by this page | What Claude Code itself sends to Anthropic. That is governed by your agreement with Anthropic, not by blast-radius. |

An individual using blast-radius purely for personal projects is outside the
Act (Section 3(c)(i), personal or domestic purposes). Use at work is not.

## What the activity log can contain

Nothing is recorded unless `audit_log` is set in your user config.

| Field | Example | Personal data? | Control |
| --- | --- | --- | --- |
| Command the agent ran | `terraform destroy` | Rarely. Passwords, tokens and keys in it are masked. | `audit_all` (record only risky commands when off) |
| Folder it ran in | `/home/ravi/src/infra` | Can include a username. | — |
| Decision, environment, resources it would delete | `deny`, `production`, `aws_db_instance.main` | No | — |
| Request typed to the agent | "Delete the test DB [email] asked about" | **Yes, it can.** Personal data in it is masked. | `audit_prompts`, `audit_redact` |
| Error message of a failed command | `Exit code 1 …` | Sometimes. Personal data in it is masked. | `audit_redact` |
| Session, prompt and tool-use IDs, subagent name, model, permission mode | `claude-opus-5-5`, `Explore` | Pseudonymous IDs that link entries together. | `audit forget --session` |
| Which AI agent sent the command | `claude_code`, `codex`, `cursor`, `gemini_cli` | No | — |
| Skills the agent loaded | `paranoid-sre:paranoid` | No | — |
| Time of each event | `2026-10-23T09:12:44Z` | Linked to the above. | `audit_retention_days` |

### What masking replaces

With `audit_redact` on (the default), before anything is written:

- **Everywhere, including commands:** passwords and secrets in flags
  (`--password=…`) and environment variables (`DB_PASSWORD=…`), credentials in
  URLs, `Bearer` tokens, AWS access keys, GitHub, Slack and OpenAI-style
  tokens, JWTs and private keys become `[secret]`, `[aws key]` and so on.
- **In requests and error messages:** email addresses, Indian mobile numbers,
  PAN, Aadhaar numbers (checked with the Verhoeff check digit), payment card
  numbers (checked with Luhn) and IPv4 addresses become `[email]`, `[phone]`,
  `[pan]`, `[aadhaar]`, `[card]` and `[ip]`.

Personal-data patterns are not applied to commands, because long numbers
there are usually account or resource IDs (`123456789012`) that you need to
see. Masking is pattern-based: it catches the common formats, not every
possible way of writing personal data. Names are not masked; use
`audit forget --match` to erase a person by name.

## Your controls

```json
{
  "audit_log": "~/.local/state/blast-radius/audit.jsonl",
  "audit_all": true,
  "audit_prompts": true,
  "audit_redact": true,
  "audit_retention_days": 90
}
```

| Setting or command | What it does |
| --- | --- |
| `audit_log` | Turns the log on and says where it lives. The file is created with mode 600 and its folder with 700, so only your user can read them. |
| `audit_redact` (default `true`) | Masks personal data and secrets as described above. |
| `audit_prompts` (default `true`) | `false` stops recording requests to the agent. Everything else keeps working; tasks show as not recorded. |
| `audit_all` | Also record harmless commands. Needed to see paranoid-sre's careful steps; turn it off to record less. |
| `audit_retention_days` | Deletes older entries automatically while `blast-radius ui` runs (at start and every hour). `blast-radius init --user` sets 90. |
| `blast-radius audit prune --older-than 30d` | Deletes old entries now. Add it to cron if you don't keep the dashboard running. |
| `blast-radius audit export --match 'Ravi'` | Prints every entry that mentions a name, email or other text: the answer to an access request. |
| `blast-radius audit forget --match 'Ravi'` | Deletes every entry that mentions it. `--session <id>` deletes a whole Claude Code session. `--dry-run` counts first. |
| Remove `audit_log` from your config | Stops recording. Delete the file to remove what was recorded. |

All of these settings are only read from your **user** config, never from a
repository's `.blast-radius.json`, so a repository can't turn recording on or
masking off.

## DPDP checklist

✅ handled by blast-radius · 🟡 blast-radius helps, your organisation completes
it · 🏢 your organisation's responsibility · ➖ does not apply

| # | Requirement | Where in the law | Status | How |
| --- | --- | --- | --- | --- |
| 1 | Lawful purpose and basis for processing | Sec. 4, 6, 7 | 🏢 | Decide your basis. For employees, Sec. 7(i) (employment purposes, including safeguarding the employer from loss or liability) may apply; otherwise obtain consent. |
| 2 | Clear notice to the people recorded | Sec. 5, Rule 3 | 🟡 | This page lists every field kept. Tell engineers the log exists, what it holds, why, for how long and how to ask for access or erasure. A template follows below. |
| 3 | Consent that can be withdrawn as easily as given | Sec. 6 | 🟡 | `audit_prompts: false`, `audit_all: false`, or removing `audit_log`, each take effect on the next command. Erase past data with `audit forget`. |
| 4 | Collect only what is needed (purpose and data minimisation) | Sec. 6(1) | ✅ | Off by default. Masking on by default. Prompts and harmless commands can each be switched off. Only names of skills are kept, never their content. |
| 5 | Accuracy and completeness | Sec. 8(3) | ✅ | Entries are written by the hook at the moment of each event, from Claude Code's own data; nothing is inferred later. |
| 6 | Reasonable security safeguards | Sec. 8(5), Rule 6 | 🟡 | File mode 600, folder 700. No network transfer. Dashboard bound to loopback, rejects other `Host` headers, sends a strict CSP and only reads. Secrets masked. You add disk encryption and access control on the machine. |
| 7 | Keep logs needed to detect unauthorised access | Rule 6 | 🟡 | The log is append-only JSON lines and can be shipped to your SIEM. If you treat it as such a log, set `audit_retention_days` to at least 365. |
| 8 | Erase data once the purpose is served (storage limitation) | Sec. 8(7), Rule 8 | ✅ | `audit_retention_days` with automatic pruning, and `audit prune`. |
| 9 | Report personal data breaches to the Board and to the people affected | Sec. 8(6), Rule 7 | 🏢 | Nothing leaves the machine through blast-radius, which keeps the exposure small. Include the log's path in your incident response plan. |
| 10 | Right to access information | Sec. 11 | ✅ | `blast-radius audit export --match` or `--session`. |
| 11 | Right to correction and erasure | Sec. 12 | ✅ | `blast-radius audit forget --match` or `--session`. Entries are records of events and are not edited, only erased. |
| 12 | Grievance redressal and a published contact | Sec. 8(9), 8(10), 13 | 🏢 | Name the person engineers contact about the log. |
| 13 | Right to nominate | Sec. 14 | 🏢 | Handled through your normal process; `audit export` produces the data. |
| 14 | Children's data | Sec. 9 | ➖ | The log is about people operating infrastructure through an agent. |
| 15 | Transfer outside India | Sec. 16 | ✅ | blast-radius transfers nothing. If you ship the log elsewhere, that transfer is yours to assess. |
| 16 | Data Processor contracts | Sec. 8(2) | ➖ | No processor is involved; the project receives no data. |
| 17 | Significant Data Fiduciary duties (DPIA, audits, DPO) | Sec. 10 | 🏢 | If your organisation is notified as one, include this log in your DPIA. |

## Notice template

Adapt this for your team:

> **AI agent activity log.** When you use Claude Code on infrastructure,
> blast-radius records, on your machine, the commands the agent ran, the
> folder, what blast-radius decided, which agent and model ran them, whether
> they ran, and the request you typed. Personal data such as emails, phone
> numbers, PAN and Aadhaar numbers, and any passwords or tokens, are masked
> before they are written. We use this to prevent and investigate accidental
> damage to our systems. Entries are deleted after 90 days. To see what is
> recorded about you, or to have it erased, contact {name, email}.

## Verifying it yourself

- `blast-radius config` prints the settings in effect, including the audit
  settings.
- The **Rules & privacy** page of `blast-radius ui` shows the same, with the
  log's size and oldest entry.
- The tests in `internal/redact/redact_test.go`, `auditcmd_test.go` and
  `record_test.go` show what is masked, kept and erased.
