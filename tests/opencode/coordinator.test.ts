import assert from "node:assert/strict";
import { collectorFixture } from "./collector-fixture.js";
import test from "node:test";
import { Coordinator } from "../../internal/agent/opencode/assets/workgraph/coordinator.js";
import {
  object,
  type Observation,
} from "../../internal/agent/opencode/assets/workgraph/normalize.js";

function sdk(
  sessionID: string,
  rows: Array<{ info: Record<string, unknown>; parts: unknown[] }>,
) {
  return {
    data: rows.map((row, index) => ({
      ...row,
      info: {
        ...row.info,
        sessionID,
        time: { created: index + 1, ...object(row.info.time) },
      },
    })),
    response: { status: 200, headers: new Headers() },
  };
}

test("awaited tools preserve HEAD window and dispose drains a late final conversation", async () => {
  let head = "a".repeat(40);
  let final = false;
  const captured: Array<{
    observation: Observation;
    before: string | undefined;
  }> = [];
  const coordinator = new Coordinator(
    {
      session: {
        get: async () => ({
          data: { id: "ses_1", time: { created: 1793300000000 } },
        }),
        messages: async () =>
          sdk("ses_1", [
            {
              info: { id: "user", role: "user" },
              parts: [
                { id: "user-text", type: "text", text: "Implement" },
                {
                  id: "tool",
                  type: "tool",
                  callID: "tool_1",
                  tool: "bash",
                  state: {
                    status: "completed",
                    input: { command: "git commit" },
                    output: "Synthetic result",
                  },
                },
              ],
            },
            ...(final
              ? [
                  {
                    info: {
                      id: "assistant",
                      role: "assistant",
                      time: { completed: 1 },
                    },
                    parts: [
                      {
                        id: "final-text",
                        type: "text",
                        text: "Final after push",
                      },
                    ],
                  },
                ]
              : []),
          ]),
      },
    },
    collectorFixture({
      probe: async () => ({ root: "/repo", head }),
      record: async (_directory, observation, before) => {
        captured.push({ observation, before });
        return true;
      },
    }),
    "/repo",
    () => assert.fail("capture failed"),
  );
  await coordinator.before("ses_1", "tool_1");
  head = "b".repeat(40);
  await coordinator.after("ses_1", "tool_1");
  final = true;
  coordinator.schedule("ses_1");
  await coordinator.dispose();
  assert.equal(captured[1]?.before, "a".repeat(40));
  assert.equal(
    captured.at(-1)?.observation.messages.at(-1)?.text,
    "Final after push",
  );
});

test("global plugin skips an uninitialized repository before reading any conversation", async () => {
  const coordinator = new Coordinator(
    {
      session: {
        get: async () => assert.fail("conversation read"),
        messages: async () => assert.fail("conversation read"),
      },
    },
    collectorFixture({
      probe: async () => undefined,
      record: async () => assert.fail("capture"),
    }),
    "/repo",
    () => assert.fail("uninitialized is not a capture error"),
  );
  await coordinator.before("ses_1", "tool_1");
  await coordinator.after("ses_1", "tool_1");
  await coordinator.dispose();
});

test("a tool HEAD window survives an initially empty exposed conversation", async () => {
  let after = false;
  let observed: string | undefined;
  const coordinator = new Coordinator(
    {
      session: {
        get: async () => ({ data: { id: "ses_empty", time: { created: 1 } } }),
        messages: async () =>
          sdk(
            "ses_empty",
            after
              ? [
                  {
                    info: {
                      id: "tool-message",
                      role: "assistant",
                      time: { completed: 2 },
                    },
                    parts: [
                      {
                        id: "tool-part",
                        callID: "call",
                        type: "tool",
                        tool: "bash",
                        state: {
                          status: "completed",
                          input: {},
                          output: "done",
                        },
                      },
                    ],
                  },
                ]
              : [],
          ),
      },
    },
    collectorFixture({
      probe: async () => ({
        root: "/repo",
        head: after ? "b".repeat(40) : "a".repeat(40),
      }),
      record: async (_directory, _observation, before) => {
        observed = before;
        return true;
      },
    }),
    "/repo",
    () => assert.fail("capture failed"),
  );
  await coordinator.before("ses_empty", "call");
  after = true;
  await coordinator.after("ses_empty", "call");
  assert.equal(observed, "a".repeat(40));
});

test("failed after-tool SDK reads retain the HEAD window for a later retry", async () => {
  let count = 0;
  let after = false;
  let reports = 0;
  const windows: Array<string | undefined> = [];
  const coordinator = new Coordinator(
    {
      session: {
        get: async () => ({ data: { id: "retry", time: { created: 1 } } }),
        messages: async () => {
          if (++count === 2) throw new Error("Transient SDK read");
          return sdk("retry", [
            {
              info: { id: "u", role: "user" },
              parts: [
                { id: "p", type: "text", text: "Implement" },
                {
                  id: "tool",
                  type: "tool",
                  callID: "call",
                  tool: "bash",
                  state: {
                    status: "completed",
                    input: { command: "git commit" },
                    output: "Synthetic result",
                  },
                },
              ],
            },
          ]);
        },
      },
    },
    collectorFixture({
      probe: async () => ({
        root: "/repo",
        head: (after ? "b" : "a").repeat(40),
      }),
      record: async (_directory, _observation, before) => {
        windows.push(before);
        return true;
      },
    }),
    "/repo",
    () => {
      reports++;
    },
  );
  await coordinator.before("retry", "call");
  after = true;
  await coordinator.after("retry", "call");
  coordinator.schedule("retry");
  await coordinator.dispose();
  assert.equal(reports, 1);
  assert(windows.includes("a".repeat(40)), "retry lost the original tool HEAD");
});
