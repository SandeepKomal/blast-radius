package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sandeepkomal/blast-radius/internal/analyze"
	"github.com/sandeepkomal/blast-radius/internal/policy"
	"github.com/sandeepkomal/blast-radius/internal/redact"
)

type auditEntry struct {
	Time     time.Time      `json:"time"`
	Tool     string         `json:"tool"`
	Dir      string         `json:"dir"`
	Command  string         `json:"command"`
	Action   policy.Action  `json:"action"`
	Findings []auditFinding `json:"findings"`
	agentInfo
}

// agentInfo identifies the Claude Code session, subagent and prompt a
// command came from, so the dashboard can tie it to what happened next.
type agentInfo struct {
	SessionID      string `json:"session_id,omitempty"`
	PromptID       string `json:"prompt_id,omitempty"`
	ToolUseID      string `json:"tool_use_id,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	AgentType      string `json:"agent_type,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`
}

type auditFinding struct {
	Tool        string        `json:"tool"`
	Target      string        `json:"target"`
	Level       string        `json:"level"`
	Action      policy.Action `json:"action"`
	Environment string        `json:"environment,omitempty"`
	Reason      string        `json:"reason,omitempty"`
	Destroyed   []string      `json:"destroyed,omitempty"`
}

// writeAudit appends one JSON line describing a checked command.
func writeAudit(path string, mask bool, req request, findings []analyze.Finding, verdicts []policy.Verdict, action policy.Action) error {
	path, err := expandHome(path)
	if err != nil {
		return err
	}
	e := auditEntry{Time: time.Now().UTC(), Tool: req.tool, Dir: req.dir, Command: req.script, Action: action, Findings: []auditFinding{}, agentInfo: req.agent}
	if req.editPath != "" {
		e.Command = "edit " + req.editPath
	}
	if mask {
		e.Command = redact.Secrets(e.Command)
	}
	for i, f := range findings {
		af := auditFinding{
			Tool: f.Tool, Target: f.Target, Level: f.Level.String(),
			Action: verdicts[i].Action, Environment: verdicts[i].Environment, Reason: verdicts[i].Reason,
		}
		if f.Guard {
			af.Level = "guard"
		}
		for _, r := range f.Resources {
			if r.Lost() {
				af.Destroyed = append(af.Destroyed, r.Name)
			}
		}
		e.Findings = append(e.Findings, af)
	}
	return appendLine(path, e)
}

// appendLine writes v as one JSON line at the end of the log, in a single
// write so lines from hooks running at the same time don't interleave.
func appendLine(path string, v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer fh.Close()
	_, err = fh.Write(append(line, '\n'))
	return err
}

// expandHome turns a leading "~/" into the user's home directory.
func expandHome(path string) (string, error) {
	p, ok := strings.CutPrefix(path, "~/")
	if !ok {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, p), nil
}

// runningEntry marks a check in progress, so `blast-radius ui` can show
// previews that are still running.
type runningEntry struct {
	Started time.Time `json:"started"`
	Timeout int       `json:"timeout"`
	Tool    string    `json:"tool"`
	Dir     string    `json:"dir"`
	Command string    `json:"command"`
}

// runningDir holds one file per check in progress, next to the audit log.
func runningDir(auditLog string) string {
	return filepath.Join(filepath.Dir(auditLog), "running")
}

// markRunning records that req is being checked and returns a function that
// clears the mark. Errors are ignored: the mark is only for display.
func markRunning(auditLog string, req request, timeout int) func() {
	path, err := expandHome(auditLog)
	if err != nil {
		return func() {}
	}
	dir := runningDir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return func() {}
	}
	b, err := json.Marshal(runningEntry{Started: time.Now().UTC(), Timeout: timeout, Tool: req.tool, Dir: req.dir, Command: req.script})
	if err != nil {
		return func() {}
	}
	f := filepath.Join(dir, fmt.Sprintf("%d-%d.json", os.Getpid(), time.Now().UnixNano()))
	if err := os.WriteFile(f, b, 0o600); err != nil {
		return func() {}
	}
	return func() { _ = os.Remove(f) }
}
