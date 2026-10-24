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

func runCodexHookTest(t *testing.T, input string, runner analyze.Runner) (string, string) {
	t.Helper()
	var out, stderr bytes.Buffer
	if code := hookCodex(strings.NewReader(input), &out, &stderr, runner); code != 0 {
		t.Fatalf("hookCodex exit %d: %s", code, stderr.String())
	}
	if out.Len() == 0 {
		return "", ""
	}
	var result codexHookOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid hook output %q: %v", out.String(), err)
	}
	return result.HookSpecificOutput.PermissionDecision, result.HookSpecificOutput.PermissionDecisionReason
}

func codexInput(command, cwd string) string {
	b, _ := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_use_id":     "codex-test",
		"session_id":      "codex-session",
		"cwd":             cwd,
		"tool_input":      map[string]string{"command": command},
	})
	return string(b)
}


func TestAgentPlatformAuditMetadata(t *testing.T) {
	for _, platform := range []string{"codex", "claude_code"} {
		b, err := json.Marshal(auditEntry{agentInfo: agentInfo{AgentPlatform: platform}})
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		if got["agent_platform"] != platform {
			t.Errorf("agent_platform = %v, want %q", got["agent_platform"], platform)
		}
	}
	b, err := json.Marshal(auditEntry{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "agent_platform") {
		t.Errorf("legacy/unknown platform should be omitted when empty: %s", b)
	}
}

func TestCodexHookUsesExistingPolicyEngine(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	runner := planRunner(destroyPlan)

	if decision, _ := runCodexHookTest(t, codexInput("git status", dir), runner); decision != "" {
		t.Fatalf("harmless command decision = %q, want no decision", decision)
	}
	decision, reason := runCodexHookTest(t, codexInput("terraform destroy -auto-approve", dir), runner)
	if decision != "deny" || !strings.Contains(reason, "human review") {
		t.Fatalf("review-needed command decision = %q, reason %q", decision, reason)
	}
}

func TestCodexHookBlocksProductionAndMalformedInput(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	prod := filepath.Join(root, "envs", "prod")
	if err := os.MkdirAll(prod, 0o755); err != nil {
		t.Fatal(err)
	}
	if decision, reason := runCodexHookTest(t, codexInput("terraform destroy -auto-approve", prod), planRunner(destroyPlan)); decision != "deny" || !strings.Contains(reason, "production") {
		t.Fatalf("production destroy decision = %q, reason %q", decision, reason)
	}
	if decision, reason := runCodexHookTest(t, "not-json", planRunner(destroyPlan)); decision != "deny" || !strings.Contains(reason, "blocked for safety") {
		t.Fatalf("malformed input decision = %q, reason %q", decision, reason)
	}
}

func TestCodexHookIgnoresUnsupportedEvents(t *testing.T) {
	isolate(t)
	input := `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"terraform destroy"}}`
	if decision, reason := runCodexHookTest(t, input, planRunner(destroyPlan)); decision != "" || reason != "" {
		t.Fatalf("unsupported event decision = %q, reason %q", decision, reason)
	}
}

func TestCodexHookFailsClosedOnAnalyzerPanic(t *testing.T) {
	isolate(t)
	panicRunner := func(_ context.Context, _ string, _ []string, _ string, _ ...string) (analyze.Result, error) {
		panic("simulated analyzer failure")
	}
	decision, reason := runCodexHookTest(t, codexInput("terraform destroy", t.TempDir()), panicRunner)
	if decision != "deny" || !strings.Contains(reason, "blocked for safety") {
		t.Fatalf("panic decision = %q, reason %q", decision, reason)
	}
}
