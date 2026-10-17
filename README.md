# blast-radius

A Claude Code hook that shows you what a `terraform apply`, `kubectl delete` or `helm uninstall`
will destroy before the agent runs it, and blocks it in production.

```text
$ blast-radius check terraform destroy -auto-approve
blast-radius: terraform destroy -auto-approve
   workspace prod · environment production · BLOCKED
   5 to destroy
     - destroy  terraform_data.database
     - destroy  terraform_data.network
     - destroy  terraform_data.web[0]
     - destroy  terraform_data.web[1]
     - destroy  terraform_data.web[2]
```

The preview comes from the tool itself (`terraform plan`, `kubectl get`, `kubectl diff`), so you
see the real list of resources instead of a guess based on the command text. Reads, plans and
applies with no changes go through untouched. Resources that hold data, such as databases,
buckets, volumes, PVCs and namespaces, are marked `(holds data)`.

## Install

Download the archive for your platform from the
[releases page](https://github.com/SandeepKomal/blast-radius/releases), put `blast-radius` on your
`PATH`, then run:

```bash
blast-radius install
```

Or build it with Go 1.22 or later:

```bash
go install github.com/sandeepkomal/blast-radius@latest
```

`install` adds a PreToolUse hook for the Bash tool to `~/.claude/settings.json`. Restart Claude
Code afterwards. `--project` writes to `.claude/settings.json` in the current directory instead,
and `blast-radius uninstall` removes the hook.

## Trying it out

`examples/demo` is a Terraform project made of `terraform_data` resources, so it needs no cloud
account or provider download:

```bash
cd examples/demo
terraform init
terraform apply -auto-approve
blast-radius check terraform destroy      # needs approval
terraform workspace new prod
terraform apply -auto-approve
blast-radius check terraform destroy      # blocked
```

## What it looks at

| Tool | Previewed | Recognised, no preview |
| --- | --- | --- |
| Terraform, OpenTofu | `apply`, `destroy`, `apply <planfile>` | `state rm/push/mv`, `workspace delete`, `taint`, `import`, `force-unlock` |
| kubectl, oc | `delete` (namespaces include their contents), `apply` | `drain`, `scale --replicas=0`, `replace --force`, `rollout restart`, `patch`, `edit`, `create` |
| Helm | | `uninstall`, `upgrade`, `install`, `rollback` |

Commands are parsed the way a shell would split them, so `cd infra && terraform destroy`,
`terraform -chdir=envs/prod apply`, `TF_WORKSPACE=prod terraform destroy`,
`kubectl -n shop --context eks-prod delete ...`, `sudo`, `bash -c "..."`, `$(...)` and pipelines
are all handled. Anything run with `--dry-run` is ignored.

When a preview fails (missing credentials, a plan error, a timeout), the command is treated as
destructive.

## Policy

Without a config file:

| | create / update | destroy / replace |
| --- | --- | --- |
| default | pass | ask |
| production (`*prod*`, `*prd*`) | ask | deny |

`pass` runs the command as usual, `ask` shows you the preview and waits for approval, and `deny`
blocks it and tells the agent not to look for another way to run it.

The patterns are matched against the Terraform workspace, the kube context, the namespace and the
working directory. To change the rules, add a `.blast-radius.json` to the repo (it is looked up
from the working directory upwards) or `~/.config/blast-radius/config.json`:

```json
{
  "default": { "change": "pass", "destroy": "ask" },
  "environments": [
    { "name": "production", "match": ["*prod*", "arn:aws:eks:*:123456789012:*"], "change": "ask", "destroy": "deny" },
    { "name": "sandbox", "match": ["kind-*", "minikube"], "destroy": "pass" }
  ],
  "preview_timeout_seconds": 90
}
```

The first matching environment wins. `blast-radius config` prints the rules in effect.

## Without Claude Code

`blast-radius check` runs the same analysis from a script or CI job. It exits 0 when the command
can run, 10 when it needs approval and 20 when it is blocked.

```bash
blast-radius check -C infra 'terraform apply -auto-approve'
blast-radius check --json 'kubectl delete ns payments'
```

## How it works

For each Bash command the agent wants to run, the hook:

1. splits it into simple commands, following `cd`, inline environment variables and wrappers
   such as `sudo` and `sh -c`;
2. runs a read-only preview of each Terraform, kubectl or Helm command with the same flags,
   directory and context (`terraform plan -lock=false`, `kubectl get`, `kubectl diff`);
3. rates it as no change, change or destroy, and applies the policy for the matching environment;
4. returns `ask` or `deny` with the preview, or prints nothing so your usual permission rules apply.

## Limitations

Only commands the agent runs through the shell are checked. Changes made by a script
(`make deploy`, `./teardown.sh`), an SDK call or the cloud console are not seen, so keep using
credentials scoped to what the agent needs.

kubectl and Helm previews have so far been tested against recorded output only. Reports from real
clusters are very welcome.

## Planned

- Hooks for Codex, Cursor, Gemini CLI and OpenCode
- An HTML report showing what depends on each deleted resource
- Cost of each change
- `aws`, `gcloud` and `az` deletions, SQL `DROP`/`TRUNCATE`, `docker system prune`
- Helm previews through `helm diff`
- A log of approved and blocked commands

## Development

```bash
go test ./...
go build .
```

The project uses only the Go standard library.

## License

MIT
