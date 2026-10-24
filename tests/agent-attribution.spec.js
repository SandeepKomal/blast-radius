import { test, expect } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdtemp, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";

const baseURL = "http://127.0.0.1:17357";
let server;
let tempDir;

test.beforeAll(async () => {
  tempDir = await mkdtemp(path.join(tmpdir(), "blast-radius-ui-"));
  const logPath = path.join(tempDir, "audit.jsonl");
  const now = Date.now();
  const entries = [
    ["claude_code", "terraform destroy claude"],
    ["codex", "terraform destroy codex"],
    [undefined, "terraform destroy legacy"],
  ].map(([platform, command], index) => ({
    time: new Date(now - index * 1000).toISOString(),
    tool: "Bash",
    dir: tempDir,
    command,
    action: "ask",
    findings: [{ tool: "terraform", target: "example resource", level: "high", action: "ask", reason: "test fixture" }],
    ...(platform ? { agent_platform: platform } : {}),
  }));
  await writeFile(logPath, entries.map((entry) => JSON.stringify(entry)).join("\n") + "\n");

  server = spawn("go", ["run", ".", "ui", "--addr", "127.0.0.1:17357", "--log", logPath], {
    cwd: process.cwd(),
    stdio: "ignore",
  });
  const deadline = Date.now() + 60_000;
  while (Date.now() < deadline) {
    if (server.exitCode !== null) throw new Error(`blast-radius UI exited early with code ${server.exitCode}`);
    try {
      const response = await fetch(baseURL + "/api/activity");
      if (response.ok) return;
    } catch {
      // Wait for go run to compile and start the local server.
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error("blast-radius UI did not become ready within 60 seconds");
});

test.afterAll(async () => {
  if (server && server.exitCode === null) {
    server.kill("SIGTERM");
  }
  if (tempDir) await rm(tempDir, { recursive: true, force: true });
});

test("activity table labels Claude Code, Codex, and legacy records correctly", async ({ page }) => {
  await page.goto(baseURL + "/#activity");
  const agents = page.locator("#rows tr td.agent");
  await expect(agents).toHaveCount(3);
  await expect(page.locator("#rows tr").filter({ hasText: "terraform destroy claude" }).locator("td.agent")).toHaveText("Claude Code");
  await expect(page.locator("#rows tr").filter({ hasText: "terraform destroy codex" }).locator("td.agent")).toHaveText("Codex");
  await expect(page.locator("#rows tr").filter({ hasText: "terraform destroy legacy" }).locator("td.agent")).toHaveText("Legacy / Unknown");
});
