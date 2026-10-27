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
	"runtime"
	"strings"
	"time"

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
	Action    string `json:"action"`
	Name      string `json:"name"`                // terraform address, or kind/name for Kubernetes
	Namespace string `json:"namespace,omitempty"` // Kubernetes namespace
	Stateful  bool   `json:"stateful,omitempty"`  // holds data that is gone once deleted
}

// Lost reports whether the resource is deleted, including by replacement.
func (r Resource) Lost() bool { return r.Action == ActDestroy || r.Action == ActReplace }

// Finding describes one risky command.
type Finding struct {
	Tool      string     `json:"tool"`
	Command   string     `json:"command"`
	Level     Level      `json:"-"`
	Guard     bool       `json:"guard,omitempty"`  // changes blast-radius's own settings
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

// Runner runs a preview command. env holds VAR=value pairs that the analyzed
// script sets (inline or with export) on top of the current environment.
type Runner func(ctx context.Context, dir string, env []string, name string, args ...string) (Result, error)

// ExecRunner runs commands on the local machine. On timeout it kills the
// whole process tree, so a hung provider plugin cannot stall the hook.
func ExecRunner(ctx context.Context, dir string, env []string, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Env = append(cmd.Env, "TF_IN_AUTOMATION=1", "TF_INPUT=0")
	cmd.Stdin = nil
	cmd.WaitDelay = 2 * time.Second
	killProcessGroup(cmd)
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
	// Aliases maps extra program names to the tool they run, e.g. "tf": "terraform".
	Aliases map[string]string
	// Stateful lists extra terraform resource types and Kubernetes kinds whose
	// deletion loses data.
	Stateful []string
}

// Shell selects how a command line is parsed.
type Shell int

const (
	Bash Shell = iota
	PowerShell
)

// state is what earlier commands in the same script changed.
type state struct {
	dir        string
	env        []string          // VAR=value set with export
	kubeCtx    string            // set with kubectl config use-context
	workspaces map[string]string // terraform working dir -> workspace selected
	// pulumi project dir -> stack selected
	pulumiStacks   map[string]string
	gcloudProject  string // set with gcloud config set project
	azSubscription string // set with az account set
}

func (s *state) envFor(c shell.Command) []string {
	return append(append([]string{}, s.env...), c.Env...)
}

// lookup finds key in the script's environment, then the process environment.
func lookup(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], key+"="); ok {
			return v
		}
	}
	return os.Getenv(key)
}

// Analyze returns a finding for every command in script that changes
// infrastructure or blast-radius's own settings. dir is the directory the
// script runs in.
func (a *Analyzer) Analyze(ctx context.Context, script, dir string, sh Shell) []Finding {
	cmds := shell.Parse(script)
	if sh == PowerShell {
		cmds = shell.ParsePowerShell(script)
	}
	st := &state{dir: dir, workspaces: map[string]string{}, pulumiStacks: map[string]string{}}
	var findings []Finding
	for _, c := range cmds {
		prog := c.Program()
		if real, ok := a.Aliases[prog]; ok {
			c.Args = append([]string{real}, c.Args[1:]...)
			prog = real
		}
		var f *Finding
		switch prog {
		case "cd", "pushd", "set-location", "sl", "chdir":
			st.dir = changeDir(st.dir, c.Args[1:])
		case "export":
			st.export(c.Args[1:])
		case "unset":
			st.unset(c.Args[1:])
		case "terraform", "tofu":
			f = a.terraform(ctx, c, st)
		case "terragrunt":
			f = a.terragrunt(ctx, c, st)
		case "kubectl", "oc":
			f = a.kubectl(ctx, c, st)
		case "kubectx":
			if len(c.Args) == 2 && !strings.HasPrefix(c.Args[1], "-") {
				st.kubeCtx = c.Args[1]
			}
		case "helm":
			f = a.helm(ctx, c, st)
		case "pulumi":
			f = a.pulumi(ctx, c, st)
		case "aws":
			f = a.aws(c, st)
		case "gcloud":
			f = a.gcloud(c, st)
		case "gsutil":
			f = a.gsutil(c, st)
		case "az":
			f = a.az(c, st)
		case "ssh":
			findings = append(findings, a.remote(ctx, c, st)...)
			continue
		default:
			if setPowerShellEnv(st, c) {
				continue
			}
		}
		if f == nil {
			f = guardCommand(c, prog, st.dir)
		}
		if f != nil && (f.Level > None || f.Guard) {
			findings = append(findings, *f)
		}
	}
	return findings
}

// AnalyzeFileEdit checks a file that Claude's Edit or Write tool is about to
// change.
func (a *Analyzer) AnalyzeFileEdit(path string) []Finding {
	if !protectedPath(path) {
		return nil
	}
	return []Finding{guardFinding("edit "+path, "edits a file that keeps blast-radius switched on")}
}

func (s *state) export(args []string) {
	for _, a := range args {
		if k, _, ok := strings.Cut(a, "="); ok && !strings.HasPrefix(a, "-") && k != "" {
			s.env = append(s.env, a)
		}
	}
}

func (s *state) unset(args []string) {
	for _, a := range args {
		s.env = append(s.env, a+"=")
	}
}

// setPowerShellEnv handles `$env:NAME = value` and `$env:NAME=value`.
func setPowerShellEnv(s *state, c shell.Command) bool {
	name, ok := strings.CutPrefix(c.Args[0], "$env:")
	if !ok {
		return false
	}
	if k, v, ok := strings.Cut(name, "="); ok {
		s.env = append(s.env, k+"="+v)
		return true
	}
	if len(c.Args) >= 3 && c.Args[1] == "=" {
		s.env = append(s.env, name+"="+c.Args[2])
	}
	return true
}

func changeDir(dir string, args []string) string {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		args = args[1:]
	}
	if len(args) == 0 || strings.Contains(args[0], "$") {
		return dir
	}
	return resolve(dir, args[0])
}

// msysDrive turns a Git Bash (MSYS) path such as /c/Users/me into C:/Users/me.
func msysDrive(p string) (string, bool) {
	if len(p) < 2 || p[0] != '/' || !isLetter(p[1]) || (len(p) > 2 && p[2] != '/') {
		return p, false
	}
	return strings.ToUpper(p[1:2]) + ":/" + strings.TrimPrefix(p[2:], "/"), true
}

func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func resolve(dir, p string) string {
	if runtime.GOOS == "windows" {
		if w, ok := msysDrive(p); ok {
			p = w
		}
	}
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

func (a *Analyzer) stateful(kind string) bool {
	if tfStateful[kind] || kubeStateful[kind] {
		return true
	}
	for _, s := range a.Stateful {
		if strings.EqualFold(s, kind) {
			return true
		}
	}
	return false
}

// unavailable records that no preview could be made. Without one we cannot
// rule out deletions, so the command counts as destructive.
func unavailable(f *Finding, what string, res Result, err error) {
	msg := what
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		msg += ": timed out"
	case err != nil:
		msg += ": " + err.Error()
	default:
		if line := firstLine(res.Stderr); line != "" {
			msg += ": " + line
		}
	}
	f.Level = Destroy
	f.Notes = append(f.Notes, "no preview ("+msg+"), so treating this as destructive")
}

func firstLine(b []byte) string {
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "│╷╵ ")
		if line != "" {
			if r := []rune(line); len(r) > 200 {
				line = string(r[:200]) + "..."
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

// positionals returns the arguments that are not flags, skipping the values
// of flags in valueFlags given without "=".
func positionals(args []string, valueFlags map[string]bool) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "-") {
			out = append(out, args[i])
			continue
		}
		if !strings.Contains(args[i], "=") && valueFlags[args[i]] {
			i++
		}
	}
	return out
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

// sshValueFlags are the ssh options that take a separate value.
var sshValueFlags = set("-b", "-c", "-D", "-E", "-e", "-F", "-I", "-i", "-J", "-L", "-l", "-m", "-O",
	"-o", "-p", "-Q", "-R", "-S", "-W", "-w", "-B", "-P")

// remote checks the command an `ssh host command` runs on another machine.
// Nothing there can be previewed from here, so infrastructure commands are
// rated as if their preview had failed.
func (a *Analyzer) remote(ctx context.Context, c shell.Command, st *state) []Finding {
	args := c.Args[1:]
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		if sshValueFlags[args[i]] {
			i++
		}
		i++
	}
	if i+1 >= len(args) {
		return nil
	}
	host, script := args[i], strings.Join(args[i+1:], " ")
	sub := &Analyzer{Run: noPreview(host), Aliases: a.Aliases, Stateful: a.Stateful}
	findings := sub.Analyze(ctx, script, st.dir, Bash)
	for i := range findings {
		f := &findings[i]
		f.Command = "ssh " + host + " " + f.Command
		f.Target = "host " + host + ", " + f.Target
		f.Labels = append(f.Labels, host)
	}
	return findings
}

func noPreview(host string) Runner {
	return func(context.Context, string, []string, string, ...string) (Result, error) {
		return Result{}, errors.New("runs on " + host)
	}
}
