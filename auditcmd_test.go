package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sandeepkomal/blast-radius/internal/policy"
)

func writeLines(t *testing.T, path string, lines ...map[string]any) {
	t.Helper()
	var b bytes.Buffer
	for _, l := range lines {
		j, _ := json.Marshal(l)
		b.Write(j)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAuditCommands(t *testing.T) {
	isolate(t)
	log := filepath.Join(t.TempDir(), "audit.jsonl")
	old := time.Now().Add(-40 * 24 * time.Hour).UTC().Format(time.RFC3339)
	now := time.Now().UTC().Format(time.RFC3339)
	reset := func() {
		writeLines(t, log,
			map[string]any{"time": old, "command": "terraform destroy", "session_id": "s1"},
			map[string]any{"kind": "prompt", "time": now, "prompt": "delete Ravi's test DB", "session_id": "s2"},
			map[string]any{"time": now, "command": "kubectl delete ns ravi-dev", "session_id": "s2"},
			map[string]any{"time": now, "command": "helm list", "session_id": "s3"},
		)
	}
	run := func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := auditCmd(append(args, "--log", log), &out, &errOut)
		return code, out.String(), errOut.String()
	}
	count := func() int {
		b, _ := os.ReadFile(log)
		return strings.Count(string(b), "\n")
	}

	reset()
	if code, out, _ := run("prune", "--older-than", "30d", "--dry-run"); code != 0 || !strings.Contains(out, "Would delete 1") || count() != 4 {
		t.Errorf("prune dry run: %d %q, %d lines", code, out, count())
	}
	if code, _, _ := run("prune", "--older-than", "30d"); code != 0 || count() != 3 {
		t.Errorf("prune: %d, %d lines", code, count())
	}

	reset()
	if code, out, errOut := run("export", "--match", "RAVI"); code != 0 || strings.Count(out, "\n") != 2 || !strings.Contains(errOut, "2 entries") {
		t.Errorf("export: %d %q %q", code, out, errOut)
	}
	if code, _, _ := run("forget", "--match", "ravi"); code != 0 || count() != 2 {
		t.Errorf("forget --match: %d, %d lines", code, count())
	}
	if code, _, _ := run("forget", "--session", "s1"); code != 0 || count() != 1 {
		t.Errorf("forget --session: %d, %d lines", code, count())
	}
	if st, _ := os.Stat(log); st.Mode().Perm()&0o077 != 0 && !strings.HasPrefix(os.Getenv("OS"), "Windows") {
		t.Errorf("rewritten log is readable by others: %v", st.Mode().Perm())
	}

	for _, bad := range [][]string{{"forget"}, {"forget", "--match", "x", "--session", "y"}, {"prune"}, {"nope"}} {
		if code, _, _ := run(bad...); code != 2 {
			t.Errorf("%v: exit %d, want 2", bad, code)
		}
	}
}

func TestRetentionFromConfig(t *testing.T) {
	log := filepath.Join(t.TempDir(), "audit.jsonl")
	writeLines(t, log,
		map[string]any{"time": time.Now().Add(-100 * 24 * time.Hour).UTC().Format(time.RFC3339), "command": "old"},
		map[string]any{"time": time.Now().UTC().Format(time.RFC3339), "command": "new"},
	)
	if n, err := pruneByRetention(log, 0); err != nil || n != 0 {
		t.Errorf("retention 0 deleted %d (%v)", n, err)
	}
	if n, err := pruneByRetention(log, 90); err != nil || n != 1 {
		t.Errorf("retention 90 deleted %d (%v)", n, err)
	}
}

func TestLogIsMasked(t *testing.T) {
	log := withAuditLog(t, true)
	dir := t.TempDir()
	sendHook(t, map[string]any{"hook_event_name": "UserPromptSubmit", "prompt": "reset the DB for ravi@example.com, PAN ABCPE1234F", "session_id": "s1", "cwd": dir})
	sendHook(t, map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_use_id": "t1", "session_id": "s1", "cwd": dir,
		"tool_input": map[string]string{"command": "mysql --password=hunter2 -e 'select 1'"}})
	sendHook(t, map[string]any{"hook_event_name": "PostToolUseFailure", "tool_name": "Bash", "tool_use_id": "t1", "session_id": "s1", "cwd": dir,
		"error": "access denied for ravi@example.com"})
	raw, _ := os.ReadFile(log)
	for _, leak := range []string{"ravi@example.com", "ABCPE1234F", "hunter2"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("%q in the log:\n%s", leak, raw)
		}
	}
	for _, kept := range []string{"reset the DB for [email]", "--password=[secret]"} {
		if !strings.Contains(string(raw), kept) {
			t.Errorf("missing %q in:\n%s", kept, raw)
		}
	}

	// With masking off, the text is kept as written.
	cfg, _ := policy.UserConfigPath()
	b, _ := json.Marshal(map[string]any{"audit_log": log, "audit_redact": false})
	os.WriteFile(cfg, b, 0o644)
	sendHook(t, map[string]any{"hook_event_name": "UserPromptSubmit", "prompt": "mail ravi@example.com", "session_id": "s1", "cwd": dir})
	if raw, _ := os.ReadFile(log); !strings.Contains(string(raw), "mail ravi@example.com") {
		t.Error("audit_redact false still masked the prompt")
	}
}
