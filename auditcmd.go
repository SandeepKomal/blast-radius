package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sandeepkomal/blast-radius/internal/policy"
)

const auditUsage = `usage:
  blast-radius audit prune [--older-than 30d] [--dry-run]
      delete entries older than a number of days (default: audit_retention_days)
  blast-radius audit forget (--match TEXT | --session ID) [--dry-run]
      delete every entry that mentions TEXT, or that comes from a session
  blast-radius audit export (--match TEXT | --session ID)
      print those entries as JSON lines, for example to answer an access request
`

// auditCmd manages the audit log: retention, erasure and access requests.
func auditCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, auditUsage)
		return 2
	}
	fs := flag.NewFlagSet("audit "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	older := fs.String("older-than", "", "age such as 30d or 12h")
	match := fs.String("match", "", "text to look for, ignoring case")
	session := fs.String("session", "", "Claude Code session id")
	dry := fs.Bool("dry-run", false, "only count what would be deleted")
	logFlag := fs.String("log", "", "audit log (default: audit_log from your config)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	dir, _ := os.Getwd()
	cfg, err := policy.Load(dir)
	if err != nil {
		fmt.Fprintln(stderr, "blast-radius:", err)
		return 1
	}
	path := *logFlag
	if path == "" {
		path = cfg.AuditLog
	}
	if path == "" {
		fmt.Fprintln(stderr, "blast-radius: no audit log configured (audit_log in your user config)")
		return 1
	}
	if path, err = expandHome(path); err != nil {
		fmt.Fprintln(stderr, "blast-radius:", err)
		return 1
	}

	var drop func(line []byte, e auditLine) bool
	switch args[0] {
	case "prune":
		age := time.Duration(cfg.AuditRetentionDays) * 24 * time.Hour
		if *older != "" {
			if age, err = parseAge(*older); err != nil {
				fmt.Fprintln(stderr, "blast-radius:", err)
				return 2
			}
		}
		if age <= 0 {
			fmt.Fprintln(stderr, "blast-radius: give --older-than or set audit_retention_days")
			return 2
		}
		cutoff := time.Now().Add(-age)
		drop = func(_ []byte, e auditLine) bool { return !e.Time.IsZero() && e.Time.Before(cutoff) }
	case "forget", "export":
		if (*match == "") == (*session == "") {
			fmt.Fprintf(stderr, "blast-radius: audit %s needs exactly one of --match or --session\n", args[0])
			return 2
		}
		drop = matcher(*match, *session)
	default:
		fmt.Fprint(stderr, auditUsage)
		return 2
	}

	if args[0] == "export" {
		n := 0
		err := eachLine(path, func(line []byte, e auditLine) error {
			if !drop(line, e) {
				return nil
			}
			n++
			_, err := stdout.Write(append(line, '\n'))
			return err
		})
		if err != nil {
			fmt.Fprintln(stderr, "blast-radius:", err)
			return 1
		}
		fmt.Fprintf(stderr, "%d entries\n", n)
		return 0
	}

	removed, err := rewriteLog(path, drop, *dry)
	if err != nil {
		fmt.Fprintln(stderr, "blast-radius:", err)
		return 1
	}
	verb := "Deleted"
	if *dry {
		verb = "Would delete"
	}
	fmt.Fprintf(stdout, "%s %d entries from %s\n", verb, removed, path)
	return 0
}

// auditLine is the part of any audit log line the audit command needs.
type auditLine struct {
	Time      time.Time `json:"time"`
	SessionID string    `json:"session_id"`
}

func matcher(match, session string) func([]byte, auditLine) bool {
	if session != "" {
		return func(_ []byte, e auditLine) bool { return e.SessionID == session }
	}
	needle := strings.ToLower(match)
	return func(line []byte, _ auditLine) bool {
		// Compare against the decoded text, so escapes in the JSON
		// (&, \") don't hide a match.
		var v any
		if json.Unmarshal(line, &v) != nil {
			return bytes.Contains(bytes.ToLower(line), []byte(needle))
		}
		return containsText(v, needle)
	}
}

func containsText(v any, needle string) bool {
	switch x := v.(type) {
	case string:
		return strings.Contains(strings.ToLower(x), needle)
	case []any:
		for _, y := range x {
			if containsText(y, needle) {
				return true
			}
		}
	case map[string]any:
		for _, y := range x {
			if containsText(y, needle) {
				return true
			}
		}
	}
	return false
}

func parseAge(s string) (time.Duration, error) {
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || days <= 0 {
			return 0, fmt.Errorf("bad age %q", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("bad age %q (use 30d or 12h)", s)
	}
	return d, nil
}

func eachLine(path string, f func([]byte, auditLine) error) error {
	fh, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64*1024), maxTail)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e auditLine
		_ = json.Unmarshal(line, &e)
		if err := f(line, e); err != nil {
			return err
		}
	}
	return sc.Err()
}

// rewriteLog removes the lines drop selects, writing the rest to a new file
// that replaces the log. A hook appending at the same moment can lose its
// line; the window is a few milliseconds.
func rewriteLog(path string, drop func([]byte, auditLine) bool, dry bool) (int, error) {
	var kept bytes.Buffer
	removed := 0
	if err := eachLine(path, func(line []byte, e auditLine) error {
		if drop(line, e) {
			removed++
			return nil
		}
		kept.Write(line)
		kept.WriteByte('\n')
		return nil
	}); err != nil {
		return 0, err
	}
	if dry || removed == 0 {
		return removed, nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".audit-*.jsonl")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(kept.Bytes()); err != nil {
		tmp.Close()
		return 0, err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	return removed, os.Rename(tmp.Name(), path)
}

// pruneByRetention applies audit_retention_days, if set. The dashboard runs
// it when it starts and every hour.
func pruneByRetention(path string, days int) (int, error) {
	if days <= 0 {
		return 0, nil
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	return rewriteLog(path, func(_ []byte, e auditLine) bool { return !e.Time.IsZero() && e.Time.Before(cutoff) }, false)
}
