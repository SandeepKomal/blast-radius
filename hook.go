package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/sandeepkomal/blast-radius/internal/analyze"
	"github.com/sandeepkomal/blast-radius/internal/policy"
	"github.com/sandeepkomal/blast-radius/internal/render"
)

// Told to the agent when a command is blocked, so it stops instead of
// looking for another way to run it.
const denyAdvice = "\nBlocked by blast-radius policy. Do not retry this command or work around it " +
	"(for example with a different tool, flag or script). Tell the user what you were trying to do " +
	"and let them run it themselves if they want it."

type hookInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	Cwd string `json:"cwd"`
}

type hookOutput struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// hookClaude implements a Claude Code PreToolUse hook for the Bash tool.
// Commands that pass produce no output, so Claude Code's own permission
// rules apply as usual.
func hookClaude(stdin io.Reader, stdout, stderr io.Writer, run analyze.Runner) int {
	var in hookInput
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		fmt.Fprintln(stderr, "blast-radius: could not read hook input:", err)
		return 0
	}
	if in.ToolName != "Bash" || in.ToolInput.Command == "" {
		return 0
	}
	dir := in.Cwd
	if dir == "" {
		dir, _ = os.Getwd()
	}

	findings, verdicts, action := evaluate(in.ToolInput.Command, dir, run, stderr)
	if action == policy.Pass {
		return 0
	}
	var out hookOutput
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	out.HookSpecificOutput.PermissionDecision = string(action)
	out.HookSpecificOutput.PermissionDecisionReason = render.Text(findings, verdicts, false)
	if action == policy.Deny {
		out.HookSpecificOutput.PermissionDecisionReason += denyAdvice
	}
	json.NewEncoder(stdout).Encode(out)
	return 0
}
