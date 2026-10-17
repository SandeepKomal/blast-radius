package policy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sandeepkomal/blast-radius/internal/analyze"
)

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

func TestParseFillsMissingRules(t *testing.T) {
	cfg, err := Parse([]byte(`{
		"default": {"destroy": "deny"},
		"environments": [{"name": "staging", "match": ["stg-*"], "change": "ask"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Default != (Rules{Change: Pass, Destroy: Deny}) {
		t.Errorf("default = %+v", cfg.Default)
	}
	if got := cfg.Environments[0].Rules; got != (Rules{Change: Ask, Destroy: Deny}) {
		t.Errorf("staging = %+v", got)
	}
	if v := cfg.Decide(analyze.Finding{Level: analyze.Change, Labels: []string{"STG-eu"}}); v.Action != Ask {
		t.Errorf("verdict = %+v", v)
	}
}

func TestParseRejectsUnknownAction(t *testing.T) {
	if _, err := Parse([]byte(`{"default": {"destroy": "yolo"}}`)); err == nil {
		t.Error("want error")
	}
}

func TestLoadWalksUp(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(root, FileName), []byte(`{"default":{"change":"ask"}}`), 0o644)
	cfg, err := Load(sub)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Source != filepath.Join(root, FileName) || cfg.Default.Change != Ask {
		t.Errorf("cfg = %+v", cfg)
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
