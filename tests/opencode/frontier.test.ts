import assert from "node:assert/strict";
import test from "node:test";
import { Coordinator } from "../../internal/agent/opencode/assets/workgraph/coordinator.js";
import type { Frontier } from "../../internal/agent/opencode/assets/workgraph/collector.js";
import { collectorFixture } from "./collector-fixture.js";

function client(messages: () => Promise<unknown>) {
  return {
    session: {
      get: async () => ({ data: { id: "source", time: { created: 1 } } }),
      messages: async () => ({
        data: await messages(),
        response: { status: 200, headers: new Headers() },
      }),
    },
  };
}
const rows = [
  {
    info: { id: "m", sessionID: "source", role: "user", time: { created: 1 } },
    parts: [
      { id: "p", type: "text", text: "Exact source" },
      {
        id: "tool",
        type: "tool",
        callID: "call",
        tool: "bash",
        state: {
          status: "completed",
          input: { command: "git commit" },
          output: "synthetic result",
        },
      },
    ],
  },
];

test("journal survives both failed lifecycle reads and a new coordinator after later human HEAD", async () => {
  let head = "a".repeat(40),
    reports = 0;
  const journal = new Map<string, Frontier>(),
    windows: Array<string | undefined> = [];
  const collector = collectorFixture(
    {
      probe: async () => ({ root: "/repo", head }),
      record: async (_d, _o, before) => {
        windows.push(before);
        return true;
      },
    },
    journal,
  );
  const failing = new Coordinator(
    client(async () => {
      throw new Error("SDK unavailable");
    }),
    collector,
    "/repo",
    () => reports++,
  );
  await failing.before("source", "call");
  head = "b".repeat(40);
  await failing.after("source", "call");
  assert.equal(reports, 2);
  assert.equal(journal.size, 1);
  assert.equal([...journal.values()][0]?.before, "a".repeat(40));
  assert.equal([...journal.values()][0]?.after, "b".repeat(40));
  head = "c".repeat(40);
  const recovered = new Coordinator(
    client(async () => rows),
    collector,
    "/repo",
    () => assert.fail("recovery failed"),
  );
  await recovered.resume();
  assert.deepEqual(windows, ["a".repeat(40)]);
  assert.equal(journal.size, 0);
});

test("a bound target recovers without SDK reads after association acknowledgement loss", async () => {
  let head = "a".repeat(40);
  const journal = new Map<string, Frontier>();
  const collector = collectorFixture(
    { probe: async () => ({ root: "/repo", head }), record: async () => true },
    journal,
  );
  const apply = collector.apply;
  collector.apply = async () => {
    throw new Error("process exited before association");
  };
  const initial = new Coordinator(
    client(async () => rows),
    collector,
    "/repo",
    () => {},
  );
  await initial.before("source", "call");
  head = "b".repeat(40);
  await initial.after("source", "call");
  assert.notEqual([...journal.values()][0]?.targetSessionID, "");
  collector.apply = apply;
  const recovered = new Coordinator(
    client(async () => assert.fail("bound recovery read source")),
    collector,
    "/repo",
    () => assert.fail("bound recovery failed"),
  );
  await recovered.resume();
  assert.equal(journal.size, 0);
});

test("startup never invents a missing end", async () => {
  const journal = new Map<string, Frontier>();
  let head = "a".repeat(40);
  const collector = collectorFixture(
    { probe: async () => ({ root: "/repo", head }), record: async () => true },
    journal,
  );
  const initial = new Coordinator(
    client(async () => rows),
    collector,
    "/repo",
    () => assert.fail(),
  );
  await initial.before("source", "call");
  head = "c".repeat(40);
  const recovered = new Coordinator(
    client(async () => assert.fail("unknown end recaptured at startup")),
    collector,
    "/repo",
    () => assert.fail(),
  );
  await recovered.resume();
  assert.equal([...journal.values()][0]?.after, null);
});

test("after boundary is saved before a queued SDK pass can delay it past human work", async () => {
  let head = "a".repeat(40),
    block = false,
    release = () => {},
    entered = () => {};
  const started = new Promise<void>((resolve) => {
    entered = resolve;
  });
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  const journal = new Map<string, Frontier>();
  const collector = collectorFixture(
    { probe: async () => ({ root: "/repo", head }), record: async () => true },
    journal,
  );
  const coordinator = new Coordinator(
    client(async () => {
      if (block) {
        entered();
        await held;
      }
      return rows;
    }),
    collector,
    "/repo",
    () => assert.fail(),
  );
  await coordinator.before("source", "call");
  block = true;
  coordinator.schedule("source");
  await started;
  head = "b".repeat(40);
  const ending = coordinator.after("source", "call");
  await new Promise((resolve) => setImmediate(resolve));
  try {
    assert.equal(
      [...journal.values()][0]?.after,
      "b".repeat(40),
      "after waited behind source I/O",
    );
  } finally {
    head = "c".repeat(40);
    release();
    await ending;
    await coordinator.dispose();
  }
});
