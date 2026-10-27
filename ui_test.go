package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sandeepkomal/blast-radius/internal/policy"
)

func TestLoopback(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:7357": true,
		"localhost:0":    true,
		"[::1]:80":       true,
		"0.0.0.0:7357":   false,
		":7357":          false,
		"10.0.0.5:7357":  false,
		"example.com:80": false,
	} {
		if got := loopback(addr); got != want {
			t.Errorf("loopback(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestUIRejectsOtherHosts(t *testing.T) {
	isolate(t)
	s := &uiServer{logFlag: filepath.Join(t.TempDir(), "audit.jsonl"), port: "7357"}
	h := s.handler()
	for host, want := range map[string]int{
		"127.0.0.1:7357": http.StatusOK,
		"localhost:7357": http.StatusOK,
		"evil.com":       http.StatusForbidden,
		"localhost:9999": http.StatusForbidden,
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/activity", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Host %s: status %d, want %d", host, rec.Code, want)
		}
	}
}

func TestUIServesPage(t *testing.T) {
	isolate(t)
	s := &uiServer{logFlag: filepath.Join(t.TempDir(), "audit.jsonl"), port: "1"}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "blast-radius") {
		t.Fatalf("status %d, body %.200s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("no Content-Security-Policy header")
	}
}

func TestUIActivityFromAuditLog(t *testing.T) {
	isolate(t)
	log := filepath.Join(t.TempDir(), "audit.jsonl")
	cfg, err := policy.UserConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]string{"audit_log": log})
	if err := os.WriteFile(cfg, b, 0o644); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	var out, errOut strings.Builder
	if code := check([]string{"-C", dir, "terraform destroy"}, &out, &errOut, planRunner(destroyPlan)); code != exitAsk {
		t.Fatalf("check exit %d: %s", code, errOut.String())
	}

	s := &uiServer{port: "1"}
	if p, enabled := s.logPath(); p != log || !enabled {
		t.Fatalf("logPath = %s, %v", p, enabled)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/activity", nil)
	req.Host = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, req)
	var a struct {
		Entries []auditEntry   `json:"entries"`
		Running []runningEntry `json:"running"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	if len(a.Entries) != 1 || a.Entries[0].Command != "terraform destroy" || a.Entries[0].Action != "ask" {
		t.Fatalf("entries = %+v", a.Entries)
	}
	if len(a.Running) != 0 {
		t.Errorf("a finished check is still running: %+v", a.Running)
	}
}

func TestReadFromKeepsPartialLines(t *testing.T) {
	log := filepath.Join(t.TempDir(), "audit.jsonl")
	write := func(s string) {
		f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString(s); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"command":"a"}` + "\n" + `{"command":`)
	got, off := readFrom(log, 0)
	if len(got) != 1 || string(got[0]) != `{"command":"a"}` {
		t.Fatalf("first read = %s", got)
	}
	write(`"b"}` + "\n")
	got, off = readFrom(log, off)
	if len(got) != 1 || string(got[0]) != `{"command":"b"}` {
		t.Fatalf("second read = %s", got)
	}
	// A rotated (smaller) file is read from the start.
	if err := os.WriteFile(log, []byte(`{"command":"c"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ = readFrom(log, off); len(got) != 1 || string(got[0]) != `{"command":"c"}` {
		t.Fatalf("after rotation = %s", got)
	}
}

func TestReadTailDropsCutLine(t *testing.T) {
	log := filepath.Join(t.TempDir(), "audit.jsonl")
	content := `{"command":"first-long-line"}` + "\n" + `{"command":"b"}` + "\n"
	if err := os.WriteFile(log, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, size, err := readTail(log, int64(len(`{"command":"b"}`)+5))
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(content)) || len(got) != 1 || string(got[0]) != `{"command":"b"}` {
		t.Fatalf("got %s, size %d", got, size)
	}
}

func TestRunningMarks(t *testing.T) {
	log := filepath.Join(t.TempDir(), "audit.jsonl")
	done := markRunning(log, request{tool: "Bash", script: "terraform apply", dir: "/src"}, 90)
	r := readRunning(log, time.Now())
	if len(r) != 1 || r[0].Command != "terraform apply" {
		t.Fatalf("running = %+v", r)
	}
	if r := readRunning(log, time.Now().Add(5*time.Minute)); len(r) != 0 {
		t.Errorf("a mark past its timeout is still shown: %+v", r)
	}
	done()
	if r := readRunning(log, time.Now()); len(r) != 0 {
		t.Errorf("mark not cleared: %+v", r)
	}
}

func TestParanoidInstalledForEveryAgent(t *testing.T) {
	home := isolate(t)
	dir := t.TempDir()
	if got := paranoidInstalled(dir); got != "" {
		t.Fatalf("found %q with nothing installed", got)
	}
	for _, skills := range []string{
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(home, ".codex", "skills"),
		filepath.Join(home, ".gemini", "skills"),
		filepath.Join(home, ".copilot", "skills"),
		filepath.Join(dir, ".github", "skills"),
	} {
		skill := filepath.Join(skills, "paranoid")
		if err := os.MkdirAll(skill, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: paranoid\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := paranoidInstalled(dir); got != skill {
			t.Errorf("paranoidInstalled = %q, want %q", got, skill)
		}
		if err := os.RemoveAll(skill); err != nil {
			t.Fatal(err)
		}
	}
}
