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

// terraformBinaries returns terraform and tofu, whichever are on PATH.
func terraformBinaries(t *testing.T) []string {
	var found []string
	for _, name := range []string{"terraform", "tofu"} {
		if _, err := exec.LookPath(name); err == nil {
			found = append(found, name)
		}
	}
	if len(found) == 0 {
		t.Skip("terraform or tofu not on PATH")
	}
	return found
}

func terraformBinary(t *testing.T) string { return terraformBinaries(t)[0] }

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
	for _, tf := range terraformBinaries(t) {
		t.Run(tf, func(t *testing.T) { testTerraform(t, tf) })
	}
}

func testTerraform(t *testing.T, tf string) {
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

	// Planning in a workspace that does not exist must not create it.
	r = check(t, dir, environ, "TF_WORKSPACE=staging "+tf+" destroy")
	if r.Action != "pass" {
		t.Errorf("destroy in an empty workspace: %+v", r)
	}
	if out := run(t, dir, environ, tf, "workspace", "list"); strings.Contains(out, "staging") {
		t.Errorf("check created a workspace:\n%s", out)
	}

	// Nothing above may have changed real state.
	for _, ws := range []string{"default", "prod"} {
		out := run(t, dir, append(environ, "TF_WORKSPACE="+ws), tf, "state", "list")
		if n := strings.Count(strings.TrimSpace(out), "\n") + 1; n != 5 {
			t.Errorf("workspace %s has %d resources after checks:\n%s", ws, n, out)
		}
	}
}

func TestTerragrunt(t *testing.T) {
	if _, err := exec.LookPath("terragrunt"); err != nil {
		t.Skip("terragrunt not on PATH")
	}
	tf := terraformBinary(t)
	environ := env(t, "TERRAGRUNT_TFPATH="+tf, "TG_TF_PATH="+tf)
	dir := demo(t, tf, environ)
	os.WriteFile(filepath.Join(dir, "terragrunt.hcl"), nil, 0o644)

	r := check(t, dir, environ, "terragrunt destroy -auto-approve")
	if r.Action != "ask" || len(r.Findings) != 1 || len(r.Findings[0].Resources) != 5 {
		t.Errorf("terragrunt destroy: %+v", r)
	}
	r = check(t, dir, environ, "terragrunt apply -auto-approve")
	if r.Action != "pass" {
		t.Errorf("terragrunt no-op apply: %+v", r)
	}
	r = check(t, dir, environ, "terragrunt run-all destroy")
	if r.Action != "ask" || r.Findings[0].Level != "destroy" {
		t.Errorf("terragrunt run-all destroy: %+v", r)
	}
	r = check(t, t.TempDir(), environ, "cd "+dir+" && terragrunt destroy")
	if r.Action != "ask" {
		t.Errorf("terragrunt destroy after cd: %+v", r)
	}
	out := run(t, dir, environ, tf, "state", "list")
	if n := strings.Count(strings.TrimSpace(out), "\n") + 1; n != 5 {
		t.Errorf("state has %d resources after checks:\n%s", n, out)
	}
}

func TestPulumi(t *testing.T) {
	if _, err := exec.LookPath("pulumi"); err != nil {
		t.Skip("pulumi not on PATH")
	}
	// A local file backend and a YAML project need no account or plugins.
	dir := t.TempDir()
	environ := env(t, "PULUMI_BACKEND_URL=file://"+filepath.ToSlash(t.TempDir()), "PULUMI_CONFIG_PASSPHRASE=e2e",
		"PULUMI_SKIP_UPDATE_CHECK=true")
	os.WriteFile(filepath.Join(dir, "Pulumi.yaml"), []byte("name: demo\nruntime: yaml\n"), 0o644)
	run(t, dir, environ, "pulumi", "stack", "init", "dev", "--non-interactive")
	run(t, dir, environ, "pulumi", "stack", "init", "prod", "--non-interactive")

	r := check(t, dir, environ, "pulumi destroy --yes")
	if r.Action != "deny" || r.Findings[0].Target != "stack prod" {
		t.Errorf("destroy with prod selected: %+v", r)
	}
	r = check(t, dir, environ, "pulumi stack select dev && pulumi destroy --yes")
	if r.Action != "ask" || r.Findings[0].Target != "stack dev" {
		t.Errorf("destroy after selecting dev: %+v", r)
	}
	run(t, dir, environ, "pulumi", "stack", "select", "dev")
	r = check(t, dir, environ, "pulumi destroy --yes")
	if r.Action != "ask" || r.Findings[0].Target != "stack dev" {
		t.Errorf("destroy with dev selected: %+v", r)
	}
	r = check(t, dir, environ, "pulumi stack rm prod --yes")
	if r.Action != "deny" || r.Findings[0].Target != "stack prod" {
		t.Errorf("stack rm: %+v", r)
	}
	r = check(t, dir, environ, "pulumi preview && pulumi stack ls")
	if r.Action != "pass" {
		t.Errorf("read-only commands: %+v", r)
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

// kubeSetup returns an environment with a copy of the user's kubeconfig that
// has an extra "prod-eks" context pointing at the same cluster, so the real
// kubeconfig is never modified.
func kubeSetup(t *testing.T) (environ []string, dir, kc, current string) {
	t.Helper()
	if os.Getenv("BLAST_RADIUS_E2E_KUBE") != "1" {
		t.Skip("set BLAST_RADIUS_E2E_KUBE=1 to run against the cluster in KUBECONFIG")
	}
	src := os.Getenv("KUBECONFIG")
	if src == "" {
		home, _ := os.UserHomeDir()
		src = filepath.Join(home, ".kube", "config")
	}
	kc = filepath.Join(t.TempDir(), "kubeconfig")
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(kc, b, 0o600)
	environ = env(t, "KUBECONFIG="+kc)
	dir = t.TempDir()
	current = strings.TrimSpace(run(t, dir, environ, "kubectl", "config", "current-context"))
	cluster := strings.TrimSpace(run(t, dir, environ, "kubectl", "config", "view", "--minify", "-o", "jsonpath={.contexts[0].context.cluster}"))
	user := strings.TrimSpace(run(t, dir, environ, "kubectl", "config", "view", "--minify", "-o", "jsonpath={.contexts[0].context.user}"))
	run(t, dir, environ, "kubectl", "config", "set-context", "prod-eks", "--cluster="+cluster, "--user="+user)
	return environ, dir, kc, current
}

func TestKubernetes(t *testing.T) {
	environ, dir, kc, current := kubeSetup(t)

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
	r = check(t, dir, environ, "kubectl scale deploy web -n br-e2e --replicas=0")
	if r.Action != "ask" {
		t.Errorf("scale to zero: %+v", r)
	}
	r = check(t, dir, environ, "kubectl --context prod-eks scale deploy web -n br-e2e --replicas=0")
	if r.Action != "deny" {
		t.Errorf("scale to zero in prod: %+v", r)
	}
	r = check(t, dir, environ, "kubectl get pods -A && kubectl describe deploy web -n br-e2e && kubectl rollout status deploy/web -n br-e2e --timeout=1s")
	if r.Action != "pass" {
		t.Errorf("read-only commands: %+v", r)
	}

	// The checks above must not have deleted anything.
	out := run(t, dir, environ, "kubectl", "get", "pvc,deploy,cm", "-n", "br-e2e", "-l", "app=br-e2e", "-o", "name")
	if !strings.Contains(out, "persistentvolumeclaim/data") || !strings.Contains(out, "deployment.apps/web") {
		t.Errorf("objects changed by checks:\n%s", out)
	}
}

func TestHelm(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	environ, dir, kc, _ := kubeSetup(t)
	// `helm create` writes a starter chart locally; no repository is needed.
	run(t, dir, environ, "helm", "create", "shop")
	run(t, dir, environ, "helm", "install", "shop", "./shop", "-n", "br-helm", "--create-namespace")
	t.Cleanup(func() {
		exec.Command("kubectl", "--kubeconfig", kc, "delete", "ns", "br-helm", "--wait=false").Run()
	})

	r := check(t, dir, environ, "helm uninstall shop -n br-helm")
	names := map[string]bool{}
	for _, res := range r.Findings[0].Resources {
		names[res.Name] = res.Action == "destroy" && res.Namespace == "br-helm"
	}
	if r.Action != "ask" || !names["deployment/shop"] || !names["service/shop"] || !names["serviceaccount/shop"] {
		t.Errorf("helm uninstall: %+v", r)
	}
	r = check(t, dir, environ, "helm uninstall shop -n br-helm --kube-context prod-eks")
	if r.Action != "deny" || len(r.Findings[0].Resources) < 3 {
		t.Errorf("helm uninstall in prod: %+v", r)
	}
	r = check(t, dir, environ, "kubectl config use-context prod-eks && helm uninstall shop -n br-helm")
	if r.Action != "deny" {
		t.Errorf("helm uninstall after switching to prod: %+v", r)
	}
	r = check(t, dir, environ, "HELM_KUBECONTEXT=prod-eks helm upgrade shop ./shop -n br-helm")
	if r.Action != "ask" || r.Findings[0].Verdict.Environment != "production" {
		t.Errorf("helm upgrade in prod: %+v", r)
	}
	r = check(t, dir, environ, "helm uninstall missing -n br-helm")
	if r.Action != "ask" || !strings.Contains(strings.Join(r.Findings[0].Notes, " "), "no preview") {
		t.Errorf("helm uninstall of an unknown release: %+v", r)
	}
	r = check(t, dir, environ, "helm list -A && helm status shop -n br-helm && helm uninstall shop -n br-helm --dry-run")
	if r.Action != "pass" {
		t.Errorf("read-only commands: %+v", r)
	}
	if out := run(t, dir, environ, "helm", "list", "-n", "br-helm", "-q"); strings.TrimSpace(out) != "shop" {
		t.Errorf("release changed by checks: %q", out)
	}
}

func TestGcloud(t *testing.T) {
	if _, err := exec.LookPath("gcloud"); err != nil {
		t.Skip("gcloud not on PATH")
	}
	// gcloud writes its configuration to CLOUDSDK_CONFIG; nothing here talks
	// to Google Cloud.
	dir := t.TempDir()
	environ := env(t, "CLOUDSDK_CONFIG="+t.TempDir(), "CLOUDSDK_CORE_DISABLE_PROMPTS=1")
	run(t, dir, environ, "gcloud", "config", "set", "project", "acme-prod", "--quiet")

	r := check(t, dir, environ, "gcloud sql instances delete orders --quiet")
	if r.Action != "deny" || r.Findings[0].Target != "project acme-prod" {
		t.Errorf("delete with a prod project active: %+v", r)
	}
	run(t, dir, environ, "gcloud", "config", "configurations", "create", "dev", "--quiet")
	run(t, dir, environ, "gcloud", "config", "set", "project", "acme-dev", "--quiet")
	r = check(t, dir, environ, "gcloud sql instances delete orders --quiet")
	if r.Action != "ask" || r.Findings[0].Target != "project acme-dev" {
		t.Errorf("delete with a dev configuration active: %+v", r)
	}
	r = check(t, dir, environ, "gcloud sql instances delete orders --configuration default")
	if r.Action != "deny" {
		t.Errorf("delete with --configuration default: %+v", r)
	}
	r = check(t, dir, environ, "gcloud config set project acme-prod && gcloud compute instances delete vm-1 --zone us-east1-b")
	if r.Action != "deny" {
		t.Errorf("delete after switching project: %+v", r)
	}
	r = check(t, dir, environ, "gcloud compute instances list && gcloud sql instances describe orders")
	if r.Action != "pass" {
		t.Errorf("read-only commands: %+v", r)
	}
}

// TestEvasion runs commands written in the many ways an agent might phrase
// them against real state in the default and prod workspaces.
func TestEvasion(t *testing.T) {
	tf := terraformBinary(t)
	environ := env(t)
	dir := demo(t, tf, environ)
	bash := map[string]string{
		tf + ` destroy`:                                     "ask",
		`TF_WORKSPACE=prod ` + tf + ` destroy`:              "deny",
		`export TF_WORKSPACE=prod; ` + tf + ` destroy`:      "deny",
		`env TF_WORKSPACE=prod ` + tf + ` destroy`:          "deny",
		tf + ` workspace select prod && ` + tf + ` destroy`: "deny",
		`sudo ` + tf + ` destroy`:                           "ask",
		`bash -c '` + tf + ` destroy'`:                      "ask",
		`sh -c "TF_WORKSPACE=prod ` + tf + ` destroy"`:      "deny",
		`eval ` + tf + ` destroy`:                           "ask",
		`echo $(` + tf + ` destroy -auto-approve)`:          "ask",
		`"` + tf + `" "destroy"`:                            "ask",
		`\` + tf + ` destroy`:                               "ask",
		`timeout 60 ` + tf + ` destroy`:                     "ask",
		`nohup ` + tf + ` destroy &`:                        "ask",
		`true && { ` + tf + ` destroy; }`:                   "ask",
		tf + " \\\n  destroy":                               "ask",
		tf + ` apply -destroy`:                              "ask",
		"bash <<EOF\n" + tf + " destroy\nEOF":               "ask",
		"cat <<EOF | sh\n" + tf + " destroy\nEOF":           "ask",
		`ssh bastion '` + tf + ` destroy'`:                  "ask",
		`blast-radius uninstall`:                            "ask",
		`echo '{}' > ~/.claude/settings.json`:               "ask",
		`sed -i 's/x/y/' .claude/settings.json`:             "ask",
		`mv .blast-radius.json /tmp/`:                       "ask",
		"cat > notes.md <<'EOF'\n" + tf + " destroy\nEOF":   "pass",
		`echo ` + tf + ` destroy`:                           "pass",
		`grep -r "` + tf + ` destroy" .`:                    "pass",
		`git commit -m "` + tf + ` destroy cleanup"`:        "pass",
		tf + ` plan -destroy`:                               "pass",
		`cat .claude/settings.json`:                         "pass",
		`ssh bastion uptime`:                                "pass",
	}
	powershell := map[string]string{
		`& ` + tf + ` destroy`:                                "ask",
		`$env:TF_WORKSPACE = 'prod'; ` + tf + ` destroy`:      "deny",
		`$env:TF_WORKSPACE="prod"; ` + tf + ` destroy`:        "deny",
		tf + ` destroy -auto-approve | Out-Null`:              "ask",
		`cmd /c ` + tf + ` destroy`:                           "ask",
		`Invoke-Expression "` + tf + ` destroy"`:              "ask",
		`iex '` + tf + ` destroy'`:                            "ask",
		`pwsh -c ` + tf + ` destroy`:                          "ask",
		tf + " `\n  destroy":                                  "ask",
		`Remove-Item .claude\settings.json`:                   "ask",
		`Set-Content -Path .claude/settings.json -Value '{}'`: "ask",
		`Write-Output "` + tf + ` destroy"`:                   "pass",
		`Get-ChildItem; ` + tf + ` state list`:                "pass",
	}
	for shell, cases := range map[string]map[string]string{"bash": bash, "powershell": powershell} {
		for command, want := range cases {
			cmd := exec.Command(binary, "check", "--json", "--shell", shell, "-C", dir, command)
			cmd.Env = environ
			out, _ := cmd.Output()
			var r result
			if err := json.Unmarshal(out, &r); err != nil || r.Action != want {
				t.Errorf("%s %q: got %q, want %q", shell, command, r.Action, want)
			}
		}
	}
	for _, ws := range []string{"default", "prod"} {
		out := run(t, dir, append(environ, "TF_WORKSPACE="+ws), tf, "state", "list")
		if n := strings.Count(strings.TrimSpace(out), "\n") + 1; n != 5 {
			t.Errorf("workspace %s has %d resources after checks", ws, n)
		}
	}
}
