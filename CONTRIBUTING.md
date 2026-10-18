# Contributing

Thanks for helping. Bug reports with a failing command are the most useful
thing you can send: `blast-radius check --json '<command>'` shows exactly what
was decided and why.

## Development

You need Go 1.24 or later.

```bash
make test       # unit tests with the race detector
make lint       # golangci-lint (install it from https://golangci-lint.run)
make e2e        # end-to-end tests; needs terraform or tofu on PATH
make fuzz       # fuzz the shell parser for a minute
```

The Kubernetes end-to-end tests run against whatever cluster is in your
KUBECONFIG (kind, minikube, k3d) when `BLAST_RADIUS_E2E_KUBE=1` is set. They
work on a copy of the kubeconfig and only touch a namespace called `br-e2e`.

## Guidelines

- **Standard library only.** blast-radius runs on every command an agent
  executes; keeping it free of dependencies keeps it small and auditable.
- **Fail closed.** If something can't be checked, the answer is "ask", never
  "pass". New code paths need a test that proves this.
- **Previews must be read-only.** Anything blast-radius runs to build a
  preview must not change state or take locks.
- **Every bug fix comes with a test** that fails without the fix.
- Keep commits focused, with a message that says why the change is needed.

## Adding a tool

Each tool lives in `internal/analyze`. A new tool needs:

1. a function that turns a parsed command into a `Finding` (or nil),
2. a case in `Analyzer.Analyze`,
3. unit tests in `analyze_test.go` covering destructive, changing and
   read-only commands, and
4. a row in the coverage table in the README.

If the tool has a read-only preview (`plan`, `diff`, `--dry-run`), use it, and
treat a failed preview as destructive.
