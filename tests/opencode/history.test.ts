import assert from "node:assert/strict";
import test from "node:test";
import {
  readHistory,
  type HistoryQuery,
  type HistoryReply,
} from "../../internal/agent/opencode/assets/workgraph/history.js";

function row(index: number) {
  return {
    info: {
      id: `message-${String(index).padStart(6, "0")}`,
      sessionID: "native-session",
      role: "user",
      time: { created: index },
    },
    parts: [
      {
        id: `part-${index}`,
        sessionID: "native-session",
        messageID: `message-${String(index).padStart(6, "0")}`,
        type: "text",
        text: `Native-${index}`,
      },
    ],
  };
}
function cursor(value: ReturnType<typeof row>): string {
  return Buffer.from(
    JSON.stringify({ id: value.info.id, time: value.info.time.created }),
  ).toString("base64url");
}
function page(rows: unknown[], next?: string): HistoryReply {
  const headers = new Headers();
  if (next) {
    headers.set("X-Next-Cursor", next);
    headers.set("Link", `<https://unrelated.invalid/never-follow>; rel="next"`);
  }
  return { data: rows, response: { status: 200, headers } };
}

test("native keyset acquisition follows exact cursors and restores oldest-first chronology", async () => {
  const latest = Array.from({ length: 100 }, (_, i) => row(i + 3));
  const seen: HistoryQuery[] = [];
  const source = {
    messages: async (query: HistoryQuery) => {
      seen.push(query);
      return query.query.before
        ? page([row(0), row(1), row(2)])
        : page(latest, cursor(latest[0]!));
    },
  };
  const got = await readHistory(source, "native-session", "/repo");
  assert.equal(got.length, 103);
  assert.deepEqual(
    got,
    Array.from({ length: 103 }, (_, i) => row(i)),
  );
  assert.equal(seen.length, 2);
  assert.equal(seen[0]?.query.limit, 100);
  assert.equal(seen[1]?.query.before, cursor(latest[0]!));
  assert(
    seen.every(
      (q) => q.path.id === "native-session" && q.query.directory === "/repo",
    ),
  );
});

test("SDK page data is owned before the next asynchronous port call", async () => {
  const latest = Array.from({ length: 100 }, (_, i) => row(i + 1));
  const source = {
    messages: async (query: HistoryQuery) => {
      if (query.query.before) {
        latest[99]!.parts[0]!.text = "Changed after page receipt";
        return page([row(0)]);
      }
      return page(latest, cursor(latest[0]!));
    },
  };
  const got = (await readHistory(
    source,
    "native-session",
    "/repo",
  )) as ReturnType<typeof row>[];
  assert.equal(got.at(-1)?.parts[0]?.text, "Native-100");
});

test("wrong response metadata, foreign identities and malformed cursor anchors reject the whole pass", async () => {
  const full = Array.from({ length: 100 }, (_, i) => row(i + 1));
  for (const bad of [
    { data: full },
    { ...page(full), error: { message: "private remote failure" } },
    { data: full, response: { status: 404, headers: new Headers() } },
    page([{ ...row(1), info: { ...row(1).info, sessionID: "other-session" } }]),
    page([
      {
        ...row(1),
        parts: [{ ...row(1).parts[0]!, messageID: "other-message" }],
      },
    ]),
    page([row(2), row(1)]),
    page([row(1), row(1)]),
    page(full, "https://unrelated.invalid/path"),
    page(full, cursor(row(0))),
    page([], cursor(row(0))),
    page([row(1)], cursor(row(1))),
    {
      data: full,
      response: {
        status: 200,
        headers: new Headers({ Link: '<https://other.invalid>; rel="next"' }),
      },
    },
    {
      data: full,
      response: { status: 200, headers: new Headers({ "X-Next-Cursor": "" }) },
    },
  ])
    await assert.rejects(
      readHistory({ messages: async () => bad }, "native-session", "/repo"),
    );
});

test("overlap, duplicate identity, cursor loop and later SDK failure never return a partial pass", async () => {
  const full = Array.from({ length: 100 }, (_, i) => row(i + 100));
  for (const second of [
    page([row(100)]),
    page([row(101)]),
    page(full, cursor(full[0]!)),
    { error: "private", response: { status: 500, headers: new Headers() } },
  ]) {
    let count = 0;
    await assert.rejects(
      readHistory(
        {
          messages: async () =>
            ++count === 1 ? page(full, cursor(full[0]!)) : second,
        },
        "native-session",
        "/repo",
      ),
    );
    assert.equal(count, 2);
  }
});

test("cancellation bounds even a source port that ignores its signal", async () => {
  const controller = new AbortController();
  const pending = readHistory(
    { messages: () => new Promise(() => {}) },
    "native-session",
    "/repo",
    controller.signal,
  );
  controller.abort();
  await assert.rejects(pending);
});

test("raw acquisition enforces page, row and total serialized-byte budgets", async () => {
  await assert.rejects(
    readHistory(
      {
        messages: async () =>
          page(Array.from({ length: 101 }, (_, i) => row(i))),
      },
      "native-session",
      "/repo",
    ),
  );
  let count = 0;
  await assert.rejects(
    readHistory(
      {
        messages: async () => {
          const start = 60000 - ++count * 100;
          const rows = Array.from({ length: 100 }, (_, i) => row(start + i));
          return page(rows, cursor(rows[0]!));
        },
      },
      "native-session",
      "/repo",
    ),
  );
  assert.equal(
    count,
    500,
    "row/page bounds did not stop acquisition at the declared frontier",
  );
  let pages = 0;
  const large = "x".repeat(33 * 1024 * 1024);
  await assert.rejects(
    readHistory(
      {
        messages: async () => {
          const start = (2 - ++pages) * 100;
          const rows = Array.from({ length: 100 }, (_, i) => row(start + i));
          rows[0]!.parts[0]!.text = large;
          return page(rows, cursor(rows[0]!));
        },
      },
      "native-session",
      "/repo",
    ),
  );
  assert.equal(
    pages,
    2,
    "byte budget was not enforced before another page request",
  );
});
