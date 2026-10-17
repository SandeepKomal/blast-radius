package render

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sandeepkomal/blast-radius/internal/analyze"
	"github.com/sandeepkomal/blast-radius/internal/policy"
)

func TestText(t *testing.T) {
	f := analyze.Finding{
		Command: "terraform destroy -auto-approve",
		Target:  "workspace prod",
		Resources: []analyze.Resource{
			{Action: analyze.ActUpdate, Name: "aws_instance.web"},
			{Action: analyze.ActDestroy, Name: "aws_db_instance.main", Stateful: true},
		},
		Notes: []string{"a note"},
	}
	got := Text([]analyze.Finding{f}, []policy.Verdict{{Action: policy.Deny, Environment: "production"}}, false)
	want := `blast-radius: terraform destroy -auto-approve
   workspace prod · environment production · BLOCKED
   1 to destroy · 1 to update
     - destroy  aws_db_instance.main  (holds data)
     ~ update   aws_instance.web
   1 resource holding data would be deleted.
   note: a note
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTextTruncatesLongLists(t *testing.T) {
	f := analyze.Finding{Command: "x", Target: "t"}
	for i := 0; i < MaxResources+5; i++ {
		f.Resources = append(f.Resources, analyze.Resource{Action: analyze.ActDestroy, Name: fmt.Sprintf("r%d", i)})
	}
	got := Text([]analyze.Finding{f}, []policy.Verdict{{Action: policy.Ask}}, false)
	if !strings.Contains(got, "... and 5 more") || strings.Contains(got, "r19") {
		t.Errorf("got:\n%s", got)
	}
}
