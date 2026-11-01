import test from "node:test";
import assert from "node:assert/strict";
import {
  conversationItems,
  groupConversations,
  toolPresentation,
} from "../../web/src/domain/conversation.js";
import type { CaptureWindow } from "../../web/src/domain/history.js";

test("conversation batches retain positions and consecutive tool chronology", () => {
  const messages = [
    { role: "user", text: "Задача" },
    { role: "tool", text: "one" },
    { role: "tool", text: "two" },
    { role: "assistant", text: "Ответ" },
  ];
  const first = conversationItems(messages, 2, false, "");
  assert.equal(first.length, 2);
  const appended = conversationItems(messages, 4, false, "");
  assert.deepEqual(
    appended.map((x) => x.key),
    [0, 1, 3],
  );
  assert.equal(appended[1]?.kind, "tools");
  if (appended[1]?.kind === "tools")
    assert.deepEqual(
      appended[1].entries.map((e) => e.index),
      [1, 2],
    );
  assert.deepEqual(
    conversationItems(messages, 4, true, "").map((x) => x.key),
    [0, 3],
  );
  assert.deepEqual(
    conversationItems(messages, 4, false, "ОТВЕТ").map((x) => x.key),
    [3],
  );
});

test("tool display tolerates mixed source formats without inventing outcomes", () => {
  const view = toolPresentation({
    role: "tool",
    tool_name: "Bash",
    text: JSON.stringify({
      command: "git status",
      output: "M app.ts",
      exit_code: 1,
    }),
  });
  assert.equal(view.command, "git status");
  assert.equal(view.output, "M app.ts");
  assert.equal(view.failed, true);
  assert.equal(
    toolPresentation({
      role: "tool",
      text: '{"arguments":"{\\"cmd\\":\\"ls\\"}"}',
    }).command,
    "ls",
  );
  for (const text of [
    "[1,2]",
    "null",
    '{"command":1}',
    "{bad json",
    "plain result",
    "x".repeat(300000),
  ]) {
    const value = toolPresentation({ role: "tool", text });
    assert.equal(value.failed, false);
    assert(value.summary.length <= 180);
  }
});

test("linked capture grouping requires verified identity and keeps repository scope", () => {
  const window = (
    id: string,
    root: string,
    segment: number,
    verified = true,
    repo = "repo",
  ): CaptureWindow => ({
    source: {
      kind: "capture",
      project_id: "p",
      repository_id: repo,
      session_id: id,
      digest: "d",
    },
    root_session_id: root,
    segment,
    agent: "codex",
    chain_verified: verified,
    parent_reference_verified: verified,
    chain_verification_limited: false,
    confirmed_commits: [],
    confirmed_count: 0,
  });
  const groups = groupConversations([
    window("second", "root", 2),
    window("root", "root", 1),
    window("unverified", "root", 3, false),
    window("other-repo", "root", 1, true, "other"),
    window("second", "root", 2),
  ]);
  assert.equal(groups.length, 3);
  assert.equal(groups[0]?.first.source.session_id, "root");
  assert.equal(groups[0]?.windows.length, 2);
});

test("search deep links pin revision and source position independently of API scope", async () => {
  const { transcriptLocation } =
    await import("../../web/src/domain/conversation.js");
  assert.deepEqual(transcriptLocation("#view=context&revision=2&message=40"), {
    revision: 2,
    message: 40,
  });
  for (const hash of [
    "#revision=NaN&message=Infinity",
    "#revision=-1&message=-1",
    "#revision=1000000001&message=1000000",
  ])
    assert.deepEqual(transcriptLocation(hash), { revision: 0, message: null });
});
