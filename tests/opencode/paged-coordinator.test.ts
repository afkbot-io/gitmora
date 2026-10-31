import assert from "node:assert/strict";
import { collectorFixture } from "./collector-fixture.js";
import test from "node:test";
import { Coordinator } from "../../internal/agent/opencode/assets/workgraph/coordinator.js";
import type { HistoryQuery } from "../../internal/agent/opencode/assets/workgraph/history.js";
import type { Observation } from "../../internal/agent/opencode/assets/workgraph/normalize.js";

function row(index: number) {
  return {
    info: {
      id: `m-${index}`,
      sessionID: "bulk",
      role: "user",
      time: { created: index },
    },
    parts: [{ id: `p-${index}`, type: "text", text: `Part-${index}` }],
  };
}
function reply(rows: unknown[], next?: string) {
  return {
    data: rows,
    response: {
      status: 200,
      headers: new Headers(next ? { "X-Next-Cursor": next } : {}),
    },
  };
}
const latest = Array.from({ length: 100 }, (_, i) => row(i + 3));
const next = Buffer.from(
  JSON.stringify({
    id: latest[0]!.info.id,
    time: latest[0]!.info.time.created,
  }),
).toString("base64url");

test("coordinator records no incomplete SDK pass and restores every chronological native row", async () => {
  let release: () => void = () => {};
  const last = new Promise<void>((resolve) => {
    release = resolve;
  });
  const captured: Observation[] = [];
  const coordinator = new Coordinator(
    {
      session: {
        get: async () => ({ data: { id: "bulk", time: { created: 1 } } }),
        messages: async (query: HistoryQuery) => {
          if (query.query.before) {
            await last;
            return reply([row(0), row(1), row(2)]);
          }
          return reply(latest, next);
        },
      },
    },
    collectorFixture({
      probe: async () => ({ root: "/repo", head: "a".repeat(40) }),
      record: async (_directory, observation) => {
        captured.push(observation);
        return true;
      },
    }),
    "/repo",
    () => assert.fail("capture failed"),
  );
  const pending = coordinator.before("bulk", "tool");
  try {
    await new Promise((resolve) => setImmediate(resolve));
    assert.equal(
      captured.length,
      0,
      "incomplete page was recorded as the available full history",
    );
  } finally {
    release();
  }
  await pending;
  assert.deepEqual(
    captured.flatMap((page) => page.messages.map((message) => message.text)),
    Array.from({ length: 103 }, (_, i) => `Part-${i}`),
  );
});

test("after-tool acquisition failure retains its original HEAD for a later complete retry", async () => {
  let head = "a".repeat(40);
  let fail = false;
  let reports = 0;
  const captured: Array<{
    before: string | undefined;
    deliver: boolean | undefined;
  }> = [];
  const coordinator = new Coordinator(
    {
      session: {
        get: async () => ({ data: { id: "bulk", time: { created: 1 } } }),
        messages: async (query: HistoryQuery) => {
          if (query.query.before) {
            if (fail) throw new Error("private source error");
            return reply([row(0), row(1), row(2)]);
          }
          return reply(latest, next);
        },
      },
    },
    collectorFixture({
      probe: async () => ({ root: "/repo", head }),
      record: async (_directory, _observation, before, deliver) => {
        captured.push({ before, deliver });
        return true;
      },
    }),
    "/repo",
    () => {
      reports++;
    },
  );
  await coordinator.before("bulk", "tool");
  const prior = captured.length;
  head = "b".repeat(40);
  fail = true;
  await coordinator.after("bulk", "tool");
  assert.equal(reports, 1);
  assert.equal(
    captured.length,
    prior,
    "failed acquisition published a partial page",
  );
  fail = false;
  coordinator.schedule("bulk");
  await coordinator.dispose();
  assert(
    captured.some(
      (value) => value.before === "a".repeat(40) && value.deliver === true,
    ),
  );
});

test("a session metadata substitution cannot retarget a lifecycle capture", async () => {
  let records = 0;
  let reports = 0;
  const coordinator = new Coordinator(
    {
      session: {
        get: async () => ({
          data: { id: "other-session", time: { created: 1 } },
        }),
        messages: async () => reply([row(0)]),
      },
    },
    collectorFixture({
      probe: async () => ({ root: "/repo", head: "" }),
      record: async () => {
        records++;
        return true;
      },
    }),
    "/repo",
    () => {
      reports++;
    },
  );
  await coordinator.before("bulk", "tool");
  assert.equal(records, 0);
  assert.equal(reports, 1);
});
