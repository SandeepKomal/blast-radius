//go:build e2e

// Package e2e runs the blast-radius binary against real tools: terraform
// (or tofu) with a local state, and a real Kubernetes API server.
//
//	go test -tags e2e ./e2e/
//
// The Kubernetes tests need a cluster in KUBECONFIG and BLAST_RADIUS_E2E_KUBE=1.
package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "blast-radius-e2e-")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "blast-radius")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "..")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type result struct {
	Action   string `json:"action"`
	Findings []struct {
		Tool      string `json:"tool"`
		Target    string `json:"target"`
		Level     string `json:"level"`
		Resources []struct {
			Action    string `json:"action"`
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
			Stateful  bool   `json:"stateful"`
		} `json:"resources"`
		Notes   []string `json:"notes"`
		Verdict struct {
			Action      string `json:"action"`
			Environment string `json:"environment"`
			Reason      string `json:"reason"`
		} `json:"verdict"`
	} `json:"findings"`
}

// env isolates blast-radius from the real user's config.
func env(t *testing.T, extra ...string) []string {
	t.Helper()
	home := t.TempDir()
	return append(os.Environ(), append([]string{
		"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"AppData=" + filepath.Join(home, "AppData"),
	}, extra...)...)
}

func check(t *testing.T, dir string, environ []string, command string) result {
	t.Helper()
	cmd := exec.Command(binary, "check", "--json", "-C", dir, command)
	cmd.Env = environ
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	var exitErr *exec.ExitError
	if err := cmd.Run(); err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("%s: %v", command, err)
	}
	var r result
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatalf("%s: bad JSON %q (stderr %s)", command, out.String(), errb.String())
	}
	return r
}

func run(t *testing.T, dir string, environ []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Env = dir, environ
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func terraformBinary(t *testing.T) string {
	for _, name := range []string{"terraform", "tofu"} {
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	t.Skip("terraform or tofu not on PATH")
	return ""
}

// demo copies examples/demo and applies it in the default and prod workspaces.
func demo(t *testing.T, tf string, environ []string) string {
	t.Helper()
	dir := t.TempDir()
	src, _ := os.ReadFile(filepath.Join("..", "examples", "demo", "main.tf"))
	os.WriteFile(filepath.Join(dir, "main.tf"), src, 0o644)
	run(t, dir, environ, tf, "init", "-input=false")
	run(t, dir, environ, tf, "apply", "-auto-approve", "-input=false")
	run(t, dir, environ, tf, "workspace", "new", "prod")
	run(t, dir, environ, tf, "apply", "-auto-approve", "-input=false")
	run(t, dir, environ, tf, "workspace", "select", "default")
	return dir
}

func TestTerraform(t *testing.T) {
	tf := terraformBinary(t)
	environ := env(t)
	dir := demo(t, tf, environ)

	r := check(t, dir, environ, tf+" destroy -auto-approve")
	if r.Action != "ask" || len(r.Findings) != 1 || len(r.Findings[0].Resources) != 5 {
		t.Errorf("destroy in default workspace: %+v", r)
	}
	r = check(t, dir, environ, tf+" workspace select prod && "+tf+" destroy -auto-approve")
	if r.Action != "deny" || r.Findings[0].Verdict.Environment != "production" {
		t.Errorf("destroy after selecting prod: %+v", r)
	}
	r = check(t, dir, environ, "TF_WORKSPACE=prod "+tf+" destroy")
	if r.Action != "deny" {
		t.Errorf("TF_WORKSPACE=prod destroy: %+v", r)
	}
	r = check(t, dir, environ, tf+" apply -var replicas=1 -auto-approve")
	if r.Action != "ask" || len(r.Findings[0].Resources) != 2 {
		t.Errorf("scale down: %+v", r)
	}
	r = check(t, dir, environ, tf+" apply -auto-approve")
	if r.Action != "pass" || len(r.Findings) != 0 {
		t.Errorf("no-op apply: %+v", r)
	}
	r = check(t, dir, environ, tf+" plan && "+tf+" output && "+tf+" state list")
	if r.Action != "pass" {
		t.Errorf("read-only commands: %+v", r)
	}

	os.WriteFile(filepath.Join(dir, ".blast-radius.json"), []byte(`{"protect": ["terraform_data.database"]}`), 0o644)
	r = check(t, dir, environ, tf+" destroy")
	if r.Action != "deny" || !strings.Contains(r.Findings[0].Verdict.Reason, "terraform_data.database") {
		t.Errorf("protected resource: %+v", r)
	}

	// Nothing above may have changed real state.
	for _, ws := range []string{"default", "prod"} {
		out := run(t, dir, append(environ, "TF_WORKSPACE="+ws), tf, "state", "list")
		if n := strings.Count(strings.TrimSpace(out), "\n") + 1; n != 5 {
			t.Errorf("workspace %s has %d resources after checks:\n%s", ws, n, out)
		}
	}
}

func TestHookBinary(t *testing.T) {
	tf := terraformBinary(t)
	environ := env(t)
	dir := demo(t, tf, environ)
	input, _ := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]string{"command": "cd " + dir + " && " + tf + " workspace select prod && " + tf + " destroy -auto-approve"},
		"cwd":             t.TempDir(),
	})
	cmd := exec.Command(binary, "hook", "claude")
	cmd.Env, cmd.Stdin = environ, bytes.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var o struct {
		HookSpecificOutput struct {
			PermissionDecision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out, &o); err != nil || o.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("hook output %s (%v)", out, err)
	}
}

const shopManifest = `apiVersion: v1
kind: Namespace
metadata: {name: br-e2e}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: web, namespace: br-e2e, labels: {app: br-e2e}}
spec:
  replicas: 1
  selector: {matchLabels: {app: web}}
  template:
    metadata: {labels: {app: web}}
    spec: {containers: [{name: web, image: registry.k8s.io/pause:3.9}]}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: data, namespace: br-e2e, labels: {app: br-e2e}}
spec: {accessModes: [ReadWriteOnce], resources: {requests: {storage: 1Gi}}}
---
apiVersion: v1
kind: ConfigMap
metadata: {name: settings, namespace: br-e2e, labels: {app: br-e2e}}
data: {LOG_LEVEL: info}
`

func TestKubernetes(t *testing.T) {
	if os.Getenv("BLAST_RADIUS_E2E_KUBE") != "1" {
		t.Skip("set BLAST_RADIUS_E2E_KUBE=1 to run against the cluster in KUBECONFIG")
	}
	// Work on a copy of the kubeconfig with an extra "prod-eks" context that
	// points at the same cluster, so the real one is never modified.
	src := os.Getenv("KUBECONFIG")
	if src == "" {
		home, _ := os.UserHomeDir()
		src = filepath.Join(home, ".kube", "config")
	}
	kc := filepath.Join(t.TempDir(), "kubeconfig")
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(kc, b, 0o600)
	environ := env(t, "KUBECONFIG="+kc)
	dir := t.TempDir()
	current := strings.TrimSpace(run(t, dir, environ, "kubectl", "config", "current-context"))
	cluster := strings.TrimSpace(run(t, dir, environ, "kubectl", "config", "view", "--minify", "-o", "jsonpath={.contexts[0].context.cluster}"))
	user := strings.TrimSpace(run(t, dir, environ, "kubectl", "config", "view", "--minify", "-o", "jsonpath={.contexts[0].context.user}"))
	run(t, dir, environ, "kubectl", "config", "set-context", "prod-eks", "--cluster="+cluster, "--user="+user)

	os.WriteFile(filepath.Join(dir, "shop.yaml"), []byte(shopManifest), 0o644)
	run(t, dir, environ, "kubectl", "apply", "-f", "shop.yaml")
	t.Cleanup(func() {
		exec.Command("kubectl", "--kubeconfig", kc, "delete", "ns", "br-e2e", "--wait=false").Run()
	})

	r := check(t, dir, environ, "kubectl delete ns br-e2e")
	if r.Action != "ask" || !r.Findings[0].Resources[0].Stateful || !strings.Contains(strings.Join(r.Findings[0].Notes, " "), "PersistentVolumeClaim") {
		t.Errorf("delete namespace on %s: %+v", current, r)
	}
	r = check(t, dir, environ, "kubectl config use-context prod-eks && kubectl delete ns br-e2e")
	if r.Action != "deny" {
		t.Errorf("delete namespace after switching to prod: %+v", r)
	}
	r = check(t, dir, environ, "kubectl delete deploy,pvc -l app=br-e2e -n br-e2e")
	if r.Action != "ask" || len(r.Findings[0].Resources) != 2 {
		t.Errorf("delete by label: %+v", r)
	}
	r = check(t, dir, environ, "kubectl apply -f shop.yaml")
	if r.Action != "pass" {
		t.Errorf("apply with no changes: %+v", r)
	}

	// The same manifest without the PVC: apply --prune would delete it.
	var kept []string
	for _, doc := range strings.Split(shopManifest, "---\n") {
		if !strings.Contains(doc, "kind: PersistentVolumeClaim") {
			kept = append(kept, doc)
		}
	}
	os.WriteFile(filepath.Join(dir, "pruned.yaml"), []byte(strings.Join(kept, "---\n")), 0o644)
	r = check(t, dir, environ, "kubectl apply -f pruned.yaml --prune -l app=br-e2e -n br-e2e")
	found := false
	for _, res := range r.Findings[0].Resources {
		if res.Action == "destroy" && res.Name == "persistentvolumeclaim/data" {
			found = true
		}
	}
	if r.Action != "ask" || !found {
		t.Errorf("apply --prune: %+v", r)
	}
	r = check(t, dir, environ, "kubectl delete ns br-e2e --dry-run=server")
	if r.Action != "pass" {
		t.Errorf("dry run: %+v", r)
	}

	// The checks above must not have deleted anything.
	out := run(t, dir, environ, "kubectl", "get", "pvc,deploy,cm", "-n", "br-e2e", "-l", "app=br-e2e", "-o", "name")
	if !strings.Contains(out, "persistentvolumeclaim/data") || !strings.Contains(out, "deployment.apps/web") {
		t.Errorf("objects changed by checks:\n%s", out)
	}
}
