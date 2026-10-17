package shell

import (
	"reflect"
	"testing"
)

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
	}
	for _, tt := range tests {
		var got [][]string
		for _, c := range Parse(tt.script) {
			got = append(got, c.Args)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Parse(%q)\n got %q\nwant %q", tt.script, got, tt.want)
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
	if cmds[0].Program() != "terraform" {
		t.Errorf("program = %q", cmds[0].Program())
	}
}
