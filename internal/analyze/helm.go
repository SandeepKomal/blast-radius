package analyze

import (
	"context"
	"strconv"
	"strings"

	"github.com/sandeepkomal/blast-radius/internal/shell"
)

var helmValueFlags = set("-n", "--namespace", "--kube-context", "--kubeconfig", "--kube-apiserver",
	"--kube-token", "--kube-as-user", "--kube-as-group", "--kube-ca-file", "--registry-config",
	"--repository-config", "--repository-cache", "--burst-limit", "--qps")

// helm uninstall is previewed with `helm get manifest`, which lists the
// objects each release owns. Other verbs are classified only.
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
		a.helmManifests(ctx, f, c, st, names)
	case "upgrade", "install", "rollback":
		f.Level = Change
	default:
		return nil
	}

	env := st.envFor(c)
	kctx := lookup(env, "HELM_KUBECONTEXT")
	if v, ok := flagValue(args, "--kube-context"); ok || kctx == "" {
		if ok {
			kctx = v
		} else {
			kctx = a.kubeContext(ctx, "kubectl", args, st, env)
		}
	}
	f.Target = "context " + kctx
	f.Labels = []string{kctx, st.dir}
	if ns, ok := flagValue(args, "-n", "--namespace"); ok {
		f.Target += ", namespace " + ns
		f.Labels = append(f.Labels, ns)
	}
	return f
}

// helmConnFlags are the flags that pick the cluster and namespace, passed on
// to `helm get manifest`.
var helmConnFlags = set("-n", "--namespace", "--kube-context", "--kubeconfig", "--kube-apiserver",
	"--kube-token", "--kube-as-user", "--kube-as-group", "--kube-ca-file")

func (a *Analyzer) helmManifests(ctx context.Context, f *Finding, c shell.Command, st *state, releases []string) {
	args := c.Args[1:]
	var conn []string
	for i := 0; i < len(args); i++ {
		name, _, hasValue := strings.Cut(args[i], "=")
		if !helmConnFlags[name] {
			continue
		}
		conn = append(conn, args[i])
		if !hasValue && i+1 < len(args) {
			conn = append(conn, args[i+1])
			i++
		}
	}
	if _, ok := flagValue(args, "--kube-context"); !ok && st.kubeCtx != "" {
		conn = append(conn, "--kube-context", st.kubeCtx)
	}
	ns, _ := flagValue(args, "-n", "--namespace")
	for _, rel := range releases {
		res, err := a.Run(ctx, st.dir, st.envFor(c), c.Args[0], append([]string{"get", "manifest", rel}, conn...)...)
		if err != nil || res.ExitCode != 0 {
			f.Resources = append(f.Resources, Resource{Action: ActDestroy, Name: "release/" + rel, Namespace: ns})
			unavailable(f, "helm get manifest "+rel+" failed", res, err)
			continue
		}
		objs := manifestObjects(string(res.Stdout))
		for _, o := range objs {
			objNs := o.namespace
			if objNs == "" {
				objNs = ns
			}
			f.Resources = append(f.Resources, Resource{
				Action:    ActDestroy,
				Name:      strings.ToLower(o.kind) + "/" + o.name,
				Namespace: objNs,
				Stateful:  a.stateful(o.kind),
			})
		}
		f.Notes = append(f.Notes, "release "+rel+" owns "+plural(len(objs), "object"))
	}
}

type manifestObject struct{ kind, name, namespace string }

// manifestObjects reads kind, metadata.name and metadata.namespace from each
// document of a rendered Helm manifest. Charts render plain YAML, so a
// line-based reader is enough and keeps blast-radius free of dependencies.
func manifestObjects(manifest string) []manifestObject {
	var out []manifestObject
	for _, doc := range strings.Split(manifest, "\n---") {
		var o manifestObject
		inMeta := false
		for _, line := range strings.Split(doc, "\n") {
			line = strings.TrimRight(line, "\r")
			if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			indented := line[0] == ' ' || line[0] == '\t'
			if !indented {
				inMeta = strings.HasPrefix(line, "metadata:")
				if v, ok := strings.CutPrefix(line, "kind:"); ok {
					o.kind = unquote(v)
				}
				continue
			}
			if !inMeta {
				continue
			}
			// Only direct children of metadata, which are indented by exactly
			// one level; this skips labels and annotations.
			trimmed := strings.TrimLeft(line, " ")
			if len(line)-len(trimmed) != 2 {
				continue
			}
			if v, ok := strings.CutPrefix(trimmed, "name:"); ok {
				o.name = unquote(v)
			} else if v, ok := strings.CutPrefix(trimmed, "namespace:"); ok {
				o.namespace = unquote(v)
			}
		}
		if o.kind != "" && o.name != "" {
			out = append(out, o)
		}
	}
	return out
}

func unquote(s string) string { return strings.Trim(strings.TrimSpace(s), `"'`) }

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
