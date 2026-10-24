package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandeepkomal/blast-radius/internal/policy"
)

// TestAgentAttributionEndToEnd follows Claude Code and Codex hook input through
// audit logging and the activity API, which is the data source used by the UI.
func TestAgentAttributionEndToEnd(t *testing.T) {
	isolate(t)
	log := filepath.Join(t.TempDir(), "audit.jsonl")
	cfgPath, err := policy.UserConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := json.Marshal(map[string]any{
		"audit_log": log,
		"audit_all": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	claudeInput, err := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"cwd":             dir,
		"tool_input":      map[string]string{"command": "terraform destroy -auto-approve"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var claudeOut, claudeErr strings.Builder
	if code := hookClaude(strings.NewReader(string(claudeInput)), &claudeOut, &claudeErr, planRunner(destroyPlan)); code != 0 {
		t.Fatalf("Claude hook exit %d: %s", code, claudeErr.String())
	}

	codexInput := codexInput("terraform destroy -auto-approve", dir)
	var codexOut, codexErr strings.Builder
	if code := hookCodex(strings.NewReader(codexInput), &codexOut, &codexErr, planRunner(destroyPlan)); code != 0 {
		t.Fatalf("Codex hook exit %d: %s", code, codexErr.String())
	}

	// A record written by an older version has no platform metadata.
	if err := appendLine(log, auditEntry{Tool: "Bash", Dir: dir, Command: "terraform plan"}); err != nil {
		t.Fatal(err)
	}

	server := &uiServer{logFlag: log, port: "1"}
	req := httptest.NewRequest(http.MethodGet, "/api/activity", nil)
	req.Host = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	server.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("activity status %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Entries []json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode activity response: %v", err)
	}
	got := map[string]int{}
	for _, raw := range response.Entries {
		var entry map[string]any
		if err := json.Unmarshal(raw, &entry); err != nil {
			t.Fatalf("decode audit entry %s: %v", raw, err)
		}
		if platform, ok := entry["agent_platform"].(string); ok {
			got[platform]++
		} else {
			got["legacy"]++
		}
	}
	if got["claude_code"] != 1 || got["codex"] != 1 || got["legacy"] != 1 {
		t.Fatalf("platform counts from activity API = %#v; want one Claude Code, one Codex, and one legacy record", got)
	}
}
