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
	"path/filepath"
	"sort"
	"strings"
)

const (
	// hookTimeoutSeconds is how long Claude Code waits for the hook. Previews
	// are capped below it (policy.MaxPreviewTimeout), so they end in "ask".
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
	project := fs.Bool("project", false, "use .claude/settings.json in this directory instead of your user settings")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	path, err := settingsPath(*project)
	if err != nil {
		fmt.Fprintln(stderr, "blast-radius:", err)
		return 1
	}
	var entry map[string]any
	if add {
		cmd := hookCommand(*project)
		entry = hookEntries(cmd)
		if other := otherBinary(cmd); other != "" {
			fmt.Fprintf(stderr, "Note: the hook will run %s, the blast-radius found on your PATH, not this copy (%s).\n"+
				"Replace that file with this one to use this version.\n", other, selfPath())
		}
	}
	status, err := updateSettings(path, entry)
	if err != nil {
		fmt.Fprintln(stderr, "blast-radius:", err)
		return 1
	}
	switch status {
	case "added":
		fmt.Fprintf(stdout, "Added the blast-radius hook to %s\nRestart Claude Code to load it, then run `blast-radius doctor` to check the setup.\n", path)
	case "updated":
		fmt.Fprintf(stdout, "Updated the blast-radius hook in %s\nRestart Claude Code to load it.\n", path)
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

func settingsPath(project bool) (string, error) {
	if project {
		dir, err := os.Getwd()
		return filepath.Join(dir, ".claude", "settings.json"), err
	}
	home, err := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json"), err
}

// hookCommand is the program Claude Code runs. User settings get an absolute
// path, because Claude Code started from a desktop launcher may not have the
// shell's PATH, and with onFailure "block" a hook it can't find would block
// every command. Project settings, which are shared, use the bare name.
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

// updateSettings replaces any blast-radius hooks in a Claude Code settings
// file with entries (one per hook event), or removes them when entries is
// nil, keeping everything else. It returns "added", "updated", "removed" or
// "unchanged".
func updateSettings(path string, entries map[string]any) (string, error) {
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
	for _, event := range append([]string{"PreToolUse"}, sortedKeys(recordedEvents)...) {
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
				if isOurHook(h) {
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, out); err != nil {
		return "", err
	}
	switch {
	case entries == nil:
		return "removed", nil
	case found:
		return "updated", nil
	}
	return "added", nil
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

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
