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
	envs    [][]string
}

func (f *fake) run(_ context.Context, _ string, env []string, name string, args ...string) (Result, error) {
	parts := []string{name}
	for _, a := range args {
		if strings.HasSuffix(a, ".tfplan") {
			a = a[:strings.LastIndexAny(a, "=")+1] + "PLAN"
		}
		parts = append(parts, a)
	}
	key := strings.Join(parts, " ")
	f.calls = append(f.calls, key)
	f.envs = append(f.envs, env)
	if r, ok := f.answers[key]; ok {
		return r, nil
	}
	return Result{ExitCode: 1, Stderr: []byte("unexpected command: " + key)}, nil
}

func analyzeIn(t *testing.T, dir, script string, answers map[string]Result) ([]Finding, *fake) {
	t.Helper()
	f := &fake{answers: answers}
	a := &Analyzer{Run: f.run, Aliases: map[string]string{"tf": "terraform", "k": "kubectl"}}
	return a.Analyze(context.Background(), script, dir, Bash), f
}

func analyze(t *testing.T, script string, answers map[string]Result) ([]Finding, *fake) {
	t.Helper()
	return analyzeIn(t, t.TempDir(), script, answers)
}

func only(t *testing.T, findings []Finding, calls []string) Finding {
	t.Helper()
	if len(findings) != 1 {
		t.Fatalf("got %d findings %+v, calls %q", len(findings), findings, calls)
	}
	return findings[0]
}

const planJSON = `{"resource_changes":[
 {"address":"aws_db_instance.main","type":"aws_db_instance","mode":"managed","change":{"actions":["delete"]}},
 {"address":"aws_launch_template.web","type":"aws_launch_template","mode":"managed","change":{"actions":["create","delete"]}},
 {"address":"aws_instance.web","type":"aws_instance","mode":"managed","change":{"actions":["update"]}},
 {"address":"data.aws_ami.ubuntu","type":"aws_ami","mode":"data","change":{"actions":["read"]}},
 {"address":"aws_s3_bucket.logs","type":"aws_s3_bucket","mode":"managed","change":{"actions":["no-op"]}}
]}`

var planAnswers = map[string]Result{
	"terraform plan -input=false -lock=false -no-color -out=PLAN":          {},
	"terraform plan -input=false -lock=false -no-color -out=PLAN -destroy": {},
	"terraform show -json -no-color PLAN":                                  {Stdout: []byte(planJSON)},
}

func TestTerraformApply(t *testing.T) {
	findings, fk := analyze(t, `cd infra && terraform apply -auto-approve -var-file prod.tfvars -target=module.db`, map[string]Result{
		"terraform plan -input=false -lock=false -no-color -out=PLAN -var-file prod.tfvars -target=module.db": {},
		"terraform show -json -no-color PLAN": {Stdout: []byte(planJSON)},
	})
	f := only(t, findings, fk.calls)
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
	if f.Target != "workspace default" || filepath.Base(f.Labels[1]) != "infra" {
		t.Errorf("target %q labels %q", f.Target, f.Labels)
	}
}

func TestTerraformDestroyUsesWorkspaceFileAndChdir(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "envs/prod/.terraform"), 0o755)
	os.WriteFile(filepath.Join(dir, "envs/prod/.terraform/environment"), []byte("prod\n"), 0o644)
	findings, fk := analyzeIn(t, dir, `tofu -chdir=envs/prod destroy`, map[string]Result{
		"tofu -chdir=envs/prod plan -input=false -lock=false -no-color -out=PLAN -destroy": {},
		"tofu -chdir=envs/prod show -json -no-color PLAN":                                  {Stdout: []byte(planJSON)},
	})
	if f := only(t, findings, fk.calls); f.Target != "workspace prod" || f.Tool != "tofu" {
		t.Errorf("finding = %+v", f)
	}
}

func TestWorkspaceSelectedEarlierInScript(t *testing.T) {
	for _, script := range []string{
		`terraform workspace select prod && terraform destroy -auto-approve`,
		`terraform workspace select -or-create prod; terraform destroy`,
		`terraform workspace new prod && terraform apply`,
		`export TF_WORKSPACE=prod && terraform destroy`,
		`TF_WORKSPACE=prod terraform destroy`,
		`tf workspace select prod && tf destroy`,
	} {
		findings, fk := analyze(t, script, planAnswers)
		if f := only(t, findings, fk.calls); f.Target != "workspace prod" {
			t.Errorf("%s: target %q", script, f.Target)
		}
	}
}

func TestExportedEnvReachesPreview(t *testing.T) {
	_, fk := analyze(t, `export AWS_PROFILE=prod-admin; terraform destroy`, planAnswers)
	if len(fk.envs) == 0 || !reflect.DeepEqual(fk.envs[0], []string{"AWS_PROFILE=prod-admin"}) {
		t.Errorf("preview env = %q", fk.envs)
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
	findings, fk := analyze(t, `terraform apply`, map[string]Result{
		"terraform plan -input=false -lock=false -no-color -out=PLAN": {ExitCode: 1, Stderr: []byte("\n│ Error: No valid credential sources found\n")},
	})
	f := only(t, findings, fk.calls)
	if f.Level != Destroy || !strings.Contains(f.Notes[0], "No valid credential sources found") {
		t.Errorf("finding = %+v", f)
	}
}

func TestPreviewTimeout(t *testing.T) {
	a := &Analyzer{Run: func(ctx context.Context, _ string, _ []string, _ string, _ ...string) (Result, error) {
		return Result{}, context.DeadlineExceeded
	}}
	findings := a.Analyze(context.Background(), "terraform destroy", t.TempDir(), Bash)
	if len(findings) != 1 || findings[0].Level != Destroy || !strings.Contains(findings[0].Notes[0], "timed out") {
		t.Errorf("findings = %+v", findings)
	}
}

func TestTerraformSavedPlanAndReadOnly(t *testing.T) {
	findings, fk := analyze(t, `terraform plan -out=x && terraform show && terraform apply x`, map[string]Result{
		"terraform show -json -no-color x": {Stdout: []byte(planJSON)},
	})
	only(t, findings, fk.calls)
	if len(fk.calls) != 1 {
		t.Errorf("calls = %q", fk.calls)
	}
}

func TestWindowsPathsAndEval(t *testing.T) {
	for _, script := range []string{
		`'C:\tools\terraform.exe' destroy`,
		`/opt/homebrew/bin/terraform destroy`,
		`eval "terraform destroy"`,
	} {
		findings, fk := analyze(t, script, map[string]Result{
			`C:\tools\terraform.exe plan -input=false -lock=false -no-color -out=PLAN -destroy`: {},
			`C:\tools\terraform.exe show -json -no-color PLAN`:                                  {Stdout: []byte(planJSON)},
		})
		if f := only(t, findings, fk.calls); f.Level != Destroy {
			t.Errorf("%s: %+v", script, f)
		}
	}
}

func TestPowerShell(t *testing.T) {
	fk := &fake{answers: planAnswers}
	a := &Analyzer{Run: fk.run}
	findings := a.Analyze(context.Background(), `$env:TF_WORKSPACE = "prod"; terraform destroy`, t.TempDir(), PowerShell)
	if f := only(t, findings, fk.calls); f.Target != "workspace prod" {
		t.Errorf("target %q", f.Target)
	}
}

func TestTerragrunt(t *testing.T) {
	findings, fk := analyze(t, `terragrunt --working-dir live/prod destroy`, map[string]Result{
		"terragrunt --working-dir live/prod plan -input=false -lock=false -no-color -out=PLAN -destroy": {},
		"terragrunt --working-dir live/prod show -json -no-color PLAN":                                  {Stdout: []byte("INFO log line\n" + planJSON)},
	})
	if f := only(t, findings, fk.calls); f.Level != Destroy || len(f.Resources) != 3 {
		t.Errorf("finding = %+v", f)
	}
	findings, fk = analyze(t, `terragrunt run-all destroy`, nil)
	if f := only(t, findings, fk.calls); f.Level != Destroy || len(fk.calls) != 0 {
		t.Errorf("run-all finding = %+v calls %q", f, fk.calls)
	}
}

const kubeList = `{"kind":"List","items":[{"kind":"Namespace","metadata":{"name":"payments"}}]}`

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
	f := only(t, findings, fk.calls)
	if f.Level != Destroy || f.Target != "context prod-eks" {
		t.Errorf("level %v target %q", f.Level, f.Target)
	}
	if want := []Resource{{Action: ActDestroy, Name: "namespace/payments", Stateful: true}}; !reflect.DeepEqual(f.Resources, want) {
		t.Errorf("resources = %+v", f.Resources)
	}
	if len(f.Notes) != 1 || f.Notes[0] != "namespace payments also takes down: 2 Pod, 1 PersistentVolumeClaim" {
		t.Errorf("notes = %q", f.Notes)
	}
}

func TestKubectlContextSwitchedEarlierInScript(t *testing.T) {
	for _, script := range []string{
		`kubectl config use-context prod-eks && kubectl delete pod web`,
		`kubectx prod-eks; k delete pod web`,
	} {
		findings, fk := analyze(t, script, map[string]Result{
			"kubectl get pod web --ignore-not-found -o json": {Stdout: []byte(`{"kind":"Pod","metadata":{"name":"web","namespace":"default"}}`)},
		})
		if f := only(t, findings, fk.calls); f.Target != "context prod-eks" {
			t.Errorf("%s: target %q", script, f.Target)
		}
	}
}

func TestKubectlNamespaceLookupsAreBounded(t *testing.T) {
	var items []string
	answers := map[string]Result{}
	for i := 0; i < 8; i++ {
		ns := "ns" + string(rune('a'+i))
		items = append(items, `{"kind":"Namespace","metadata":{"name":"`+ns+`"}}`)
		answers["kubectl get all,pvc,secret,configmap,ingress -n "+ns+" -o json"] = Result{Stdout: []byte(`{"kind":"List","items":[]}`)}
	}
	answers["kubectl get ns --ignore-not-found -o json"] = Result{Stdout: []byte(`{"kind":"List","items":[` + strings.Join(items, ",") + `]}`)}
	answers["kubectl config current-context"] = Result{Stdout: []byte("dev\n")}
	findings, fk := analyze(t, `kubectl delete ns --all`, answers)
	f := only(t, findings, fk.calls)
	if len(f.Resources) != 8 || len(fk.calls) != 2+maxNamespaceLookups {
		t.Errorf("resources %d, calls %d", len(f.Resources), len(fk.calls))
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
	findings, fk := analyze(t, `kubectl apply -f k8s/ --prune -l app=shop --context kind-dev`, map[string]Result{
		"kubectl diff -f k8s/ --prune -l app=shop --context kind-dev": {ExitCode: 1, Stdout: []byte(diffOut)},
	})
	f := only(t, findings, fk.calls)
	want := []Resource{
		{Action: ActUpdate, Name: "deployment/web", Namespace: "shop"},
		{Action: ActCreate, Name: "configmap/web.config", Namespace: "shop"},
		{Action: ActDestroy, Name: "persistentvolumeclaim/data", Namespace: "shop", Stateful: true},
	}
	if f.Level != Destroy || !reflect.DeepEqual(f.Resources, want) {
		t.Errorf("level %v resources %+v", f.Level, f.Resources)
	}
	if env := fk.envs[0]; env[len(env)-1] != "KUBECTL_EXTERNAL_DIFF=diff -u -N" {
		t.Errorf("diff env = %q", env)
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

func TestExtraStatefulTypes(t *testing.T) {
	fk := &fake{answers: map[string]Result{
		"terraform plan -input=false -lock=false -no-color -out=PLAN -destroy": {},
		"terraform show -json -no-color PLAN":                                  {Stdout: []byte(`{"resource_changes":[{"address":"acme_db.x","type":"acme_db","mode":"managed","change":{"actions":["delete"]}}]}`)},
	}}
	a := &Analyzer{Run: fk.run, Stateful: []string{"acme_db"}}
	findings := a.Analyze(context.Background(), "terraform destroy", t.TempDir(), Bash)
	if !findings[0].Resources[0].Stateful {
		t.Errorf("resource = %+v", findings[0].Resources[0])
	}
}

func TestClassifyOnly(t *testing.T) {
	tests := map[string]Level{
		`kubectl drain node-1 --ignore-daemonsets --context c`:                                          Destroy,
		`kubectl scale deploy/web --replicas=0 --context c`:                                             Destroy,
		`kubectl scale deploy/web --replicas 3 --context c`:                                             Change,
		`kubectl rollout restart deploy/web --context c`:                                                Change,
		`kubectl rollout status deploy/web --context c`:                                                 None,
		`helm uninstall shop -n shop --kube-context c`:                                                  Destroy,
		`helm upgrade --install shop ./chart --kube-context c`:                                          Change,
		`helm upgrade shop ./chart --dry-run --kube-context c`:                                          None,
		`helm list --kube-context c`:                                                                    None,
		`terraform state rm aws_db_instance.main`:                                                       Destroy,
		`terraform workspace delete prod`:                                                               Destroy,
		`terraform fmt && terraform validate && terraform output`:                                       None,
		`pulumi destroy --yes -s prod`:                                                                  Destroy,
		`pulumi up -s dev`:                                                                              Change,
		`pulumi preview -s prod`:                                                                        None,
		`aws s3 rm s3://bucket --recursive`:                                                             Destroy,
		`aws s3 sync . s3://bucket --delete`:                                                            Destroy,
		`aws s3 cp s3://bucket/x .`:                                                                     None,
		`aws s3 cp x s3://bucket/x`:                                                                     Change,
		`aws --profile prod rds delete-db-instance --db-instance-identifier main --skip-final-snapshot`: Destroy,
		`aws ec2 terminate-instances --instance-ids i-1`:                                                Destroy,
		`aws ec2 terminate-instances --instance-ids i-1 --dry-run`:                                      None,
		`aws ec2 describe-instances`:                                                                    None,
		`aws lambda update-function-code --function-name f --zip-file fileb://f.zip`:                    Change,
		`gcloud compute instances delete vm-1 --zone us-east1-b`:                                        Destroy,
		`gcloud sql instances delete main --project prod`:                                               Destroy,
		`gcloud compute instances list`:                                                                 None,
		`gcloud run deploy api --image x`:                                                               Change,
		`gsutil -m rm -r gs://bucket`:                                                                   Destroy,
		`gsutil ls gs://bucket`:                                                                         None,
		`az group delete -n prod-rg --yes`:                                                              Destroy,
		`az vm list`:                                                                                    None,
		`az webapp restart -n api -g rg`:                                                                Change,
	}
	for script, want := range tests {
		findings, _ := analyze(t, script, map[string]Result{"kubectl config current-context": {Stdout: []byte("c\n")}})
		got := None
		if len(findings) > 0 {
			got = findings[0].Level
		}
		if got != want {
			t.Errorf("%s: level %v, want %v", script, got, want)
		}
	}
}

func TestCloudStatefulAndTargets(t *testing.T) {
	findings, _ := analyze(t, `AWS_PROFILE=prod aws rds delete-db-instance --db-instance-identifier main`, nil)
	f := findings[0]
	if !f.Resources[0].Stateful || f.Target != "profile prod" || f.Labels[0] != "prod" {
		t.Errorf("aws finding = %+v", f)
	}
	findings, _ = analyze(t, `az group delete -n prod-rg -g prod-rg --yes`, nil)
	if f := findings[0]; !f.Resources[0].Stateful || !strings.Contains(strings.Join(f.Notes, " "), "resource group") {
		t.Errorf("az finding = %+v", f)
	}
}

func TestGuard(t *testing.T) {
	guarded := []string{
		`blast-radius uninstall`,
		`rm .blast-radius.json`,
		`echo '{}' > ~/.claude/settings.json`,
		`jq 'del(.hooks)' ~/.claude/settings.json > /tmp/s && mv /tmp/s ~/.claude/settings.json`,
		`sed -i 's/blast-radius//' .claude/settings.local.json`,
		`cp permissive.json ~/.config/blast-radius/config.json`,
		`Remove-Item .blast-radius.json`,
	}
	for _, script := range guarded {
		findings, _ := analyze(t, script, nil)
		if len(findings) == 0 || !findings[len(findings)-1].Guard {
			t.Errorf("%s: not guarded: %+v", script, findings)
		}
	}
	allowed := []string{
		`cat ~/.claude/settings.json`,
		`jq .hooks ~/.claude/settings.json`,
		`git diff .blast-radius.json`,
		`blast-radius check terraform destroy`,
		`blast-radius install`,
	}
	for _, script := range allowed {
		if findings, _ := analyze(t, script, nil); len(findings) != 0 {
			t.Errorf("%s: unexpectedly flagged: %+v", script, findings)
		}
	}
	a := &Analyzer{}
	if len(a.AnalyzeFileEdit("/home/me/.claude/settings.json")) != 1 || len(a.AnalyzeFileEdit("/home/me/app/main.go")) != 0 {
		t.Error("AnalyzeFileEdit mismatch")
	}
}
