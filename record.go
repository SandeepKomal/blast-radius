package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/sandeepkomal/blast-radius/internal/policy"
	"github.com/sandeepkomal/blast-radius/internal/redact"
)

// recordedEvents are the Claude Code hook events blast-radius only records
// in the audit log, so the dashboard can show which agent and model ran a
// command, for which request, whether it ran after being checked, and which
// skills (such as paranoid-sre) the agent loaded. They never print anything
// and never block: on UserPromptSubmit and SessionStart, anything printed
// would be added to Claude's context.
var recordedEvents = map[string]string{
	"PostToolUse":        "Bash|PowerShell|Skill",
	"PostToolUseFailure": "Bash|PowerShell",
	"PermissionDenied":   "Bash|PowerShell",
	"SessionStart":       "",
	"UserPromptSubmit":   "",
}

func recorded(event string) bool {
	_, ok := recordedEvents[event]
	return ok
}

// maxRecorded caps the text kept from a prompt or an error message.
const maxRecorded = 500

// What happened to a command after blast-radius checked it.
type resultEntry struct {
	Kind       string    `json:"kind"` // "result"
	Time       time.Time `json:"time"`
	Outcome    string    `json:"outcome"` // ran, failed, interrupted, denied
	Error      string    `json:"error,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	agentInfo
}

// A Claude Code session starting, with the model it runs.
type sessionEntry struct {
	Kind   string    `json:"kind"` // "session"
	Time   time.Time `json:"time"`
	Model  string    `json:"model,omitempty"`
	Source string    `json:"source,omitempty"`
	Dir    string    `json:"dir,omitempty"`
	agentInfo
}

// A skill the agent loaded. Only its name is kept.
type skillEntry struct {
	Kind  string    `json:"kind"` // "skill"
	Time  time.Time `json:"time"`
	Skill string    `json:"skill"`
	agentInfo
}

// A request the user gave the agent.
type promptEntry struct {
	Kind   string    `json:"kind"` // "prompt"
	Time   time.Time `json:"time"`
	Prompt string    `json:"prompt"`
	agentInfo
}

// recordEvent appends one line for a recorded event, when the user has an
// audit log. Errors go to stderr only.
func recordEvent(in hookInput, stderr io.Writer) {
	dir := in.Cwd
	if dir == "" {
		dir, _ = os.Getwd()
	}
	cfg, err := policy.Load(dir)
	if err != nil || cfg.AuditLog == "" {
		return
	}
	path, err := expandHome(cfg.AuditLog)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	var line any
	switch in.HookEventName {
	case "PostToolUse", "PostToolUseFailure", "PermissionDenied":
		if in.ToolName == "Skill" {
			if in.HookEventName != "PostToolUse" || in.ToolInput.Skill == "" {
				return
			}
			line = skillEntry{Kind: "skill", Time: now, Skill: truncate(in.ToolInput.Skill), agentInfo: in.agentInfo}
			break
		}
		if in.ToolName != "Bash" && in.ToolName != "PowerShell" {
			return
		}
		r := resultEntry{Kind: "result", Time: now, Outcome: "ran", DurationMS: in.DurationMS, agentInfo: in.agentInfo}
		switch {
		case in.HookEventName == "PermissionDenied":
			r.Outcome, r.Error = "denied", in.Reason
		case in.HookEventName == "PostToolUseFailure" && in.IsInterrupt:
			r.Outcome, r.Error = "interrupted", in.Error
		case in.HookEventName == "PostToolUseFailure":
			r.Outcome, r.Error = "failed", in.Error
		}
		if cfg.Redact() {
			r.Error = redact.Text(r.Error)
		}
		r.Error = truncate(r.Error)
		line = r
	case "SessionStart":
		model := ""
		switch m := in.Model.(type) {
		case string:
			model = m
		case map[string]any:
			if id, ok := m["id"].(string); ok {
				model = id
			} else if name, ok := m["display_name"].(string); ok {
				model = name
			}
		}
		line = sessionEntry{Kind: "session", Time: now, Model: model, Source: in.Source, Dir: in.Cwd, agentInfo: in.agentInfo}
	case "UserPromptSubmit":
		if !cfg.RecordPrompts() {
			return
		}
		prompt := in.Prompt
		if cfg.Redact() {
			prompt = redact.Text(prompt)
		}
		line = promptEntry{Kind: "prompt", Time: now, Prompt: truncate(prompt), agentInfo: in.agentInfo}
	default:
		return
	}
	if err := appendLine(path, line); err != nil {
		fmt.Fprintf(stderr, "blast-radius: audit log: %v\n", err)
	}
}

func truncate(s string) string {
	r := []rune(s)
	if len(r) <= maxRecorded {
		return s
	}
	return string(r[:maxRecorded]) + "…"
}
