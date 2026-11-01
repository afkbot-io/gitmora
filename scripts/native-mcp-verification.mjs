import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createInterface } from "node:readline";

/** The child receives no bearer env/argument; it must load the existing OS-store account. */
export async function verifySavedMCP(binary, env, url, children) {
  assert(!Object.hasOwn(env, "WORKGRAPH_TOKEN"));
  const child = spawn(binary, ["mcp", "--server", url], {
    env,
    stdio: ["pipe", "pipe", "pipe"],
  });
  children.add(child);
  let sequence = 0,
    diagnostic = "";
  const pending = new Map();
  const lines = createInterface({ input: child.stdout });
  lines.on("line", (line) => {
    assert(
      !/\b(?:wg_|wcd_|wcp_)[A-Za-z0-9_-]{43}\b/.test(line),
      "MCP exposed authority",
    );
    const reply = JSON.parse(line);
    pending.get(reply.id)?.resolve(reply);
  });
  child.stderr.on("data", (chunk) => {
    diagnostic += chunk;
  });
  const ended = new Promise((resolve, reject) => {
    child.on("error", () => reject(new Error("Native MCP could not launch")));
    child.on("close", (code) => {
      for (const value of pending.values())
        value.reject(new Error("Native MCP closed before reply"));
      resolve(code);
    });
  });
  async function rpc(method, params) {
    const id = ++sequence;
    let timer;
    try {
      return await new Promise((resolve, reject) => {
        pending.set(id, { resolve, reject });
        timer = setTimeout(
          () => reject(new Error("Native MCP response timeout")),
          15000,
        );
        child.stdin.write(
          JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n",
        );
      });
    } finally {
      clearTimeout(timer);
      pending.delete(id);
    }
  }
  try {
    const hello = await rpc("initialize", {
      protocolVersion: "2025-11-25",
      capabilities: {},
      clientInfo: { name: "saved-account-verification", version: "1" },
    });
    assert(hello.result);
    child.stdin.write(
      JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" }) +
        "\n",
    );
    const tools = await rpc("tools/list", {});
    assert.deepEqual(tools.result.tools.map((x) => x.name).sort(), [
      "context.browse",
      "context.expand",
      "context.file",
      "context.pack",
      "context.search",
      "context.resolve",
    ]);
    assert(tools.result.tools.every((x) => x.annotations.readOnlyHint));
  } finally {
    child.stdin.end();
    const timer = setTimeout(() => child.kill("SIGTERM"), 5000);
    await ended;
    clearTimeout(timer);
    lines.close();
    children.delete(child);
    assert(
      !/\b(?:wg_|wcd_|wcp_)[A-Za-z0-9_-]{43}\b/.test(diagnostic),
      "MCP diagnostic exposed authority",
    );
  }
}
