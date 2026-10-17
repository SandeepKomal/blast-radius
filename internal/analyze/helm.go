package analyze

import (
	"context"
	"strings"

	"github.com/sandeepkomal/blast-radius/internal/shell"
)

var helmValueFlags = set("-n", "--namespace", "--kube-context", "--kubeconfig", "--kube-apiserver",
	"--kube-token", "--kube-as-user", "--kube-as-group", "--kube-ca-file", "--registry-config",
	"--repository-config", "--repository-cache", "--burst-limit", "--qps")

// helm has no dry-run preview of an uninstall, so it is classified only.
func (a *Analyzer) helm(ctx context.Context, c shell.Command, dir string) *Finding {
	args := c.Args[1:]
	var verb string
	var positional []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			if helmValueFlags[args[i]] {
				i++
			}
			continue
		}
		if verb == "" {
			verb = args[i]
		} else {
			positional = append(positional, args[i])
		}
	}
	if hasFlag(args, "--dry-run") {
		return nil
	}

	f := &Finding{Tool: "helm", Command: c.String()}
	switch verb {
	case "uninstall", "delete", "del", "un":
		f.Level = Destroy
		for _, r := range positional {
			f.Resources = append(f.Resources, Resource{Action: ActDestroy, Name: "release " + r})
		}
		f.Notes = append(f.Notes, "removes every Kubernetes object the release created")
	case "upgrade", "install", "rollback":
		f.Level = Change
	default:
		return nil
	}

	kctx, ok := flagValue(args, "--kube-context")
	if !ok {
		var conn []string
		if v, ok := flagValue(args, "--kubeconfig"); ok {
			conn = []string{"--kubeconfig", v}
		}
		kctx = a.currentContext(ctx, shell.Command{Env: c.Env, Args: []string{"kubectl"}}, dir, conn)
	}
	f.Target = "context " + kctx
	f.Labels = []string{kctx, dir}
	if ns, ok := flagValue(args, "-n", "--namespace"); ok {
		f.Target += ", namespace " + ns
		f.Labels = append(f.Labels, ns)
	}
	return f
}
