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
		Command      string `json:"command"`
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
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

// writeDecision prints the hook's answer. If that fails the hook exits 1,
// which Claude Code treats as a hook failure and, with onFailure "block",
// blocks the action.
func writeDecision(w io.Writer, action policy.Action, reason string) int {
	var out hookOutput
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	out.HookSpecificOutput.PermissionDecision = string(action)
	out.HookSpecificOutput.PermissionDecisionReason = reason
	if err := json.NewEncoder(w).Encode(out); err != nil {
		return 1
	}
	return 0
}

// hookClaude implements a Claude Code PreToolUse hook for shell commands
// (Bash, PowerShell) and file edits (Edit, Write). Commands that pass produce
// no output, so Claude Code's own permission rules apply as usual. Anything
// that goes wrong ends in "ask", never in a silent pass.
func hookClaude(stdin io.Reader, stdout, stderr io.Writer, run analyze.Runner) (code int) {
	defer func() {
		if r := recover(); r != nil {
			code = writeDecision(stdout, policy.Ask, fmt.Sprintf(
				"blast-radius hit an internal error (%v) and could not check this command. Review it yourself before approving.", r))
		}
	}()

	var in hookInput
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return writeDecision(stdout, policy.Ask, "blast-radius could not read the hook input ("+err.Error()+"). Review this action yourself before approving.")
	}
	req := request{tool: in.ToolName, dir: in.Cwd}
	if req.dir == "" {
		req.dir, _ = os.Getwd()
	}
	switch in.ToolName {
	case "Bash":
		req.script = in.ToolInput.Command
	case "PowerShell":
		req.script, req.shell = in.ToolInput.Command, analyze.PowerShell
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		req.editPath = in.ToolInput.FilePath
		if req.editPath == "" {
			req.editPath = in.ToolInput.NotebookPath
		}
		if req.editPath == "" {
			return 0
		}
	default:
		return 0
	}
	if req.script == "" && req.editPath == "" {
		return 0
	}

	findings, verdicts, action := evaluate(req, run, stderr)
	if action == policy.Pass {
		return 0
	}
	reason := render.Text(findings, verdicts, false)
	if action == policy.Deny {
		reason += denyAdvice
	} else if w := askWarning(findings); w != "" {
		// Claude Code preselects "Yes" in its prompt, so lead with what is lost.
		reason = w + "\n" + reason
	}
	return writeDecision(stdout, action, reason)
}

// askWarning summarises how many resources a command would delete, or
// returns "" if it deletes none that blast-radius could list.
func askWarning(findings []analyze.Finding) string {
	n := 0
	for _, f := range findings {
		for _, r := range f.Resources {
			if r.Lost() {
				n++
			}
		}
	}
	switch n {
	case 0:
		return ""
	case 1:
		return "WARNING: this deletes 1 resource. Choose No unless you meant to."
	}
	return fmt.Sprintf("WARNING: this deletes %d resources. Choose No unless you meant to.", n)
}
