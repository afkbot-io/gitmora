import assert from "node:assert/strict";
import test from "node:test";
import {
  ApiClient,
  ApiError,
  csrfCookie,
} from "../../web/src/transport/api.js";
import { LatestRequest } from "../../web/src/application/latest.js";
import {
  isMergeRequestURL,
  searchQuery,
  safeExternalURL,
} from "../../web/src/domain/context.js";

test("ordinary URLs remain searchable text while MR/PR links select context", () => {
  assert(
    isMergeRequestURL(
      "https://github.example/team/repo/pull/4?tab=files#discussion",
    ),
  );
  assert(
    isMergeRequestURL("https://gitlab.example/team/repo/-/merge_requests/12/"),
  );
  for (const value of [
    "https://docs.example/installation",
    "https://github.example/team/repo/commit/abc1234",
    "https://github.example/team/repo/pull/4/files",
    "https://user@github.example/team/repo/pull/4",
    "not a URL",
  ])
    assert.equal(isMergeRequestURL(value), false);
});

test("a stalled request produces a retryable timeout instead of leaving the screen busy", async () => {
  const client = new ApiClient(
    (_path, init) =>
      new Promise((resolve, reject) => {
        const timer = setTimeout(() => resolve(new Response("{}")), 30);
        init?.signal?.addEventListener(
          "abort",
          () => {
            clearTimeout(timer);
            reject(init.signal?.reason);
          },
          { once: true },
        );
      }),
    () => "",
    5,
  );
  await assert.rejects(client.request("/v1/me"), /Сервер не ответил/);
});

test("browser requests use cookies and CSRF without accepting bearer credentials", async () => {
  const calls: { path: string; init: RequestInit }[] = [];
  const client = new ApiClient(
    async (path, init) => {
      calls.push({ path: String(path), init: init ?? {} });
      return new Response("{}", {
        headers: { "Content-Type": "application/json" },
      });
    },
    () => "workgraph_csrf_dev=wcs_fixture",
  );
  await client.request("/v1/context/search", { project_id: "p" });
  assert.equal(
    new Headers(calls[0]!.init.headers).get("X-Workgraph-CSRF"),
    "wcs_fixture",
  );
  assert.equal(calls[0]!.init.credentials, "same-origin");
  await client.request("/v1/me");
  assert.equal(new Headers(calls[1]!.init.headers).get("Authorization"), null);
  assert.equal(
    new Headers(calls[1]!.init.headers).get("X-Workgraph-CSRF"),
    null,
  );
  assert.equal(calls[1]!.path, "/v1/me");
});

test("authentication changes discard late responses even when fetch ignores abort", async () => {
  let finish!: (value: Response) => void;
  const client = new ApiClient(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
    () => "",
  );
  const pending = client.request("/v1/me");
  client.clear();
  finish(new Response('{"private":"old account"}'));
  await assert.rejects(pending, { name: "AbortError" });
});

test("project/search replacement cancels prior work and fences late completions", () => {
  const latest = new LatestRequest();
  const a = latest.begin();
  const b = latest.begin();
  assert.equal(a.signal.aborted, true);
  assert.equal(a.current(), false);
  assert.equal(b.current(), true);
  latest.cancel();
  assert.equal(b.current(), false);
});

test("errors never copy untrusted server bodies and API client rejects external paths", async () => {
  const client = new ApiClient(
    async () => new Response('{"error":"wg_secret <script>"}', { status: 403 }),
    () => "",
  );
  await assert.rejects(
    client.request("/v1/me"),
    (e: unknown) =>
      e instanceof ApiError &&
      e.status === 403 &&
      !e.message.includes("wg_secret"),
  );
  for (const path of [
    "https://foreign.test/v1/me",
    "//foreign.test/v1/me",
    "/v1/../foreign",
    "/v1/me?token=secret",
    "/v1/me#x",
    "/v1/a%2fb",
  ]) {
    await assert.rejects(client.request(path), /Invalid API path/);
  }
});

test("cookie parsing rejects duplicates and does not confuse secure and dev names", () => {
  assert.equal(
    csrfCookie("x=a; __Host-workgraph_csrf=wcs_secure", true),
    "wcs_secure",
  );
  assert.equal(
    csrfCookie(
      "workgraph_csrf_dev=wcs_dev; __Host-workgraph_csrf=wcs_secure",
      false,
    ),
    "wcs_dev",
  );
  assert.equal(
    csrfCookie("workgraph_csrf_dev=a; workgraph_csrf_dev=b", false),
    "",
  );
});

test("queries preserve explicit scope, bound UTF-8 and allow safe external source links", () => {
  assert.deepEqual(
    searchQuery("project", "repo", "  refreshToken  ", "code", true),
    {
      project_id: "project",
      repository_id: "repo",
      text: "refreshToken",
      kinds: ["code"],
      history: true,
      limit: 20,
    },
  );
  assert.throws(() => searchQuery("p", "", "я".repeat(257), "", false), /512/);
  assert.throws(() => searchQuery("p", "", "   ", "", false), /запрос/);
  assert.equal(
    safeExternalURL("https://gitlab.internal/team/repo"),
    "https://gitlab.internal/team/repo",
  );
  for (const value of [
    "javascript:alert(1)",
    "data:text/html,x",
    "https://user:pass@host/",
    "//host/x",
  ])
    assert.equal(safeExternalURL(value), null);
});

test("search filters support model-only discovery without changing the primary query", () => {
  assert.deepEqual(
    searchQuery("project", "repo", "", "", false, {
      agent: " Codex ",
      model: " model-alpha ",
      role: "assistant",
      file_path: "src/main.go",
    }),
    {
      project_id: "project",
      repository_id: "repo",
      text: "",
      history: false,
      limit: 20,
      agent: "Codex",
      model: "model-alpha",
      role: "assistant",
      file_path: "src/main.go",
    },
  );
  assert.throws(
    () =>
      searchQuery("project", "", "", "", false, { file_path: "src/main.go" }),
    /репозиторий/,
  );
  assert.equal(
    searchQuery("project", "", "abcdef0", "", false).text,
    "abcdef0",
  );
});
