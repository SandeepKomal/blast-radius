package shell

import (
	"reflect"
	"testing"
)

func args(cmds []Command) [][]string {
	var out [][]string
	for _, c := range cmds {
		out = append(out, c.Args)
	}
	return out
}

func TestParse(t *testing.T) {
	tests := []struct {
		script string
		want   [][]string
	}{
		{`terraform destroy -auto-approve`, [][]string{{"terraform", "destroy", "-auto-approve"}}},
		{`cd infra && terraform apply; echo done`, [][]string{{"cd", "infra"}, {"terraform", "apply"}, {"echo", "done"}}},
		{`kubectl get pods | grep web || true`, [][]string{{"kubectl", "get", "pods"}, {"grep", "web"}, {"true"}}},
		{`sudo -u root kubectl delete ns prod`, [][]string{{"kubectl", "delete", "ns", "prod"}}},
		{`bash -lc "cd x && terraform destroy"`, [][]string{{"cd", "x"}, {"terraform", "destroy"}}},
		{`echo $(kubectl delete pod web)`, [][]string{{"kubectl", "delete", "pod", "web"}, {"echo", "$(kubectl delete pod web)"}}},
		{"echo `terraform destroy`", [][]string{{"terraform", "destroy"}, {"echo", "`terraform destroy`"}}},
		{`terraform apply -var 'name=a b' 2>&1 > out.log`, [][]string{{"terraform", "apply", "-var", "name=a b"}}},
		{`timeout 30s terraform plan # comment`, [][]string{{"terraform", "plan"}}},
		{`if terraform plan; then terraform apply; fi`, [][]string{{"terraform", "plan"}, {"terraform", "apply"}}},
		{"terraform \\\n  destroy", [][]string{{"terraform", "destroy"}}},
		{`echo "unterminated`, [][]string{{"echo", "unterminated"}}},
		{`eval "terraform destroy -auto-approve"`, [][]string{{"terraform", "destroy", "-auto-approve"}}},
		{`pwsh -NoProfile -Command terraform destroy`, [][]string{{"terraform", "destroy"}}},
		{`(cd infra; terraform destroy)`, [][]string{{"cd", "infra"}, {"terraform", "destroy"}}},
		// Heredoc bodies are data unless a shell runs them.
		{"cat > notes.md <<'EOF'\nterraform destroy\nEOF\necho done", [][]string{{"cat"}, {"echo", "done"}}},
		{"cat <<-EOF\n\tterraform destroy\n\tEOF\nterraform apply", [][]string{{"cat"}, {"terraform", "apply"}}},
		{"kubectl apply -f - <<EOF\nkind: Pod\nEOF", [][]string{{"kubectl", "apply", "-f", "-"}}},
		{"bash <<EOF\nterraform destroy\nEOF", [][]string{{"bash"}, {"terraform", "destroy"}}},
		{"cat <<EOF | sh\nkubectl delete ns x\nEOF", [][]string{{"cat"}, {"sh"}, {"kubectl", "delete", "ns", "x"}}},
		{"cat <<EOF\nnever closed\nterraform destroy", [][]string{{"cat"}}},
		{`cat <<< "terraform destroy"`, [][]string{{"cat"}}},
	}
	for _, tt := range tests {
		if got := args(Parse(tt.script)); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Parse(%q)\n got %q\nwant %q", tt.script, got, tt.want)
		}
	}
}

func TestParsePowerShell(t *testing.T) {
	tests := []struct {
		script string
		want   [][]string
	}{
		{`& "C:\Program Files\Terraform\terraform.exe" destroy`, [][]string{{`C:\Program Files\Terraform\terraform.exe`, "destroy"}}},
		{`cd C:\infra; terraform apply`, [][]string{{"cd", `C:\infra`}, {"terraform", "apply"}}},
		{"kubectl delete ns `\"prod`\"", [][]string{{"kubectl", "delete", "ns", `"prod"`}}},
		{`$env:TF_WORKSPACE = 'prod'; terraform destroy`, [][]string{{"$env:TF_WORKSPACE", "=", "prod"}, {"terraform", "destroy"}}},
		{`Invoke-Expression "terraform destroy"`, [][]string{{"terraform", "destroy"}}},
		{`iex 'kubectl delete ns x'`, [][]string{{"kubectl", "delete", "ns", "x"}}},
		{`cmd /c terraform destroy`, [][]string{{"terraform", "destroy"}}},
		{`cmd.exe /d /c "cd C:\infra && terraform destroy"`, [][]string{{"cd", `C:\infra`}, {"terraform", "destroy"}}},
		{`cmd /?`, [][]string{{"cmd", "/?"}}},
	}
	for _, tt := range tests {
		if got := args(ParsePowerShell(tt.script)); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParsePowerShell(%q)\n got %q\nwant %q", tt.script, got, tt.want)
		}
	}
}

func TestParseEnv(t *testing.T) {
	cmds := Parse(`TF_WORKSPACE=prod env AWS_PROFILE=x terraform destroy`)
	if len(cmds) != 1 {
		t.Fatalf("got %d commands", len(cmds))
	}
	if want := []string{"TF_WORKSPACE=prod", "AWS_PROFILE=x"}; !reflect.DeepEqual(cmds[0].Env, want) {
		t.Errorf("env = %q, want %q", cmds[0].Env, want)
	}
}

func TestRedirects(t *testing.T) {
	cmds := Parse(`jq '.hooks = {}' s.json > ~/.claude/settings.json 2>/dev/null; > .blast-radius.json`)
	if len(cmds) != 2 {
		t.Fatalf("got %d commands: %+v", len(cmds), cmds)
	}
	if want := []string{"~/.claude/settings.json", "/dev/null"}; !reflect.DeepEqual(cmds[0].Redirects, want) {
		t.Errorf("redirects = %q, want %q", cmds[0].Redirects, want)
	}
	if want := []string{".blast-radius.json"}; !reflect.DeepEqual(cmds[1].Redirects, want) {
		t.Errorf("bare redirect = %+v", cmds[1])
	}
}

func TestProgram(t *testing.T) {
	for in, want := range map[string]string{
		"terraform":                 "terraform",
		"/usr/local/bin/tofu":       "tofu",
		`C:\tools\Terraform.exe`:    "terraform",
		"./bin/kubectl":             "kubectl",
		`C:/Program Files/helm.EXE`: "helm",
	} {
		if got := (Command{Args: []string{in}}).Program(); got != want {
			t.Errorf("Program(%q) = %q, want %q", in, got, want)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{
		`terraform destroy`, `a && b || c; d | e &`, `echo "$(x "$(y)")"`, "`a`", `'`, `"`, `$(`, `\`,
		`2>&1 >`, `bash -c`, `eval`, `pwsh -Command`, "cd x\nkubectl delete ns y",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		Parse(s)
		ParsePowerShell(s)
	})
}
