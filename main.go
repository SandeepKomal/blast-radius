// Command blast-radius stops AI coding agents from running destructive
// infrastructure commands without a human seeing what they would break.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/sandeepkomal/blast-radius/internal/analyze"
	"github.com/sandeepkomal/blast-radius/internal/policy"
	"github.com/sandeepkomal/blast-radius/internal/render"
)

// version is set by the release build. go install builds fall back to the
// module version.
var version = "dev"

func versionString() string {
	if version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
	}
	return version
}

const usage = `blast-radius shows what an infrastructure command would break before an AI agent runs it.

Usage:
  blast-radius install [--project]   add the Claude Code hook (user settings, or .claude/settings.json)
  blast-radius uninstall [--project] remove the Claude Code hook
  blast-radius check [-C dir] [--json] [--shell bash|powershell] [--agent name] <command>
                                     preview a command yourself, or from any agent's hook
  blast-radius doctor                check that the hook, config and tools are set up correctly
  blast-radius init [--user]         write a starter .blast-radius.json (or your user config)
  blast-radius config                print the policy in effect for this directory
  blast-radius ui [--addr host:port] open a local dashboard of what was checked, asked and blocked
  blast-radius audit prune|forget|export ...
                                     apply retention, erase or export entries in the audit log
  blast-radius hook claude           run as a Claude Code hook (reads JSON on stdin)
  blast-radius hook codex            run as a Codex PreToolUse hook (reads JSON on stdin)
  blast-radius version

check exits 0 when the command can run, 10 when it needs approval, 20 when it is blocked.
`

// Exit codes of `check`.
const (
	exitPass = 0
	exitAsk  = 10
	exitDeny = 20
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "hook":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "usage: blast-radius hook claude|codex")
			return 2
		}
		switch args[1] {
		case "claude":
			return hookClaude(stdin, stdout, stderr, analyze.ExecRunner)
		case "codex":
			return hookCodex(stdin, stdout, stderr, analyze.ExecRunner)
		default:
			fmt.Fprintln(stderr, "usage: blast-radius hook claude|codex")
			return 2
		}
	case "check":
		return check(args[1:], stdout, stderr, analyze.ExecRunner)
	case "install", "uninstall":
		return install(args[0] == "install", args[1:], stdout, stderr)
	case "doctor":
		return doctor(stdout, analyze.ExecRunner)
	case "init":
		return initConfig(args[1:], stdout, stderr)
	case "config":
		return printConfig(stdout, stderr)
	case "ui":
		return serveUI(args[1:], stdout, stderr)
	case "audit":
		return auditCmd(args[1:], stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "blast-radius", versionString())
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
	return 2
}

// request is one thing to check: a shell command or a file edit.
type request struct {
	tool     string // Bash, PowerShell, Edit...
	script   string
	shell    analyze.Shell
	editPath string
	dir      string
	agent    agentInfo // who asked, when the request comes from a hook
	model    string    // the agent's model, when its hook says
}

// evaluate analyzes a request and applies the policy for its directory.
func evaluate(req request, run analyze.Runner, stderr io.Writer) ([]analyze.Finding, []policy.Verdict, policy.Action) {
	cfg, cfgErr := policy.Load(req.dir)
	if cfgErr != nil {
		fmt.Fprintf(stderr, "blast-radius: %v\n", cfgErr)
	}
	a := &analyze.Analyzer{Run: run, Aliases: cfg.Aliases, Stateful: cfg.Stateful}

	var findings []analyze.Finding
	if req.editPath != "" {
		findings = a.AnalyzeFileEdit(req.editPath)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.PreviewTimeoutSeconds)*time.Second)
		defer cancel()
		if cfg.AuditLog != "" && !selfTesting {
			defer markRunning(cfg.AuditLog, req, cfg.PreviewTimeoutSeconds)()
		}
		findings = a.Analyze(ctx, req.script, req.dir, req.shell)
	}

	verdicts := make([]policy.Verdict, len(findings))
	for i, f := range findings {
		verdicts[i] = cfg.Decide(f)
		// A broken config could be hiding stricter rules, so never let it pass.
		if cfgErr != nil && verdicts[i].Action == policy.Pass {
			verdicts[i].Action = policy.Ask
			verdicts[i].Reason = "config error: " + cfgErr.Error()
		}
	}
	action := policy.Strictest(verdicts)
	if cfg.AuditLog != "" && !selfTesting && (len(findings) > 0 || cfg.AuditAll && fromAgent(req) && req.script != "") {
		if err := writeAudit(cfg.AuditLog, cfg.Redact(), req, findings, verdicts, action); err != nil {
			fmt.Fprintf(stderr, "blast-radius: audit log: %v\n", err)
		}
	}
	return findings, verdicts, action
}

// fromAgent reports whether an agent sent the request (through a hook or
// `check --agent`), rather than a person running `check` by hand.
func fromAgent(req request) bool {
	return req.agent.ToolUseID != "" || req.agent.AgentPlatform != ""
}

// agentName turns `check --agent` into the name the dashboard shows:
// lower case, at most 40 letters, digits, '-', '_' or '.'.
func agentName(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || len(s) > 40 {
		return "", false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return "", false
		}
	}
	return s, true
}

func check(args []string, stdout, stderr io.Writer, run analyze.Runner) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("C", "", "directory the command runs in (default: current directory)")
	asJSON := fs.Bool("json", false, "print findings as JSON")
	shellName := fs.String("shell", "bash", "how to parse the command: bash or powershell")
	agent := fs.String("agent", "", "record the command as run by this AI agent (for agents without a built-in hook)")
	session := fs.String("session", "", "with --agent: the agent's session or conversation ID")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "usage: blast-radius check [-C dir] [--json] [--shell bash|powershell] [--agent name] <command>")
		return 2
	}
	if *dir == "" {
		*dir, _ = os.Getwd()
	}
	req := request{tool: "check", script: strings.Join(fs.Args(), " "), dir: *dir}
	if *agent != "" {
		name, ok := agentName(*agent)
		if !ok {
			fmt.Fprintf(stderr, "invalid --agent %q: use letters, digits, '-', '_' or '.' (at most 40)\n", *agent)
			return 2
		}
		req.tool = "Bash"
		if *shellName == "powershell" || *shellName == "pwsh" {
			req.tool = "PowerShell"
		}
		req.agent = agentInfo{AgentPlatform: name, SessionID: truncate(*session)}
	}
	switch *shellName {
	case "bash":
	case "powershell", "pwsh":
		req.shell = analyze.PowerShell
	default:
		fmt.Fprintf(stderr, "unknown shell %q (use bash or powershell)\n", *shellName)
		return 2
	}

	findings, verdicts, action := evaluate(req, run, stderr)
	if *asJSON {
		type item struct {
			analyze.Finding
			Level   string         `json:"level"`
			Verdict policy.Verdict `json:"verdict"`
		}
		out := struct {
			Action   policy.Action `json:"action"`
			Findings []item        `json:"findings"`
		}{Action: action, Findings: []item{}}
		for i, f := range findings {
			out.Findings = append(out.Findings, item{f, f.Level.String(), verdicts[i]})
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintln(stderr, "blast-radius:", err)
			return 1
		}
	} else if len(findings) == 0 {
		fmt.Fprintln(stdout, "blast-radius: nothing destructive found")
	} else {
		fmt.Fprint(stdout, render.Text(findings, verdicts, isTerminal(stdout)))
	}

	switch action {
	case policy.Deny:
		return exitDeny
	case policy.Ask:
		return exitAsk
	}
	return exitPass
}

func printConfig(stdout, stderr io.Writer) int {
	dir, _ := os.Getwd()
	cfg, err := policy.Load(dir)
	if err != nil {
		fmt.Fprintln(stderr, "blast-radius:", err)
		return 1
	}
	if len(cfg.Sources) == 0 {
		fmt.Fprintf(stdout, "# source: built-in default (run `blast-radius init` to create %s)\n", policy.FileName)
	}
	for _, s := range cfg.Sources {
		fmt.Fprintln(stdout, "# source:", s)
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		fmt.Fprintln(stderr, "blast-radius:", err)
		return 1
	}
	return 0
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok || os.Getenv("NO_COLOR") != "" {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
