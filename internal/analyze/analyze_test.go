package analyze

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fake answers preview commands from a table keyed by the joined command
// line. Arguments ending in .tfplan are replaced by "PLAN" in the key.
type fake struct {
	answers map[string]Result
	calls   []string
}

func (f *fake) run(_ context.Context, _ string, _ []string, name string, args ...string) (Result, error) {
	parts := []string{name}
	for _, a := range args {
		if strings.HasSuffix(a, ".tfplan") {
			a = a[:strings.LastIndexAny(a, "=")+1] + "PLAN"
		}
		parts = append(parts, a)
	}
	key := strings.Join(parts, " ")
	f.calls = append(f.calls, key)
	if r, ok := f.answers[key]; ok {
		return r, nil
	}
	return Result{ExitCode: 1, Stderr: []byte("unexpected command: " + key)}, nil
}

func analyze(t *testing.T, script string, answers map[string]Result) ([]Finding, *fake) {
	t.Helper()
	f := &fake{answers: answers}
	a := &Analyzer{Run: f.run}
	return a.Analyze(context.Background(), script, t.TempDir()), f
}

const planJSON = `{"resource_changes":[
 {"address":"aws_db_instance.main","type":"aws_db_instance","mode":"managed","change":{"actions":["delete"]}},
 {"address":"aws_launch_template.web","type":"aws_launch_template","mode":"managed","change":{"actions":["create","delete"]}},
 {"address":"aws_instance.web","type":"aws_instance","mode":"managed","change":{"actions":["update"]}},
 {"address":"data.aws_ami.ubuntu","type":"aws_ami","mode":"data","change":{"actions":["read"]}},
 {"address":"aws_s3_bucket.logs","type":"aws_s3_bucket","mode":"managed","change":{"actions":["no-op"]}}
]}`

func TestTerraformApply(t *testing.T) {
	findings, fk := analyze(t, `cd infra && terraform apply -auto-approve -var-file prod.tfvars -target=module.db`, map[string]Result{
		"terraform plan -input=false -lock=false -no-color -out=PLAN -var-file prod.tfvars -target=module.db": {},
		"terraform show -json -no-color PLAN": {Stdout: []byte(planJSON)},
	})
	if len(findings) != 1 {
		t.Fatalf("got %d findings, calls %q", len(findings), fk.calls)
	}
	f := findings[0]
	if f.Level != Destroy {
		t.Errorf("level = %v, want destroy (notes %q)", f.Level, f.Notes)
	}
	want := []Resource{
		{Action: ActDestroy, Name: "aws_db_instance.main", Stateful: true},
		{Action: ActReplace, Name: "aws_launch_template.web"},
		{Action: ActUpdate, Name: "aws_instance.web"},
	}
	if !reflect.DeepEqual(f.Resources, want) {
		t.Errorf("resources = %+v", f.Resources)
	}
	if f.Target != "workspace default" || !strings.HasSuffix(f.Labels[1], "/infra") {
		t.Errorf("target %q labels %q", f.Target, f.Labels)
	}
}

func TestTerraformDestroyUsesWorkspaceAndChdir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "envs/prod/.terraform"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "envs/prod/.terraform/environment"), []byte("prod\n"), 0o644)

	fk := &fake{answers: map[string]Result{
		"tofu -chdir=envs/prod plan -input=false -lock=false -no-color -out=PLAN -destroy": {},
		"tofu -chdir=envs/prod show -json -no-color PLAN":                                  {Stdout: []byte(planJSON)},
	}}
	findings := (&Analyzer{Run: fk.run}).Analyze(context.Background(), `tofu -chdir=envs/prod destroy`, dir)
	if len(findings) != 1 || findings[0].Target != "workspace prod" || findings[0].Tool != "tofu" {
		t.Fatalf("findings = %+v, calls %q", findings, fk.calls)
	}
}

func TestTerraformNoChangesPasses(t *testing.T) {
	findings, _ := analyze(t, `terraform apply -auto-approve`, map[string]Result{
		"terraform plan -input=false -lock=false -no-color -out=PLAN": {},
		"terraform show -json -no-color PLAN":                         {Stdout: []byte(`{"resource_changes":[]}`)},
	})
	if len(findings) != 0 {
		t.Errorf("want no findings, got %+v", findings)
	}
}

func TestTerraformPlanFailureIsDestructive(t *testing.T) {
	findings, _ := analyze(t, `terraform apply`, map[string]Result{
		"terraform plan -input=false -lock=false -no-color -out=PLAN": {ExitCode: 1, Stderr: []byte("\n│ Error: No valid credential sources found\n")},
	})
	if len(findings) != 1 || findings[0].Level != Destroy {
		t.Fatalf("findings = %+v", findings)
	}
	if !strings.Contains(findings[0].Notes[0], "No valid credential sources found") {
		t.Errorf("notes = %q", findings[0].Notes)
	}
}

func TestTerraformSavedPlanAndReadOnly(t *testing.T) {
	findings, fk := analyze(t, `terraform plan -out=x && terraform show && terraform apply x`, map[string]Result{
		"terraform show -json -no-color x": {Stdout: []byte(planJSON)},
	})
	if len(findings) != 1 || len(fk.calls) != 1 {
		t.Fatalf("findings = %+v, calls %q", findings, fk.calls)
	}
}

const kubeList = `{"kind":"List","items":[
 {"kind":"Namespace","metadata":{"name":"payments"}}
]}`

const nsContents = `{"kind":"List","items":[
 {"kind":"Pod","metadata":{"name":"a","namespace":"payments"}},
 {"kind":"Pod","metadata":{"name":"b","namespace":"payments"}},
 {"kind":"PersistentVolumeClaim","metadata":{"name":"data","namespace":"payments"}}
]}`

func TestKubectlDeleteNamespace(t *testing.T) {
	findings, fk := analyze(t, `kubectl --context prod-eks delete ns payments --wait=false --grace-period 0`, map[string]Result{
		"kubectl --context prod-eks get ns payments --ignore-not-found -o json":               {Stdout: []byte(kubeList)},
		"kubectl --context prod-eks get all,pvc,secret,configmap,ingress -n payments -o json": {Stdout: []byte(nsContents)},
	})
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, calls %q", findings, fk.calls)
	}
	f := findings[0]
	if f.Level != Destroy || f.Target != "context prod-eks" {
		t.Errorf("level %v target %q", f.Level, f.Target)
	}
	if len(f.Resources) != 1 || f.Resources[0].Name != "namespace/payments" || !f.Resources[0].Stateful {
		t.Errorf("resources = %+v", f.Resources)
	}
	if len(f.Notes) != 1 || f.Notes[0] != "namespace payments also takes down: 2 Pod, 1 PersistentVolumeClaim" {
		t.Errorf("notes = %q", f.Notes)
	}
}

func TestKubectlDeleteNothingMatches(t *testing.T) {
	findings, _ := analyze(t, `kubectl delete pods -l app=gone -n dev`, map[string]Result{
		"kubectl config current-context":                                 {Stdout: []byte("kind-dev\n")},
		"kubectl get pods -l app=gone -n dev --ignore-not-found -o json": {Stdout: []byte(`{"kind":"List","items":[]}`)},
	})
	if len(findings) != 0 {
		t.Errorf("findings = %+v", findings)
	}
}

func TestKubectlDryRunAndReadOnly(t *testing.T) {
	findings, fk := analyze(t, `kubectl delete pod x --dry-run=client; kubectl get pods; kubectl logs web`, nil)
	if len(findings) != 0 || len(fk.calls) != 0 {
		t.Errorf("findings = %+v, calls %q", findings, fk.calls)
	}
}

const diffOut = `diff -u -N /tmp/LIVE-1/apps.v1.Deployment.shop.web /tmp/MERGED-1/apps.v1.Deployment.shop.web
--- /tmp/LIVE-1/apps.v1.Deployment.shop.web
+++ /tmp/MERGED-1/apps.v1.Deployment.shop.web
@@ -6,7 +6,7 @@
-  replicas: 2
+  replicas: 3
diff -u -N /tmp/LIVE-1/v1.ConfigMap.shop.web.config /tmp/MERGED-1/v1.ConfigMap.shop.web.config
@@ -0,0 +1,5 @@
+kind: ConfigMap
diff -u -N /tmp/LIVE-1/v1.PersistentVolumeClaim.shop.data /tmp/MERGED-1/v1.PersistentVolumeClaim.shop.data
@@ -1,9 +0,0 @@
-kind: PersistentVolumeClaim
`

func TestKubectlApply(t *testing.T) {
	findings, _ := analyze(t, `kubectl apply -f k8s/ --prune -l app=shop --context kind-dev`, map[string]Result{
		"kubectl diff -f k8s/ --prune -l app=shop --context kind-dev": {ExitCode: 1, Stdout: []byte(diffOut)},
	})
	if len(findings) != 1 {
		t.Fatalf("findings = %+v", findings)
	}
	want := []Resource{
		{Action: ActUpdate, Name: "deployment/web (ns shop)"},
		{Action: ActCreate, Name: "configmap/web.config (ns shop)"},
		{Action: ActDestroy, Name: "persistentvolumeclaim/data (ns shop)", Stateful: true},
	}
	if f := findings[0]; f.Level != Destroy || !reflect.DeepEqual(f.Resources, want) {
		t.Errorf("level %v resources %+v", f.Level, f.Resources)
	}
}

func TestKubectlApplyUpToDate(t *testing.T) {
	findings, _ := analyze(t, `kubectl apply -f app.yaml --context c`, map[string]Result{
		"kubectl diff -f app.yaml --context c": {},
	})
	if len(findings) != 0 {
		t.Errorf("findings = %+v", findings)
	}
}

func TestClassifyOnly(t *testing.T) {
	tests := map[string]Level{
		`kubectl drain node-1 --ignore-daemonsets --context c`:    Destroy,
		`kubectl scale deploy/web --replicas=0 --context c`:       Destroy,
		`kubectl scale deploy/web --replicas 3 --context c`:       Change,
		`kubectl rollout restart deploy/web --context c`:          Change,
		`kubectl rollout status deploy/web --context c`:           None,
		`helm uninstall shop -n shop --kube-context c`:            Destroy,
		`helm upgrade --install shop ./chart --kube-context c`:    Change,
		`helm upgrade shop ./chart --dry-run --kube-context c`:    None,
		`terraform state rm aws_db_instance.main`:                 Destroy,
		`terraform workspace delete prod`:                         Destroy,
		`terraform fmt && terraform validate && terraform output`: None,
	}
	for script, want := range tests {
		findings, _ := analyze(t, script, nil)
		got := None
		if len(findings) > 0 {
			got = findings[0].Level
		}
		if got != want {
			t.Errorf("%s: level %v, want %v", script, got, want)
		}
	}
}
