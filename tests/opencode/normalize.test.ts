import assert from "node:assert/strict";
import test from "node:test";
import {
  normalize,
  normalizePages,
} from "../../internal/agent/opencode/assets/workgraph/normalize.js";

const session = { id: "ses_native", time: { created: 1793300000000 } };
const user = {
  info: { role: "user", model: { providerID: "local", modelID: "fixture" } },
  parts: [{ type: "text", text: "Implement task" }],
};

test("stable complete prefix excludes reasoning, streaming text and unfinished parallel tools", () => {
  const rows: unknown[] = [
    user,
    {
      info: {
        role: "assistant",
        modelID: "fixture",
        providerID: "local",
        time: {},
      },
      parts: [
        { type: "reasoning", text: "PRIVATE REASONING" },
        { type: "tool", id: "p1", tool: "bash", state: { status: "running" } },
        {
          type: "tool",
          id: "p2",
          tool: "read",
          state: { status: "completed", input: {}, output: "later tool" },
        },
      ],
    },
  ];
  const first = normalize(session, rows, "/repo", "/repo");
  assert.equal(first?.messages.length, 1);
  assert(!JSON.stringify(first).includes("PRIVATE"));
  rows[1] = {
    info: {
      role: "assistant",
      modelID: "fixture",
      providerID: "local",
      time: {},
    },
    parts: [{ type: "text", text: "stream" }],
  };
  assert.equal(normalize(session, rows, "/repo", "/repo")?.messages.length, 1);
});

test("native windows bound long histories and explicitly start compaction summaries", () => {
  const rows = Array.from({ length: 6000 }, (_, i) => ({
    info: { id: "message-" + i, role: "user" },
    parts: [{ id: "text-" + i, type: "text", text: "Message " + i }],
  }));
  const pages = normalizePages(session, rows, "/repo", "/repo");
  assert(pages.length >= 6);
  assert.equal(pages.flatMap((page) => page.messages).length, 6000);
  assert(
    pages.every(
      (page) =>
        page.messages.length <= 1000 &&
        Buffer.byteLength(JSON.stringify(page)) < 1024 * 1024,
    ),
  );
  assert.equal(
    new Set(pages.map((page) => page.native_history?.page_id)).size,
    pages.length,
  );
  const compacted = normalizePages(
    session,
    [
      rows[0],
      {
        info: {
          id: "summary",
          role: "assistant",
          summary: true,
          finish: "stop",
          time: { completed: 2 },
        },
        parts: [
          {
            id: "summary-part",
            type: "text",
            text: "Compacted source summary",
          },
        ],
      },
    ],
    "/repo",
    "/repo",
  );
  assert.equal(compacted.length, 2);
  assert.equal(compacted[1]?.native_history?.reason, "compaction");
  assert.equal(compacted[1]?.messages[0]?.kind, "agent_summary");
});

test("completed tool files have durable identities and stay within the Git worktree", () => {
  const rows = [
    user,
    {
      info: { role: "assistant", time: { completed: 1793300000010 } },
      parts: [
        {
          type: "tool",
          id: "part-edit",
          tool: "write",
          state: {
            status: "completed",
            input: { filePath: "/repo/src/a.ts" },
            output: "written",
            metadata: { filepath: "/repo/src/a.ts" },
          },
        },
        {
          type: "patch",
          id: "part-patch",
          files: ["/outside/secret", "/repo/.git/config", "/repo/src/a.ts"],
        },
        { type: "text", text: "Done" },
      ],
    },
  ];
  const result = normalize(session, rows, "/repo", "/repo/src");
  assert.deepEqual(result?.changed_files, []);
  assert.deepEqual(
    result?.file_events.map((event) => event.path),
    ["src/a.ts", "src/a.ts"],
  );
  assert.equal(result?.agent.model, "local/fixture");
  assert.equal(result?.messages.at(-1)?.text, "Done");
  assert.equal(
    normalize(session, rows, "/repo", "/repo/src")?.event_id,
    result?.event_id,
  );
  assert(!JSON.stringify(result).includes("outside"));
});

test("invalid sources and oversized conversation fail explicitly; attachment URLs are excluded", () => {
  assert.throws(() =>
    normalize({ id: "bad", time: { created: NaN } }, [user], "/repo", "/repo"),
  );
  assert.throws(() =>
    normalize(session, new Array(5001).fill(user), "/repo", "/repo"),
  );
  const result = normalize(
    session,
    [
      user,
      {
        info: { role: "user" },
        parts: [{ type: "file", url: "data:private" }],
      },
    ],
    "/repo",
    "/repo",
  );
  assert(!JSON.stringify(result).includes("data:private"));
});

test("native and Git aliases retain paths relative to the same observed worktree", () => {
  const result = normalize(
    session,
    [
      user,
      {
        info: { role: "assistant", time: { completed: 1 } },
        parts: [
          { type: "patch", id: "alias-patch", files: ["/var/repo/src/a.ts"] },
        ],
      },
    ],
    "/private/repo",
    "/var/repo/src",
    "/var/repo",
  );
  assert.deepEqual(result?.file_events, [
    { id: "alias-patch", path: "src/a.ts" },
  ]);
});

test("windowed patch-only parts keep file evidence and pruned tools have stable identities", () => {
  const rows = [
    {
      info: { id: "u", role: "user" },
      parts: [{ id: "ut", type: "text", text: "Implement" }],
    },
    {
      info: { id: "a", role: "assistant", time: { completed: 1 } },
      parts: [
        { id: "patch", type: "patch", files: ["/repo/a.go"] },
        {
          id: "tool",
          type: "tool",
          tool: "read",
          state: {
            status: "completed",
            input: {},
            output: "old bytes",
            time: { compacted: 2 },
          },
        },
      ],
    },
  ];
  const pages = normalizePages(session, rows, "/repo", "/repo");
  assert.equal(pages.length, 1);
  assert.deepEqual(pages[0]?.file_events, [{ id: "patch", path: "a.go" }]);
  assert.deepEqual(pages[0]?.native_history?.pruned, [1]);
  assert(!JSON.stringify(pages).includes("old bytes"));
});
