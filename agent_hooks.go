package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/sandeepkomal/blast-radius/internal/analyze"
	"github.com/sandeepkomal/blast-radius/internal/policy"
	"github.com/sandeepkomal/blast-radius/internal/render"
)

// Hooks for agents other than Claude Code. Each agent has its own wire
// format, but every command goes through evaluate(), so the analysis, policy
// and activity log are the same for all of them.

// noAskNote leads the reason when policy says "ask" but the agent's hook
// cannot ask the user, so the command is denied rather than let through.
const noAskNote = "blast-radius requires human review. %s hooks cannot ask you to approve a command, " +
	"so it is blocked rather than silently allowed. Review it with 'blast-radius check', then run it yourself if you mean to.\n"

// hookReason explains an ask or deny decision to the agent and the user.
func hookReason(findings []analyze.Finding, verdicts []policy.Verdict, action policy.Action) string {
	reason := render.Text(findings, verdicts, false)
	if action == policy.Deny {
		return reason + denyAdvice
	}
	if w := askWarning(findings); w != "" {
		// Prompts preselect "Yes", so lead with what is lost.
		return w + "\n" + reason
	}
	return reason
}

// agentShell is how an agent's shell tool runs commands on this OS: the
// agents below use PowerShell on Windows and bash elsewhere.
func agentShell() (tool string, shell analyze.Shell) {
	if runtime.GOOS == "windows" {
		return "PowerShell", analyze.PowerShell
	}
	return "Bash", analyze.Bash
}

// commandDir is where a command runs: dir resolved against the hook's cwd,
// or the cwd, or failing that our own working directory.
func commandDir(cwd, dir string) string {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if dir == "" {
		return cwd
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(cwd, dir)
}

// evaluateEdits checks each file an agent is about to change, resolved
// against req.dir, and returns the strictest decision.
func evaluateEdits(req request, files []string, run analyze.Runner, stderr io.Writer) ([]analyze.Finding, []policy.Verdict, policy.Action) {
	var findings []analyze.Finding
	var verdicts []policy.Verdict
	for _, f := range files {
		req.editPath = commandDir(req.dir, f)
		fs, vs, _ := evaluate(req, run, stderr)
		findings, verdicts = append(findings, fs...), append(verdicts, vs...)
	}
	return findings, verdicts, policy.Strictest(verdicts)
}

// Gemini CLI BeforeTool hook, for its run_shell_command tool and its file
// writing tools (write_file, replace). Gemini CLI has no "ask" decision for
// hooks, and treats a hook that crashes or prints something other than JSON
// as "allow", so every failure must still end in a printed denial.
type geminiHookInput struct {
	HookEventName string `json:"hook_event_name"`
	SessionID     string `json:"session_id"`
	Cwd           string `json:"cwd"`
	ToolName      string `json:"tool_name"`
	ToolInput     struct {
		Command  string `json:"command"`
		DirPath  string `json:"dir_path"`
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
}

type geminiHookOutput struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// writeGeminiDeny denies the tool call. If stdout can't be written, exit
// code 2 with the reason on stderr also blocks it.
func writeGeminiDeny(stdout, stderr io.Writer, reason string) int {
	if err := json.NewEncoder(stdout).Encode(geminiHookOutput{Decision: "deny", Reason: reason}); err != nil {
		fmt.Fprintln(stderr, reason)
		return 2
	}
	return 0
}

func hookGemini(stdin io.Reader, stdout, stderr io.Writer, run analyze.Runner) (code int) {
	defer func() {
		if r := recover(); r != nil {
			code = writeGeminiDeny(stdout, stderr, fmt.Sprintf(
				"blast-radius hit an internal error (%v) and could not check this command; blocked for safety. Review it yourself before running it.", r))
		}
	}()
	var in geminiHookInput
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return writeGeminiDeny(stdout, stderr, "blast-radius could not read the Gemini CLI hook input ("+err.Error()+"); command blocked for safety.")
	}
	if in.HookEventName != "BeforeTool" {
		return 0
	}
	tool, shell := agentShell()
	req := request{tool: tool, shell: shell, agent: agentInfo{SessionID: in.SessionID, AgentPlatform: "gemini_cli"}}
	var findings []analyze.Finding
	var verdicts []policy.Verdict
	var action policy.Action
	switch in.ToolName {
	case "run_shell_command":
		if in.ToolInput.Command == "" {
			return writeGeminiDeny(stdout, stderr, "blast-radius received an empty shell command from Gemini CLI; blocked for safety.")
		}
		req.script, req.dir = in.ToolInput.Command, commandDir(in.Cwd, in.ToolInput.DirPath)
		findings, verdicts, action = evaluate(req, run, stderr)
	case "write_file", "replace":
		if in.ToolInput.FilePath == "" {
			return writeGeminiDeny(stdout, stderr, "blast-radius could not find which file Gemini CLI is changing; blocked for safety.")
		}
		req.tool, req.dir = in.ToolName, commandDir(in.Cwd, "")
		findings, verdicts, action = evaluateEdits(req, []string{in.ToolInput.FilePath}, run, stderr)
	default:
		return 0
	}
	switch action {
	case policy.Pass:
		return 0
	case policy.Ask:
		return writeGeminiDeny(stdout, stderr, fmt.Sprintf(noAskNote, "Gemini CLI")+hookReason(findings, verdicts, action))
	}
	return writeGeminiDeny(stdout, stderr, hookReason(findings, verdicts, action))
}

// GitHub Copilot CLI PreToolUse hook, configured with the PascalCase event
// name so the input uses snake_case fields and Claude Code's tool names: both
// of Copilot's shell tools (bash, powershell) arrive as "Bash", and its file
// tools as "Edit" and "Write". Copilot denies the tool call when
// a command hook exits non-zero, and supports "ask" (denied in autopilot
// mode and in the cloud agent, where nobody can answer).
type copilotHookInput struct {
	HookEventName string          `json:"hook_event_name"`
	SessionID     string          `json:"session_id"`
	AgentID       string          `json:"agent_id"`
	AgentType     string          `json:"agent_type"`
	Cwd           string          `json:"cwd"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
}

type copilotHookOutput struct {
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason"`
}

// writeCopilotDecision answers the hook. If stdout can't be written, the
// non-zero exit makes Copilot deny the tool call.
func writeCopilotDecision(w io.Writer, action policy.Action, reason string) int {
	if err := json.NewEncoder(w).Encode(copilotHookOutput{PermissionDecision: string(action), PermissionDecisionReason: reason}); err != nil {
		return 1
	}
	return 0
}

// parseCopilotArgs reads tool_input, which Copilot passes as an object, or
// as a JSON string when it couldn't parse it.
func parseCopilotArgs(raw json.RawMessage) (map[string]any, error) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		raw = json.RawMessage(s)
	}
	var args map[string]any
	err := json.Unmarshal(raw, &args)
	return args, err
}

// copilotEditFiles lists the files a Copilot edit changes: its path
// argument, or for apply_patch (also reported as "Edit") the files named in
// the patch.
func copilotEditFiles(args map[string]any) []string {
	for _, key := range []string{"path", "file_path"} {
		if p, ok := args[key].(string); ok && p != "" {
			return []string{p}
		}
	}
	var files []string
	for _, v := range args {
		if text, ok := v.(string); ok {
			files = append(files, patchFiles(text)...)
		}
	}
	return files
}

func hookCopilot(stdin io.Reader, stdout, stderr io.Writer, run analyze.Runner) (code int) {
	defer func() {
		if r := recover(); r != nil {
			code = writeCopilotDecision(stdout, policy.Deny, fmt.Sprintf(
				"blast-radius hit an internal error (%v) and could not check this command; blocked for safety. Review it yourself before running it.", r))
		}
	}()
	var in copilotHookInput
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return writeCopilotDecision(stdout, policy.Deny, "blast-radius could not read the Copilot hook input ("+err.Error()+"); command blocked for safety.")
	}
	if in.HookEventName != "PreToolUse" {
		return 0
	}
	tool, shell := agentShell()
	req := request{
		tool: tool, shell: shell, dir: commandDir(in.Cwd, ""),
		agent: agentInfo{SessionID: in.SessionID, AgentID: in.AgentID, AgentType: in.AgentType, AgentPlatform: "copilot"},
	}
	var findings []analyze.Finding
	var verdicts []policy.Verdict
	var action policy.Action
	switch in.ToolName {
	case "Bash":
		args, err := parseCopilotArgs(in.ToolInput)
		command, _ := args["command"].(string)
		if err != nil || command == "" {
			return writeCopilotDecision(stdout, policy.Deny, "blast-radius could not find the shell command in the Copilot hook input; blocked for safety.")
		}
		req.script = command
		findings, verdicts, action = evaluate(req, run, stderr)
	case "Edit", "Write":
		args, err := parseCopilotArgs(in.ToolInput)
		files := copilotEditFiles(args)
		if err != nil || len(files) == 0 {
			return writeCopilotDecision(stdout, policy.Ask, "blast-radius could not find which file Copilot is changing. Check it yourself before approving.")
		}
		req.tool = in.ToolName
		findings, verdicts, action = evaluateEdits(req, files, run, stderr)
	default:
		return 0
	}
	if action == policy.Pass {
		return 0
	}
	return writeCopilotDecision(stdout, action, hookReason(findings, verdicts, action))
}
