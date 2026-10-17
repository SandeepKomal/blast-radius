// Package shell splits a shell command line into simple commands.
//
// It is a best-effort parser for the kind of commands coding agents run
// (pipelines, &&-chains, env prefixes, sudo, sh -c, $(...)), not a full
// POSIX shell. It never executes anything.
package shell

import (
	"path"
	"regexp"
	"strings"
)

// Command is one simple command with its quotes removed.
type Command struct {
	Env  []string // leading VAR=value assignments, e.g. TF_WORKSPACE=prod
	Args []string // program and its arguments
}

// Program returns the base name of the program, e.g. "terraform" for
// "/usr/local/bin/terraform".
func (c Command) Program() string {
	if len(c.Args) == 0 {
		return ""
	}
	return path.Base(c.Args[0])
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

// Parse splits script into simple commands in execution order. Commands
// nested in $(...), backticks or `sh -c '...'` are included, before the
// command that contains them.
func Parse(script string) []Command {
	p := &parser{}
	p.parse(script)
	return p.out
}

type parser struct {
	out      []Command
	words    []string
	word     strings.Builder
	inWord   bool
	skipWord bool // the next word is a redirection target
}

func (p *parser) flushWord() {
	if !p.inWord {
		return
	}
	if p.skipWord {
		p.skipWord = false
	} else {
		p.words = append(p.words, p.word.String())
	}
	p.word.Reset()
	p.inWord = false
}

func (p *parser) flushCommand() {
	p.flushWord()
	if len(p.words) > 0 {
		p.out = append(p.out, newCommands(p.words)...)
	}
	p.words = nil
}

func (p *parser) parse(s string) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\':
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
		case c == ' ' || c == '\t':
			p.flushWord()
		case c == '#' && !p.inWord:
			for i < len(s) && s[i] != '\n' {
				i++
			}
			p.flushCommand()
		case c == '>' || c == '<' || (c == '&' && i+1 < len(s) && s[i+1] == '>'):
			// Redirection: drop a pending fd number ("2>") and the target word.
			if p.inWord && isDigits(p.word.String()) {
				p.word.Reset()
				p.inWord = false
			}
			p.flushWord()
			for i+1 < len(s) && strings.IndexByte("<>&", s[i+1]) >= 0 {
				i++
			}
			p.skipWord = true
		case c == '\n' || c == ';' || c == '&' || c == '|' || c == '(' || c == ')':
			p.flushCommand()
		default:
			p.word.WriteByte(c)
			p.inWord = true
		}
	}
	p.flushCommand()
}

// doubleQuoted reads a "..." string starting after the opening quote and
// returns the index of the closing quote.
func (p *parser) doubleQuoted(s string, i int) int {
	for ; i < len(s) && s[i] != '"'; i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\"\\$`\n", s[i+1]) >= 0:
			i++
			if s[i] != '\n' {
				p.word.WriteByte(s[i])
			}
		case s[i] == '$' && i+1 < len(s) && s[i+1] == '(':
			i = p.substitution(s, i)
		case s[i] == '`':
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
	p.out = append(p.out, Parse(s[i+2:min(j, len(s))])...)
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
	"env":     set("-u", "-C", "-S"),
	"time":    {},
	"nohup":   {},
	"exec":    {},
	"command": {},
	"builtin": {},
	"nice":    set("-n"),
	"timeout": set("-s", "-k", "--signal", "--kill-after"),
	"xargs":   set("-n", "-I", "-P", "-d", "-L", "-s", "-E", "-a"),
	"watch":   set("-n", "-d"),
}

var shells = set("sh", "bash", "zsh", "dash", "ksh")

func newCommands(words []string) []Command {
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
		valueFlags, ok := wrappers[path.Base(words[0])]
		if !ok {
			break
		}
		name := path.Base(words[0])
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

	if shells[path.Base(words[0])] {
		if script, ok := shellScript(words[1:]); ok {
			nested := Parse(script)
			for i := range nested {
				nested[i].Env = append(append([]string{}, env...), nested[i].Env...)
			}
			return nested
		}
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

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
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
