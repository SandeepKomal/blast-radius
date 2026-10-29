package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandeepkomal/blast-radius/internal/analyze"
)

// isolate points the user config at an empty temp dir.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	p, err := UserConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultPolicy(t *testing.T) {
	cfg := Default()
	tests := []struct {
		level  analyze.Level
		labels []string
		want   Action
		env    string
	}{
		{analyze.Change, []string{"kind-dev", "/src/app"}, Pass, ""},
		{analyze.Destroy, []string{"kind-dev", "/src/app"}, Ask, ""},
		{analyze.Change, []string{"arn:aws:eks:us-east-1:1:cluster/PROD-main"}, Ask, "production"},
		{analyze.Destroy, []string{"default", "/infra/envs/production"}, Deny, "production"},
		{analyze.None, []string{"prod"}, Pass, "production"},
	}
	for _, tt := range tests {
		v := cfg.Decide(analyze.Finding{Level: tt.level, Labels: tt.labels})
		if v.Action != tt.want || v.Environment != tt.env {
			t.Errorf("%v %q: got %+v, want %s in %q", tt.level, tt.labels, v, tt.want, tt.env)
		}
	}
}

func TestGuardAlwaysAsks(t *testing.T) {
	cfg, err := Parse([]byte(`{"default": {"change": "pass", "destroy": "pass"}}`), Default())
	if err != nil {
		t.Fatal(err)
	}
	if v := cfg.Decide(analyze.Finding{Guard: true}); v.Action != Ask {
		t.Errorf("guard verdict = %+v", v)
	}
}

func TestProtect(t *testing.T) {
	cfg, err := Parse([]byte(`{"protect": ["aws_db_instance.*", "*/persistentvolumeclaim/*"]}`), Default())
	if err != nil {
		t.Fatal(err)
	}
	db := analyze.Finding{Level: analyze.Destroy, Labels: []string{"dev"}, Resources: []analyze.Resource{
		{Action: analyze.ActUpdate, Name: "aws_db_instance.main"},
		{Action: analyze.ActReplace, Name: "aws_db_instance.replica"},
	}}
	if v := cfg.Decide(db); v.Action != Deny || !strings.Contains(v.Reason, "aws_db_instance.replica") {
		t.Errorf("db verdict = %+v", v)
	}
	pvc := analyze.Finding{Level: analyze.Destroy, Resources: []analyze.Resource{
		{Action: analyze.ActDestroy, Name: "persistentvolumeclaim/data", Namespace: "shop"},
	}}
	if v := cfg.Decide(pvc); v.Action != Deny {
		t.Errorf("pvc verdict = %+v", v)
	}
	update := analyze.Finding{Level: analyze.Change, Resources: []analyze.Resource{{Action: analyze.ActUpdate, Name: "aws_db_instance.main"}}}
	if v := cfg.Decide(update); v.Action != Pass {
		t.Errorf("an update of a protected resource should pass: %+v", v)
	}
}

func TestParseFillsMissingRules(t *testing.T) {
	cfg, err := Parse([]byte(`{
		"default": {"destroy": "deny"},
		"environments": [{"name": "staging", "match": ["stg-*"], "change": "ask"}]
	}`), Default())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Default != (Rules{Change: Pass, Destroy: Deny}) {
		t.Errorf("default = %+v", cfg.Default)
	}
	if got := cfg.Environments[0].Rules; got != (Rules{Change: Ask, Destroy: Deny}) {
		t.Errorf("staging = %+v", got)
	}
	if cfg.Aliases["tf"] != "terraform" {
		t.Errorf("default aliases lost: %v", cfg.Aliases)
	}
}

func TestParseRejectsBadConfig(t *testing.T) {
	for _, bad := range []string{
		`{"default": {"destroy": "yolo"}}`,
		`{"default": {"destory": "pass"}}`, //nolint:misspell // a typo must be rejected
		`{"enviroments": []}`,              //nolint:misspell // a typo must be rejected
		`{"environments": [{"name": "x", "match": []}]}`,
		`{"preview_timeout_seconds": 9999}`,
		`not json`,
		`{"audit_log": "x"}` + "\n" + `"environments": [{"name": "qa", "match": ["qa"], "destroy": "deny"}]`,
		`{} {}`,
		`{}}`,
	} {
		if _, err := Parse([]byte(bad), Default()); err == nil {
			t.Errorf("%s: want error", bad)
		}
	}
}

func TestProjectConfigCanOnlyTighten(t *testing.T) {
	isolate(t)
	repo := t.TempDir()
	write(t, filepath.Join(repo, FileName), `{
		"default": {"change": "pass", "destroy": "pass"},
		"environments": [{"name": "anything", "match": ["*"], "destroy": "pass"}]
	}`)
	cfg, err := Load(filepath.Join(repo, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if v := cfg.Decide(analyze.Finding{Level: analyze.Destroy, Labels: []string{"prod"}}); v.Action != Deny {
		t.Errorf("project config loosened prod: %+v", v)
	}
	if v := cfg.Decide(analyze.Finding{Level: analyze.Destroy, Labels: []string{"dev"}}); v.Action != Ask {
		t.Errorf("project config loosened dev: %+v", v)
	}

	write(t, filepath.Join(repo, FileName), `{"environments": [{"name": "staging", "match": ["stg"], "destroy": "deny"}], "protect": ["aws_s3_bucket.*"]}`)
	cfg, err = Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if v := cfg.Decide(analyze.Finding{Level: analyze.Destroy, Labels: []string{"stg"}}); v.Action != Deny || v.Environment != "staging" {
		t.Errorf("project config failed to tighten: %+v", v)
	}
	if len(cfg.Protect) != 1 || len(cfg.Sources) != 1 {
		t.Errorf("protect %v sources %v", cfg.Protect, cfg.Sources)
	}
}

func TestUserConfigCanLoosen(t *testing.T) {
	p := isolate(t)
	write(t, p, `{"environments": [{"name": "sandbox", "match": ["kind-*"], "destroy": "pass"}], "audit_log": "/tmp/x.jsonl"}`)
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if v := cfg.Decide(analyze.Finding{Level: analyze.Destroy, Labels: []string{"kind-dev"}}); v.Action != Pass {
		t.Errorf("user sandbox rule ignored: %+v", v)
	}
	if cfg.AuditLog != "/tmp/x.jsonl" || cfg.Sources[0] != p {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestBrokenProjectConfigReportsError(t *testing.T) {
	isolate(t)
	repo := t.TempDir()
	write(t, filepath.Join(repo, FileName), `{"default": {"destroy": "pass",}}`)
	cfg, err := Load(repo)
	if err == nil {
		t.Fatal("want error")
	}
	if v := cfg.Decide(analyze.Finding{Level: analyze.Destroy}); v.Action != Ask {
		t.Errorf("fallback should keep the default rules: %+v", v)
	}
}

func TestStrictest(t *testing.T) {
	if got := Strictest([]Verdict{{Action: Ask}, {Action: Deny}, {Action: Pass}}); got != Deny {
		t.Errorf("got %s", got)
	}
	if got := Strictest(nil); got != Pass {
		t.Errorf("got %s", got)
	}
}
