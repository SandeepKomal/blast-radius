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

func (a *Analyzer) kubectl(ctx context.Context, c shell.Command, dir string) *Finding {
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
	if v, ok := flagValue(all, "--dry-run"); (ok && v != "none") || hasDryRunBool(all) {
		return nil
	}

	f := &Finding{Tool: c.Program(), Command: c.String()}
	switch verb {
	case "delete":
		f.Level = Destroy
		a.kubeDelete(ctx, f, c, dir, pre, post)
	case "apply":
		a.kubeApply(ctx, f, c, dir, pre, post)
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

	conn := connFlags(all)
	kctx, ok := flagValue(all, "--context")
	if !ok {
		kctx = a.currentContext(ctx, c, dir, conn)
	}
	f.Target = "context " + kctx
	f.Labels = []string{kctx, dir}
	if ns, ok := flagValue(all, "-n", "--namespace"); ok {
		f.Target += ", namespace " + ns
		f.Labels = append(f.Labels, ns)
	}
	return f
}

// hasDryRunBool catches the old boolean form `--dry-run` with no value.
func hasDryRunBool(args []string) bool {
	for _, a := range args {
		if a == "--dry-run" {
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

func (a *Analyzer) currentContext(ctx context.Context, c shell.Command, dir string, conn []string) string {
	kubectl := c.Args[0]
	if p := c.Program(); p != "kubectl" && p != "oc" {
		kubectl = "kubectl"
	}
	res, err := a.Run(ctx, dir, c.Env, kubectl, append(conn, "config", "current-context")...)
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

func (o kubeObject) String() string {
	s := strings.ToLower(o.Kind) + "/" + o.Metadata.Name
	if o.Metadata.Namespace != "" {
		s += " (ns " + o.Metadata.Namespace + ")"
	}
	return s
}

// kubeDelete lists exactly what `kubectl delete` would match by running
// `kubectl get` with the same selection.
func (a *Analyzer) kubeDelete(ctx context.Context, f *Finding, c shell.Command, dir string, pre, post []string) {
	getArgs := append(append([]string{}, pre...), "get")
	getArgs = append(getArgs, dropFlags(post, kubeDeleteBool, kubeDeleteValue)...)
	getArgs = append(getArgs, "--ignore-not-found", "-o", "json")
	res, err := a.Run(ctx, dir, c.Env, c.Args[0], getArgs...)
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
	for _, o := range objs {
		f.Resources = append(f.Resources, Resource{Action: ActDestroy, Name: o.String(), Stateful: kubeStateful[o.Kind]})
		if o.Kind == "Namespace" {
			a.namespaceContents(ctx, f, c, dir, conn, o.Metadata.Name)
		}
	}
}

func (a *Analyzer) namespaceContents(ctx context.Context, f *Finding, c shell.Command, dir string, conn []string, ns string) {
	args := append(append([]string{}, conn...), "get", "all,pvc,secret,configmap,ingress", "-n", ns, "-o", "json")
	res, err := a.Run(ctx, dir, c.Env, c.Args[0], args...)
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
func (a *Analyzer) kubeApply(ctx context.Context, f *Finding, c shell.Command, dir string, pre, post []string) {
	diffArgs := append(append([]string{}, pre...), "diff")
	diffArgs = append(diffArgs, dropFlags(post, kubeApplyBool, kubeApplyValue)...)
	res, err := a.Run(ctx, dir, c.Env, c.Args[0], diffArgs...)
	switch {
	case err != nil || res.ExitCode > 1:
		unavailable(f, "kubectl diff failed", res, err)
	case res.ExitCode == 0:
		f.Level = None // already up to date
	default:
		applyDiff(f, string(res.Stdout))
	}
}

// applyDiff reads `kubectl diff` output. Each object starts with a line like
//
//	diff -u -N /tmp/LIVE-123/apps.v1.Deployment.default.web /tmp/MERGED-123/apps.v1.Deployment.default.web
func applyDiff(f *Finding, out string) {
	f.Level = Change
	var cur *Resource
	var kind string
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "diff "); ok {
			fields := strings.Fields(rest)
			if len(fields) == 0 {
				continue
			}
			var name string
			kind, name = diffObjectName(filepath.Base(fields[len(fields)-1]))
			f.Resources = append(f.Resources, Resource{Action: ActUpdate, Name: name})
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
			f.Level = Destroy
			cur.Stateful = kubeStateful[kind]
		}
	}
}

// diffObjectName turns "apps.v1.Deployment.default.web" into the kind
// "Deployment" and the name "deployment/web (ns default)".
func diffObjectName(base string) (kind, name string) {
	parts := strings.Split(base, ".")
	for i, p := range parts {
		if p == "" || p[0] < 'A' || p[0] > 'Z' || i+2 > len(parts) {
			continue
		}
		name = strings.ToLower(p) + "/" + strings.Join(parts[i+2:], ".")
		if ns := parts[i+1]; ns != "" {
			name += " (ns " + ns + ")"
		}
		return p, name
	}
	return "", base
}
