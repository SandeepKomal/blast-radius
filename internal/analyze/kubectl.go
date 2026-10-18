package analyze

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sandeepkomal/blast-radius/internal/shell"
)

// kubectl global flags that take a separate value, so the value is not
// mistaken for the verb in `kubectl -n prod delete ...`.
var kubeValueFlags = set("-n", "--namespace", "--context", "--kubeconfig", "--cluster", "--user",
	"-s", "--server", "--token", "--as", "--as-group", "--as-uid", "--request-timeout",
	"--certificate-authority", "--client-certificate", "--client-key", "--tls-server-name",
	"--cache-dir", "--password", "--username", "-v", "--v")

// Flags that pick the cluster; reused for follow-up queries.
var kubeConnFlags = set("--context", "--kubeconfig", "--cluster", "--user", "-s", "--server",
	"--token", "--as", "--as-group", "--as-uid", "--certificate-authority", "--client-certificate",
	"--client-key", "--tls-server-name", "--insecure-skip-tls-verify")

// Flags of `kubectl delete` that `kubectl get` does not accept.
var (
	kubeDeleteBool  = set("--force", "--now", "--wait", "--all", "-i", "--interactive", "--cascade", "--raw")
	kubeDeleteValue = set("--grace-period", "--timeout", "-o", "--output")
)

// Flags of `kubectl apply` that `kubectl diff` does not accept.
var (
	kubeApplyBool  = set("--force", "--overwrite", "--wait", "--record", "--validate", "--all", "--cascade", "--openapi-patch")
	kubeApplyValue = set("--timeout", "--grace-period", "-o", "--output", "--template")
)

// Kinds whose deletion loses data or cascades to everything inside.
var kubeStateful = set("Namespace", "PersistentVolumeClaim", "PersistentVolume", "StatefulSet",
	"Secret", "CustomResourceDefinition")

// maxNamespaceLookups bounds the follow-up queries for `kubectl delete ns --all`.
const maxNamespaceLookups = 5

func (a *Analyzer) kubectl(ctx context.Context, c shell.Command, st *state) *Finding {
	args := c.Args[1:]
	verbAt := -1
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "-") {
			verbAt = i
			break
		}
		if kubeValueFlags[args[i]] {
			i++
		}
	}
	if verbAt < 0 {
		return nil
	}
	pre, verb, post := args[:verbAt], args[verbAt], args[verbAt+1:]
	all := append(append([]string{}, pre...), post...)
	// A context switched to earlier in the script applies to the preview too,
	// so it looks at the cluster the command will really run against.
	if st.kubeCtx != "" && verb != "config" && !hasFlag(all, "--context") {
		pre = append([]string{"--context", st.kubeCtx}, pre...)
		all = append([]string{"--context", st.kubeCtx}, all...)
	}

	if verb == "config" {
		if p := positionals(post, kubeValueFlags); len(p) == 2 && p[0] == "use-context" {
			st.kubeCtx = p[1]
		}
		return nil
	}
	if v, ok := flagValue(all, "--dry-run"); (ok && v != "none") || hasExact(all, "--dry-run") {
		return nil
	}

	f := &Finding{Tool: c.Program(), Command: c.String()}
	env := st.envFor(c)
	switch verb {
	case "delete":
		f.Level = Destroy
		a.kubeDelete(ctx, f, c, st.dir, env, pre, post)
	case "apply":
		a.kubeApply(ctx, f, c, st.dir, env, pre, post)
	case "drain":
		f.Level = Destroy
		f.Notes = append(f.Notes, "drain evicts every pod on the node")
	case "scale":
		if v, _ := flagValue(post, "--replicas"); v == "0" {
			f.Level = Destroy
			f.Notes = append(f.Notes, "scales to zero replicas")
		} else {
			f.Level = Change
		}
	case "replace":
		if hasFlag(post, "--force") {
			f.Level = Destroy
			f.Notes = append(f.Notes, "replace --force deletes and recreates the objects")
		} else {
			f.Level = Change
		}
	case "rollout":
		if len(post) == 0 || !set("restart", "undo", "pause", "resume")[post[0]] {
			return nil
		}
		f.Level = Change
	case "patch", "edit", "set", "label", "annotate", "create", "expose", "autoscale", "cordon", "uncordon", "taint":
		f.Level = Change
	default:
		return nil
	}

	kctx := a.kubeContext(ctx, c.Args[0], all, st, env)
	f.Target = "context " + kctx
	f.Labels = []string{kctx, st.dir}
	if ns, ok := flagValue(all, "-n", "--namespace"); ok {
		f.Target += ", namespace " + ns
		f.Labels = append(f.Labels, ns)
	}
	return f
}

// hasExact catches the old boolean form, e.g. `--dry-run` with no value.
func hasExact(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func connFlags(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		name, _, hasValue := strings.Cut(args[i], "=")
		if !kubeConnFlags[name] {
			continue
		}
		out = append(out, args[i])
		if !hasValue && kubeValueFlags[name] && i+1 < len(args) {
			out = append(out, args[i+1])
			i++
		}
	}
	return out
}

// kubeContext finds the context a kubectl or helm command will use.
func (a *Analyzer) kubeContext(ctx context.Context, kubectl string, args []string, st *state, env []string) string {
	if v, ok := flagValue(args, "--context", "--kube-context"); ok {
		return v
	}
	if st.kubeCtx != "" {
		return st.kubeCtx
	}
	var conn []string
	if v, ok := flagValue(args, "--kubeconfig"); ok {
		conn = []string{"--kubeconfig", v}
	}
	res, err := a.Run(ctx, st.dir, env, kubectl, append(conn, "config", "current-context")...)
	if err != nil || res.ExitCode != 0 {
		return "unknown"
	}
	if s := strings.TrimSpace(string(res.Stdout)); s != "" {
		return s
	}
	return "unknown"
}

type kubeObject struct {
	Kind     string       `json:"kind"`
	Items    []kubeObject `json:"items"`
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
}

func (o kubeObject) flatten() []kubeObject {
	if strings.HasSuffix(o.Kind, "List") {
		return o.Items
	}
	if o.Kind == "" {
		return nil
	}
	return []kubeObject{o}
}

// kubeDelete lists exactly what `kubectl delete` would match by running
// `kubectl get` with the same selection.
func (a *Analyzer) kubeDelete(ctx context.Context, f *Finding, c shell.Command, dir string, env, pre, post []string) {
	getArgs := append(append([]string{}, pre...), "get")
	getArgs = append(getArgs, dropFlags(post, kubeDeleteBool, kubeDeleteValue)...)
	getArgs = append(getArgs, "--ignore-not-found", "-o", "json")
	res, err := a.Run(ctx, dir, env, c.Args[0], getArgs...)
	if err != nil || res.ExitCode != 0 {
		unavailable(f, "kubectl get failed", res, err)
		return
	}
	var objs []kubeObject
	dec := json.NewDecoder(strings.NewReader(string(res.Stdout)))
	for dec.More() {
		var o kubeObject
		if err := dec.Decode(&o); err != nil {
			unavailable(f, "could not read kubectl output", Result{}, err)
			return
		}
		objs = append(objs, o.flatten()...)
	}
	if len(objs) == 0 {
		f.Level = None // nothing matches, nothing to delete
		return
	}
	conn := connFlags(append(append([]string{}, pre...), post...))
	lookups := 0
	for _, o := range objs {
		f.Resources = append(f.Resources, Resource{
			Action:    ActDestroy,
			Name:      strings.ToLower(o.Kind) + "/" + o.Metadata.Name,
			Namespace: o.Metadata.Namespace,
			Stateful:  a.stateful(o.Kind),
		})
		if o.Kind != "Namespace" {
			continue
		}
		if lookups++; lookups > maxNamespaceLookups {
			continue
		}
		a.namespaceContents(ctx, f, c, dir, env, conn, o.Metadata.Name)
	}
	if lookups > maxNamespaceLookups {
		f.Notes = append(f.Notes, fmt.Sprintf("contents listed for the first %d namespaces only", maxNamespaceLookups))
	}
}

func (a *Analyzer) namespaceContents(ctx context.Context, f *Finding, c shell.Command, dir string, env, conn []string, ns string) {
	args := append(append([]string{}, conn...), "get", "all,pvc,secret,configmap,ingress", "-n", ns, "-o", "json")
	res, err := a.Run(ctx, dir, env, c.Args[0], args...)
	if err != nil || res.ExitCode != 0 {
		f.Notes = append(f.Notes, "could not list the contents of namespace "+ns)
		return
	}
	var list kubeObject
	if err := json.Unmarshal(res.Stdout, &list); err != nil {
		return
	}
	counts := map[string]int{}
	for _, o := range list.flatten() {
		counts[o.Kind]++
	}
	if len(counts) == 0 {
		return
	}
	kinds := make([]string, 0, len(counts))
	for k := range counts {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool {
		if counts[kinds[i]] != counts[kinds[j]] {
			return counts[kinds[i]] > counts[kinds[j]]
		}
		return kinds[i] < kinds[j]
	})
	parts := make([]string, len(kinds))
	for i, k := range kinds {
		parts[i] = fmt.Sprintf("%d %s", counts[k], k)
	}
	f.Notes = append(f.Notes, "namespace "+ns+" also takes down: "+strings.Join(parts, ", "))
}

// kubeApply previews `kubectl apply` with `kubectl diff`.
func (a *Analyzer) kubeApply(ctx context.Context, f *Finding, c shell.Command, dir string, env, pre, post []string) {
	diffArgs := append(append([]string{}, pre...), "diff")
	diffArgs = append(diffArgs, dropFlags(post, kubeApplyBool, kubeApplyValue)...)
	// A custom differ (dyff, delta...) would change the output format we read.
	env = append(env, "KUBECTL_EXTERNAL_DIFF=diff -u -N")
	res, err := a.Run(ctx, dir, env, c.Args[0], diffArgs...)
	switch {
	case err != nil || res.ExitCode > 1:
		unavailable(f, "kubectl diff failed", res, err)
	case res.ExitCode == 0:
		f.Level = None // already up to date
	default:
		a.applyDiff(f, string(res.Stdout))
	}
}

// applyDiff reads `kubectl diff` output. Each object starts with a line like
//
//	diff -u -N /tmp/LIVE-123/apps.v1.Deployment.default.web /tmp/MERGED-123/apps.v1.Deployment.default.web
func (a *Analyzer) applyDiff(f *Finding, out string) {
	f.Level = Change
	var cur *Resource
	var kind string
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "diff "); ok {
			fields := strings.Fields(rest)
			if len(fields) == 0 {
				continue
			}
			var r Resource
			kind, r = diffObject(filepath.Base(fields[len(fields)-1]))
			r.Action = ActUpdate
			f.Resources = append(f.Resources, r)
			cur = &f.Resources[len(f.Resources)-1]
			continue
		}
		if cur == nil || !strings.HasPrefix(line, "@@ ") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "@@ -0,0 "):
			cur.Action = ActCreate
		case strings.Contains(line, " +0,0 @@"):
			cur.Action = ActDestroy
			cur.Stateful = a.stateful(kind)
			f.Level = Destroy
		}
	}
}

// diffObject turns "apps.v1.Deployment.default.web" into the kind
// "Deployment" and the resource deployment/web in namespace default.
func diffObject(base string) (string, Resource) {
	parts := strings.Split(base, ".")
	for i, p := range parts {
		if p == "" || p[0] < 'A' || p[0] > 'Z' || i+2 > len(parts) {
			continue
		}
		return p, Resource{Name: strings.ToLower(p) + "/" + strings.Join(parts[i+2:], "."), Namespace: parts[i+1]}
	}
	return "", Resource{Name: base}
}
