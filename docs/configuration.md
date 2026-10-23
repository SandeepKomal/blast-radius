# Configuration

blast-radius works without any config. To change its rules, use one or both
of these files:

| File | Scope | Can it loosen rules? |
| --- | --- | --- |
| Your user config: `~/.config/blast-radius/config.json` on Linux, `~/Library/Application Support/blast-radius/config.json` on macOS, `%AppData%\blast-radius\config.json` on Windows | Every project | Yes |
| `.blast-radius.json` in a repository (found by walking up from the working directory) | That repository | **No.** It can only make decisions stricter |

`blast-radius init --user` and `blast-radius init` write starter files.
`blast-radius config` prints the policy in effect and which files it came
from. Unknown keys are an error, so a typo can't quietly weaken the policy.

Why can't a project file loosen rules? The agent can write files in the
repository it works in. If a `.blast-radius.json` could set `destroy` to
`pass`, the agent could switch the guard off for itself. So blast-radius
decides under your user config and under the project file separately, and
keeps the stricter answer.

## Example

```json
{
  "$schema": "https://raw.githubusercontent.com/SandeepKomal/blast-radius/main/docs/config.schema.json",
  "default": { "change": "pass", "destroy": "ask" },
  "environments": [
    { "name": "production", "match": ["*prod*", "*prd*", "arn:aws:eks:*:123456789012:*"], "change": "ask", "destroy": "deny" },
    { "name": "sandbox", "match": ["kind-*", "minikube", "docker-desktop"], "destroy": "pass" }
  ],
  "protect": ["aws_db_instance.*", "aws_s3_bucket.backups", "*/persistentvolumeclaim/*"],
  "stateful": ["acme_database"],
  "aliases": { "tf": "terraform", "k": "kubectl", "tg": "terragrunt" },
  "preview_timeout_seconds": 90,
  "audit_log": "~/.local/state/blast-radius/audit.jsonl"
}
```

The `$schema` line gives you completion and validation in VS Code and
JetBrains editors.

## Keys

### `default`

What to do when no environment matches. Each command is rated `change`
(creates or modifies things) or `destroy` (deletes or replaces things, or
couldn't be previewed), and each rating maps to an action:

| Action | Effect |
| --- | --- |
| `pass` | Runs as normal. Claude Code's own permission rules still apply. |
| `ask` | Claude Code shows you the preview and waits for your approval. In non-interactive runs (`claude -p`), Claude Code treats this as a deny. |
| `deny` | Blocked. The agent is told not to retry or work around it. |

Built-in default: `{"change": "pass", "destroy": "ask"}`.

### `environments`

A list of `{name, match, change, destroy}`. The first environment with a
matching pattern wins; rules it leaves out come from `default`.

Patterns are globs where `*` matches anything (including `/`), compared
case-insensitively, against:

- the Terraform workspace and working directory,
- the kube context and namespace (`--context`, `kubectl config use-context`,
  or `kubectl config current-context`),
- the AWS profile, account and region, the gcloud project, the Azure
  subscription and resource group, the Pulumi stack.

The AWS account is read from the AWS config file (`~/.aws/config`, or
`AWS_CONFIG_FILE`) without calling AWS: `aws_account_id` or `sso_account_id`
in the profile, or the account in its `role_arn`, following `source_profile`.
`AWS_ACCOUNT_ID` overrides it. When access keys in the environment override
the profile, the account can't be told offline and only the profile name is
matched. To protect a whole account, whatever the profile is called:

```json
{ "name": "production", "match": ["123456789012"], "change": "ask", "destroy": "deny" }
```

Built-in: one environment, `production`, matching `*prod*` and `*prd*`, with
`change: ask` and `destroy: deny`. That pattern also matches names like
`preprod` and `nonprod`. This errs on the safe side, but add your own
environments if it's too broad for you.

### `protect`

Resources that may never be deleted or replaced, in any environment, whatever
the rules say. Matched against:

- Terraform and OpenTofu addresses: `aws_db_instance.main`, `module.db.*`
- Kubernetes `kind/name` and `namespace/kind/name`: `namespace/kube-system`,
  `*/persistentvolumeclaim/*`
- Helm releases: `release/payments`

Updates to a protected resource are not affected.

### `stateful`

Extra Terraform resource types or Kubernetes kinds that hold data. They are
marked `(holds data)` in previews. Databases, buckets, volumes, key vaults,
namespaces, PVCs, StatefulSets, Secrets and CRDs are built in.

### `aliases`

Extra names for supported tools. Built-in: `tf` for `terraform`, `k` for
`kubectl`.

### `preview_timeout_seconds`

How long previews may take in total, from 1 to 270 (default 90). A preview
that runs out of time counts as destructive. The hook timeout that
`blast-radius install` sets is 300 seconds, so a slow plan ends in "ask",
not in a hook failure.

### `audit_log`

A file that gets one JSON line for every command that had findings, with the
decision and the resources it would have deleted. Only read from your user
config. `~/` is expanded. `blast-radius ui` shows it as a live feed, and while a
preview runs, a small file in a `running` directory next to the log lets the
dashboard show it as in progress.

```json
{"time":"2026-10-18T09:12:44Z","tool":"Bash","dir":"/src/infra","command":"terraform destroy","action":"deny","findings":[{"tool":"terraform","target":"workspace prod","level":"destroy","action":"deny","environment":"production","destroyed":["aws_db_instance.main"]}]}
```
