import assert from "node:assert/strict";
import test from "node:test";
import {
  messageBatch,
  previousMessageBatch,
} from "../../web/src/application/transcript.js";
import { conversationItems } from "../../web/src/domain/conversation.js";
import type {
  ContextPack,
  MessageCursor,
  ScopedMessage,
  Selection,
  SessionRevision,
} from "../../web/src/domain/history.js";

const project = "11111111-1111-4111-8111-111111111111";
const repository = "22222222-2222-4222-8222-222222222222";
const session = "33333333-3333-4333-8333-333333333333";
const child = "44444444-4444-4444-8444-444444444444";
const digest = "a".repeat(64);
const selection: Selection = {
  project_id: project,
  repository_id: repository,
  commit_sha: "b".repeat(40),
  dialogue_id: session,
};
const cursor = (offset: number): MessageCursor => ({
  repository_id: repository,
  session_id: session,
  revision: 2,
  digest,
  segment: 1,
  offset,
});
const message = (index: number): ScopedMessage => ({
  source: {
    kind: "capture",
    project_id: project,
    repository_id: repository,
    session_id: session,
    revision: 2,
    digest,
  },
  index,
  segment: 1,
  message: { role: "user", text: `message ${index}` },
});
function pack(
  query: Selection,
  start: number,
  count: number,
  next?: MessageCursor,
): ContextPack {
  return {
    schema_version: 1,
    query,
    read_at: "2026-11-02T12:00:00Z",
    transcript: {
      items: Array.from({ length: count }, (_, n) => message(start + n)),
      has_more: !!next,
      ...(next ? { next } : {}),
    },
    captures: { items: [], omitted_for_permissions: false },
    merge_requests: {
      items: [],
      has_more: false,
      omitted_for_permissions: false,
    },
    evidence: [],
    evidence_has_more: false,
    pending_sources_in_project: 0,
    failed_sources_in_project: 0,
    budget_limited: false,
  };
}

test("one click fills 20 records across byte-limited pages, requesting only the remainder", async () => {
  const limits: number[] = [];
  const result = await messageBatch(selection, async (query) => {
    limits.push(query.message_limit!);
    const start = query.message_after?.offset ?? 0;
    const count = Math.min(2, query.message_limit!);
    return pack(query, start, count, cursor(start + count));
  });
  assert.deepEqual(
    result.items.map((m) => m.index),
    Array.from({ length: 20 }, (_, n) => n),
  );
  assert.deepEqual(limits, [20, 18, 16, 14, 12, 10, 8, 6, 4, 2]);
  assert.equal(result.next?.offset, 20);
});

test("initial batch is completed and the final short batch has no surplus request", async () => {
  let calls = 0;
  const initial = pack(
    { ...selection, message_view: "compact" },
    0,
    2,
    cursor(2),
  );
  const result = await messageBatch(
    selection,
    async (query) => {
      calls++;
      assert.equal(query.message_limit, 18);
      return pack(query, 2, 18, cursor(20));
    },
    undefined,
    initial,
  );
  assert.equal(result.items.length, 20);
  assert.equal(calls, 1);
  const tail = await messageBatch(
    selection,
    async (query) => pack(query, 20, 3),
    result.next,
  );
  assert.equal(tail.items.length, 3);
  assert.equal(tail.next, undefined);
});

test("repeated, fragmented and wrong-scope pages fail without presenting a successful batch", async () => {
  for (const mode of ["repeated", "fragment", "scope"]) {
    await assert.rejects(
      messageBatch(selection, async (query) => {
        const value = pack(query, 0, 1, cursor(1));
        if (mode === "fragment")
          value.transcript!.items[0]!.text_truncated = true;
        if (mode === "scope")
          value.query = { ...query, commit_sha: "c".repeat(40) };
        return value;
      }),
    );
  }
});

const revision = (id = session): SessionRevision => ({
  snapshot: {
    schema_version: 1,
    repository_id: repository,
    session_id: id,
    revision: 2,
    agent: { name: "fixture", model: "local" },
    started_at: "2026-11-02T12:00:00Z",
    messages: Array.from({ length: 50 }, (_, n) => ({
      role: "user",
      text: `message ${n}`,
    })),
    files: [],
    commits: [],
  },
  digest,
  confirmed_commits: [],
});

test("earlier history prepends exact exclusive batches without duplicates", async () => {
  const read = async () => revision();
  const first = await previousMessageBatch(project, cursor(45), read);
  assert.deepEqual(
    first.items.map((m) => m.index),
    Array.from({ length: 20 }, (_, n) => n + 25),
  );
  const second = await previousMessageBatch(project, first.before!, read);
  assert.deepEqual(
    second.items.map((m) => m.index),
    Array.from({ length: 20 }, (_, n) => n + 5),
  );
  const tail = await previousMessageBatch(project, second.before!, read);
  assert.deepEqual(
    tail.items.map((m) => m.index),
    [0, 1, 2, 3, 4],
  );
  assert.equal(tail.before, undefined);
});

test("previous history crosses only verified exact parent revisions", async () => {
  const current = revision(child);
  current.snapshot.messages = [{ role: "assistant", text: "current" }];
  current.snapshot.continuation = {
    root_session_id: session,
    previous_session_id: session,
    previous_revision: 2,
    previous_digest: digest,
    segment: 2,
    reason: "window",
  };
  current.parent_reference_verified = true;
  const from = { ...cursor(0), session_id: child, segment: 2 };
  const calls: MessageCursor[] = [];
  const read = async (position: MessageCursor) => {
    calls.push(position);
    return position.session_id === child ? current : revision();
  };
  const result = await previousMessageBatch(project, from, read);
  assert.equal(result.items.length, 20);
  assert.equal(result.items[0]?.index, 30);
  assert.equal(result.items.at(-1)?.index, 49);
  assert.deepEqual(
    calls.map((c) => [c.session_id, c.revision, c.digest]),
    [
      [child, 2, digest],
      [session, 2, digest],
    ],
  );
  current.parent_reference_verified = false;
  calls.length = 0;
  const unverified = await previousMessageBatch(project, from, read);
  assert.equal(unverified.items.length, 0);
  assert.equal(calls.length, 1);
  assert.equal(unverified.before, undefined);
  await assert.rejects(
    previousMessageBatch(project, cursor(20), async () => ({
      ...revision(),
      digest: "f".repeat(64),
    })),
  );
});

test("collapsed action groups are bounded so appended actions create readable new blocks", () => {
  const groups = conversationItems(
    Array.from({ length: 45 }, () => ({ role: "tool", text: "output" })),
    45,
    false,
    "",
  );
  assert.deepEqual(
    groups.map((g) => (g.kind === "tools" ? g.entries.length : 0)),
    [20, 20, 5],
  );
});
