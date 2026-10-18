// Package render formats findings as the preview a human reads before
// approving a command.
package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sandeepkomal/blast-radius/internal/analyze"
	"github.com/sandeepkomal/blast-radius/internal/policy"
)

// MaxResources is how many resources are listed per finding.
const MaxResources = 15

var actionOrder = map[string]int{
	analyze.ActDestroy: 0, analyze.ActReplace: 1, analyze.ActUpdate: 2, analyze.ActCreate: 3, analyze.ActForget: 4,
}

// Same symbols as terraform plan.
var symbols = map[string]string{
	analyze.ActDestroy: "-", analyze.ActReplace: "-/+", analyze.ActUpdate: "~", analyze.ActCreate: "+", analyze.ActForget: "-",
}

type style struct{ on bool }

func (s style) wrap(code, text string) string {
	if !s.on {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s style) bold(t string) string   { return s.wrap("1", t) }
func (s style) dim(t string) string    { return s.wrap("2", t) }
func (s style) red(t string) string    { return s.wrap("31", t) }
func (s style) yellow(t string) string { return s.wrap("33", t) }
func (s style) green(t string) string  { return s.wrap("32", t) }

func (s style) action(act, text string) string {
	switch act {
	case analyze.ActDestroy, analyze.ActReplace:
		return s.red(text)
	case analyze.ActUpdate, analyze.ActForget:
		return s.yellow(text)
	}
	return s.green(text)
}

func (s style) verdict(a policy.Action) string {
	switch a {
	case policy.Deny:
		return s.bold(s.red("BLOCKED"))
	case policy.Ask:
		return s.bold(s.yellow("NEEDS APPROVAL"))
	}
	return s.green("allowed")
}

// Text renders findings and their verdicts. color adds ANSI colours.
func Text(findings []analyze.Finding, verdicts []policy.Verdict, color bool) string {
	s := style{color}
	var b strings.Builder
	for i, f := range findings {
		if i > 0 {
			b.WriteString("\n")
		}
		v := verdicts[i]
		fmt.Fprintf(&b, "%s %s\n", s.bold("blast-radius:"), s.bold(truncate(f.Command, 100)))
		where := f.Target
		if v.Environment != "" {
			where += " · environment " + v.Environment
		}
		fmt.Fprintf(&b, "   %s · %s\n", s.dim(where), s.verdict(v.Action))
		if v.Reason != "" {
			fmt.Fprintf(&b, "   %s\n", s.bold(v.Reason))
		}

		res := append([]analyze.Resource{}, f.Resources...)
		sort.SliceStable(res, func(i, j int) bool { return actionOrder[res[i].Action] < actionOrder[res[j].Action] })
		if summary := counts(res); summary != "" {
			fmt.Fprintf(&b, "   %s\n", summary)
		}
		width := 0
		for i, r := range res {
			if n := len(displayName(r)); i < MaxResources && n > width {
				width = n
			}
		}
		for i, r := range res {
			if i == MaxResources {
				fmt.Fprintf(&b, "   %s\n", s.dim(fmt.Sprintf("... and %d more", len(res)-MaxResources)))
				break
			}
			line := fmt.Sprintf("%3s %-7s  %-*s", symbols[r.Action], r.Action, width, displayName(r))
			line = s.action(r.Action, line)
			if r.Stateful {
				line += "  " + s.bold(s.red("(holds data)"))
			}
			fmt.Fprintf(&b, "   %s\n", strings.TrimRight(line, " "))
		}
		if n := stateful(res); n > 0 {
			fmt.Fprintf(&b, "   %s\n", s.bold(s.red(fmt.Sprintf("%d resource%s holding data would be deleted.", n, plural(n)))))
		}
		for _, note := range f.Notes {
			fmt.Fprintf(&b, "   %s\n", s.yellow("note: "+note))
		}
	}
	return b.String()
}

func displayName(r analyze.Resource) string {
	if r.Namespace != "" {
		return r.Name + " (ns " + r.Namespace + ")"
	}
	return r.Name
}

func counts(res []analyze.Resource) string {
	n := map[string]int{}
	for _, r := range res {
		n[r.Action]++
	}
	var parts []string
	for _, act := range []string{analyze.ActDestroy, analyze.ActReplace, analyze.ActUpdate, analyze.ActCreate, analyze.ActForget} {
		if n[act] > 0 {
			parts = append(parts, fmt.Sprintf("%d to %s", n[act], act))
		}
	}
	return strings.Join(parts, " · ")
}

func stateful(res []analyze.Resource) int {
	n := 0
	for _, r := range res {
		if r.Stateful {
			n++
		}
	}
	return n
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-3]) + "..."
	}
	return s
}
