package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sandeepkomal/blast-radius/internal/analyze"
)

func panicRunner(context.Context, string, []string, string, ...string) (analyze.Result, error) {
	panic("simulated analyzer failure")
}

// prodDir returns a directory the default policy treats as production.
func prodDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "envs", "prod")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runGemini(t *testing.T, input any, run analyze.Runner) geminiHookOutput {
	t.Helper()
	b, ok := input.([]byte)
	if !ok {
		b, _ = json.Marshal(input)
	}
	var out, errOut bytes.Buffer
	if code := hookGemini(bytes.NewReader(b), &out, &errOut, run); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var o geminiHookOutput
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &o); err != nil {
			t.Fatalf("invalid output %q: %v", out.String(), err)
		}
	}
	return o
}

func geminiShell(command, cwd string) map[string]any {
	return map[string]any{
		"hook_event_name": "BeforeTool", "session_id": "gemini-session", "cwd": cwd,
		"tool_name": "run_shell_command", "tool_input": map[string]any{"command": command},
	}
}

func TestGeminiHook(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	run := planRunner(destroyPlan)

	if o := runGemini(t, geminiShell("ls -la", dir), run); o.Decision != "" {
		t.Errorf("harmless command: %+v", o)
	}
	if o := runGemini(t, geminiShell("terraform destroy", dir), run); o.Decision != "deny" ||
		!strings.Contains(o.Reason, "requires human review") || !strings.Contains(o.Reason, "aws_db_instance.main") {
		t.Errorf("destroy needing review: %+v", o)
	}
	if o := runGemini(t, geminiShell("terraform destroy", prodDir(t)), run); o.Decision != "deny" || !strings.Contains(o.Reason, "Do not retry") {
		t.Errorf("production destroy: %+v", o)
	}
	// dir_path is relative to the hook's cwd.
	in := geminiShell("terraform destroy", filepath.Dir(filepath.Dir(prodDir(t))))
	in["tool_input"] = map[string]any{"command": "terraform destroy", "dir_path": filepath.Join("envs", "prod")}
	if o := runGemini(t, in, run); o.Decision != "deny" || !strings.Contains(o.Reason, "production") {
		t.Errorf("dir_path into production: %+v", o)
	}
	other := map[string]any{"hook_event_name": "BeforeTool", "tool_name": "read_file", "tool_input": map[string]any{"file_path": "x"}}
	if o := runGemini(t, other, run); o.Decision != "" {
		t.Errorf("other tool: %+v", o)
	}
}

func TestGeminiHookGuardsItsSettings(t *testing.T) {
	home := isolate(t)
	for _, tool := range []string{"write_file", "replace"} {
		in := map[string]any{
			"hook_event_name": "BeforeTool", "cwd": home, "tool_name": tool,
			"tool_input": map[string]any{"file_path": filepath.Join(".gemini", "settings.json")},
		}
		if o := runGemini(t, in, planRunner(destroyPlan)); o.Decision != "deny" || !strings.Contains(o.Reason, "blast-radius settings") {
			t.Errorf("%s of settings: %+v", tool, o)
		}
	}
	in := map[string]any{"hook_event_name": "BeforeTool", "cwd": home, "tool_name": "write_file", "tool_input": map[string]any{"file_path": "main.go"}}
	if o := runGemini(t, in, planRunner(destroyPlan)); o.Decision != "" {
		t.Errorf("ordinary write: %+v", o)
	}
}

func TestGeminiHookFailsClosed(t *testing.T) {
	isolate(t)
	cases := map[string]struct {
		input any
		run   analyze.Runner
	}{
		"malformed input": {[]byte("not json"), planRunner(destroyPlan)},
		"empty command":   {geminiShell("", t.TempDir()), planRunner(destroyPlan)},
		"no file path":    {map[string]any{"hook_event_name": "BeforeTool", "tool_name": "write_file", "tool_input": map[string]any{}}, planRunner(destroyPlan)},
		"analyzer panic":  {geminiShell("terraform destroy", t.TempDir()), panicRunner},
	}
	for name, tc := range cases {
		if o := runGemini(t, tc.input, tc.run); o.Decision != "deny" || o.Reason == "" {
			t.Errorf("%s: %+v", name, o)
		}
	}
}

func TestGeminiHookExitsTwoWhenOutputFails(t *testing.T) {
	isolate(t)
	b, _ := json.Marshal(geminiShell("terraform destroy", t.TempDir()))
	var errOut bytes.Buffer
	if code := hookGemini(bytes.NewReader(b), failingWriter{}, &errOut, planRunner(destroyPlan)); code != 2 || !strings.Contains(errOut.String(), "terraform destroy") {
		t.Errorf("exit %d, stderr %q", code, errOut.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }

func runCopilot(t *testing.T, input any, run analyze.Runner) copilotHookOutput {
	t.Helper()
	b, ok := input.([]byte)
	if !ok {
		b, _ = json.Marshal(input)
	}
	var out, errOut bytes.Buffer
	if code := hookCopilot(bytes.NewReader(b), &out, &errOut, run); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var o copilotHookOutput
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &o); err != nil {
			t.Fatalf("invalid output %q: %v", out.String(), err)
		}
	}
	return o
}

func copilotShell(toolInput any, cwd string) map[string]any {
	return map[string]any{
		"hook_event_name": "PreToolUse", "session_id": "copilot-session", "timestamp": "2026-10-27T10:00:00Z",
		"cwd": cwd, "tool_name": "Bash", "tool_input": toolInput,
	}
}

func TestCopilotHook(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	run := planRunner(destroyPlan)

	if o := runCopilot(t, copilotShell(map[string]any{"command": "git status"}, dir), run); o.PermissionDecision != "" {
		t.Errorf("harmless command: %+v", o)
	}
	o := runCopilot(t, copilotShell(map[string]any{"command": "terraform destroy", "description": "clean up"}, dir), run)
	if o.PermissionDecision != "ask" || !strings.Contains(o.PermissionDecisionReason, "WARNING: this deletes 1 resource") {
		t.Errorf("destroy: %+v", o)
	}
	// Arguments Copilot couldn't parse arrive as a JSON string.
	if o := runCopilot(t, copilotShell(`{"command":"terraform destroy"}`, prodDir(t)), run); o.PermissionDecision != "deny" || !strings.Contains(o.PermissionDecisionReason, "Do not retry") {
		t.Errorf("production destroy: %+v", o)
	}
	other := map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Read", "tool_input": map[string]any{"path": "x"}}
	if o := runCopilot(t, other, run); o.PermissionDecision != "" {
		t.Errorf("other tool: %+v", o)
	}
}

func TestCopilotHookGuardsItsSettings(t *testing.T) {
	home := isolate(t)
	hooks := filepath.Join(home, ".copilot", "hooks", "blast-radius.json")
	patch := "*** Begin Patch\n*** Delete File: " + hooks + "\n*** End Patch"
	for name, in := range map[string]map[string]any{
		"edit path":  {"tool_name": "Edit", "tool_input": map[string]any{"path": hooks}},
		"write path": {"tool_name": "Write", "tool_input": map[string]any{"file_path": hooks}},
		"patch":      {"tool_name": "Edit", "tool_input": map[string]any{"input": patch}},
	} {
		in["hook_event_name"], in["cwd"] = "PreToolUse", home
		if o := runCopilot(t, in, planRunner(destroyPlan)); o.PermissionDecision != "ask" || !strings.Contains(o.PermissionDecisionReason, "blast-radius settings") {
			t.Errorf("%s: %+v", name, o)
		}
	}
	ordinary := map[string]any{"hook_event_name": "PreToolUse", "cwd": home, "tool_name": "Edit", "tool_input": map[string]any{"path": "main.go"}}
	if o := runCopilot(t, ordinary, planRunner(destroyPlan)); o.PermissionDecision != "" {
		t.Errorf("ordinary edit: %+v", o)
	}
	unknown := map[string]any{"hook_event_name": "PreToolUse", "cwd": home, "tool_name": "Edit", "tool_input": map[string]any{"text": "?"}}
	if o := runCopilot(t, unknown, planRunner(destroyPlan)); o.PermissionDecision != "ask" {
		t.Errorf("edit of an unknown file: %+v", o)
	}
}

func TestCopilotHookFailsClosed(t *testing.T) {
	isolate(t)
	cases := map[string]struct {
		input any
		run   analyze.Runner
	}{
		"malformed input": {[]byte("{"), planRunner(destroyPlan)},
		"no command":      {copilotShell(map[string]any{"description": "x"}, t.TempDir()), planRunner(destroyPlan)},
		"unparsed args":   {copilotShell("terraform destroy", t.TempDir()), planRunner(destroyPlan)},
		"analyzer panic":  {copilotShell(map[string]any{"command": "terraform destroy"}, t.TempDir()), panicRunner},
	}
	for name, tc := range cases {
		if o := runCopilot(t, tc.input, tc.run); o.PermissionDecision != "deny" || o.PermissionDecisionReason == "" {
			t.Errorf("%s: %+v", name, o)
		}
	}
	b, _ := json.Marshal(copilotShell(map[string]any{"command": "terraform destroy"}, t.TempDir()))
	if code := hookCopilot(bytes.NewReader(b), failingWriter{}, &bytes.Buffer{}, planRunner(destroyPlan)); code == 0 {
		t.Error("unwritable output must exit non-zero, which Copilot treats as deny")
	}
}

func TestCodexHookGuardsPatches(t *testing.T) {
	home := isolate(t)
	patch := func(files ...string) string {
		var b strings.Builder
		b.WriteString("*** Begin Patch\n")
		for _, f := range files {
			b.WriteString("*** Update File: " + f + "\n@@\n-a\n+b\n")
		}
		b.WriteString("*** End Patch\n")
		return b.String()
	}
	input := func(command string) string {
		b, _ := json.Marshal(map[string]any{
			"hook_event_name": "PreToolUse", "tool_name": "apply_patch", "tool_use_id": "call-1",
			"session_id": "codex-session", "cwd": home, "tool_input": map[string]string{"command": command},
		})
		return string(b)
	}
	if d, _ := runCodexHookTest(t, input(patch("main.go", "README.md")), planRunner(destroyPlan)); d != "" {
		t.Errorf("ordinary patch decision = %q", d)
	}
	// Relative to the cwd, as Codex resolves them.
	if d, r := runCodexHookTest(t, input(patch("main.go", filepath.Join(".codex", "hooks.json"))), planRunner(destroyPlan)); d != "deny" || !strings.Contains(r, "blast-radius settings") {
		t.Errorf("patch of the hooks file: %q %q", d, r)
	}
	if d, _ := runCodexHookTest(t, input("*** Begin Patch\n*** End Patch"), planRunner(destroyPlan)); d != "deny" {
		t.Errorf("patch without files decision = %q", d)
	}
	heredoc, _ := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse", "tool_name": "Bash", "cwd": home,
		"tool_input": map[string]string{"command": "apply_patch <<'EOF'\n" + patch(filepath.Join(".codex", "hooks.json")) + "EOF\n"},
	})
	if d, r := runCodexHookTest(t, string(heredoc), planRunner(destroyPlan)); d != "deny" || !strings.Contains(r, "blast-radius settings") {
		t.Errorf("patch of the hooks file through the shell: %q %q", d, r)
	}
}

func TestPatchFiles(t *testing.T) {
	got := patchFiles("*** Begin Patch\n*** Add File: a.txt\n+x\n*** Delete File: b.txt\n  *** Update File: c.txt\n*** Move to: d.txt\n*** End Patch")
	if strings.Join(got, ",") != "a.txt,b.txt,c.txt,d.txt" {
		t.Errorf("patchFiles = %v", got)
	}
}

func TestCodexRecordsSessionsAndModel(t *testing.T) {
	logPath := withAuditLog(t, true)
	dir := t.TempDir()
	send := func(event map[string]any) {
		t.Helper()
		b, _ := json.Marshal(event)
		var out, errOut bytes.Buffer
		if code := hookCodex(bytes.NewReader(b), &out, &errOut, planRunner(destroyPlan)); code != 0 || out.Len() != 0 {
			t.Fatalf("exit %d, output %q, stderr %q", code, out.String(), errOut.String())
		}
	}
	base := func(event string) map[string]any {
		return map[string]any{"hook_event_name": event, "session_id": "s1", "cwd": dir, "model": "gpt-test", "transcript_path": nil}
	}
	start := base("SessionStart")
	start["source"] = "startup"
	send(start)
	prompt := base("UserPromptSubmit")
	prompt["prompt"] = "list the pods"
	send(prompt)
	pre := base("PreToolUse")
	pre["tool_name"], pre["tool_use_id"], pre["tool_input"] = "Bash", "call-1", map[string]any{"command": "kubectl get pods"}
	send(pre)
	post := base("PostToolUse")
	post["tool_name"], post["tool_use_id"] = "Bash", "call-1"
	post["tool_input"], post["tool_response"] = map[string]any{"command": "kubectl get pods"}, "ok"
	send(post)

	lines := readLog(t, logPath)
	kinds := []string{}
	for _, l := range lines {
		if l["agent_platform"] != "codex" || l["session_id"] != "s1" {
			t.Errorf("not attributed to the Codex session: %v", l)
		}
		kind, _ := l["kind"].(string)
		kinds = append(kinds, kind)
	}
	if strings.Join(kinds, ",") != "session,prompt,,result" {
		t.Fatalf("kinds = %v\n%v", kinds, lines)
	}
	if lines[0]["model"] != "gpt-test" || lines[2]["model"] != "gpt-test" || lines[3]["outcome"] != "ran" {
		t.Errorf("lines = %v", lines)
	}
}

func TestInstallOtherAgents(t *testing.T) {
	home := isolate(t)
	codexPath := filepath.Join(home, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(codexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	theirs := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"lint.sh"}]}]}}`
	if err := os.WriteFile(codexPath, []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, agent := range []string{"codex", "gemini", "copilot"} {
		var out, errOut bytes.Buffer
		if code := install(true, []string{"--agent", agent}, &out, &errOut); code != 0 {
			t.Fatalf("install %s: %d %s", agent, code, errOut.String())
		}
		path, _ := hookTargets[agent].path(false)
		h, err := findAgentHook(path, hookTargets[agent])
		if err != nil || h == nil {
			t.Fatalf("%s: no hook in %s (%v)", agent, path, err)
		}
		if p := hookProgram(h, agent); !filepath.IsAbs(p) {
			t.Errorf("%s hook runs %q, want an absolute path", agent, p)
		}
		out.Reset()
		install(true, []string{"--agent", agent}, &out, &errOut)
		if !strings.Contains(out.String(), "already installed") {
			t.Errorf("%s: second install: %s", agent, out.String())
		}
	}

	var codex struct {
		Hooks map[string][]struct {
			Matcher string           `json:"matcher"`
			Hooks   []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	b, _ := os.ReadFile(codexPath)
	if err := json.Unmarshal(b, &codex); err != nil {
		t.Fatal(err)
	}
	pre := codex.Hooks["PreToolUse"]
	if len(pre) != 2 || pre[0].Hooks[0]["command"] != "lint.sh" || pre[1].Matcher != "Bash|apply_patch" || pre[1].Hooks[0]["timeout"] != float64(hookTimeoutSeconds) {
		t.Errorf("Codex PreToolUse = %+v", pre)
	}
	for _, event := range []string{"PostToolUse", "SessionStart", "UserPromptSubmit"} {
		if len(codex.Hooks[event]) != 1 {
			t.Errorf("Codex %s = %+v", event, codex.Hooks[event])
		}
	}

	var doc bytes.Buffer
	doctor(&doc, planRunner(""))
	for _, want := range []string{"Codex hook installed", "Gemini CLI hook installed", "GitHub Copilot CLI hook installed", "self-test: hook decisions are correct"} {
		if !strings.Contains(doc.String(), want) {
			t.Errorf("doctor output lacks %q:\n%s", want, doc.String())
		}
	}
	if strings.Contains(doc.String(), "no blast-radius hook in") {
		t.Errorf("doctor says nothing is installed:\n%s", doc.String())
	}

	for _, agent := range []string{"codex", "gemini", "copilot"} {
		var out bytes.Buffer
		if code := install(false, []string{"--agent", agent}, &out, &bytes.Buffer{}); code != 0 || !strings.Contains(out.String(), "Removed") {
			t.Errorf("uninstall %s: %d %s", agent, code, out.String())
		}
	}
	if b, _ := os.ReadFile(codexPath); normalizeString(b) != normalizeString([]byte(theirs)) {
		t.Errorf("uninstall changed the user's own hooks: %s", b)
	}
	if p, _ := copilotHooksPath(false); fileExists(p) {
		t.Errorf("%s still exists after uninstall", p)
	}
}

func normalizeString(b []byte) string { return string(normalize(b)) }

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestCopilotInstallKeepsForeignFile(t *testing.T) {
	isolate(t)
	path, _ := copilotHooksPath(false)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"hooks":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	if code := install(true, []string{"--agent", "copilot"}, &bytes.Buffer{}, &errOut); code != 1 || !strings.Contains(errOut.String(), "move it away") {
		t.Errorf("install over a foreign file: %d %s", code, errOut.String())
	}
	if code := install(true, []string{"--agent", "cursor"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
		t.Errorf("unknown agent: exit %d", code)
	}
}

func TestShellCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		program := `C:\Program Files\it's\blast-radius.exe`
		for powershell, want := range map[bool]string{
			false: `"C:\Program Files\it's\blast-radius.exe" hook codex`,
			true:  `& 'C:\Program Files\it''s\blast-radius.exe' hook codex`,
		} {
			got := shellCommand(program, powershell, "hook", "codex")
			if got != want {
				t.Errorf("shellCommand(powershell %v) = %s, want %s", powershell, got, want)
			}
			if back := hookProgram(map[string]any{"command": got}, "codex"); back != program {
				t.Errorf("hookProgram(%s) = %q", got, back)
			}
		}
		return
	}
	for program, want := range map[string]string{
		"/usr/local/bin/blast-radius":   "/usr/local/bin/blast-radius hook codex",
		"/Users/a b/bin/blast-radius":   "'/Users/a b/bin/blast-radius' hook codex",
		"/home/o'neil/bin/blast-radius": `'/home/o'\''neil/bin/blast-radius' hook codex`,
	} {
		got := shellCommand(program, false, "hook", "codex")
		if got != want {
			t.Errorf("shellCommand(%q) = %s, want %s", program, got, want)
		}
		if back := hookProgram(map[string]any{"command": got}, "codex"); back != program {
			t.Errorf("hookProgram(%s) = %q, want %q", got, back, program)
		}
	}
	if back := hookProgram(map[string]any{"command": `& 'C:\a''b\blast-radius.exe' hook gemini`}, "gemini"); back != `C:\a'b\blast-radius.exe` {
		t.Errorf("PowerShell form: hookProgram = %q", back)
	}
}

func TestShellHookIs(t *testing.T) {
	ours := shellHookIs("codex")
	for _, h := range []map[string]any{
		{"command": "blast-radius hook codex"},
		{"command": "'/a b/blast-radius' hook codex"},
		{"command": `& 'C:\Program Files\blast-radius.exe' hook codex`},
		{"command": `"C:\x\blast-radius.exe" hook codex`},
	} {
		if !ours(h) {
			t.Errorf("%v not recognised", h)
		}
	}
	for _, h := range []map[string]any{
		{"command": "blast-radius hook gemini"},
		{"command": "/home/codex/lint.sh"},
		{"command": "blast-radius-wrapper hook codex --dry-run"},
		{"command": "~/bin/blast-radius-wrap.sh hook codex"},
	} {
		if ours(h) {
			t.Errorf("%v wrongly recognised", h)
		}
	}
	if !shellHookIs("copilot")(map[string]any{"exec": "/bin/blast-radius", "args": []any{"hook", "copilot"}}) {
		t.Error("exec hook not recognised")
	}
}

// FuzzAgentHooks feeds arbitrary input to the hooks for agents that run a
// command when the hook fails: whatever arrives, the hook must exit 0 and
// print nothing (a tool it doesn't check) or a valid denial.
func FuzzAgentHooks(f *testing.F) {
	home := f.TempDir()
	f.Setenv("HOME", home)
	f.Setenv("USERPROFILE", home)
	f.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	f.Setenv("AppData", filepath.Join(home, "AppData"))
	for _, seed := range []string{
		codexInput("terraform destroy", home),
		`{"hook_event_name":"PreToolUse","tool_name":"apply_patch","tool_input":{"command":"*** Begin Patch\n*** Add File: .codex/hooks.json\n*** End Patch"}}`,
		`{"hook_event_name":"BeforeTool","tool_name":"run_shell_command","tool_input":{"command":"kubectl delete ns x","dir_path":"../.."}}`,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":"{\"command\":\"helm uninstall x\"}"}`,
		`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"path":7}}`,
		`not json`, ``, `null`, `[]`,
	} {
		f.Add([]byte(seed))
	}
	noRun := func(context.Context, string, []string, string, ...string) (analyze.Result, error) {
		return analyze.Result{}, os.ErrNotExist
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		for name, hook := range map[string]func(io.Reader, io.Writer, io.Writer, analyze.Runner) int{
			"codex": hookCodex, "gemini": hookGemini, "copilot": hookCopilot,
		} {
			var out bytes.Buffer
			if code := hook(bytes.NewReader(input), &out, io.Discard, noRun); code != 0 {
				t.Fatalf("%s: exit %d for %q", name, code, input)
			}
			if out.Len() == 0 {
				continue
			}
			var o struct {
				Decision           string `json:"decision"`
				PermissionDecision string `json:"permissionDecision"`
				HookSpecificOutput struct {
					PermissionDecision string `json:"permissionDecision"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal(out.Bytes(), &o); err != nil {
				t.Fatalf("%s: invalid output %q for %q", name, out.String(), input)
			}
			switch o.Decision + o.PermissionDecision + o.HookSpecificOutput.PermissionDecision {
			case "deny", "ask":
			default:
				t.Fatalf("%s: output %q for %q is neither a denial nor a question", name, out.String(), input)
			}
		}
	})
}

func TestFindAgentHookTolerantSettings(t *testing.T) {
	dir := t.TempDir()
	gemini := hookTargets["gemini"]
	for name, content := range map[string]string{
		"comments, no hook": "// my settings\n{\"theme\": \"dark\"}",
		"other hooks keys":  `{"hooks": {"disabled": ["x"], "BeforeTool": "odd"}}`,
		"no hooks at all":   `{"theme": "dark"}`,
	} {
		p := filepath.Join(dir, name+".json")
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if h, err := findAgentHook(p, gemini); h != nil || err != nil {
			t.Errorf("%s: hook %v, error %v", name, h, err)
		}
	}
	p := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(p, []byte(`{"hooks": {"BeforeTool": [{"hooks": [{"command": "blast-radius hook gemini"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := findAgentHook(p, gemini); err == nil {
		t.Error("a broken file holding the hook must be reported")
	}
	ok := `{"hooks": {"disabled": ["x"], "BeforeTool": [{"matcher": "run_shell_command", "hooks": [{"type": "command", "command": "/bin/blast-radius hook gemini"}]}]}}`
	if err := os.WriteFile(p, []byte(ok), 0o600); err != nil {
		t.Fatal(err)
	}
	if h, err := findAgentHook(p, gemini); h == nil || err != nil {
		t.Errorf("hook beside other keys: %v %v", h, err)
	}
}
