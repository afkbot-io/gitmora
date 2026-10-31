import { object } from "./normalize.js";

/** Exact source query; cursors are data for the same SDK method, never remote URLs. */
export interface HistoryQuery {
  path: { id: string };
  query: { directory: string; limit?: number; before?: string };
  signal: AbortSignal;
}

/** Successful SDK metadata is required to distinguish a final page from a partial/error response. */
export interface HistoryReply {
  data?: unknown;
  error?: unknown;
  response?: { status: number; headers: { get(name: string): string | null } };
}

/** The supplied native SDK retains server routing/authentication; no independent fetch client is created. */
export interface HistorySource {
  messages(query: HistoryQuery): Promise<HistoryReply>;
}

/** Read one complete bounded pass before any window is recorded or delivered. */
export async function readHistory(
  source: HistorySource,
  sessionID: string,
  directory: string,
  signal = AbortSignal.timeout(30000),
): Promise<unknown[]> {
  if (
    !nativeID(sessionID) ||
    !directory ||
    directory.length > 8192 ||
    /[\0\r\n]/u.test(directory)
  )
    fail();
  const pages: unknown[][] = [];
  const cursors = new Set<string>();
  const identities = new Set<string>();
  let before: string | undefined;
  let boundary: Key | undefined;
  let bytes = 0;
  for (;;) {
    if (signal.aborted) fail();
    const query: HistoryQuery = {
      path: { id: sessionID },
      query: { directory, limit: 100 },
      signal: AbortSignal.any([signal, AbortSignal.timeout(3000)]),
    };
    if (before !== undefined) query.query.before = before;
    const reply = await request(source, query);
    if (
      reply.error !== undefined ||
      reply.response?.status !== 200 ||
      typeof reply.response.headers?.get !== "function" ||
      !Array.isArray(reply.data) ||
      reply.data.length > 100
    )
      fail();
    const raw = JSON.stringify(reply.data);
    bytes += Buffer.byteLength(raw);
    if (bytes > 64 * 1024 * 1024) fail();
    const rows = JSON.parse(raw) as unknown[]; // Own page bytes across the next asynchronous adapter call.
    let previous: Key | undefined;
    for (const value of rows) {
      const row = object(value);
      const info = object(row?.info);
      const key = rowKey(value);
      if (
        info?.sessionID !== sessionID ||
        !Array.isArray(row?.parts) ||
        identities.has(key.id) ||
        (previous && !earlier(previous, key)) ||
        (boundary && !earlier(key, boundary))
      )
        fail();
      for (const value of row.parts) {
        const part = object(value);
        if (
          !part ||
          (part.sessionID !== undefined && part.sessionID !== sessionID) ||
          (part.messageID !== undefined && part.messageID !== key.id)
        )
          fail();
      }
      identities.add(key.id);
      previous = key;
    }
    if (identities.size > 50000 || pages.length >= 500) fail();
    const next = reply.response.headers.get("X-Next-Cursor");
    const link = reply.response.headers.get("Link");
    if (next === "") fail();
    if (!next && link && /\brel\s*=\s*"?next\b/iu.test(link)) fail();
    pages.push(rows);
    if (!next) return pages.reverse().flat();
    if (
      rows.length !== 100 ||
      identities.size === 50000 ||
      pages.length === 500 ||
      cursors.has(next)
    )
      fail();
    const oldest = rowKey(rows[0]);
    const anchor = cursor(next);
    if (anchor.id !== oldest.id || anchor.time !== oldest.time) fail();
    cursors.add(next);
    before = next;
    boundary = oldest;
  }
}

interface Key {
  id: string;
  time: number;
}
function fail(): never {
  throw new Error("Native history acquisition failed");
}
function nativeID(value: unknown): value is string {
  return typeof value === "string" && /^[!-~]{1,200}$/u.test(value);
}
function rowKey(value: unknown): Key {
  const info = object(object(value)?.info);
  const time = object(info?.time)?.created;
  if (
    !nativeID(info?.id) ||
    typeof time !== "number" ||
    !Number.isSafeInteger(time) ||
    time < 0
  )
    fail();
  return { id: info.id, time };
}
function earlier(a: Key, b: Key): boolean {
  return a.time < b.time || (a.time === b.time && a.id < b.id);
}

/** Validate the pinned 1.19.5 cursor codec and its exact oldest-row anchor; never interpret it as a URL. */
function cursor(value: string): Key {
  if (value.length > 2048 || !/^[A-Za-z0-9_-]+$/u.test(value)) fail();
  const parsed = object(
    JSON.parse(Buffer.from(value, "base64url").toString("utf8")),
  );
  if (
    !nativeID(parsed?.id) ||
    typeof parsed.time !== "number" ||
    !Number.isSafeInteger(parsed.time) ||
    parsed.time < 0
  )
    fail();
  const key = { id: parsed.id, time: parsed.time };
  if (Buffer.from(JSON.stringify(key)).toString("base64url") !== value) fail();
  return key;
}

/** Bound completion even if a faulty source adapter ignores the provided abort signal. */
function request(
  source: HistorySource,
  query: HistoryQuery,
): Promise<HistoryReply> {
  return new Promise((resolve, reject) => {
    if (query.signal.aborted) {
      reject(new Error("Native history acquisition interrupted"));
      return;
    }
    const aborted = () =>
      reject(new Error("Native history acquisition interrupted"));
    query.signal.addEventListener("abort", aborted, { once: true });
    void Promise.resolve()
      .then(() => source.messages(query))
      .then(resolve, reject)
      .finally(() => query.signal.removeEventListener("abort", aborted));
  });
}
