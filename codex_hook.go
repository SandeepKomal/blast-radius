package main

import (
	"encoding/json"
	"io"
	"os"

	"github.com/sandeepkomal/blast-radius/internal/analyze"
	"github.com/sandeepkomal/blast-radius/internal/policy"
	"github.com/sandeepkomal/blast-radius/internal/render"
)

// Codex PreToolUse uses a different wire protocol from Claude Code. Codex
// does not currently support an "ask" decision for PreToolUse. To preserve
// safety, Ask and Deny become explicit denials; the reason explains the
// manual review path.
type codexHookInput struct {
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
	ToolUseID     string `json:"tool_use_id"`
	SessionID     string `json:"session_id"`
	Cwd           string `json:"cwd"`
	Model         string `json:"model"`
	ToolInput     struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

type codexHookOutput struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

func writeCodexDecision(w io.Writer, reason string) int {
	var out codexHookOutput
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	out.HookSpecificOutput.PermissionDecision = "deny"
	out.HookSpecificOutput.PermissionDecisionReason = reason
	if err := json.NewEncoder(w).Encode(out); err != nil {
		return 2
	}
	return 0
}

// hookCodex evaluates Codex Bash commands with the same analyzer and policy
// engine as hookClaude. Unsupported events/tools are ignored. Malformed input
// and non-pass decisions never silently allow a command.
func hookCodex(stdin io.Reader, stdout, stderr io.Writer, run analyze.Runner) (code int) {
	var in codexHookInput
	defer func() {
		if recovered := recover(); recovered != nil {
			code = writeCodexDecision(stdout, "blast-radius encountered an internal error while checking this command; blocked for safety. Review it manually before running it.")
		}
	}()
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return writeCodexDecision(stdout, "blast-radius could not read Codex hook input; command blocked for safety.")
	}
	if in.HookEventName != "PreToolUse" || in.ToolName != "Bash" {
		return 0
	}
	if in.ToolInput.Command == "" {
		return writeCodexDecision(stdout, "blast-radius received an empty or unsupported Codex command; blocked for safety.")
	}
	dir := in.Cwd
	if dir == "" {
		dir, _ = os.Getwd()
	}
	req := request{
		tool: "Bash", script: in.ToolInput.Command, dir: dir,
		agent: agentInfo{ToolUseID: in.ToolUseID, SessionID: in.SessionID, AgentPlatform: "codex"},
	}
	findings, verdicts, action := evaluate(req, run, stderr)
	if action == policy.Pass {
		return 0
	}
	reason := render.Text(findings, verdicts, false)
	if action == policy.Deny {
		reason += denyAdvice
	} else {
		reason = "blast-radius requires human review. Codex PreToolUse does not support an approval-prompt decision, so this command is blocked rather than silently allowed. Review with 'blast-radius check', then run it yourself only if appropriate.\n" + reason
	}
	return writeCodexDecision(stdout, reason)
}
