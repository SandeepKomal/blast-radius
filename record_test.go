package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandeepkomal/blast-radius/internal/policy"
)

// withAuditLog writes a user config with an audit log and returns its path.
func withAuditLog(t *testing.T, all bool) string {
	t.Helper()
	isolate(t)
	log := filepath.Join(t.TempDir(), "audit.jsonl")
	cfg, err := policy.UserConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{"audit_log": log, "audit_all": all})
	if err := os.WriteFile(cfg, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return log
}

func readLog(t *testing.T, path string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("%v: %s", err, line)
		}
		out = append(out, m)
	}
	return out
}

func sendHook(t *testing.T, event map[string]any) string {
	t.Helper()
	b, _ := json.Marshal(event)
	var out, errOut bytes.Buffer
	if code := hookClaude(bytes.NewReader(b), &out, &errOut, planRunner(destroyPlan)); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	return out.String()
}

func TestRecordedEvents(t *testing.T) {
	log := withAuditLog(t, false)
	dir := t.TempDir()
	base := map[string]any{"session_id": "s1", "cwd": dir, "agent_type": "Explore", "prompt_id": "p1"}
	with := func(kv ...any) map[string]any {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}

	events := []map[string]any{
		with("hook_event_name", "SessionStart", "model", "claude-opus-5", "source", "startup"),
		with("hook_event_name", "UserPromptSubmit", "prompt", "tear down the demo"),
		with("hook_event_name", "PostToolUse", "tool_name", "Bash", "tool_use_id", "t1", "duration_ms", 12),
		with("hook_event_name", "PostToolUseFailure", "tool_name", "Bash", "tool_use_id", "t2", "error", "Exit code 1"),
		with("hook_event_name", "PermissionDenied", "tool_name", "Bash", "tool_use_id", "t3", "reason", "[Irreversible Local Destruction]"),
		with("hook_event_name", "PostToolUse", "tool_name", "Read", "tool_use_id", "t4"),
		with("hook_event_name", "PostToolUse", "tool_name", "Skill", "tool_use_id", "t5", "tool_input", map[string]string{"skill": "paranoid-sre:paranoid"}),
	}
	for _, e := range events {
		if out := sendHook(t, e); out != "" {
			t.Errorf("%s printed %q; recording hooks must print nothing", e["hook_event_name"], out)
		}
	}

	got := readLog(t, log)
	if len(got) != 6 {
		t.Fatalf("want 6 lines (Read is not recorded), got %d: %v", len(got), got)
	}
	want := []map[string]any{
		{"kind": "session", "model": "claude-opus-5", "agent_type": "Explore", "session_id": "s1"},
		{"kind": "prompt", "prompt": "tear down the demo", "prompt_id": "p1"},
		{"kind": "result", "outcome": "ran", "tool_use_id": "t1"},
		{"kind": "result", "outcome": "failed", "tool_use_id": "t2", "error": "Exit code 1"},
		{"kind": "result", "outcome": "denied", "tool_use_id": "t3"},
		{"kind": "skill", "skill": "paranoid-sre:paranoid", "agent_type": "Explore"},
	}
	for i, w := range want {
		for k, v := range w {
			if got[i][k] != v {
				t.Errorf("line %d: %s = %v, want %v", i, k, got[i][k], v)
			}
		}
	}
}

func TestRecordingNeedsAuditLog(t *testing.T) {
	isolate(t)
	out := sendHook(t, map[string]any{"hook_event_name": "UserPromptSubmit", "prompt": "hi", "cwd": t.TempDir()})
	if out != "" {
		t.Errorf("printed %q", out)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".local", "state", "blast-radius")); err == nil {
		t.Error("wrote a log without audit_log configured")
	}
}

func TestAuditAll(t *testing.T) {
	for _, all := range []bool{false, true} {
		log := withAuditLog(t, all)
		dir := t.TempDir()
		pre := func(cmd, id string) {
			t.Helper()
			sendHook(t, map[string]any{
				"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_use_id": id,
				"tool_input": map[string]string{"command": cmd}, "cwd": dir, "session_id": "s1", "agent_type": "Explore",
			})
		}
		pre("ls -la", "t1")
		pre("terraform destroy", "t2")
		got := readLog(t, log)
		want := 1
		if all {
			want = 2
		}
		if len(got) != want {
			t.Fatalf("audit_all=%v: %d lines: %v", all, len(got), got)
		}
		last := got[len(got)-1]
		if last["tool_use_id"] != "t2" || last["agent_type"] != "Explore" || last["session_id"] != "s1" || last["action"] != "ask" {
			t.Errorf("audit_all=%v: entry %v", all, last)
		}
		if all && (got[0]["command"] != "ls -la" || got[0]["action"] != "pass") {
			t.Errorf("harmless entry %v", got[0])
		}
	}
}

func TestHasRecordingHooks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	old := map[string]any{"PreToolUse": hookEntries("/bin/blast-radius")["PreToolUse"]}
	if _, err := updateSettings(path, old); err != nil {
		t.Fatal(err)
	}
	if hasRecordingHooks(path) {
		t.Error("an install with only PreToolUse has recording hooks")
	}
	if _, err := updateSettings(path, hookEntries("/bin/blast-radius")); err != nil {
		t.Fatal(err)
	}
	if !hasRecordingHooks(path) {
		t.Error("a full install has no recording hooks")
	}
	// An install from before skills were recorded has an older matcher.
	b, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), "Bash|PowerShell|Skill", "Bash|PowerShell", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if hasRecordingHooks(path) {
		t.Error("an install with the old PostToolUse matcher counts as current")
	}
}

func TestAuditPromptsOff(t *testing.T) {
	isolate(t)
	log := filepath.Join(t.TempDir(), "audit.jsonl")
	cfg, _ := policy.UserConfigPath()
	os.MkdirAll(filepath.Dir(cfg), 0o755)
	b, _ := json.Marshal(map[string]any{"audit_log": log, "audit_prompts": false})
	os.WriteFile(cfg, b, 0o644)
	dir := t.TempDir()
	sendHook(t, map[string]any{"hook_event_name": "UserPromptSubmit", "prompt": "secret plans", "session_id": "s1", "cwd": dir})
	sendHook(t, map[string]any{"hook_event_name": "PostToolUse", "tool_name": "Bash", "tool_use_id": "t1", "session_id": "s1", "cwd": dir})
	got := readLog(t, log)
	if len(got) != 1 || got[0]["kind"] != "result" {
		t.Fatalf("want only the result line, got %v", got)
	}
	if raw, _ := os.ReadFile(log); strings.Contains(string(raw), "secret plans") {
		t.Error("prompt was recorded with audit_prompts false")
	}
}
