package analyze

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/sandeepkomal/blast-radius/internal/shell"
)

// Programs that only read the files they are given.
var readOnly = set("cat", "less", "more", "head", "tail", "grep", "egrep", "fgrep", "rg", "ag", "jq", "yq",
	"ls", "stat", "file", "wc", "diff", "bat", "batcat", "view", "md5sum", "sha256sum", "shasum",
	"get-content", "gc", "type", "select-string", "test-path", "get-item", "get-childitem", "code", ":", "echo")

var readOnlyGit = set("diff", "log", "show", "status", "add", "blame", "ls-files", "grep")

// protectedPath reports whether p is a file that keeps blast-radius switched
// on: Claude Code settings, which hold the hook, or blast-radius's own config.
func protectedPath(p string) bool {
	p = filepath.ToSlash(strings.Trim(p, `"'`))
	switch {
	case path.Base(p) == ".blast-radius.json":
		return true
	case strings.HasSuffix(p, ".claude/settings.json"), strings.HasSuffix(p, ".claude/settings.local.json"):
		return true
	case strings.HasSuffix(p, "blast-radius/config.json"):
		return true
	}
	return false
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
// it, or writing to its config or to the Claude Code settings that hold the
// hook.
func guardCommand(c shell.Command, prog string) *Finding {
	if prog == "blast-radius" && len(c.Args) > 1 && c.Args[1] == "uninstall" {
		f := guardFinding(c.String(), "removes the blast-radius hook")
		return &f
	}
	for _, r := range c.Redirects {
		if protectedPath(r) {
			f := guardFinding(c.String(), "writes to "+r+", which keeps blast-radius switched on")
			return &f
		}
	}
	if readOnly[prog] || (prog == "git" && len(c.Args) > 1 && readOnlyGit[c.Args[1]]) {
		return nil
	}
	for _, a := range c.Args[1:] {
		if protectedPath(a) {
			f := guardFinding(c.String(), "changes "+a+", which keeps blast-radius switched on")
			return &f
		}
	}
	return nil
}
