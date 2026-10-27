//go:build e2e

package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The Codex tests run the real Codex CLI with the hooks that
// `blast-radius install --agent codex` writes. A mock model asks Codex to run
// one tool call; the test reads what Codex sent back to the model.
//
//	BLAST_RADIUS_E2E_CODEX=$(command -v codex) go test -tags e2e -run Codex ./e2e/

// codexModel is a model Codex knows offline, so it offers apply_patch.
const codexModel = "gpt-5.5"

type codexEnv struct {
	codex string
	home  string
	env   []string
}

func newCodexEnv(t *testing.T) *codexEnv {
	t.Helper()
	codex := os.Getenv("BLAST_RADIUS_E2E_CODEX")
	if codex == "" {
		t.Skip("set BLAST_RADIUS_E2E_CODEX to the codex binary to run the Codex tests")
	}
	c := &codexEnv{codex: codex}
	c.env = env(t)
	for _, kv := range c.env {
		if h, ok := strings.CutPrefix(kv, "HOME="); ok {
			c.home = h
		}
	}
	c.env = append(c.env, "CODEX_HOME="+filepath.Join(c.home, ".codex"))
	for i, kv := range c.env {
		if strings.HasPrefix(kv, "OPENAI_API_KEY=") || strings.HasPrefix(kv, "CODEX_API_KEY=") {
			c.env[i] = "IGNORED_" + kv
		}
	}

	cfg := filepath.Join(c.home, ".config", "blast-radius", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	audit := fmt.Sprintf(`{"audit_log": %q, "audit_all": true}`, filepath.Join(c.home, "audit.jsonl"))
	if err := os.WriteFile(cfg, []byte(audit), 0o600); err != nil {
		t.Fatal(err)
	}
	install := exec.Command(binary, "install", "--agent", "codex")
	install.Env = c.env
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	return c
}

// hooks asks Codex which hooks it loaded for dir, through its app-server.
func (c *codexEnv) hooks(t *testing.T, dir string) []map[string]any {
	t.Helper()
	cmd := exec.Command(c.codex, "app-server")
	cmd.Env = c.env
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	send := func(v any) {
		b, _ := json.Marshal(v)
		if _, err := stdin.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	send(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]string{"name": "e2e", "title": "e2e", "version": "1"}}})
	lines := bufio.NewScanner(stdout)
	lines.Buffer(make([]byte, 1<<20), 1<<24)
	for lines.Scan() {
		var msg struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(lines.Bytes(), &msg) != nil {
			continue
		}
		switch msg.ID {
		case 1:
			send(map[string]any{"method": "initialized"})
			send(map[string]any{"id": 2, "method": "hooks/list", "params": map[string]any{"cwds": []string{dir}}})
		case 2:
			var res struct {
				Data []struct {
					Hooks    []map[string]any `json:"hooks"`
					Warnings []string         `json:"warnings"`
					Errors   []any            `json:"errors"`
				} `json:"data"`
			}
			if err := json.Unmarshal(msg.Result, &res); err != nil || len(res.Data) != 1 {
				t.Fatalf("hooks/list: %s %s", msg.Result, msg.Error)
			}
			if len(res.Data[0].Warnings)+len(res.Data[0].Errors) > 0 {
				t.Fatalf("Codex could not load the hooks: %+v", res.Data[0])
			}
			return res.Data[0].Hooks
		}
	}
	t.Fatalf("no hooks/list answer: %v", lines.Err())
	return nil
}

// trust records the hooks' current hashes in config.toml, as trusting them
// in Codex's /hooks does.
func (c *codexEnv) trust(t *testing.T, hooks []map[string]any) {
	t.Helper()
	var b strings.Builder
	for _, h := range hooks {
		fmt.Fprintf(&b, "[hooks.state.%s]\ntrusted_hash = %q\n\n", strconv.Quote(h["key"].(string)), h["currentHash"])
	}
	if err := os.WriteFile(filepath.Join(c.home, ".codex", "config.toml"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// toolCall has the mock model ask for one tool call, and returns what Codex
// reported back to the model as its output.
func (c *codexEnv) toolCall(t *testing.T, dir string, item map[string]any) string {
	t.Helper()
	item["call_id"] = "call-1"
	var mu sync.Mutex
	requests, output := 0, ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/responses") {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Input []struct {
				Type   string `json:"type"`
				CallID string `json:"call_id"`
				Output any    `json:"output"`
			} `json:"input"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		mu.Lock()
		requests++
		n := requests
		for _, in := range body.Input {
			if strings.HasSuffix(in.Type, "_output") && in.CallID == "call-1" {
				if s, ok := in.Output.(string); ok {
					output = s
				} else {
					o, _ := json.Marshal(in.Output)
					output = string(o)
				}
			}
		}
		mu.Unlock()
		reply := map[string]any{"type": "message", "role": "assistant", "id": "m1", "content": []any{map[string]any{"type": "output_text", "text": "done"}}}
		if n == 1 {
			reply = item
		}
		w.Header().Set("Content-Type", "text/event-stream")
		id := "r" + strconv.Itoa(n)
		usage := map[string]any{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0}
		for _, ev := range []map[string]any{
			{"type": "response.created", "response": map[string]any{"id": id}},
			{"type": "response.output_item.done", "item": reply},
			{"type": "response.completed", "response": map[string]any{"id": id, "usage": usage}},
		} {
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev["type"], data)
		}
	}))
	defer server.Close()

	cmd := exec.Command(c.codex, "exec", "--skip-git-repo-check", "-C", dir,
		"-c", fmt.Sprintf("model=%q", codexModel), "-c", `model_provider="mock"`,
		"-c", fmt.Sprintf(`model_providers.mock={name="mock",base_url=%q,wire_api="responses",requires_openai_auth=false,supports_websockets=false,request_max_retries=0,stream_max_retries=0}`, server.URL+"/v1"),
		"-c", `approval_policy="never"`, "-c", `sandbox_mode="danger-full-access"`, "run it")
	cmd.Env = c.env
	done := make(chan []byte, 1)
	go func() {
		out, _ := cmd.CombinedOutput()
		done <- out
	}()
	select {
	case out := <-done:
		mu.Lock()
		defer mu.Unlock()
		if output == "" {
			t.Fatalf("Codex sent no tool output to the model:\n%s", out)
		}
		return output
	case <-time.After(3 * time.Minute):
		_ = cmd.Process.Kill()
		t.Fatal("codex exec timed out")
	}
	return ""
}

func shellCall(command string) map[string]any {
	args, _ := json.Marshal(map[string]string{"cmd": command})
	return map[string]any{"type": "function_call", "name": "exec_command", "arguments": string(args)}
}

func patchCall(patch string) map[string]any {
	return map[string]any{"type": "custom_tool_call", "name": "apply_patch", "input": patch}
}

func TestCodex(t *testing.T) {
	c := newCodexEnv(t)
	dir := filepath.Join(c.home, "project")
	prod := filepath.Join(dir, "envs", "prod")
	if err := os.MkdirAll(prod, 0o755); err != nil {
		t.Fatal(err)
	}
	hooks := c.hooks(t, dir)
	events := map[string]bool{}
	for _, h := range hooks {
		events[h["eventName"].(string)] = true
	}
	for _, e := range []string{"preToolUse", "postToolUse", "sessionStart", "userPromptSubmit"} {
		if !events[e] {
			t.Fatalf("Codex did not load the %s hook: %v", e, hooks)
		}
	}

	// Codex skips hooks nobody has trusted: the command runs unchecked.
	if out := c.toolCall(t, dir, shellCall("terraform destroy -auto-approve")); strings.Contains(out, "blast-radius") {
		t.Fatalf("untrusted hook ran: %s", out)
	}
	c.trust(t, hooks)

	cases := []struct {
		name  string
		item  map[string]any
		dir   string
		wants []string
	}{
		{"harmless command runs", shellCall("echo hello-from-codex"), dir, []string{"hello-from-codex"}},
		{"destroy needs review", shellCall("terraform destroy -auto-approve"), dir, []string{"blocked by PreToolUse hook", "requires human review"}},
		{"production is blocked", shellCall("terraform destroy -auto-approve"), prod, []string{"blocked by PreToolUse hook", "Do not retry"}},
		{"removing the hooks needs review", shellCall("rm ~/.codex/hooks.json"), dir, []string{"blocked by PreToolUse hook", "blast-radius settings"}},
		{"patching the hooks needs review", patchCall("*** Begin Patch\n*** Delete File: " + filepath.Join(c.home, ".codex", "hooks.json") + "\n*** End Patch\n"), dir, []string{"blocked by PreToolUse hook", "blast-radius settings"}},
		{"ordinary patch applies", patchCall("*** Begin Patch\n*** Add File: notes.txt\n+hello\n*** End Patch\n"), dir, []string{"notes.txt"}},
	}
	for _, tc := range cases {
		out := c.toolCall(t, tc.dir, tc.item)
		for _, want := range tc.wants {
			if !strings.Contains(out, want) {
				t.Errorf("%s: output lacks %q:\n%s", tc.name, want, out)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(c.home, ".codex", "hooks.json")); err != nil {
		t.Errorf("hooks file: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(c.home, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		if e["agent_platform"] != "codex" {
			t.Errorf("activity log entry not attributed to Codex: %s", line)
		}
		kind, _ := e["kind"].(string)
		kinds[kind]++
	}
	if kinds["session"] == 0 || kinds["prompt"] == 0 || kinds[""] == 0 || kinds["result"] == 0 {
		t.Errorf("activity log kinds = %v; want sessions, prompts, checks and results", kinds)
	}
}
