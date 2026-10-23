// Package policy decides what happens to a risky command: let it through,
// ask a human, or block it.
package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sandeepkomal/blast-radius/internal/analyze"
)

// FileName is the per-project config file, looked up from the working
// directory upwards.
const FileName = ".blast-radius.json"

// MaxPreviewTimeout keeps previews well inside the hook timeout that
// `blast-radius install` sets, so a slow plan ends in "ask", not a hook failure.
const MaxPreviewTimeout = 270

// Action is what to do with a command.
type Action string

const (
	Pass Action = "pass" // run it as normal
	Ask  Action = "ask"  // show the preview and ask a human
	Deny Action = "deny" // block it and tell the agent why
)

func (a Action) rank() int {
	switch a {
	case Ask:
		return 1
	case Deny:
		return 2
	}
	return 0
}

// Rules says what to do at each level of risk.
type Rules struct {
	Change  Action `json:"change,omitempty"`
	Destroy Action `json:"destroy,omitempty"`
}

// Environment applies its rules when any pattern matches the workspace,
// kube context, namespace, cloud profile or directory of a command. Patterns
// are globs where * matches anything, compared case-insensitively.
type Environment struct {
	Name  string   `json:"name"`
	Match []string `json:"match"`
	Rules
}

// SchemaURL is the JSON schema for config files, for editor completion.
const SchemaURL = "https://raw.githubusercontent.com/SandeepKomal/blast-radius/main/docs/config.schema.json"

// Config is the policy.
type Config struct {
	Schema       string        `json:"$schema,omitempty"`
	Default      Rules         `json:"default"`
	Environments []Environment `json:"environments"`
	// Protect lists resources that may never be deleted or replaced, in any
	// environment: terraform addresses ("aws_db_instance.*"), Kubernetes
	// "kind/name" or "namespace/kind/name" ("*/persistentvolumeclaim/*").
	Protect []string `json:"protect,omitempty"`
	// Stateful adds terraform types and Kubernetes kinds that hold data.
	Stateful []string `json:"stateful,omitempty"`
	// Aliases maps extra program names to the tool they run.
	Aliases               map[string]string `json:"aliases,omitempty"`
	PreviewTimeoutSeconds int               `json:"preview_timeout_seconds,omitempty"`
	// AuditLog is a file that gets one JSON line per risky command.
	AuditLog string `json:"audit_log,omitempty"`
	// AuditAll also records shell commands with nothing risky in them.
	AuditAll bool `json:"audit_all,omitempty"`

	// Sources are the files the config came from; empty for the built-in one.
	Sources []string `json:"-"`
	// project holds the repository's .blast-radius.json, which can only make
	// decisions stricter.
	project *Config
}

// Default is used when no config file exists: changes outside production
// run as normal, deletions ask first, and deletions in production are
// blocked.
func Default() Config {
	return Config{
		Default: Rules{Change: Pass, Destroy: Ask},
		Environments: []Environment{{
			Name:  "production",
			Match: []string{"*prod*", "*prd*"},
			Rules: Rules{Change: Ask, Destroy: Deny},
		}},
		Aliases:               map[string]string{"tf": "terraform", "k": "kubectl"},
		PreviewTimeoutSeconds: 90,
	}
}

// UserConfigPath is where the user-level config lives.
func UserConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "blast-radius", "config.json"), nil
}

// Load builds the policy for dir: the user config (or the built-in default),
// tightened by the nearest .blast-radius.json at or above dir.
func Load(dir string) (Config, error) {
	cfg := Default()
	if p, err := UserConfigPath(); err == nil {
		b, err := os.ReadFile(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return Default(), err
		default:
			if cfg, err = Parse(b, Default()); err != nil {
				return Default(), fmt.Errorf("%s: %w", p, err)
			}
			cfg.Sources = []string{p}
		}
	}

	p, b, err := findProjectFile(dir)
	if err != nil {
		return cfg, err
	}
	if p == "" {
		return cfg, nil
	}
	proj, err := Parse(b, cfg)
	if err != nil {
		return cfg, fmt.Errorf("%s: %w", p, err)
	}
	cfg.project = &proj
	cfg.Sources = append(cfg.Sources, p)
	cfg.Protect = append(cfg.Protect, proj.Protect...)
	cfg.Stateful = append(cfg.Stateful, proj.Stateful...)
	for k, v := range proj.Aliases {
		if _, ok := cfg.Aliases[k]; !ok {
			cfg.Aliases[k] = v
		}
	}
	if proj.PreviewTimeoutSeconds < cfg.PreviewTimeoutSeconds {
		cfg.PreviewTimeoutSeconds = proj.PreviewTimeoutSeconds
	}
	return cfg, nil
}

func findProjectFile(dir string) (string, []byte, error) {
	for d := dir; ; d = filepath.Dir(d) {
		p := filepath.Join(d, FileName)
		b, err := os.ReadFile(p)
		if err == nil {
			return p, b, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", nil, err
		}
		if filepath.Dir(d) == d {
			return "", nil, nil
		}
	}
}

// Parse reads a config file. Rules it leaves out come from base. Unknown
// keys are an error, so a typo can't silently weaken the policy.
func Parse(b []byte, base Config) (Config, error) {
	cfg := Config{Default: base.Default, PreviewTimeoutSeconds: base.PreviewTimeoutSeconds, Aliases: map[string]string{}}
	for k, v := range base.Aliases {
		cfg.Aliases[k] = v
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var raw Config
	if err := dec.Decode(&raw); err != nil {
		return base, err
	}
	if err := raw.Default.validate(); err != nil {
		return base, fmt.Errorf("default: %w", err)
	}
	cfg.Default = raw.Default.fill(base.Default)
	cfg.Environments = raw.Environments
	if raw.Environments == nil {
		cfg.Environments = base.Environments
	}
	for i, e := range cfg.Environments {
		if err := e.validate(); err != nil {
			return base, fmt.Errorf("environment %q: %w", e.Name, err)
		}
		if len(e.Match) == 0 {
			return base, fmt.Errorf("environment %q: match is empty", e.Name)
		}
		cfg.Environments[i].Rules = e.fill(cfg.Default)
	}
	cfg.Protect = raw.Protect
	cfg.Stateful = raw.Stateful
	for k, v := range raw.Aliases {
		cfg.Aliases[k] = v
	}
	cfg.AuditLog = raw.AuditLog
	cfg.AuditAll = raw.AuditAll
	if raw.PreviewTimeoutSeconds != 0 {
		if raw.PreviewTimeoutSeconds < 1 || raw.PreviewTimeoutSeconds > MaxPreviewTimeout {
			return base, fmt.Errorf("preview_timeout_seconds must be between 1 and %d", MaxPreviewTimeout)
		}
		cfg.PreviewTimeoutSeconds = raw.PreviewTimeoutSeconds
	}
	return cfg, nil
}

func (r Rules) fill(from Rules) Rules {
	if r.Change == "" {
		r.Change = from.Change
	}
	if r.Destroy == "" {
		r.Destroy = from.Destroy
	}
	return r
}

func (r Rules) validate() error {
	for _, a := range []Action{r.Change, r.Destroy} {
		if a != "" && a != Pass && a != Ask && a != Deny {
			return fmt.Errorf("unknown action %q (use pass, ask or deny)", a)
		}
	}
	return nil
}

// Verdict is the decision for one finding.
type Verdict struct {
	Action      Action `json:"action"`
	Environment string `json:"environment,omitempty"` // matched environment name
	Reason      string `json:"reason,omitempty"`
}

// Decide applies the policy to one finding. A project config can make the
// decision stricter but never looser.
func (c Config) Decide(f analyze.Finding) Verdict {
	v := c.decide(f)
	if c.project != nil {
		if pv := c.project.decide(f); pv.Action.rank() > v.Action.rank() {
			v = pv
		}
	}
	return v
}

func (c Config) decide(f analyze.Finding) Verdict {
	if f.Guard {
		return Verdict{Action: Ask, Reason: "this changes blast-radius's own settings"}
	}
	rules, env := c.Default, ""
	if e, ok := c.match(f.Labels); ok {
		rules, env = e.Rules, e.Name
	}
	v := Verdict{Action: Pass, Environment: env}
	switch f.Level {
	case analyze.Change:
		v.Action = rules.Change
	case analyze.Destroy:
		v.Action = rules.Destroy
	}
	if hit := c.protected(f); hit != "" {
		v.Action, v.Reason = Deny, "protected resource: "+hit
	}
	return v
}

func (c Config) match(labels []string) (Environment, bool) {
	for _, e := range c.Environments {
		for _, pattern := range e.Match {
			re := glob(pattern)
			for _, l := range labels {
				if l != "" && re.MatchString(l) {
					return e, true
				}
			}
		}
	}
	return Environment{}, false
}

// protected returns the first deleted resource that matches a protect pattern.
func (c Config) protected(f analyze.Finding) string {
	for _, r := range f.Resources {
		if !r.Lost() {
			continue
		}
		names := []string{r.Name}
		if r.Namespace != "" {
			names = append(names, r.Namespace+"/"+r.Name)
		}
		for _, pattern := range c.Protect {
			re := glob(pattern)
			for _, n := range names {
				if re.MatchString(n) {
					return n
				}
			}
		}
	}
	return ""
}

func glob(pattern string) *regexp.Regexp {
	q := regexp.QuoteMeta(pattern)
	q = strings.ReplaceAll(q, `\*`, `.*`)
	q = strings.ReplaceAll(q, `\?`, `.`)
	return regexp.MustCompile(`(?i)^` + q + `$`)
}

// Strictest returns the most severe action among verdicts.
func Strictest(verdicts []Verdict) Action {
	out := Pass
	for _, v := range verdicts {
		if v.Action.rank() > out.rank() {
			out = v.Action
		}
	}
	return out
}
