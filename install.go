package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	// hookTimeoutSeconds is how long the agent waits for the hook. Previews
	// are capped below it (policy.MaxPreviewTimeout), so they end in "ask"
	// rather than in a timeout, which Codex, Gemini CLI and Copilot let
	// through.
	hookTimeoutSeconds = 300
	// hookMatcher covers shell commands on every platform, and file edits so
	// changes to blast-radius's own settings need approval.
	hookMatcher = "Bash|PowerShell|Edit|Write|MultiEdit|NotebookEdit"
)

func install(add bool, args []string, stdout, stderr io.Writer) int {
	name := "install"
	if !add {
		name = "uninstall"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	agent := fs.String("agent", "claude", "the agent: claude, codex, gemini or copilot")
	project := fs.Bool("project", false, "use this repository's settings instead of your user settings")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	t, ok := hookTargets[*agent]
	if !ok {
		fmt.Fprintf(stderr, "unknown --agent %q (use claude, codex, gemini or copilot)\n", *agent)
		return 2
	}

	path, err := t.path(*project)
	if err != nil {
		fmt.Fprintln(stderr, "blast-radius:", err)
		return 1
	}
	var entries map[string]any
	if add {
		cmd := hookCommand(*project)
		entries = t.entries(cmd, *project)
		if other := otherBinary(cmd); other != "" {
			fmt.Fprintf(stderr, "Note: the hook will run %s, the blast-radius found on your PATH, not this copy (%s).\n"+
				"Replace that file with this one to use this version.\n", other, selfPath())
		}
	}
	update := updateSettings
	if t.ownFile {
		update = updateOwnFile
	}
	status, err := update(path, t, entries)
	if err != nil {
		fmt.Fprintln(stderr, "blast-radius:", err)
		return 1
	}
	switch status {
	case "added":
		fmt.Fprintf(stdout, "Added the blast-radius hook to %s\n%s\n", path, t.next)
	case "updated":
		fmt.Fprintf(stdout, "Updated the blast-radius hook in %s\n%s\n", path, t.next)
	case "removed":
		fmt.Fprintf(stdout, "Removed the blast-radius hook from %s\n", path)
	case "unchanged":
		if add {
			fmt.Fprintf(stdout, "blast-radius is already installed in %s\n", path)
		} else {
			fmt.Fprintf(stdout, "blast-radius was not installed in %s\n", path)
		}
	}
	return 0
}

// hookTarget is an agent blast-radius can add its hooks to.
type hookTarget struct {
	name string
	// path is the file that holds the hooks, for the user or this repository.
	path func(project bool) (string, error)
	// entries returns blast-radius's entry for each hook event, keyed by
	// event, for a hook that runs command.
	entries func(command string, project bool) map[string]any
	// ours reports whether a hook in the file is blast-radius's.
	ours func(h any) bool
	// ownFile is set when the agent reads every file in a hooks folder, so
	// blast-radius keeps its hooks in a file of its own.
	ownFile bool
	// next tells the user what to do after installing.
	next string
}

var hookTargets = map[string]hookTarget{
	"claude": {
		name:    "Claude Code",
		path:    settingsPath,
		entries: func(command string, _ bool) map[string]any { return hookEntries(command) },
		ours:    isOurHook,
		next:    "Restart Claude Code to load it, then run `blast-radius doctor` to check the setup.",
	},
	"codex": {
		name:    "Codex",
		path:    codexHooksPath,
		entries: codexEntries,
		ours:    shellHookIs("codex"),
		next: "Restart Codex and open /hooks to review and trust the blast-radius hooks: Codex does not run a hook until you trust it, " +
			"and asks again whenever the hook changes.",
	},
	"gemini": {
		name:    "Gemini CLI",
		path:    homeOrProject(".gemini", "settings.json"),
		entries: geminiEntries,
		ours:    shellHookIs("gemini"),
		next:    "Restart Gemini CLI to load it, and check it with /hooks.",
	},
	"copilot": {
		name:    "GitHub Copilot CLI",
		path:    copilotHooksPath,
		entries: copilotEntries,
		ours:    shellHookIs("copilot"),
		ownFile: true,
		next:    "Restart Copilot CLI to load it.",
	},
}

// homeOrProject returns a path function for a settings file kept in a
// folder of the user's home directory, or of the current directory.
func homeOrProject(dir, file string) func(bool) (string, error) {
	return func(project bool) (string, error) {
		base, err := os.UserHomeDir()
		if project {
			base, err = os.Getwd()
		}
		return filepath.Join(base, dir, file), err
	}
}

// codexHooksPath is Codex's hooks.json, in $CODEX_HOME (default ~/.codex) or
// the repository's .codex folder.
func codexHooksPath(project bool) (string, error) {
	if home := os.Getenv("CODEX_HOME"); home != "" && !project {
		return filepath.Join(home, "hooks.json"), nil
	}
	return homeOrProject(".codex", "hooks.json")(project)
}

// copilotHooksPath is blast-radius's own file in Copilot's hooks folder:
// $COPILOT_HOME/hooks (default ~/.copilot/hooks) or .github/hooks.
func copilotHooksPath(project bool) (string, error) {
	if home := os.Getenv("COPILOT_HOME"); home != "" && !project {
		return filepath.Join(home, "hooks", "blast-radius.json"), nil
	}
	if project {
		return homeOrProject(".github", filepath.Join("hooks", "blast-radius.json"))(true)
	}
	return homeOrProject(".copilot", filepath.Join("hooks", "blast-radius.json"))(false)
}

// codexEntries checks shell commands and file edits (apply_patch) before
// they run, and records sessions, requests and results for the dashboard.
// Codex runs hooks through the shell.
func codexEntries(command string, _ bool) map[string]any {
	hook := func(timeout int) []any {
		return []any{map[string]any{"type": "command", "command": shellCommand(command, false, "hook", "codex"), "timeout": timeout}}
	}
	pre := hook(hookTimeoutSeconds)
	pre[0].(map[string]any)["statusMessage"] = "blast-radius: previewing what this command changes"
	return map[string]any{
		"PreToolUse":       map[string]any{"matcher": "Bash|apply_patch", "hooks": pre},
		"PostToolUse":      map[string]any{"matcher": "Bash", "hooks": hook(10)},
		"SessionStart":     map[string]any{"hooks": hook(10)},
		"UserPromptSubmit": map[string]any{"hooks": hook(10)},
	}
}

// geminiEntries checks shell commands and file writes before they run.
// Gemini CLI runs hooks through the shell and counts timeouts in ms.
func geminiEntries(command string, _ bool) map[string]any {
	return map[string]any{
		"BeforeTool": map[string]any{
			"matcher": "run_shell_command|write_file|replace",
			"hooks": []any{map[string]any{
				"name": "blast-radius", "type": "command",
				"command": shellCommand(command, true, "hook", "gemini"), "timeout": hookTimeoutSeconds * 1000,
			}},
		},
	}
}

// copilotEntries checks shell commands and file edits. The user's hook runs
// the binary directly; a repository's also has to work in the cloud agent,
// which only runs hooks through bash.
func copilotEntries(command string, project bool) map[string]any {
	h := map[string]any{"type": "command", "matcher": "Bash|Edit|Write", "timeoutSec": hookTimeoutSeconds}
	if project {
		h["command"] = shellCommand(command, true, "hook", "copilot")
	} else {
		h["exec"], h["args"] = command, []any{"hook", "copilot"}
	}
	return map[string]any{"version": 1, "hooks": map[string]any{"PreToolUse": []any{h}}}
}

// shellCommand quotes program, if it needs it, for the shell the agent runs
// hooks with, and appends args, which need no quoting. Elsewhere that is sh;
// on Windows, Codex uses cmd.exe and Gemini CLI PowerShell, where a quoted
// program name needs the call operator.
func shellCommand(program string, powershell bool, args ...string) string {
	switch {
	case runtime.GOOS == "windows" && powershell:
		program = "& '" + strings.ReplaceAll(program, "'", "''") + "'"
	case runtime.GOOS == "windows":
		if strings.ContainsAny(program, " &()[]{}^=;!'+,`~%") {
			program = `"` + program + `"`
		}
	case strings.ContainsAny(program, " \t\n'\"$`\\&|;<>()*?[]{}#~%!"):
		program = "'" + strings.ReplaceAll(program, "'", `'\''`) + "'"
	}
	return strings.Join(append([]string{program}, args...), " ")
}

// shellHookIs returns a matcher for blast-radius's hook for agent: a command
// line running blast-radius with "hook <agent>", or an exec hook with those
// arguments.
func shellHookIs(agent string) func(any) bool {
	return func(h any) bool {
		m, ok := h.(map[string]any)
		if !ok {
			return false
		}
		if exe, ok := m["exec"].(string); ok {
			args, _ := m["args"].([]any)
			return isBlastRadius(exe) && len(args) == 2 && args[0] == "hook" && args[1] == agent
		}
		cmd, _ := m["command"].(string)
		return strings.HasSuffix(strings.TrimSpace(cmd), " hook "+agent) && isBlastRadius(hookProgram(m, agent))
	}
}

// isBlastRadius reports whether program is a blast-radius binary: named
// so, or this one under another name.
func isBlastRadius(program string) bool {
	base := strings.ToLower(path.Base(strings.ReplaceAll(program, `\`, "/")))
	return strings.TrimSuffix(base, ".exe") == "blast-radius" || sameFile(program, selfPath())
}

func settingsPath(project bool) (string, error) {
	if project {
		dir, err := os.Getwd()
		return filepath.Join(dir, ".claude", "settings.json"), err
	}
	home, err := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json"), err
}

// hookCommand is the program the agent runs. User settings get an absolute
// path, because an agent started from a desktop launcher may not have the
// shell's PATH: Claude Code (onFailure "block") would then block every
// command, and the other agents would silently skip the hook. Project
// settings, which are shared, use the bare name.
func hookCommand(project bool) string {
	if project {
		return "blast-radius"
	}
	if p, err := exec.LookPath("blast-radius"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
	}
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "blast-radius"
}

// hookEntries returns the settings entry for each hook event, keyed by
// event. PreToolUse checks commands and fails closed; the other events are
// only recorded for the dashboard, so a failure there must not block.
func hookEntries(command string) map[string]any {
	entries := map[string]any{
		"PreToolUse": map[string]any{
			"matcher": hookMatcher,
			"hooks": []any{map[string]any{
				"type":      "command",
				"command":   command,
				"args":      []any{"hook", "claude"},
				"timeout":   hookTimeoutSeconds,
				"onFailure": "block",
			}},
		},
	}
	for event, matcher := range recordedEvents {
		e := map[string]any{"hooks": []any{map[string]any{
			"type":    "command",
			"command": command,
			"args":    []any{"hook", "claude"},
			"timeout": 10,
		}}}
		if matcher != "" {
			e["matcher"] = matcher
		}
		entries[event] = e
	}
	return entries
}

func isOurHook(h any) bool {
	m, ok := h.(map[string]any)
	if !ok {
		return false
	}
	cmd, _ := m["command"].(string)
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(filepath.ToSlash(cmd))), ".exe")
	if args, ok := m["args"].([]any); ok && len(args) > 0 && args[0] == "hook" && base == "blast-radius" {
		return true
	}
	return strings.Contains(cmd, "blast-radius") && strings.Contains(cmd, "hook claude")
}

// updateSettings replaces any blast-radius hooks in an agent's settings
// file with entries (one per hook event), or removes them when entries is
// nil, keeping everything else. It returns "added", "updated", "removed" or
// "unchanged".
func updateSettings(path string, t hookTarget, entries map[string]any) (string, error) {
	settings := map[string]any{}
	before, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if entries == nil {
			return "unchanged", nil
		}
	case err != nil:
		return "", err
	default:
		if err := json.Unmarshal(before, &settings); err != nil {
			return "", fmt.Errorf("%s is not valid JSON: %w", path, err)
		}
	}

	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}

	found := false
	for _, event := range sortedKeys(t.entries("blast-radius", false)) {
		existing, _ := hooks[event].([]any)
		var kept []any
		for _, e := range existing {
			m, ok := e.(map[string]any)
			inner, _ := m["hooks"].([]any)
			if !ok {
				kept = append(kept, e)
				continue
			}
			var rest []any
			for _, h := range inner {
				if t.ours(h) {
					found = true
				} else {
					rest = append(rest, h)
				}
			}
			if len(rest) == len(inner) {
				kept = append(kept, e)
			} else if len(rest) > 0 {
				m["hooks"] = rest
				kept = append(kept, m)
			}
		}
		if e, ok := entries[event]; ok {
			kept = append(kept, e)
		}
		if len(kept) > 0 {
			hooks[event] = kept
		} else {
			delete(hooks, event)
		}
	}
	if entries == nil && !found {
		return "unchanged", nil
	}
	if len(hooks) > 0 {
		settings["hooks"] = hooks
	} else {
		delete(settings, "hooks")
	}

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", err
	}
	out = append(out, '\n')
	if bytes.Equal(normalize(before), normalize(out)) {
		return "unchanged", nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, out); err != nil {
		return "", err
	}
	return writtenStatus(entries == nil, found), nil
}

func writtenStatus(removed, found bool) string {
	switch {
	case removed:
		return "removed"
	case found:
		return "updated"
	}
	return "added"
}

// updateOwnFile writes blast-radius's own hooks file, entries being the
// whole file, or deletes it when entries is nil. A file of that name that
// isn't blast-radius's is left alone.
func updateOwnFile(path string, t hookTarget, entries map[string]any) (string, error) {
	before, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if exists && !ownFileIsOurs(before, t) {
		return "", fmt.Errorf("%s exists and has no blast-radius hook; move it away first", path)
	}
	if entries == nil {
		if !exists {
			return "unchanged", nil
		}
		return "removed", os.Remove(path)
	}
	out, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return "", err
	}
	out = append(out, '\n')
	if exists && bytes.Equal(normalize(before), normalize(out)) {
		return "unchanged", nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, out); err != nil {
		return "", err
	}
	return writtenStatus(false, exists), nil
}

// ownFileIsOurs reports whether a hooks file holds a blast-radius hook.
func ownFileIsOurs(b []byte, t hookTarget) bool {
	var file struct {
		Hooks map[string][]any `json:"hooks"`
	}
	if json.Unmarshal(b, &file) != nil {
		return false
	}
	for _, hooks := range file.Hooks {
		for _, h := range hooks {
			if t.ours(h) {
				return true
			}
		}
	}
	return false
}

// normalize re-encodes JSON so formatting differences don't count as changes.
func normalize(b []byte) []byte {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return b
	}
	out, _ := json.Marshal(v)
	return out
}

// writeFileAtomic writes through a temp file so a crash can't leave Claude
// Code with half a settings file.
func writeFileAtomic(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// selfPath is the running binary with symlinks resolved.
func selfPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		return real
	}
	return exe
}

// otherBinary returns the file the hook command runs if it is not the
// running binary, or "" if it is the same or can't be found.
func otherBinary(command string) string {
	p := command
	if !filepath.IsAbs(p) {
		found, err := exec.LookPath(p)
		if err != nil {
			return ""
		}
		p = found
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return ""
	}
	self := selfPath()
	if self == "" || sameFile(real, self) {
		return ""
	}
	return real
}

func sameFile(a, b string) bool {
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
