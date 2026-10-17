// Package analyze works out what an infrastructure command would change
// before it runs, using each tool's own read-only preview (terraform plan,
// kubectl get, kubectl diff).
package analyze

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sandeepkomal/blast-radius/internal/shell"
)

// Level is how much a command can hurt.
type Level int

const (
	None    Level = iota // read-only or not an infrastructure command
	Change               // creates or modifies things
	Destroy              // deletes or replaces things, or we could not tell
)

func (l Level) String() string {
	switch l {
	case Change:
		return "change"
	case Destroy:
		return "destroy"
	}
	return "none"
}

// Resource actions, in the order they are shown.
const (
	ActDestroy = "destroy"
	ActReplace = "replace"
	ActUpdate  = "update"
	ActCreate  = "create"
	ActForget  = "forget"
)

// Resource is one thing the command would touch.
type Resource struct {
	Action   string `json:"action"`
	Name     string `json:"name"`
	Stateful bool   `json:"stateful,omitempty"` // holds data that is gone once deleted
}

// Finding describes one risky command.
type Finding struct {
	Tool      string     `json:"tool"`
	Command   string     `json:"command"`
	Level     Level      `json:"-"`
	Target    string     `json:"target"`           // e.g. "workspace prod"
	Labels    []string   `json:"labels,omitempty"` // matched against environment rules
	Resources []Resource `json:"resources,omitempty"`
	Notes     []string   `json:"notes,omitempty"`
}

// Result is the output of a preview command.
type Result struct {
	Stdout, Stderr []byte
	ExitCode       int
}

// Runner runs a preview command. extraEnv holds VAR=value pairs set inline
// on the command being analyzed.
type Runner func(ctx context.Context, dir string, extraEnv []string, name string, args ...string) (Result, error)

// ExecRunner runs commands on the local machine.
func ExecRunner(ctx context.Context, dir string, extraEnv []string, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.Env = append(cmd.Env, "TF_IN_AUTOMATION=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		err = nil
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return res, err
}

// Analyzer inspects shell commands.
type Analyzer struct {
	Run Runner
}

// Analyze returns a finding for every command in script that changes
// infrastructure. dir is the directory the script runs in.
func (a *Analyzer) Analyze(ctx context.Context, script, dir string) []Finding {
	var findings []Finding
	for _, c := range shell.Parse(script) {
		var f *Finding
		switch c.Program() {
		case "cd":
			dir = changeDir(dir, c.Args[1:])
		case "terraform", "tofu":
			f = a.terraform(ctx, c, dir)
		case "kubectl", "oc":
			f = a.kubectl(ctx, c, dir)
		case "helm":
			f = a.helm(ctx, c, dir)
		}
		if f != nil && f.Level > None {
			findings = append(findings, *f)
		}
	}
	return findings
}

func changeDir(dir string, args []string) string {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") || strings.Contains(args[0], "$") {
		return dir
	}
	return resolve(dir, args[0])
}

func resolve(dir, p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return filepath.Clean(p)
}

// unavailable records that no preview could be made. Without one we cannot
// rule out deletions, so the command counts as destructive.
func unavailable(f *Finding, what string, res Result, err error) {
	msg := what
	if err != nil {
		msg += ": " + err.Error()
	} else if line := firstLine(res.Stderr); line != "" {
		msg += ": " + line
	}
	f.Level = Destroy
	f.Notes = append(f.Notes, "no preview ("+msg+"), so treating this as destructive")
}

func firstLine(b []byte) string {
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "│╷╵ ")
		if line != "" {
			if len(line) > 200 {
				line = line[:200] + "..."
			}
			return line
		}
	}
	return ""
}

// flagValue returns the value of the first of names found in args, given as
// either "--name=value" or "--name value".
func flagValue(args []string, names ...string) (string, bool) {
	for i, a := range args {
		for _, n := range names {
			if a == n && i+1 < len(args) {
				return args[i+1], true
			}
			if v, ok := strings.CutPrefix(a, n+"="); ok {
				return v, true
			}
		}
	}
	return "", false
}

func hasFlag(args []string, names ...string) bool {
	for _, a := range args {
		name, _, _ := strings.Cut(a, "=")
		for _, n := range names {
			if name == n {
				return true
			}
		}
	}
	return false
}

// dropFlags removes flags in boolFlags or valueFlags from args. Flags in
// valueFlags given without "=" also drop the argument after them.
func dropFlags(args []string, boolFlags, valueFlags map[string]bool) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		name, _, hasValue := strings.Cut(args[i], "=")
		if boolFlags[name] {
			continue
		}
		if valueFlags[name] {
			if !hasValue {
				i++
			}
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func set(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}
