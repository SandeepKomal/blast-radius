// Package shell splits a shell command line into simple commands.
//
// It is a best-effort parser for the kind of commands coding agents run
// (pipelines, &&-chains, env prefixes, sudo, sh -c, eval, $(...)), not a full
// POSIX or PowerShell implementation. It never executes anything.
package shell

import (
	"regexp"
	"strings"
)

// Command is one simple command with its quotes removed.
type Command struct {
	Env       []string // leading VAR=value assignments, e.g. TF_WORKSPACE=prod
	Args      []string // program and its arguments
	Redirects []string // files the command reads or writes through <, > or >>
}

// Program returns the lower-case base name of the program without a Windows
// .exe suffix, e.g. "terraform" for "/usr/local/bin/terraform" or
// `C:\tools\terraform.exe`.
func (c Command) Program() string {
	if len(c.Args) == 0 {
		return ""
	}
	p := c.Args[0]
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		p = p[i+1:]
	}
	p = strings.ToLower(p)
	return strings.TrimSuffix(p, ".exe")
}

// String renders the command roughly as it was typed.
func (c Command) String() string {
	parts := append(append([]string{}, c.Env...), c.Args...)
	for i, p := range parts {
		if p == "" || strings.ContainsAny(p, " \t\"'$`\\|&;<>()") {
			parts[i] = "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
		}
	}
	return strings.Join(parts, " ")
}

// Parse splits a POSIX shell script into simple commands in execution order.
// Commands nested in $(...), backticks, eval or `sh -c '...'` are included,
// before the command that contains them.
func Parse(script string) []Command {
	p := &parser{}
	p.parse(script)
	return p.out
}

// ParsePowerShell is Parse for PowerShell, where the backtick is the escape
// character and backslashes are literal path separators.
func ParsePowerShell(script string) []Command {
	p := &parser{ps: true}
	p.parse(script)
	return p.out
}

type parser struct {
	ps        bool
	out       []Command
	words     []string
	redirects []string
	word      strings.Builder
	inWord    bool
	skipWord  bool // the next word is a redirection target
	lineStart int  // index in out of the first command on the current line
	heredocs  []heredoc
}

// heredoc is a << redirection whose body starts on the next line.
type heredoc struct {
	delim     string
	stripTabs bool // <<- removes leading tabs
}

func (p *parser) nested(script string) []Command {
	if p.ps {
		return ParsePowerShell(script)
	}
	return Parse(script)
}

func (p *parser) flushWord() {
	if !p.inWord {
		return
	}
	if p.skipWord {
		p.skipWord = false
		p.redirects = append(p.redirects, p.word.String())
	} else {
		p.words = append(p.words, p.word.String())
	}
	p.word.Reset()
	p.inWord = false
}

func (p *parser) flushCommand() {
	p.flushWord()
	if len(p.words) > 0 {
		cmds := p.newCommands(p.words)
		for i := range cmds {
			cmds[i].Redirects = append(cmds[i].Redirects, p.redirects...)
		}
		p.out = append(p.out, cmds...)
	} else if len(p.redirects) > 0 {
		// A bare redirection such as `> file` still writes the file.
		p.out = append(p.out, Command{Args: []string{":"}, Redirects: p.redirects})
	}
	p.words = nil
	p.redirects = nil
}

func (p *parser) parse(s string) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c == '\\' && !p.ps) || (c == '`' && p.ps):
			if i+1 < len(s) {
				i++
				if s[i] != '\n' {
					p.word.WriteByte(s[i])
					p.inWord = true
				}
			}
		case c == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				end = len(s) - i - 1
			}
			p.word.WriteString(s[i+1 : i+1+end])
			p.inWord = true
			i += end + 1
		case c == '"':
			i = p.doubleQuoted(s, i+1)
			p.inWord = true
		case c == '$' && i+1 < len(s) && s[i+1] == '(':
			i = p.substitution(s, i)
		case c == '`':
			i = p.backtick(s, i)
		case c == ' ' || c == '\t' || c == '\r':
			p.flushWord()
		case c == '#' && !p.inWord:
			for i < len(s) && s[i] != '\n' {
				i++
			}
			p.flushCommand()
		case c == '>' || c == '<' || (c == '&' && i+1 < len(s) && s[i+1] == '>'):
			// Redirection: drop a pending fd number ("2>") and keep the target.
			if p.inWord && isDigits(p.word.String()) {
				p.word.Reset()
				p.inWord = false
			}
			p.flushWord()
			start := i
			for i+1 < len(s) && strings.IndexByte("<>&", s[i+1]) >= 0 {
				i++
			}
			if !p.ps && s[start:i+1] == "<<" {
				i = p.heredocStart(s, i+1)
				continue
			}
			// `2>&1` duplicates a descriptor rather than naming a file.
			if s[i] == '&' {
				for i+1 < len(s) && (isDigit(s[i+1]) || s[i+1] == '-') {
					i++
				}
				continue
			}
			p.skipWord = true
		case c == '\n' && len(p.heredocs) > 0:
			p.flushCommand()
			i = p.heredocBodies(s, i+1)
			p.lineStart = len(p.out)
		case c == '\n':
			p.flushCommand()
			p.lineStart = len(p.out)
		case c == ';' || c == '&' || c == '|' || c == '(' || c == ')':
			p.flushCommand()
		default:
			p.word.WriteByte(c)
			p.inWord = true
		}
	}
	p.flushCommand()
}

// heredocStart reads the delimiter of a << redirection starting at s[i] and
// returns the index of its last character.
func (p *parser) heredocStart(s string, i int) int {
	h := heredoc{}
	if i < len(s) && s[i] == '-' {
		h.stripTabs = true
		i++
	}
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	var d strings.Builder
	for ; i < len(s) && strings.IndexByte(" \t\r\n;&|<>()", s[i]) < 0; i++ {
		if s[i] != '\'' && s[i] != '"' && s[i] != '\\' {
			d.WriteByte(s[i])
		}
	}
	h.delim = d.String()
	p.heredocs = append(p.heredocs, h)
	return i - 1
}

// heredocBodies reads the bodies of the heredocs started on the line that
// just ended, starting at s[i], and returns the index of the last character
// it consumed. A body is data unless a shell on that line reads it as a
// script, as in `bash <<EOF` or `cat <<EOF | sh`; then it is parsed as
// commands.
func (p *parser) heredocBodies(s string, i int) int {
	script := false
	for _, c := range p.out[p.lineStart:] {
		name := c.Program()
		if (shells[name] || powershells[name] || name == "source" || name == ".") && len(c.Args) == 1 {
			script = true
		}
	}
	end := i - 1
	for _, h := range p.heredocs {
		var body []string
		for i < len(s) {
			line := s[i:]
			if j := strings.IndexByte(line, '\n'); j >= 0 {
				line = line[:j]
			}
			i += len(line) + 1
			end = min(i, len(s)) - 1
			line = strings.TrimRight(line, "\r")
			if h.stripTabs {
				line = strings.TrimLeft(line, "\t")
			}
			if line == h.delim {
				break
			}
			body = append(body, line)
		}
		if script {
			p.out = append(p.out, p.nested(strings.Join(body, "\n"))...)
		}
	}
	p.heredocs = nil
	return end
}

// doubleQuoted reads a "..." string starting after the opening quote and
// returns the index of the closing quote.
func (p *parser) doubleQuoted(s string, i int) int {
	escape := byte('\\')
	if p.ps {
		escape = '`'
	}
	for ; i < len(s) && s[i] != '"'; i++ {
		switch {
		case s[i] == escape && i+1 < len(s) && (p.ps || strings.IndexByte("\"\\$`\n", s[i+1]) >= 0):
			i++
			if s[i] != '\n' {
				p.word.WriteByte(s[i])
			}
		case s[i] == '$' && i+1 < len(s) && s[i+1] == '(':
			i = p.substitution(s, i)
		case s[i] == '`' && !p.ps:
			i = p.backtick(s, i)
		default:
			p.word.WriteByte(s[i])
		}
	}
	return i
}

// substitution handles $(...) starting at s[i] == '$'. The inner script is
// parsed as its own commands and the literal text is kept in the word.
func (p *parser) substitution(s string, i int) int {
	depth, j := 0, i+1
	for ; j < len(s); j++ {
		if s[j] == '(' {
			depth++
		} else if s[j] == ')' {
			depth--
			if depth == 0 {
				break
			}
		}
	}
	p.out = append(p.out, p.nested(s[i+2:min(j, len(s))])...)
	p.word.WriteString(s[i:min(j+1, len(s))])
	p.inWord = true
	return j
}

func (p *parser) backtick(s string, i int) int {
	j := strings.IndexByte(s[i+1:], '`')
	if j < 0 {
		j = len(s)
	} else {
		j += i + 1
	}
	p.out = append(p.out, Parse(s[i+1:j])...)
	p.word.WriteString(s[i:min(j+1, len(s))])
	p.inWord = true
	return j
}

var assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// Shell keywords that can start a simple command without being the program.
var keywords = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "fi": true,
	"do": true, "done": true, "while": true, "until": true,
	"{": true, "}": true, "!": true,
}

// Wrappers that run another command. The value is the set of their flags
// that take a separate argument.
var wrappers = map[string]map[string]bool{
	"sudo":    set("-u", "-g", "-C", "-D", "-h", "-p", "-r", "-t", "-U"),
	"doas":    set("-u", "-C"),
	"env":     set("-u", "-C", "-S"),
	"time":    {},
	"nohup":   {},
	"exec":    {},
	"command": {},
	"builtin": {},
	"nice":    set("-n"),
	"ionice":  set("-c", "-n", "-p"),
	"timeout": set("-s", "-k", "--signal", "--kill-after"),
	"xargs":   set("-n", "-I", "-P", "-d", "-L", "-s", "-E", "-a"),
	"watch":   set("-n", "-d"),
}

var shells = set("sh", "bash", "zsh", "dash", "ksh", "fish")

var powershells = set("powershell", "pwsh")

func base(word string) string {
	return Command{Args: []string{word}}.Program()
}

func (p *parser) newCommands(words []string) []Command {
	var env []string
	for {
		for len(words) > 0 && keywords[words[0]] {
			words = words[1:]
		}
		for len(words) > 0 && assignment.MatchString(words[0]) {
			env = append(env, words[0])
			words = words[1:]
		}
		if len(words) == 0 {
			return nil
		}
		name := base(words[0])
		valueFlags, ok := wrappers[name]
		if !ok {
			break
		}
		words = words[1:]
		for len(words) > 0 && strings.HasPrefix(words[0], "-") {
			if valueFlags[words[0]] && len(words) > 1 {
				words = words[1:]
			}
			words = words[1:]
		}
		if name == "timeout" && len(words) > 0 {
			words = words[1:] // the duration
		}
	}

	var script string
	var nested []Command
	switch name := base(words[0]); {
	case name == "eval" && len(words) > 1:
		script = strings.Join(words[1:], " ")
		nested = p.nested(script)
	case shells[name]:
		if s, ok := shellScript(words[1:]); ok {
			nested = Parse(s)
			script = s
		}
	case powershells[name]:
		if s, ok := powershellScript(words[1:]); ok {
			nested = ParsePowerShell(s)
			script = s
		}
	}
	if script != "" {
		for i := range nested {
			nested[i].Env = append(append([]string{}, env...), nested[i].Env...)
		}
		return nested
	}
	return []Command{{Env: env, Args: words}}
}

// shellScript finds the script passed to `sh -c`, including combined flags
// such as `bash -lc`.
func shellScript(args []string) (string, bool) {
	for i, a := range args {
		if !strings.HasPrefix(a, "-") || strings.HasPrefix(a, "--") {
			return "", false
		}
		if strings.Contains(a, "c") && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// powershellScript finds the script passed to `pwsh -Command`. Everything
// after -Command is the script, as in PowerShell itself.
func powershellScript(args []string) (string, bool) {
	for i, a := range args {
		switch strings.ToLower(a) {
		case "-c", "-command", "-com", "-comm", "-comma", "-comman":
			if i+1 < len(args) {
				return strings.Join(args[i+1:], " "), true
			}
			return "", false
		}
		if !strings.HasPrefix(a, "-") {
			return "", false
		}
	}
	return "", false
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

func set(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}
