package main

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sandeepkomal/blast-radius/internal/policy"
)

//go:embed ui
var uiFiles embed.FS

const (
	defaultUIAddr  = "127.0.0.1:7357"
	defaultLogPath = "~/.local/state/blast-radius/audit.jsonl"
	// maxTail is how much of the end of the audit log the dashboard reads.
	maxTail = 4 << 20
)

// serveUI runs a dashboard on this machine that shows what the hook
// checked, asked about and blocked, from the audit log. It only reads files
// and never runs a preview, so a page that reaches it can't change anything.
func serveUI(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ui", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", defaultUIAddr, "address to listen on (this machine only)")
	logPath := fs.String("log", "", "audit log to show (default: audit_log from your config)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !loopback(*addr) {
		fmt.Fprintf(stderr, "blast-radius: %s is not on this machine; the dashboard only listens on 127.0.0.1, ::1 or localhost\n", *addr)
		return 2
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintln(stderr, "blast-radius:", err)
		return 1
	}
	s := &uiServer{logFlag: *logPath, port: portOf(ln.Addr().String())}
	srv := &http.Server{Handler: s.handler(), ReadHeaderTimeout: 10 * time.Second}

	url := "http://" + ln.Addr().String()
	fmt.Fprintf(stdout, "blast-radius dashboard: %s\nPress Ctrl+C to stop.\n", url)
	if log, enabled := s.logPath(); !enabled {
		fmt.Fprintf(stdout, "The activity log is off, so the dashboard will stay empty. Add\n  \"audit_log\": %q\nto your config (`blast-radius init --user` writes one with it).\n", log)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(stderr, "blast-radius:", err)
		return 1
	}
	return 0
}

func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func portOf(addr string) string {
	_, port, _ := net.SplitHostPort(addr)
	return port
}

type uiServer struct {
	logFlag string
	port    string
}

func (s *uiServer) handler() http.Handler {
	static, _ := fs.Sub(uiFiles, "ui")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(static))
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/activity", s.activity)
	mux.HandleFunc("GET /api/stream", s.stream)
	return s.guard(mux)
}

// guard only answers requests addressed to this machine by name, so a web
// page can't read the dashboard through DNS rebinding.
func (s *uiServer) guard(next http.Handler) http.Handler {
	allowed := map[string]bool{
		"127.0.0.1:" + s.port: true,
		"localhost:" + s.port: true,
		"[::1]:" + s.port:     true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.Host] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// logPath returns the audit log to show and whether the hook writes it.
func (s *uiServer) logPath() (string, bool) {
	if s.logFlag != "" {
		p, _ := expandHome(s.logFlag)
		return p, true
	}
	dir, _ := os.Getwd()
	cfg, _ := policy.Load(dir)
	if cfg.AuditLog == "" {
		p, _ := expandHome(defaultLogPath)
		return p, false
	}
	p, _ := expandHome(cfg.AuditLog)
	return p, true
}

type setupStep struct {
	ID      string `json:"id"`
	Done    bool   `json:"done"`
	Detail  string `json:"detail"`
	Unknown bool   `json:"unknown,omitempty"`
}

type uiStatus struct {
	Version     string          `json:"version"`
	Binary      string          `json:"binary"`
	Log         string          `json:"log"`
	LogEnabled  bool            `json:"log_enabled"`
	ConfigError string          `json:"config_error,omitempty"`
	Sources     []string        `json:"sources"`
	Policy      policy.Config   `json:"policy"`
	Steps       []setupStep     `json:"steps"`
	Tools       map[string]bool `json:"tools"`
}

func (s *uiServer) status(w http.ResponseWriter, _ *http.Request) {
	dir, _ := os.Getwd()
	cfg, cfgErr := policy.Load(dir)
	log, enabled := s.logPath()
	exe, _ := os.Executable()
	st := uiStatus{
		Version: versionString(), Binary: exe, Log: log, LogEnabled: enabled,
		Sources: cfg.Sources, Policy: cfg, Tools: map[string]bool{},
	}
	if st.Sources == nil {
		st.Sources = []string{}
	}
	if cfgErr != nil {
		st.ConfigError = cfgErr.Error()
	}
	for _, t := range []string{"terraform", "tofu", "terragrunt", "kubectl", "helm", "pulumi", "aws", "gcloud", "az", "claude"} {
		_, err := exec.LookPath(t)
		st.Tools[t] = err == nil
	}

	st.Steps = append(st.Steps, setupStep{ID: "binary", Done: true, Detail: exe})
	hook := setupStep{ID: "hook"}
	for _, project := range []bool{false, true} {
		p, err := settingsPath(project)
		if err != nil {
			continue
		}
		if h, err := findOurHook(p); err == nil && h != nil {
			hook.Done, hook.Detail = true, p
			break
		}
	}
	st.Steps = append(st.Steps, hook)
	logStep := setupStep{ID: "log", Done: enabled}
	if enabled {
		logStep.Detail = log
	}
	st.Steps = append(st.Steps, logStep)
	where := paranoidInstalled(dir)
	st.Steps = append(st.Steps, setupStep{ID: "paranoid", Done: where != "", Detail: where})
	st.Steps = append(st.Steps, setupStep{ID: "restart", Unknown: true})
	writeJSON(w, st)
}

// paranoidInstalled says where the paranoid-sre skill is installed, or ""
// if it can't find it.
func paranoidInstalled(dir string) string {
	home, _ := os.UserHomeDir()
	for _, p := range []string{
		filepath.Join(dir, ".claude", "skills", "paranoid", "SKILL.md"),
		filepath.Join(home, ".claude", "skills", "paranoid", "SKILL.md"),
	} {
		if _, err := os.Stat(p); err == nil {
			return filepath.Dir(p)
		}
	}
	p := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
	if b, err := os.ReadFile(p); err == nil && bytes.Contains(b, []byte("paranoid-sre@")) {
		return "Claude Code plugin paranoid-sre"
	}
	return ""
}

type activity struct {
	Entries []json.RawMessage `json:"entries"`
	Running []runningEntry    `json:"running"`
}

func (s *uiServer) activity(w http.ResponseWriter, _ *http.Request) {
	log, _ := s.logPath()
	entries, _, err := readTail(log, maxTail)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if entries == nil {
		entries = []json.RawMessage{}
	}
	writeJSON(w, activity{Entries: entries, Running: readRunning(log, time.Now())})
}

// stream sends new audit entries and the checks in progress as server-sent
// events, polling the files once a second.
func (s *uiServer) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	log, _ := s.logPath()
	offset := int64(0)
	if fi, err := os.Stat(log); err == nil {
		offset = fi.Size()
	}
	lastRunning := ""
	send := func(event string, v any) bool {
		b, err := json.Marshal(v)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		var entries []json.RawMessage
		entries, offset = readFrom(log, offset)
		for _, e := range entries {
			if !send("entry", e) {
				return
			}
		}
		running := readRunning(log, time.Now())
		if b, _ := json.Marshal(running); string(b) != lastRunning {
			lastRunning = string(b)
			if !send("running", running) {
				return
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
	}
}

// readTail returns the entries in the last max bytes of the log, oldest
// first, and the size of the file.
func readTail(path string, max int64) ([]json.RawMessage, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	start := fi.Size() - max
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, 0, err
	}
	b, err := io.ReadAll(io.LimitReader(f, fi.Size()-start))
	if err != nil {
		return nil, 0, err
	}
	if start > 0 {
		// Drop the line cut in half by starting mid-file.
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	return parseLines(b), fi.Size(), nil
}

// readFrom returns the complete entries written after offset and the offset
// to read from next. A file that shrank was rotated, so it is read again
// from the start.
func readFrom(path string, offset int64) ([]json.RawMessage, int64) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, offset
	}
	if fi.Size() < offset {
		offset = 0
	}
	if fi.Size() == offset {
		return nil, offset
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset
	}
	b, err := io.ReadAll(io.LimitReader(f, min(fi.Size()-offset, maxTail)))
	if err != nil {
		return nil, offset
	}
	// Leave a line that is still being written for the next read.
	end := bytes.LastIndexByte(b, '\n')
	if end < 0 {
		return nil, offset
	}
	return parseLines(b[:end+1]), offset + int64(end) + 1
}

func parseLines(b []byte) []json.RawMessage {
	var out []json.RawMessage
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), maxTail)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || !json.Valid(line) {
			continue
		}
		out = append(out, json.RawMessage(append([]byte(nil), line...)))
	}
	return out
}

// readRunning lists the checks in progress, oldest first. Marks left behind
// by a hook that was killed are ignored once their preview would have timed
// out.
func readRunning(log string, now time.Time) []runningEntry {
	dir := runningDir(log)
	files, err := os.ReadDir(dir)
	if err != nil {
		return []runningEntry{}
	}
	out := []runningEntry{}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			continue
		}
		var e runningEntry
		if json.Unmarshal(b, &e) != nil {
			continue
		}
		if now.Sub(e.Started) > time.Duration(e.Timeout+30)*time.Second {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
