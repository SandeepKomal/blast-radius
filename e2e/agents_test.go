//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Every supported agent is tested with its real CLI and the hooks that
// `blast-radius install` writes for it. A mock model stands in for the
// agent's model: it asks the agent to make one tool call, then reads what the
// agent sent back as that call's result. Set the agents' binaries to run
// them:
//
//	BLAST_RADIUS_E2E_CLAUDE=$(command -v claude) BLAST_RADIUS_E2E_CODEX=$(command -v codex) \
//	BLAST_RADIUS_E2E_GEMINI=$(command -v gemini) BLAST_RADIUS_E2E_COPILOT=$(command -v copilot) \
//	go test -tags e2e -run Agents ./e2e/

// Deleting from clusters that don't exist: the preview fails whether or not
// kubectl is installed, so blast-radius treats the command as destructive,
// and the command itself can't change anything.
const (
	reviewCmd     = "kubectl delete namespace payments --context e2e-staging-missing"
	productionCmd = "kubectl delete namespace payments --context e2e-prod-missing"
)

// toolCall is what the mock model asks for: a tool with JSON arguments, or a
// free-form tool (Codex's apply_patch) with its input.
type toolCall struct {
	name  string
	args  map[string]any
	input string
}

// wire is a model API as the mock model speaks it.
type wire struct {
	path   string                           // suffix of the request path it answers
	result func(body []byte) (string, bool) // the call's result, once the agent sends it
	call   func(w http.ResponseWriter, req []byte, c toolCall)
	reply  func(w http.ResponseWriter, req []byte) // a short text answer
}

// agent is one agent CLI under test.
type agent struct {
	name, platform, binEnv string
	hookFile               string // relative to the home directory
	wire                   wire
	shell                  func(command string) toolCall
	env                    func(model string) []string // how the agent finds the mock model
	args                   func(dir, model string) []string
	setup                  func(t *testing.T, a agent, h *home, dir string) // after install (optional)
	records                []string                                         // kinds the recording hooks write
}

// home is a throwaway home directory with blast-radius's activity log on.
type home struct {
	dir string
	env []string
}

// agentVars are prefixes of the environment variables the agents read: none
// reaches an agent under test, so each starts clean and only the mock model is
// reachable, whatever session the tests run in.
var agentVars = []string{"ANTHROPIC_", "CLAUDE", "CODEX_", "OPENAI_", "GEMINI_", "GOOGLE_", "COPILOT_", "GITHUB_TOKEN=", "GH_TOKEN="}

func newHome(t *testing.T, a agent) *home {
	t.Helper()
	h := &home{dir: t.TempDir()}
	for _, kv := range os.Environ() {
		if !slices.ContainsFunc(agentVars, func(p string) bool { return strings.HasPrefix(kv, p) }) {
			h.env = append(h.env, kv)
		}
	}
	h.env = append(h.env, "HOME="+h.dir, "USERPROFILE="+h.dir, "XDG_CONFIG_HOME="+filepath.Join(h.dir, ".config"),
		"CODEX_HOME="+filepath.Join(h.dir, ".codex"), "COPILOT_HOME="+filepath.Join(h.dir, ".copilot"))
	h.write(t, filepath.Join(".config", "blast-radius", "config.json"),
		fmt.Sprintf(`{"audit_log": %q, "audit_all": true}`, filepath.Join(h.dir, "audit.jsonl")))
	install := exec.Command(binary, "install", "--agent", a.name)
	install.Env = h.env
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("install --agent %s: %v\n%s", a.name, err, out)
	}
	return h
}

func (h *home) write(t *testing.T, rel, content string) {
	t.Helper()
	p := filepath.Join(h.dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// run has the agent make c in dir and returns the result it reported.
func (a agent) run(t *testing.T, h *home, bin, dir string, c toolCall) string {
	t.Helper()
	var mu sync.Mutex
	called, result := false, ""
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, a.wire.path) {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Tools []json.RawMessage `json:"tools"`
		}
		_ = json.Unmarshal(body, &req)
		mu.Lock()
		defer mu.Unlock()
		if res, ok := a.wire.result(body); ok {
			result = res
		}
		// Only a request that offers tools is the agent's own turn; others
		// (titles, summaries) get text.
		if !called && len(req.Tools) > 0 {
			called = true
			a.wire.call(w, body, c)
			return
		}
		a.wire.reply(w, body)
	}))
	defer model.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, a.args(dir, model.URL)...)
	cmd.Dir, cmd.Env = dir, slices.Concat(h.env, a.env(model.URL))
	out, err := cmd.CombinedOutput()
	mu.Lock()
	defer mu.Unlock()
	if result == "" {
		t.Fatalf("%s sent no tool result to the model (%v):\n%s", a.name, err, out)
	}
	return result
}

func TestAgents(t *testing.T) {
	for _, a := range []agent{claudeAgent, codexAgent, geminiAgent, copilotAgent} {
		t.Run(a.name, func(t *testing.T) {
			bin := os.Getenv(a.binEnv)
			if bin == "" {
				t.Skipf("set %s to the %s binary", a.binEnv, a.name)
			}
			h := newHome(t, a)
			dir := filepath.Join(h.dir, "project")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if a.setup != nil {
				a.setup(t, a, h, dir)
			}
			hookFile := filepath.Join(h.dir, a.hookFile)
			for _, tc := range []struct {
				name, command string
				wants         []string
			}{
				{"a harmless command runs", "echo hello-from-e2e", []string{"hello-from-e2e"}},
				{"a destroy needs review", reviewCmd, []string{"NEEDS APPROVAL"}},
				{"production is blocked", productionCmd, []string{"BLOCKED", "Do not retry"}},
				{"removing the hook is stopped", "rm " + hookFile, nil},
			} {
				out := a.run(t, h, bin, dir, a.shell(tc.command))
				for _, want := range tc.wants {
					if !strings.Contains(out, want) {
						t.Errorf("%s: result lacks %q:\n%s", tc.name, want, out)
					}
				}
			}
			if _, err := os.Stat(hookFile); err != nil {
				t.Errorf("the agent removed its blast-radius hook: %v", err)
			}
			checkLog(t, h, a, map[string]string{"echo hello-from-e2e": "pass", reviewCmd: "ask", productionCmd: "deny", "rm " + hookFile: "ask"})
		})
	}
}

// checkLog checks that the activity log attributes every line to the agent,
// has its recorded kinds, and recorded each command with the decision given.
func checkLog(t *testing.T, h *home, a agent, decisions map[string]string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var e struct {
			Kind, Command, Action string
			Platform              string `json:"agent_platform"`
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		if e.Platform != a.platform {
			t.Errorf("activity log line attributed to %q, want %q: %s", e.Platform, a.platform, line)
		}
		kinds[e.Kind] = true
		if want, ok := decisions[e.Command]; ok {
			if e.Action != want {
				t.Errorf("%q was recorded as %q, want %q", e.Command, e.Action, want)
			}
			delete(decisions, e.Command)
		}
	}
	for cmd := range decisions {
		t.Errorf("%q is not in the activity log", cmd)
	}
	for _, k := range a.records {
		if !kinds[k] {
			t.Errorf("activity log has no %q lines: %s", k, b)
		}
	}
}

// writeSSE writes server-sent events; named ones carry their type as the
// event name, as the Anthropic and OpenAI Responses APIs do.
func writeSSE(w http.ResponseWriter, named bool, events ...map[string]any) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, ev := range events {
		data, _ := json.Marshal(ev)
		if named {
			fmt.Fprintf(w, "event: %s\n", ev["type"])
		}
		fmt.Fprintf(w, "data: %s\n\n", data)
	}
}

// text turns a tool result that may be a string or JSON into text.
func text(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// Claude Code: the Anthropic Messages API.

var claudeAgent = agent{
	name: "claude", platform: "claude_code", binEnv: "BLAST_RADIUS_E2E_CLAUDE",
	hookFile: filepath.Join(".claude", "settings.json"),
	wire:     anthropicWire("toolu_e2e"),
	shell: func(c string) toolCall {
		return toolCall{name: "Bash", args: map[string]any{"command": c, "description": "e2e"}}
	},
	env: func(model string) []string {
		return []string{"ANTHROPIC_BASE_URL=" + model, "ANTHROPIC_API_KEY=sk-e2e", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1"}
	},
	args:    func(string, string) []string { return []string{"-p", "run it", "--output-format", "text"} },
	records: []string{"", "session", "prompt", "result"},
}

func anthropicWire(id string) wire {
	message := func(w http.ResponseWriter, stop string, block map[string]any, delta map[string]any) {
		writeSSE(w, true,
			map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_e2e", "type": "message", "role": "assistant", "model": "claude-e2e",
				"content": []any{}, "stop_reason": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1}}},
			map[string]any{"type": "content_block_start", "index": 0, "content_block": block},
			map[string]any{"type": "content_block_delta", "index": 0, "delta": delta},
			map[string]any{"type": "content_block_stop", "index": 0},
			map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop}, "usage": map[string]any{"output_tokens": 1}},
			map[string]any{"type": "message_stop"})
	}
	return wire{
		path: "/v1/messages",
		result: func(body []byte) (string, bool) {
			var req struct {
				Messages []struct {
					Content json.RawMessage `json:"content"`
				} `json:"messages"`
			}
			_ = json.Unmarshal(body, &req)
			for _, m := range req.Messages {
				var blocks []struct {
					Type    string `json:"type"`
					ID      string `json:"tool_use_id"`
					Content any    `json:"content"`
				}
				if json.Unmarshal(m.Content, &blocks) != nil {
					continue
				}
				for _, b := range blocks {
					if b.Type == "tool_result" && b.ID == id {
						return text(b.Content), true
					}
				}
			}
			return "", false
		},
		call: func(w http.ResponseWriter, _ []byte, c toolCall) {
			args, _ := json.Marshal(c.args)
			message(w, "tool_use", map[string]any{"type": "tool_use", "id": id, "name": c.name, "input": map[string]any{}},
				map[string]any{"type": "input_json_delta", "partial_json": string(args)})
		},
		reply: func(w http.ResponseWriter, _ []byte) {
			message(w, "end_turn", map[string]any{"type": "text", "text": ""}, map[string]any{"type": "text_delta", "text": "done"})
		},
	}
}

// Gemini CLI: the Gemini API's streamGenerateContent.

var geminiAgent = agent{
	name: "gemini", platform: "gemini_cli", binEnv: "BLAST_RADIUS_E2E_GEMINI",
	hookFile: filepath.Join(".gemini", "settings.json"),
	wire:     geminiWire,
	shell: func(c string) toolCall {
		return toolCall{name: "run_shell_command", args: map[string]any{"command": c}}
	},
	env: func(model string) []string {
		return []string{"GOOGLE_GEMINI_BASE_URL=" + model, "GEMINI_API_KEY=e2e", "GEMINI_CLI_NO_RELAUNCH=true"}
	},
	args: func(string, string) []string { return []string{"-p", "run it", "--yolo", "-m", "gemini-2.5-flash"} },
	// Gemini CLI needs an auth method and, to run headless, a trusted folder.
	setup: func(t *testing.T, _ agent, h *home, dir string) {
		p := filepath.Join(h.dir, ".gemini", "settings.json")
		var settings map[string]any
		if b, err := os.ReadFile(p); err != nil || json.Unmarshal(b, &settings) != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		settings["security"] = map[string]any{"auth": map[string]any{"selectedType": "gemini-api-key"}}
		b, _ := json.Marshal(settings)
		h.write(t, filepath.Join(".gemini", "settings.json"), string(b))
		trusted, _ := json.Marshal(map[string]string{dir: "TRUST_FOLDER"})
		h.write(t, filepath.Join(".gemini", "trustedFolders.json"), string(trusted))
	},
	records: []string{""},
}

var geminiWire = wire{
	path: ":streamGenerateContent",
	result: func(body []byte) (string, bool) {
		var req struct {
			Contents []struct {
				Parts []struct {
					FunctionResponse *struct {
						Response any `json:"response"`
					} `json:"functionResponse"`
				} `json:"parts"`
			} `json:"contents"`
		}
		_ = json.Unmarshal(body, &req)
		for _, c := range req.Contents {
			for _, p := range c.Parts {
				if p.FunctionResponse != nil {
					return text(p.FunctionResponse.Response), true
				}
			}
		}
		return "", false
	},
	call: func(w http.ResponseWriter, _ []byte, c toolCall) {
		geminiChunk(w, map[string]any{"functionCall": map[string]any{"name": c.name, "args": c.args}})
	},
	reply: func(w http.ResponseWriter, _ []byte) { geminiChunk(w, map[string]any{"text": "done"}) },
}

func geminiChunk(w http.ResponseWriter, part map[string]any) {
	writeSSE(w, false, map[string]any{
		"candidates":    []any{map[string]any{"content": map[string]any{"role": "model", "parts": []any{part}}, "finishReason": "STOP", "index": 0}},
		"usageMetadata": map[string]any{"promptTokenCount": 1, "candidatesTokenCount": 1, "totalTokenCount": 2},
	})
}

// GitHub Copilot CLI: an OpenAI-compatible chat completions provider (BYOK).

var copilotAgent = agent{
	name: "copilot", platform: "copilot", binEnv: "BLAST_RADIUS_E2E_COPILOT",
	hookFile: filepath.Join(".copilot", "hooks", "blast-radius.json"),
	wire:     chatWire("call_e2e"),
	shell: func(c string) toolCall {
		return toolCall{name: "bash", args: map[string]any{"command": c, "description": "e2e", "mode": "sync"}}
	},
	env: func(model string) []string {
		return []string{"COPILOT_OFFLINE=true", "COPILOT_PROVIDER_BASE_URL=" + model, "COPILOT_PROVIDER_API_KEY=e2e",
			"COPILOT_MODEL=gpt-4.1", "COPILOT_AUTO_UPDATE=false"}
	},
	args:    func(string, string) []string { return []string{"-p", "run it", "--allow-all-tools", "--no-color"} },
	records: []string{""},
}

func chatWire(id string) wire {
	// completion answers with one message, streamed when the request asks.
	completion := func(w http.ResponseWriter, req []byte, message map[string]any, finish string) {
		var r struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(req, &r)
		usage := map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
		if !r.Stream {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "chatcmpl-e2e", "object": "chat.completion", "created": 1, "model": "gpt-4.1",
				"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}, "usage": usage})
			return
		}
		if calls, ok := message["tool_calls"].([]any); ok {
			calls[0].(map[string]any)["index"] = 0
		}
		chunk := func(delta map[string]any, finish any) map[string]any {
			return map[string]any{"id": "chatcmpl-e2e", "object": "chat.completion.chunk", "created": 1, "model": "gpt-4.1",
				"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}, "usage": usage}
		}
		writeSSE(w, false, chunk(message, nil), chunk(map[string]any{}, finish))
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
	return wire{
		path: "/chat/completions",
		result: func(body []byte) (string, bool) {
			var req struct {
				Messages []struct {
					Role       string `json:"role"`
					ToolCallID string `json:"tool_call_id"`
					Content    any    `json:"content"`
				} `json:"messages"`
			}
			_ = json.Unmarshal(body, &req)
			for _, m := range req.Messages {
				if m.Role == "tool" && m.ToolCallID == id {
					return text(m.Content), true
				}
			}
			return "", false
		},
		call: func(w http.ResponseWriter, req []byte, c toolCall) {
			args, _ := json.Marshal(c.args)
			completion(w, req, map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{
				"id": id, "type": "function", "function": map[string]any{"name": c.name, "arguments": string(args)}}}}, "tool_calls")
		},
		reply: func(w http.ResponseWriter, req []byte) {
			completion(w, req, map[string]any{"role": "assistant", "content": "done"}, "stop")
		},
	}
}

// Agents without a built-in hook call `blast-radius check --agent NAME` from
// their own hook or wrapper, and go by its exit code: 0 runs the command, 10
// asks the user, 20 blocks it.
func TestOtherAgents(t *testing.T) {
	h := newHome(t, claudeAgent)
	dir := filepath.Join(h.dir, "project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	type record struct{ agent, action string }
	want := map[string]record{}
	for _, name := range []string{"cursor", "windsurf", "cline", "aider", "opencode", "roo_code", "continue", "amazon_q"} {
		for _, tc := range []struct {
			command, action string
			exit            int
		}{
			{"echo hello from " + name, "pass", 0},
			{reviewCmd + "-" + name, "ask", 10},
			{productionCmd + "-" + name, "deny", 20},
		} {
			cmd := exec.Command(binary, "check", "--agent", name, "--session", name+"-session", "-C", dir, tc.command)
			cmd.Env = h.env
			out, err := cmd.CombinedOutput()
			code := 0
			if exit := (*exec.ExitError)(nil); errors.As(err, &exit) {
				code = exit.ExitCode()
			}
			if code != tc.exit {
				t.Errorf("%s: %q exited %d, want %d:\n%s", name, tc.command, code, tc.exit, out)
			}
			want[tc.command] = record{name, tc.action}
		}
	}
	b, err := os.ReadFile(filepath.Join(h.dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var e struct {
			Command, Action string
			Platform        string `json:"agent_platform"`
			Session         string `json:"session_id"`
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		w := want[e.Command]
		if e.Platform != w.agent || e.Session != w.agent+"-session" || e.Action != w.action {
			t.Errorf("recorded as %s/%s/%s, want %s/%s-session/%s: %s", e.Platform, e.Session, e.Action, w.agent, w.agent, w.action, line)
		}
		delete(want, e.Command)
	}
	for c := range want {
		t.Errorf("%q is not in the activity log", c)
	}
}
