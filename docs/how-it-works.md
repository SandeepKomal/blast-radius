# How it works

## The hook

`blast-radius install` registers a Claude Code `PreToolUse` hook:

```json
{
  "matcher": "Bash|PowerShell|Edit|Write|MultiEdit|NotebookEdit",
  "hooks": [{
    "type": "command",
    "command": "/usr/local/bin/blast-radius",
    "args": ["hook", "claude"],
    "timeout": 300,
    "onFailure": "block"
  }]
}
```

- **`Bash|PowerShell`**: on Windows without Git Bash, Claude Code runs shell
  commands through its PowerShell tool, so a hook that only matches `Bash`
  never fires there.
- **`Edit|Write|...`**: edits to blast-radius's own settings need approval
  (see [Self-protection](#self-protection)).
- **`args`**: exec form, so no shell sits between Claude Code and the binary.
- **Absolute path** in user settings, because Claude Code started from a
  desktop launcher may not have your shell's `PATH`.
- **`onFailure: "block"`**: if the hook can't start, crashes or times out,
  Claude Code blocks the tool call. Without this, Claude Code lets the call
  through when a hook fails. `onFailure` needs Claude Code v2.1.295 or later;
  on older versions a failing hook lets the call through, so keep Claude Code
  up to date.

## A command's path through blast-radius

1. **Parse.** The command line is split into simple commands the way a shell
   would: `&&`, `||`, `;`, pipes, subshells, `$(...)`, backticks, `eval`,
   `sh -c`, `pwsh -Command`, env prefixes and wrappers such as `sudo`,
   `env`, `timeout` and `xargs`. PowerShell commands are parsed with
   PowerShell's quoting rules.
2. **Follow state.** Earlier commands in the same line change what later
   ones mean: `cd`, `export`/`unset`, `$env:NAME = ...`,
   `terraform workspace select`, `kubectl config use-context` and `kubectx`.
   So `terraform workspace select prod && terraform destroy` is judged
   against `prod`.
3. **Preview.** Each infrastructure command is previewed with its own
   read-only mode, using the same flags, directory, environment and context:

   | Command | Preview |
   | --- | --- |
   | `terraform`/`tofu apply`, `destroy` | `plan -lock=false -input=false -out=<tmp>`, then `show -json` |
   | `terraform apply <planfile>` | `show -json <planfile>` |
   | `terragrunt apply`/`destroy` (one module) | the same, through terragrunt |
   | `kubectl delete` | `kubectl get` with the same selection; namespaces also list their contents |
   | `kubectl apply` | `kubectl diff` with the same flags |

   Previews never take state locks or write state. They run with a deadline,
   and on timeout the whole process tree is killed, so a hung provider
   plugin can't stall the hook.
4. **Rate.** Each command is `none`, `change` or `destroy`. A preview that
   fails (missing credentials, an uninitialised directory, a timeout) rates
   the command `destroy`, because blast-radius can't rule out deletions.
5. **Decide.** The environment is matched and the policy applied (see
   [Configuration](configuration.md)). Protected resources are always denied.
6. **Answer.** `ask` and `deny` are returned to Claude Code with the preview.
   A pass prints nothing, so your usual permission rules apply. When several
   commands in one line have findings, the strictest decision wins.

## Fail closed

| What goes wrong | Result |
| --- | --- |
| A preview fails or times out | treated as `destroy` (asks, or denies in production) |
| The config file is invalid | every risky command asks, with the error shown |
| The hook input can't be read | ask |
| blast-radius panics | ask |
| The hook can't start, crashes hard or exceeds 300 s | Claude Code blocks the call (`onFailure: "block"`) |

## Self-protection

The agent runs as you and can edit any file you can, so blast-radius watches
for attempts to switch it off:

- `blast-radius uninstall`,
- shell commands that write to (or redirect into) `.claude/settings.json`,
  `.claude/settings.local.json`, `.blast-radius.json` or the user config,
- Claude's Edit and Write tools changing any of those files.

These always ask, so you can still make the change yourself.

## Threat model

blast-radius is a guard against **mistakes**: an agent that misreads a task,
runs in the wrong directory or context, or "cleans up" too eagerly. It makes
every destructive change visible and, in production, refuses it.

It is **not** a sandbox against an agent actively trying to evade it. An agent
that writes a script and runs it (`python cleanup.py`, `make destroy`,
`./teardown.sh`), calls a cloud SDK directly, or uses a tool blast-radius
doesn't know, isn't previewed. For hard guarantees, combine it with:

- **least-privilege credentials**: the agent's cloud and cluster credentials
  shouldn't allow deleting production in the first place,
- **Claude Code's permission rules and sandboxing**, to limit which commands
  and paths the agent can use at all,
- **reviewed pipelines** (pull requests and CI) for production changes.
