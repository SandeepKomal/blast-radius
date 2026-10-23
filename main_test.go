package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sandeepkomal/blast-radius/internal/analyze"
	"github.com/sandeepkomal/blast-radius/internal/policy"
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

// isolate gives the test its own home directory, so no real Claude Code
// settings or blast-radius config are read or written.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	return home
}

func hookJSON(tool, field, value, cwd string) string {
	b, _ := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       tool,
		"tool_input":      map[string]string{field: value},
		"cwd":             cwd,
	})
	return string(b)
}

func runHook(t *testing.T, input string, run analyze.Runner) (decision, reason string) {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := hookClaude(strings.NewReader(input), &out, &errOut, run); code != 0 {
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
	isolate(t)
	dev := filepath.Join(t.TempDir(), "dev")
	prod := filepath.Join(t.TempDir(), "envs", "prod")
	os.MkdirAll(dev, 0o755)
	os.MkdirAll(prod, 0o755)
	run := planRunner(destroyPlan)

	if d, _ := runHook(t, hookJSON("Bash", "command", "ls -la && git status", dev), run); d != "" {
		t.Errorf("harmless command: decision %q", d)
	}
	d, reason := runHook(t, hookJSON("Bash", "command", "terraform destroy -auto-approve", dev), run)
	if d != "ask" || !strings.Contains(reason, "aws_db_instance.main") || !strings.Contains(reason, "(holds data)") ||
		!strings.HasPrefix(reason, "WARNING: this deletes ") {
		t.Errorf("dev destroy: %q\n%s", d, reason)
	}
	d, reason = runHook(t, hookJSON("Bash", "command", "terraform destroy -auto-approve", prod), run)
	if d != "deny" || !strings.Contains(reason, "Do not retry") || !strings.Contains(reason, "environment production") {
		t.Errorf("prod destroy: %q\n%s", d, reason)
	}
	d, _ = runHook(t, hookJSON("PowerShell", "command", `$env:TF_WORKSPACE = "prod"; terraform destroy`, dev), run)
	if d != "deny" {
		t.Errorf("powershell prod destroy: %q", d)
	}
}

func TestHookGuardsItsOwnSettings(t *testing.T) {
	home := isolate(t)
	run := planRunner(destroyPlan)
	for _, input := range []string{
		hookJSON("Edit", "file_path", filepath.Join(home, ".claude", "settings.json"), home),
		hookJSON("Write", "file_path", filepath.Join(home, "repo", ".blast-radius.json"), home),
		hookJSON("Bash", "command", "blast-radius uninstall", home),
	} {
		if d, _ := runHook(t, input, run); d != "ask" {
			t.Errorf("%s: decision %q, want ask", input, d)
		}
	}
	if d, _ := runHook(t, hookJSON("Edit", "file_path", filepath.Join(home, "main.go"), home), run); d != "" {
		t.Errorf("ordinary edit: decision %q", d)
	}
}

func TestHookFailsClosed(t *testing.T) {
	isolate(t)
	if d, _ := runHook(t, "this is not json", planRunner(destroyPlan)); d != "ask" {
		t.Errorf("unreadable input: decision %q", d)
	}
	panicky := func(context.Context, string, []string, string, ...string) (analyze.Result, error) {
		panic("boom")
	}
	d, reason := runHook(t, hookJSON("Bash", "command", "terraform destroy", t.TempDir()), panicky)
	if d != "ask" || !strings.Contains(reason, "internal error (boom)") {
		t.Errorf("panic: %q %q", d, reason)
	}

	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, policy.FileName), []byte(`{"default": {"destory": "pass"}}`), 0o644) //nolint:misspell // a typo must be rejected
	empty := `{"resource_changes":[{"address":"a.b","type":"a","mode":"managed","change":{"actions":["update"]}}]}`
	d, reason = runHook(t, hookJSON("Bash", "command", "terraform apply", repo), planRunner(empty))
	if d != "ask" || !strings.Contains(reason, "config error") {
		t.Errorf("broken config: %q %q", d, reason)
	}
}

func TestHookIgnoresOtherTools(t *testing.T) {
	isolate(t)
	if d, _ := runHook(t, `{"tool_name":"Read","tool_input":{"file_path":"x"}}`, planRunner(destroyPlan)); d != "" {
		t.Errorf("decision %q", d)
	}
}

func TestAuditLog(t *testing.T) {
	home := isolate(t)
	logPath := filepath.Join(home, "audit", "log.jsonl")
	cfgPath, _ := policy.UserConfigPath()
	os.MkdirAll(filepath.Dir(cfgPath), 0o755)
	os.WriteFile(cfgPath, []byte(`{"audit_log": "`+filepath.ToSlash(logPath)+`"}`), 0o644)

	runHook(t, hookJSON("Bash", "command", "terraform destroy", t.TempDir()), planRunner(destroyPlan))
	runHook(t, hookJSON("Bash", "command", "ls", t.TempDir()), planRunner(destroyPlan))
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d:\n%s", len(lines), b)
	}
	var e auditEntry
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatal(err)
	}
	if e.Action != policy.Ask || e.Command != "terraform destroy" || e.Findings[0].Destroyed[0] != "aws_db_instance.main" {
		t.Errorf("entry = %+v", e)
	}
}

func TestCheckExitCodes(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	var out bytes.Buffer
	if code := check([]string{"-C", dir, "terraform", "destroy"}, &out, io.Discard, planRunner(destroyPlan)); code != exitAsk {
		t.Errorf("exit %d, output %s", code, out.String())
	}
	out.Reset()
	if code := check([]string{"-C", dir, "--json", "kubectl get pods"}, &out, io.Discard, planRunner(destroyPlan)); code != exitPass {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(out.String(), `"action": "pass"`) {
		t.Errorf("json = %s", out.String())
	}
	if code := check([]string{"--shell", "fish", "ls"}, io.Discard, io.Discard, planRunner(destroyPlan)); code != 2 {
		t.Errorf("bad shell: exit %d", code)
	}
}

func readSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestUpdateSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	// An older blast-radius entry next to someone else's hook.
	os.WriteFile(path, []byte(`{
  "model": "opus",
  "hooks": {"PreToolUse": [
    {"matcher": "Bash", "hooks": [{"type": "command", "command": "other-hook"}]},
    {"matcher": "Bash", "hooks": [{"type": "command", "command": "blast-radius hook claude", "timeout": 120}]}
  ]}
}`), 0o600)

	entry := hookEntries("/usr/local/bin/blast-radius")
	if status, err := updateSettings(path, entry); err != nil || status != "updated" {
		t.Fatalf("upgrade: %q %v", status, err)
	}
	if status, _ := updateSettings(path, entry); status != "unchanged" {
		t.Errorf("second install: %q", status)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	if !strings.Contains(s, "other-hook") || !strings.Contains(s, `"model": "opus"`) || strings.Count(s, "blast-radius") != 1+len(recordedEvents) {
		t.Errorf("after install:\n%s", s)
	}
	// Only the check fails closed; the recording hooks must never block.
	if strings.Count(s, `"onFailure"`) != 1 {
		t.Errorf("onFailure on a recording hook:\n%s", s)
	}
	for event := range recordedEvents {
		if !strings.Contains(s, `"`+event+`"`) {
			t.Errorf("no %s hook in:\n%s", event, s)
		}
	}
	for _, want := range []string{`"onFailure": "block"`, `"matcher": "` + hookMatcher + `"`, `"hook"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in:\n%s", want, s)
		}
	}
	if st, _ := os.Stat(path); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("file mode changed to %v", st.Mode().Perm())
	}

	if status, err := updateSettings(path, nil); err != nil || status != "removed" {
		t.Fatalf("uninstall: %q %v", status, err)
	}
	m := readSettings(t, path)
	if b, _ := json.Marshal(m); strings.Contains(string(b), "blast-radius") || !strings.Contains(string(b), "other-hook") {
		t.Errorf("after uninstall: %s", b)
	}
	if status, _ := updateSettings(path, nil); status != "unchanged" {
		t.Errorf("second uninstall: %q", status)
	}
}

func TestUpdateSettingsRejectsBrokenJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(path, []byte(`{"hooks": `), 0o644)
	if _, err := updateSettings(path, hookEntries("x")); err == nil {
		t.Error("want error")
	}
	if b, _ := os.ReadFile(path); string(b) != `{"hooks": ` {
		t.Errorf("broken file was overwritten: %q", b)
	}
}

func TestDoctor(t *testing.T) {
	home := isolate(t)
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	if code := doctor(&out, planRunner("")); code != 1 || !strings.Contains(out.String(), "no blast-radius hook") {
		t.Errorf("doctor without hook: %d\n%s", code, out.String())
	}

	exe := filepath.Join(home, "bin", "blast-radius")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755)
	updateSettings(filepath.Join(home, ".claude", "settings.json"), hookEntries(exe))
	out.Reset()
	if code := doctor(&out, planRunner("")); code != 0 {
		t.Errorf("doctor with hook: %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "self-test: hook decisions are correct") {
		t.Errorf("output:\n%s", out.String())
	}

	updateSettings(filepath.Join(home, ".claude", "settings.json"), hookEntries(filepath.Join(home, "gone", "blast-radius")))
	out.Reset()
	if code := doctor(&out, planRunner("")); code != 1 || !strings.Contains(out.String(), "does not exist") {
		t.Errorf("doctor with missing binary: %d\n%s", code, out.String())
	}
}

func TestInit(t *testing.T) {
	isolate(t)
	t.Chdir(t.TempDir())
	if code := initConfig(nil, io.Discard, io.Discard); code != 0 {
		t.Fatalf("init: %d", code)
	}
	b, _ := os.ReadFile(policy.FileName)
	if _, err := policy.Parse(b, policy.Default()); err != nil {
		t.Errorf("template does not parse: %v", err)
	}
	if code := initConfig(nil, io.Discard, io.Discard); code != 1 {
		t.Error("init overwrote an existing file")
	}
	if code := initConfig([]string{"--user"}, io.Discard, io.Discard); code != 0 {
		t.Fatal("init --user failed")
	}
	p, _ := policy.UserConfigPath()
	b, _ = os.ReadFile(p)
	if _, err := policy.Parse(b, policy.Default()); err != nil {
		t.Errorf("user template does not parse: %v", err)
	}
}

func TestParseVersion(t *testing.T) {
	cases := map[string][3]int{
		"2.1.302 (Claude Code)\n": {2, 1, 302},
		"claude v2.1.295":         {2, 1, 295},
		"1.0.0-beta.1":            {1, 0, 0},
	}
	for in, want := range cases {
		if got, ok := parseVersion(in); !ok || got != want {
			t.Errorf("parseVersion(%q) = %v, %v", in, got, ok)
		}
	}
	if _, ok := parseVersion("unknown"); ok {
		t.Error("parsed a version from text without one")
	}
	if !lessVersion([3]int{2, 1, 278}, minClaudeVersion) || lessVersion([3]int{2, 1, 302}, minClaudeVersion) || lessVersion(minClaudeVersion, minClaudeVersion) {
		t.Error("lessVersion")
	}
}
