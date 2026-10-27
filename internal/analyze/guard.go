package analyze

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/sandeepkomal/blast-radius/internal/shell"
)

// Programs that only read the files they are given, or change directory.
var readOnly = set("cat", "less", "more", "head", "tail", "grep", "egrep", "fgrep", "rg", "ag", "jq", "yq",
	"ls", "stat", "file", "wc", "diff", "bat", "batcat", "view", "md5sum", "sha256sum", "shasum",
	"get-content", "gc", "type", "select-string", "test-path", "get-item", "get-childitem", "code", ":", "echo",
	"cd", "pushd", "popd", "set-location", "sl", "chdir")

var readOnlyGit = set("diff", "log", "show", "status", "add", "blame", "ls-files", "grep")

// hookFiles are the files, for every agent blast-radius supports, that hold
// its hooks or can switch hooks off, and blast-radius's own user config.
// Removing or moving a folder that holds one of them (".codex", ".github")
// counts as changing it.
var hookFiles = []string{
	".claude/settings.json", ".claude/settings.local.json",
	".codex/hooks.json", ".codex/config.toml",
	".gemini/settings.json",
	".copilot/settings.json", ".copilot/hooks/blast-radius.json", ".github/hooks/blast-radius.json",
	".config/blast-radius/config.json",
}

// homeHookFiles are the hook files in folders that CODEX_HOME and
// COPILOT_HOME move, relative to that folder.
var homeHookFiles = map[string][]string{
	"CODEX_HOME":   {"hooks.json", "config.toml"},
	"COPILOT_HOME": {"settings.json", "hooks/blast-radius.json"},
}

// normalPath puts p in the form protectedPath compares: slashes, no quotes,
// no "./", "//" or "..", lower case as on macOS and Windows file systems,
// and without the trailing dots, spaces and "::$DATA" that Windows ignores.
func normalPath(p string) string {
	// Backslashes are path separators in PowerShell and cmd on every OS.
	p = strings.ReplaceAll(filepath.ToSlash(strings.Trim(p, `"'`)), `\`, "/")
	p = strings.ToLower(path.Clean(p))
	p = strings.TrimSuffix(p, "::$data")
	return strings.TrimRight(p, ". ")
}

// protectedPath reports whether p is, or is a folder holding, a file that
// keeps blast-radius switched on: an agent's settings, which hold the hook,
// or blast-radius's own config.
func protectedPath(p string) bool {
	p = normalPath(p)
	if path.Base(p) == ".blast-radius.json" || strings.HasSuffix(p, "blast-radius/config.json") {
		return true
	}
	for _, f := range hookFiles {
		for ; f != "."; f = path.Dir(f) {
			if p == f || strings.HasSuffix(p, "/"+f) {
				return true
			}
		}
	}
	for env, files := range homeHookFiles {
		name := strings.ToLower(env)
		homes := []string{"$" + name, "${" + name + "}", "$env:" + name}
		if dir := os.Getenv(env); dir != "" {
			homes = append(homes, normalPath(dir))
		}
		for _, home := range homes {
			for _, f := range files {
				for ; f != "."; f = path.Dir(f) {
					if p == home+"/"+f {
						return true
					}
				}
			}
			if p == home {
				return true
			}
		}
	}
	return false
}

// protectedArg reports whether a command's argument names a protected file,
// as written or resolved against the directory the command runs in.
func protectedArg(a, dir string) bool {
	if protectedPath(a) {
		return true
	}
	if dir == "" || filepath.IsAbs(a) || strings.HasPrefix(a, "~") || strings.HasPrefix(a, "$") || strings.HasPrefix(a, "-") {
		return false
	}
	return protectedPath(filepath.Join(dir, a))
}

func guardFinding(command, note string) Finding {
	return Finding{
		Tool:    "blast-radius",
		Command: command,
		Guard:   true,
		Target:  "blast-radius settings",
		Notes:   []string{note},
	}
}

// guardCommand flags commands that would switch blast-radius off: uninstalling
// it, or writing to its config or to the agent settings that hold the hook.
// dir is the directory the command runs in.
func guardCommand(c shell.Command, prog, dir string) *Finding {
	if prog == "blast-radius" && len(c.Args) > 1 && c.Args[1] == "uninstall" {
		f := guardFinding(c.String(), "removes the blast-radius hook")
		return &f
	}
	for _, r := range c.Redirects {
		if protectedArg(r, dir) {
			f := guardFinding(c.String(), "writes to "+r+", which keeps blast-radius switched on")
			return &f
		}
	}
	if readOnly[prog] || (prog == "git" && len(c.Args) > 1 && readOnlyGit[c.Args[1]]) {
		return nil
	}
	for _, a := range c.Args[1:] {
		if protectedArg(a, dir) {
			f := guardFinding(c.String(), "changes "+a+", which keeps blast-radius switched on")
			return &f
		}
	}
	return nil
}
