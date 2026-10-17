package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandeepkomal/blast-radius/internal/analyze"
)

const destroyPlan = `{"resource_changes":[
 {"address":"aws_db_instance.main","type":"aws_db_instance","mode":"managed","change":{"actions":["delete"]}}]}`

// planRunner answers every terraform show with the given plan.
func planRunner(plan string) analyze.Runner {
	return func(_ context.Context, _ string, _ []string, _ string, args ...string) (analyze.Result, error) {
		for _, a := range args {
			if a == "show" {
				return analyze.Result{Stdout: []byte(plan)}, nil
			}
		}
		return analyze.Result{}, nil
	}
}

func runHook(t *testing.T, command, cwd string) (decision, reason string) {
	t.Helper()
	in, _ := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]string{"command": command},
		"cwd":             cwd,
	})
	var out, errOut bytes.Buffer
	if code := hookClaude(bytes.NewReader(in), &out, &errOut, planRunner(destroyPlan)); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if out.Len() == 0 {
		return "", ""
	}
	var o hookOutput
	if err := json.Unmarshal(out.Bytes(), &o); err != nil {
		t.Fatalf("bad output %q: %v", out.String(), err)
	}
	if o.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("hookEventName = %q", o.HookSpecificOutput.HookEventName)
	}
	return o.HookSpecificOutput.PermissionDecision, o.HookSpecificOutput.PermissionDecisionReason
}

func TestHook(t *testing.T) {
	dev := filepath.Join(t.TempDir(), "dev")
	prod := filepath.Join(t.TempDir(), "envs", "prod")
	os.MkdirAll(dev, 0o755)
	os.MkdirAll(prod, 0o755)

	if d, _ := runHook(t, "ls -la && git status", dev); d != "" {
		t.Errorf("harmless command: decision %q", d)
	}
	d, reason := runHook(t, "terraform destroy -auto-approve", dev)
	if d != "ask" || !strings.Contains(reason, "aws_db_instance.main") || !strings.Contains(reason, "(holds data)") {
		t.Errorf("dev destroy: %q\n%s", d, reason)
	}
	d, reason = runHook(t, "terraform destroy -auto-approve", prod)
	if d != "deny" || !strings.Contains(reason, "Do not retry") || !strings.Contains(reason, "environment production") {
		t.Errorf("prod destroy: %q\n%s", d, reason)
	}
}

func TestHookIgnoresOtherTools(t *testing.T) {
	var out bytes.Buffer
	in := `{"tool_name":"Write","tool_input":{"file_path":"x"}}`
	hookClaude(strings.NewReader(in), &out, &bytes.Buffer{}, planRunner(destroyPlan))
	if out.Len() != 0 {
		t.Errorf("output %q", out.String())
	}
}

func TestCheckExitCodes(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if code := check([]string{"-C", dir, "terraform", "destroy"}, &out, &bytes.Buffer{}, planRunner(destroyPlan)); code != exitAsk {
		t.Errorf("exit %d, output %s", code, out.String())
	}
	out.Reset()
	if code := check([]string{"-C", dir, "--json", "kubectl get pods"}, &out, &bytes.Buffer{}, planRunner(destroyPlan)); code != exitPass {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(out.String(), `"action": "pass"`) {
		t.Errorf("json = %s", out.String())
	}
}

func TestUpdateSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(`{
  "model": "opus",
  "hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "other-hook"}]}]}
}`), 0o644)

	if changed, err := updateSettings(path, "blast-radius hook claude", true); err != nil || !changed {
		t.Fatalf("install: changed=%v err=%v", changed, err)
	}
	if changed, _ := updateSettings(path, "blast-radius hook claude", true); changed {
		t.Error("second install changed the file")
	}
	b, _ := os.ReadFile(path)
	if s := string(b); !strings.Contains(s, "other-hook") || !strings.Contains(s, `"model": "opus"`) || strings.Count(s, "blast-radius") != 1 {
		t.Errorf("after install:\n%s", s)
	}

	if changed, err := updateSettings(path, "", false); err != nil || !changed {
		t.Fatalf("uninstall: changed=%v err=%v", changed, err)
	}
	b, _ = os.ReadFile(path)
	if s := string(b); strings.Contains(s, "blast-radius") || !strings.Contains(s, "other-hook") {
		t.Errorf("after uninstall:\n%s", s)
	}
}
