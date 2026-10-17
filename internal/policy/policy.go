// Package policy decides what happens to a risky command: let it through,
// ask a human, or block it.
package policy

import (
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
// kube context, namespace or directory of a command. Patterns are globs
// where * matches anything, compared case-insensitively.
type Environment struct {
	Name  string   `json:"name"`
	Match []string `json:"match"`
	Rules
}

// Config is the policy.
type Config struct {
	Default               Rules         `json:"default"`
	Environments          []Environment `json:"environments"`
	PreviewTimeoutSeconds int           `json:"preview_timeout_seconds,omitempty"`

	Source string `json:"-"` // file the config came from, or "" for built-in
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
		PreviewTimeoutSeconds: 90,
	}
}

// Load reads the nearest .blast-radius.json at or above dir, then the user
// config (~/.config/blast-radius/config.json), then falls back to Default.
func Load(dir string) (Config, error) {
	for _, p := range candidates(dir) {
		b, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Default(), err
		}
		cfg, err := Parse(b)
		if err != nil {
			return Default(), fmt.Errorf("%s: %w", p, err)
		}
		cfg.Source = p
		return cfg, nil
	}
	return Default(), nil
}

func candidates(dir string) []string {
	var out []string
	for d := dir; ; d = filepath.Dir(d) {
		out = append(out, filepath.Join(d, FileName))
		if filepath.Dir(d) == d {
			break
		}
	}
	if cfgDir, err := os.UserConfigDir(); err == nil {
		out = append(out, filepath.Join(cfgDir, "blast-radius", "config.json"))
	}
	return out
}

// Parse reads a config file. Missing default rules keep the built-in ones.
func Parse(b []byte) (Config, error) {
	cfg := Default()
	cfg.Environments = nil
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Default(), err
	}
	def := Default().Default
	cfg.Default = cfg.Default.fill(def)
	for i, e := range cfg.Environments {
		cfg.Environments[i].Rules = e.Rules.fill(cfg.Default)
		if err := e.Rules.validate(); err != nil {
			return Default(), fmt.Errorf("environment %q: %w", e.Name, err)
		}
	}
	if err := cfg.Default.validate(); err != nil {
		return Default(), fmt.Errorf("default: %w", err)
	}
	if cfg.PreviewTimeoutSeconds <= 0 {
		cfg.PreviewTimeoutSeconds = Default().PreviewTimeoutSeconds
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
}

// Decide applies the policy to one finding.
func (c Config) Decide(f analyze.Finding) Verdict {
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
