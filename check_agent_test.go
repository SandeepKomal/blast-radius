package main

import (
	"io"
	"strings"
	"testing"
)

func TestCheckAgentRecordsAnyAgent(t *testing.T) {
	logPath := withAuditLog(t, true)
	dir := t.TempDir()
	if code := check([]string{"-C", dir, "--agent", "Aider", "--session", "s1", "terraform destroy"}, io.Discard, io.Discard, planRunner(destroyPlan)); code != exitAsk {
		t.Fatalf("exit %d, want %d", code, exitAsk)
	}
	if code := check([]string{"-C", dir, "--agent", "aider", "ls"}, io.Discard, io.Discard, planRunner(destroyPlan)); code != exitPass {
		t.Fatalf("exit %d, want %d", code, exitPass)
	}
	// A person running check by hand is still only logged when risky.
	if code := check([]string{"-C", dir, "ls"}, io.Discard, io.Discard, planRunner(destroyPlan)); code != exitPass {
		t.Fatalf("exit %d", code)
	}
	lines := readLog(t, logPath)
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %v", lines)
	}
	for _, l := range lines {
		if l["agent_platform"] != "aider" || l["tool"] != "Bash" {
			t.Errorf("entry = %v", l)
		}
	}
	if lines[0]["session_id"] != "s1" {
		t.Errorf("session = %v", lines[0]["session_id"])
	}
}

func TestCheckAgentRejectsBadNames(t *testing.T) {
	isolate(t)
	for _, name := range []string{"<script>", "a b", strings.Repeat("x", 41), " "} {
		if code := check([]string{"--agent", name, "terraform destroy"}, io.Discard, io.Discard, planRunner(destroyPlan)); code != 2 {
			t.Errorf("--agent %q: exit %d, want 2", name, code)
		}
	}
}
