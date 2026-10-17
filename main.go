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
  blast-radius check [-C dir] [--json] <command>
                                     preview a command yourself
  blast-radius hook claude           run as a Claude Code PreToolUse hook (reads JSON on stdin)
  blast-radius config                print the policy in effect for this directory
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
		if len(args) < 2 || args[1] != "claude" {
			fmt.Fprintln(stderr, "usage: blast-radius hook claude")
			return 2
		}
		return hookClaude(stdin, stdout, stderr, analyze.ExecRunner)
	case "check":
		return check(args[1:], stdout, stderr, analyze.ExecRunner)
	case "install", "uninstall":
		return install(args[0] == "install", args[1:], stdout, stderr)
	case "config":
		return printConfig(stdout, stderr)
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

// evaluate analyzes script and applies the policy for dir.
func evaluate(script, dir string, run analyze.Runner, stderr io.Writer) ([]analyze.Finding, []policy.Verdict, policy.Action) {
	cfg, err := policy.Load(dir)
	if err != nil {
		fmt.Fprintf(stderr, "blast-radius: %v (using the built-in policy)\n", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.PreviewTimeoutSeconds)*time.Second)
	defer cancel()

	findings := (&analyze.Analyzer{Run: run}).Analyze(ctx, script, dir)
	verdicts := make([]policy.Verdict, len(findings))
	for i, f := range findings {
		verdicts[i] = cfg.Decide(f)
	}
	return findings, verdicts, policy.Strictest(verdicts)
}

func check(args []string, stdout, stderr io.Writer, run analyze.Runner) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("C", "", "directory the command runs in (default: current directory)")
	asJSON := fs.Bool("json", false, "print findings as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "usage: blast-radius check [-C dir] [--json] <command>")
		return 2
	}
	if *dir == "" {
		*dir, _ = os.Getwd()
	}
	script := strings.Join(fs.Args(), " ")

	findings, verdicts, action := evaluate(script, *dir, run, stderr)
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
		enc.Encode(out)
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
	src := cfg.Source
	if src == "" {
		src = "built-in default (create " + policy.FileName + " to change it)"
	}
	fmt.Fprintln(stdout, "# source:", src)
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	enc.Encode(cfg)
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
