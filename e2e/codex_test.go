//go:build e2e

package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// codexModel is a model Codex knows offline, so it offers apply_patch.
const codexModel = "gpt-5.5"

// Codex: the OpenAI Responses API, through a custom model provider.

var codexAgent = agent{
	name: "codex", platform: "codex", binEnv: "BLAST_RADIUS_E2E_CODEX",
	hookFile: filepath.Join(".codex", "hooks.json"),
	wire:     responsesWire("call_e2e"),
	shell:    func(c string) toolCall { return toolCall{name: "exec_command", args: map[string]any{"cmd": c}} },
	env:      func(string) []string { return nil },
	args: func(dir, model string) []string {
		return []string{"exec", "--skip-git-repo-check", "-C", dir,
			"-c", fmt.Sprintf("model=%q", codexModel), "-c", `model_provider="mock"`,
			"-c", fmt.Sprintf(`model_providers.mock={name="mock",base_url=%q,wire_api="responses",requires_openai_auth=false,supports_websockets=false,request_max_retries=0,stream_max_retries=0}`, model+"/v1"),
			"-c", `approval_policy="never"`, "-c", `sandbox_mode="danger-full-access"`, "run it"}
	},
	setup:   codexSetup,
	records: []string{"", "session", "prompt", "result"},
}

func responsesWire(id string) wire {
	respond := func(w http.ResponseWriter, item map[string]any) {
		usage := map[string]any{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0}
		writeSSE(w, true,
			map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_e2e"}},
			map[string]any{"type": "response.output_item.done", "item": item},
			map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_e2e", "usage": usage}})
	}
	return wire{
		path: "/responses",
		result: func(body []byte) (string, bool) {
			var req struct {
				Input []struct {
					Type   string `json:"type"`
					CallID string `json:"call_id"`
					Output any    `json:"output"`
				} `json:"input"`
			}
			_ = json.Unmarshal(body, &req)
			for _, in := range req.Input {
				if strings.HasSuffix(in.Type, "_output") && in.CallID == id {
					return text(in.Output), true
				}
			}
			return "", false
		},
		call: func(w http.ResponseWriter, _ []byte, c toolCall) {
			if c.args == nil {
				respond(w, map[string]any{"type": "custom_tool_call", "call_id": id, "name": c.name, "input": c.input})
				return
			}
			args, _ := json.Marshal(c.args)
			respond(w, map[string]any{"type": "function_call", "call_id": id, "name": c.name, "arguments": string(args)})
		},
		reply: func(w http.ResponseWriter, _ []byte) {
			respond(w, map[string]any{"type": "message", "role": "assistant", "id": "msg_e2e",
				"content": []any{map[string]any{"type": "output_text", "text": "done"}}})
		},
	}
}

// codexSetup checks that Codex loads every hook, that it skips them until
// they are trusted, and then trusts them as /hooks does.
func codexSetup(t *testing.T, a agent, h *home, dir string) {
	t.Helper()
	bin := os.Getenv(a.binEnv)
	hooks := codexHooks(t, h, bin, dir)
	events := map[string]bool{}
	for _, hk := range hooks {
		events[hk["eventName"].(string)] = true
	}
	for _, e := range []string{"preToolUse", "postToolUse", "sessionStart", "userPromptSubmit"} {
		if !events[e] {
			t.Fatalf("Codex did not load the %s hook: %v", e, hooks)
		}
	}
	if out := a.run(t, h, bin, dir, a.shell(reviewCmd)); strings.Contains(out, "blast-radius") {
		t.Fatalf("an untrusted hook ran: %s", out)
	}
	var b strings.Builder
	for _, hk := range hooks {
		fmt.Fprintf(&b, "[hooks.state.%s]\ntrusted_hash = %q\n\n", strconv.Quote(hk["key"].(string)), hk["currentHash"])
	}
	h.write(t, filepath.Join(".codex", "config.toml"), b.String())
}

// codexHooks asks Codex's app-server which hooks it loaded for dir.
func codexHooks(t *testing.T, h *home, bin, dir string) []map[string]any {
	t.Helper()
	cmd := exec.Command(bin, "app-server")
	cmd.Env = h.env
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

// Codex also edits files with apply_patch, which the hook checks too.
func TestCodexPatches(t *testing.T) {
	bin := os.Getenv(codexAgent.binEnv)
	if bin == "" {
		t.Skipf("set %s to the codex binary", codexAgent.binEnv)
	}
	h := newHome(t, codexAgent)
	dir := filepath.Join(h.dir, "project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	codexSetup(t, codexAgent, h, dir)
	patch := func(body string) toolCall {
		return toolCall{name: "apply_patch", input: "*** Begin Patch\n" + body + "*** End Patch\n"}
	}
	hooks := filepath.Join(h.dir, ".codex", "hooks.json")
	if out := codexAgent.run(t, h, bin, dir, patch("*** Delete File: "+hooks+"\n")); !strings.Contains(out, "blast-radius settings") {
		t.Errorf("patching the hooks file: %s", out)
	}
	if _, err := os.Stat(hooks); err != nil {
		t.Errorf("hooks file: %v", err)
	}
	if out := codexAgent.run(t, h, bin, dir, patch("*** Add File: notes.txt\n+hello\n")); !strings.Contains(out, "notes.txt") {
		t.Errorf("an ordinary patch: %s", out)
	}
}
