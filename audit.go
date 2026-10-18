package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sandeepkomal/blast-radius/internal/analyze"
	"github.com/sandeepkomal/blast-radius/internal/policy"
)

type auditEntry struct {
	Time     time.Time      `json:"time"`
	Tool     string         `json:"tool"`
	Dir      string         `json:"dir"`
	Command  string         `json:"command"`
	Action   policy.Action  `json:"action"`
	Findings []auditFinding `json:"findings"`
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
func writeAudit(path string, req request, findings []analyze.Finding, verdicts []policy.Verdict, action policy.Action) error {
	if p, ok := strings.CutPrefix(path, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		path = filepath.Join(home, p)
	}
	e := auditEntry{Time: time.Now().UTC(), Tool: req.tool, Dir: req.dir, Command: req.script, Action: action}
	if req.editPath != "" {
		e.Command = "edit " + req.editPath
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
	line, err := json.Marshal(e)
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
