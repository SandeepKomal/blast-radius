package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/sandeepkomal/blast-radius/internal/analyze"
	"github.com/sandeepkomal/blast-radius/internal/policy"
)

// Codex PreToolUse uses a different wire protocol from Claude Code. Codex
// does not support an "ask" decision for PreToolUse, so Ask and Deny both
// become explicit denials; the reason explains the manual review path.
type codexHookInput struct {
	HookEventName  string `json:"hook_event_name"`
	ToolName       string `json:"tool_name"`
	ToolUseID      string `json:"tool_use_id"`
	SessionID      string `json:"session_id"`
	AgentID        string `json:"agent_id"`
	AgentType      string `json:"agent_type"`
	PermissionMode string `json:"permission_mode"`
	Cwd            string `json:"cwd"`
	Model          string `json:"model"`
	ToolInput      struct {
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

// writeCodexDecision denies the tool call. If stdout can't be written, exit
// code 2 with the reason on stderr also blocks it; Codex ignores any other
// failure and runs the command.
func writeCodexDecision(stdout, stderr io.Writer, reason string) int {
	var out codexHookOutput
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	out.HookSpecificOutput.PermissionDecision = "deny"
	out.HookSpecificOutput.PermissionDecisionReason = reason
	if err := json.NewEncoder(stdout).Encode(out); err != nil {
		fmt.Fprintln(stderr, reason)
		return 2
	}
	return 0
}

// hookCodex evaluates Codex shell commands (Bash) and file edits
// (apply_patch) with the same analyzer and policy engine as hookClaude.
// Other tools are ignored. Malformed input and non-pass decisions never
// silently allow a command.
//
// Codex's SessionStart, UserPromptSubmit and PostToolUse events are only
// recorded for the dashboard, like Claude Code's (see record.go).
func hookCodex(stdin io.Reader, stdout, stderr io.Writer, run analyze.Runner) (code int) {
	var in codexHookInput
	defer func() {
		if recovered := recover(); recovered != nil {
			if recorded(in.HookEventName) {
				code = 0
				return
			}
			code = writeCodexDecision(stdout, stderr, "blast-radius encountered an internal error while checking this command; blocked for safety. Review it manually before running it.")
		}
	}()
	raw, err := io.ReadAll(stdin)
	if err == nil {
		err = json.Unmarshal(raw, &in)
	}
	if err != nil {
		return writeCodexDecision(stdout, stderr, "blast-radius could not read Codex hook input; command blocked for safety.")
	}
	if recorded(in.HookEventName) {
		var rec hookInput
		if json.Unmarshal(raw, &rec) == nil {
			rec.AgentPlatform = "codex"
			recordEvent(rec, stderr)
		}
		return 0
	}
	if in.HookEventName != "PreToolUse" || (in.ToolName != "Bash" && in.ToolName != "apply_patch") {
		return 0
	}
	if in.ToolInput.Command == "" {
		return writeCodexDecision(stdout, stderr, "blast-radius received an empty or unsupported Codex command; blocked for safety.")
	}
	req := request{
		tool: "Bash", dir: commandDir(in.Cwd, ""),
		agent: agentInfo{
			ToolUseID: in.ToolUseID, SessionID: in.SessionID, AgentID: in.AgentID, AgentType: in.AgentType,
			PermissionMode: in.PermissionMode, AgentPlatform: "codex",
		},
		model: in.Model,
	}
	var findings []analyze.Finding
	var verdicts []policy.Verdict
	var action policy.Action
	if in.ToolName == "apply_patch" {
		files := patchFiles(in.ToolInput.Command)
		if len(files) == 0 {
			return writeCodexDecision(stdout, stderr, "blast-radius could not find which files this Codex patch changes; blocked for safety.")
		}
		req.tool = "apply_patch"
		findings, verdicts, action = evaluateEdits(req, files, run, stderr)
	} else {
		req.script = in.ToolInput.Command
		findings, verdicts, action = evaluate(req, run, stderr)
		// A patch can also reach apply_patch through the shell, as a heredoc.
		if files := patchFiles(in.ToolInput.Command); len(files) > 0 {
			f, v, _ := evaluateEdits(request{tool: "apply_patch", dir: req.dir, agent: req.agent, model: req.model}, files, run, stderr)
			findings, verdicts = append(findings, f...), append(verdicts, v...)
			action = policy.Strictest(verdicts)
		}
	}
	switch action {
	case policy.Pass:
		return 0
	case policy.Ask:
		return writeCodexDecision(stdout, stderr, fmt.Sprintf(noAskNote, "Codex")+hookReason(findings, verdicts, action))
	}
	return writeCodexDecision(stdout, stderr, hookReason(findings, verdicts, action))
}

// patchFiles lists the files a Codex apply_patch patch adds, deletes,
// updates or moves a file to.
func patchFiles(patch string) []string {
	var files []string
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimSpace(line)
		for _, marker := range []string{"*** Add File: ", "*** Delete File: ", "*** Update File: ", "*** Move to: "} {
			if f, ok := strings.CutPrefix(line, marker); ok && strings.TrimSpace(f) != "" {
				files = append(files, strings.TrimSpace(f))
			}
		}
	}
	return files
}
