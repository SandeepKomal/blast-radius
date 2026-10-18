package analyze

import (
	"context"

	"github.com/sandeepkomal/blast-radius/internal/shell"
)

var helmValueFlags = set("-n", "--namespace", "--kube-context", "--kubeconfig", "--kube-apiserver",
	"--kube-token", "--kube-as-user", "--kube-as-group", "--kube-ca-file", "--registry-config",
	"--repository-config", "--repository-cache", "--burst-limit", "--qps")

// helm has no dry-run preview of an uninstall, so it is classified only.
func (a *Analyzer) helm(ctx context.Context, c shell.Command, st *state) *Finding {
	args := c.Args[1:]
	if hasFlag(args, "--dry-run") {
		return nil
	}
	pos := positionals(args, helmValueFlags)
	if len(pos) == 0 {
		return nil
	}
	verb, names := pos[0], pos[1:]

	f := &Finding{Tool: "helm", Command: c.String()}
	switch verb {
	case "uninstall", "delete", "del", "un":
		f.Level = Destroy
		for _, r := range names {
			f.Resources = append(f.Resources, Resource{Action: ActDestroy, Name: "release/" + r})
		}
		f.Notes = append(f.Notes, "removes every Kubernetes object the release created")
	case "upgrade", "install", "rollback":
		f.Level = Change
	default:
		return nil
	}

	kctx := a.kubeContext(ctx, "kubectl", args, st, st.envFor(c))
	f.Target = "context " + kctx
	f.Labels = []string{kctx, st.dir}
	if ns, ok := flagValue(args, "-n", "--namespace"); ok {
		f.Target += ", namespace " + ns
		f.Labels = append(f.Labels, ns)
		for i := range f.Resources {
			f.Resources[i].Namespace = ns
		}
	}
	return f
}
