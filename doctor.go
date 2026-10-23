package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sandeepkomal/blast-radius/internal/analyze"
	"github.com/sandeepkomal/blast-radius/internal/policy"
)

type checkList struct {
	w      io.Writer
	failed bool
}

func (c *checkList) ok(format string, a ...any)   { fmt.Fprintf(c.w, "ok    "+format+"\n", a...) }
func (c *checkList) warn(format string, a ...any) { fmt.Fprintf(c.w, "warn  "+format+"\n", a...) }
func (c *checkList) info(format string, a ...any) { fmt.Fprintf(c.w, "info  "+format+"\n", a...) }
func (c *checkList) fail(format string, a ...any) {
	c.failed = true
	fmt.Fprintf(c.w, "FAIL  "+format+"\n", a...)
}

// doctor checks the hook, config and tools, and runs the hook once on known
// inputs to prove it works on this machine.
func doctor(stdout io.Writer, run analyze.Runner) int {
	c := &checkList{w: stdout}
	exe, _ := os.Executable()
	c.info("blast-radius %s (%s)", versionString(), exe)

	user, _ := settingsPath(false)
	project, _ := settingsPath(true)
	installed := false
	for _, p := range []string{user, project} {
		h, err := findOurHook(p)
		switch {
		case err != nil:
			c.fail("%s: %v", p, err)
		case h == nil:
			continue
		default:
			installed = true
			checkHook(c, p, h)
			if !hasRecordingHooks(p) {
				c.info("%s has no recording hooks; run `blast-radius install` again so `blast-radius ui` can show which agent ran a command and whether it ran", p)
			}
		}
	}
	if !installed {
		c.fail("no blast-radius hook in %s or %s; run `blast-radius install`", user, project)
	}

	dir, _ := os.Getwd()
	if cfg, err := policy.Load(dir); err != nil {
		c.fail("config: %v", err)
	} else if len(cfg.Sources) == 0 {
		c.ok("config: built-in default policy")
	} else {
		c.ok("config: %s", strings.Join(cfg.Sources, ", "))
	}

	selfTest(c)
	checkClaudeVersion(c)

	for _, tool := range []string{"terraform", "tofu", "terragrunt", "kubectl", "helm"} {
		if p, err := exec.LookPath(tool); err == nil {
			c.ok("%s found at %s", tool, p)
		} else {
			c.info("%s not found (only needed if you use it)", tool)
		}
	}
	if _, err := exec.LookPath("kubectl"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if res, err := run(ctx, dir, nil, "kubectl", "config", "current-context"); err == nil && res.ExitCode == 0 {
			c.info("kube context: %s", strings.TrimSpace(string(res.Stdout)))
		}
	}

	if c.failed {
		return 1
	}
	fmt.Fprintln(stdout, "\nAll checks passed.")
	return 0
}

// minClaudeVersion is the first Claude Code release with onFailure, which
// makes a crashed or timed-out hook block the tool call.
var minClaudeVersion = [3]int{2, 1, 295}

func checkClaudeVersion(c *checkList) {
	if _, err := exec.LookPath("claude"); err != nil {
		c.info("claude not found on PATH; Claude Code v%d.%d.%d or later is needed for fail-closed hooks", minClaudeVersion[0], minClaudeVersion[1], minClaudeVersion[2])
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "claude", "--version").Output()
	if err != nil {
		c.info("could not run `claude --version`: %v", err)
		return
	}
	v, ok := parseVersion(string(out))
	switch {
	case !ok:
		c.info("could not read the Claude Code version from %q", strings.TrimSpace(string(out)))
	case lessVersion(v, minClaudeVersion):
		c.warn("Claude Code %d.%d.%d is older than %d.%d.%d: a crash or timeout of the hook lets commands through. Run `claude update`",
			v[0], v[1], v[2], minClaudeVersion[0], minClaudeVersion[1], minClaudeVersion[2])
	default:
		c.ok("Claude Code %d.%d.%d", v[0], v[1], v[2])
	}
}

// parseVersion reads the first x.y.z in s.
func parseVersion(s string) ([3]int, bool) {
	for _, field := range strings.Fields(s) {
		parts := strings.SplitN(strings.TrimPrefix(field, "v"), ".", 3)
		if len(parts) != 3 {
			continue
		}
		var v [3]int
		ok := true
		for i, p := range parts {
			n := 0
			digits := 0
			for _, r := range p {
				if r < '0' || r > '9' {
					break
				}
				n = n*10 + int(r-'0')
				digits++
			}
			if digits == 0 {
				ok = false
				break
			}
			v[i] = n
		}
		if ok {
			return v, true
		}
	}
	return [3]int{}, false
}

func lessVersion(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// binaryVersion asks another blast-radius binary for its version.
func binaryVersion(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return "version unknown"
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "blast-radius"))
}

func findOurHook(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var settings struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []any  `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &settings); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	for _, e := range settings.Hooks["PreToolUse"] {
		for _, h := range e.Hooks {
			if isOurHook(h) {
				m := h.(map[string]any)
				m["matcher"] = e.Matcher
				return m, nil
			}
		}
	}
	return nil, nil
}

func checkHook(c *checkList, path string, h map[string]any) {
	c.ok("hook installed in %s", path)
	cmd, _ := h["command"].(string)
	if filepath.IsAbs(cmd) {
		if _, err := os.Stat(cmd); err != nil {
			c.fail("hook command %s does not exist; every command will be blocked. Run `blast-radius install` again", cmd)
		}
	} else if _, err := exec.LookPath(cmd); err != nil {
		c.warn("hook command %q is not on PATH here; Claude Code may not find it either", cmd)
	}
	if other := otherBinary(cmd); other != "" {
		c.warn("the hook runs %s (%s), not this copy (%s); replace that file to use this version",
			other, binaryVersion(other), versionString())
	}
	if h["onFailure"] != "block" {
		c.warn("hook does not set onFailure: \"block\", so a crash or timeout lets commands through; run `blast-radius install` to update")
	}
	matcher, _ := h["matcher"].(string)
	for _, tool := range []string{"Bash", "PowerShell", "Edit", "Write"} {
		if !strings.Contains(matcher, tool) {
			c.warn("hook matcher %q does not cover %s; run `blast-radius install` to update", matcher, tool)
		}
	}
}

// selfTest runs the hook on inputs with known answers, without touching
// any real infrastructure.
// selfTesting is set while doctor runs sample inputs through the hook, so
// they are not recorded in the activity log as if an agent had run them.
var selfTesting bool

func selfTest(c *checkList) {
	selfTesting = true
	defer func() { selfTesting = false }()
	fake := func(context.Context, string, []string, string, ...string) (analyze.Result, error) {
		return analyze.Result{}, errors.New("not run during doctor")
	}
	cases := []struct {
		name, input, want string
	}{
		{"read-only command passes", `{"tool_name":"Bash","tool_input":{"command":"ls -la"},"cwd":"/"}`, ""},
		{"uninstalling the guard asks", `{"tool_name":"Bash","tool_input":{"command":"blast-radius uninstall"},"cwd":"/"}`, "ask"},
		{"editing hook settings asks", `{"tool_name":"Edit","tool_input":{"file_path":"/x/.claude/settings.json"},"cwd":"/"}`, "ask"},
		{"unreadable input asks", `not json`, "ask"},
	}
	for _, tc := range cases {
		var out bytes.Buffer
		hookClaude(strings.NewReader(tc.input), &out, io.Discard, fake)
		got := ""
		if out.Len() > 0 {
			var o hookOutput
			if err := json.Unmarshal(out.Bytes(), &o); err != nil {
				c.fail("self-test %s: bad output %q", tc.name, out.String())
				continue
			}
			got = o.HookSpecificOutput.PermissionDecision
		}
		if got != tc.want {
			c.fail("self-test %s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	c.ok("self-test: hook decisions are correct")
}

const projectTemplate = `{
  "$schema": "` + policy.SchemaURL + `",
  "environments": [
    { "name": "production", "match": ["*prod*", "*prd*"], "change": "ask", "destroy": "deny" }
  ],
  "protect": []
}
`

const userTemplate = `{
  "$schema": "` + policy.SchemaURL + `",
  "default": { "change": "pass", "destroy": "ask" },
  "environments": [
    { "name": "production", "match": ["*prod*", "*prd*"], "change": "ask", "destroy": "deny" },
    { "name": "sandbox", "match": ["kind-*", "minikube", "docker-desktop"], "destroy": "pass" }
  ],
  "protect": [],
  "preview_timeout_seconds": 90,
  "audit_log": "~/.local/state/blast-radius/audit.jsonl",
  "audit_all": true,
  "audit_retention_days": 90
}
`

func initConfig(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	user := fs.Bool("user", false, "write your user config instead of .blast-radius.json in this directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	// A repository's .blast-radius.json is meant to be committed; the user
	// config can name production accounts and clusters, so only its owner
	// may read it.
	path, content, dirMode, fileMode := policy.FileName, projectTemplate, os.FileMode(0o755), os.FileMode(0o644)
	if *user {
		p, err := policy.UserConfigPath()
		if err != nil {
			fmt.Fprintln(stderr, "blast-radius:", err)
			return 1
		}
		path, content, dirMode, fileMode = p, userTemplate, 0o700, 0o600
	}
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(stderr, "blast-radius: %s already exists\n", path)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		fmt.Fprintln(stderr, "blast-radius:", err)
		return 1
	}
	if err := os.WriteFile(path, []byte(content), fileMode); err != nil {
		fmt.Fprintln(stderr, "blast-radius:", err)
		return 1
	}
	fmt.Fprintf(stdout, "Wrote %s\n", path)
	return 0
}

// hasRecordingHooks reports whether a settings file has the hooks that
// record what happened after a check (added by newer installs).
func hasRecordingHooks(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var settings struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []any  `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(b, &settings) != nil {
		return false
	}
	for event, matcher := range recordedEvents {
		ok := false
		for _, e := range settings.Hooks[event] {
			for _, h := range e.Hooks {
				// A different matcher is an install from an older version.
				ok = ok || isOurHook(h) && e.Matcher == matcher
			}
		}
		if !ok {
			return false
		}
	}
	return true
}
