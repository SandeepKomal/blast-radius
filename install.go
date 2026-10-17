package main

import (
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
)

const hookTimeoutSeconds = 120

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
	changed, err := updateSettings(path, hookCommand(), add)
	if err != nil {
		fmt.Fprintln(stderr, "blast-radius:", err)
		return 1
	}
	switch {
	case add && changed:
		fmt.Fprintf(stdout, "Added the blast-radius hook to %s\nRestart Claude Code (or run /hooks) to load it.\n", path)
	case add:
		fmt.Fprintf(stdout, "blast-radius is already installed in %s\n", path)
	case changed:
		fmt.Fprintf(stdout, "Removed the blast-radius hook from %s\n", path)
	default:
		fmt.Fprintf(stdout, "blast-radius was not installed in %s\n", path)
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

// hookCommand is the command Claude Code runs: the bare name when it is on
// PATH, otherwise the absolute path of this binary.
func hookCommand() string {
	if _, err := exec.LookPath("blast-radius"); err == nil {
		return "blast-radius hook claude"
	}
	if exe, err := os.Executable(); err == nil {
		return fmt.Sprintf("%q hook claude", exe)
	}
	return "blast-radius hook claude"
}

func isOurHook(h any) bool {
	m, ok := h.(map[string]any)
	if !ok {
		return false
	}
	cmd, _ := m["command"].(string)
	return strings.Contains(cmd, "blast-radius") && strings.Contains(cmd, "hook claude")
}

// updateSettings adds or removes the PreToolUse hook entry in a Claude Code
// settings file, keeping everything else. It reports whether it changed.
func updateSettings(path, command string, add bool) (bool, error) {
	settings := map[string]any{}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if !add {
			return false, nil
		}
	case err != nil:
		return false, err
	default:
		if err := json.Unmarshal(b, &settings); err != nil {
			return false, fmt.Errorf("%s is not valid JSON: %w", path, err)
		}
	}

	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	entries, _ := hooks["PreToolUse"].([]any)

	found := false
	var kept []any
	for _, e := range entries {
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
	if found == add {
		return false, nil // already installed, or nothing to remove
	}
	if add {
		kept = append(kept, map[string]any{
			"matcher": "Bash",
			"hooks": []any{map[string]any{
				"type":    "command",
				"command": command,
				"timeout": hookTimeoutSeconds,
			}},
		})
	}

	if len(kept) > 0 {
		hooks["PreToolUse"] = kept
	} else {
		delete(hooks, "PreToolUse")
	}
	if len(hooks) > 0 {
		settings["hooks"] = hooks
	} else {
		delete(settings, "hooks")
	}

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, append(out, '\n'), 0o644)
}
