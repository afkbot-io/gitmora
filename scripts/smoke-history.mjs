import { clickUI } from "./browser-auth.mjs";
import { auditUI } from "./audit-ui.mjs";
import { signInAs } from "./browser-auth.mjs";
/** Built-process work navigation. All successful source data comes from API/PostgreSQL/providers. */
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdir } from "node:fs/promises";
import { chromium, webkit, firefox, devices } from "playwright";
import { watchBrowserPolicy } from "./browser-policy.mjs";

let raw = "";
for await (const chunk of process.stdin) raw += chunk;
const fixture = JSON.parse(raw);
raw = "";
await mkdir("output/playwright", { recursive: true });
const engine = { chromium, webkit, firefox }[
  process.env.GITMORA_BROWSER ?? "chromium"
];
assert.ok(engine, "Unsupported browser engine");
const browser = await engine.launch({ headless: true });
const mobile = process.env.GITMORA_MOBILE_DEVICE === "1";
const context = await browser.newContext({
  viewport: { width: 1440, height: 1000 },
  ...(mobile ? devices[engine === webkit ? "iPhone 13" : "Pixel 7"] : {}),
});
const faults = [];
function watch(page) {
  page.on("pageerror", (e) => faults.push(e.message));
  page.on("console", (e) => {
    if (
      /violates.*Content Security Policy|Refused to.*(?:script|style|font)/i.test(
        e.text(),
      )
    )
      if (engine !== webkit) faults.push(e.text());
  });
}
context.on("page", watch);
const page = await context.newPage();
const policy = await watchBrowserPolicy(page);
async function visible(locator) {
  await locator.waitFor({ state: "visible", timeout: 15_000 });
}
async function click(name) {
  await clickUI(page, name);
}
async function api(
  path,
  body,
  method = body === undefined ? "GET" : "POST",
  token = fixture.admin,
) {
  const response = await fetch(fixture.url + path, {
    method,
    headers: {
      Authorization: "Bearer " + token,
      "Content-Type": "application/json",
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  return {
    status: response.status,
    value:
      response.status === 204 ||
      !response.headers.get("Content-Type")?.includes("application/json")
        ? null
        : await response.json(),
  };
}
async function poll(check) {
  for (let i = 0; i < 100; i++) {
    if (await check()) return;
    await new Promise((r) => setTimeout(r, 150));
  }
  throw new Error("Real source reconciliation did not converge");
}
async function login(target, token) {
  await signInAs(target, fixture, token);
  await visible(target.locator(".sidebar"));
}
async function openSession(id) {
  await page.goto(
    fixture.url +
      `/#view=context&project=${fixture.project}&repository=${fixture.repository}&session=${id}`,
  );
  await visible(
    page.getByRole("region", { name: "Запись сессии", exact: true }),
  );
  await visible(page.locator(".transcript-message").first());
}
async function exerciseImportedOrigin() {
  const id = "00000001" + randomUUID().slice(8);
  const claims = Array.from({ length: 24 }, (_, i) => ({
    path: "src/imported.go",
    start: i * 2 + 1,
    end: i * 2 + 1,
    key: i.toString(16).padStart(16, "0"),
    kind: "ai",
    agent: {
      tool: "fixture-tool",
      session_id: `source-${i}`,
      model: "fixture-model",
    },
  }));
  claims.push({
    path: "src/imported.go",
    start: 54,
    end: 55,
    key: "h_abcdefabcdefab",
    kind: "known_human",
    author:
      "<img src=x onerror=window.untrustedExecuted=true> SourceAuthorMarker",
  });
  const snapshot = {
    schema_version: 1,
    repository_id: fixture.repository,
    session_id: id,
    revision: 1,
    agent: { name: "git-ai-import", model: "unreported" },
    started_at: "2026-10-30T10:01:00Z",
    messages: [
      {
        role: "system",
        kind: "provenance_report",
        text: "Source-reported ranges; uncovered lines are unknown.",
      },
    ],
    files: ["src/imported.go"],
    commits: ["a".repeat(40)],
    origin: {
      system: "git-ai",
      format: "authorship/3.0.0",
      producer_version: "fixture",
      source_id: "a".repeat(40),
      object_id: "b".repeat(40),
      ref: "refs/notes/ai",
      ref_object: "c".repeat(40),
      source_digest: "d".repeat(64),
      target_commit: "a".repeat(40),
      base_commit: "f".repeat(40),
      part: 1,
      parts: 1,
      time_kind: "commit_recorded",
      external_transcripts: true,
      claims,
    },
  };
  assert.equal((await api("/v1/captures", snapshot)).status, 201);
  await openSession(id);
  const origin = page.getByRole("region", { name: "Происхождение импорта" });
  await visible(
    origin.getByRole("heading", { name: "Импорт: Git AI", exact: true }),
  );
  await visible(origin.getByText("Данные источника.", { exact: false }));
  await origin
    .locator("summary")
    .filter({ hasText: "Диапазоны, указанные источником" })
    .click();
  assert.equal(await origin.locator("li").count(), 20);
  await click("Следующие диапазоны");
  assert.equal(await origin.locator("li").count(), 5);
  await visible(origin.getByText("SourceAuthorMarker", { exact: false }));
  assert.equal(await origin.locator("img").count(), 0);
  assert.equal(await page.evaluate(() => window.untrustedExecuted), undefined);
  await page.setViewportSize({ width: 390, height: 844 });
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
    "Import origin mobile overflow",
  );
  await auditUI(page, "history-import", { enabled: true });
  if (!mobile) await page.setViewportSize({ width: 1440, height: 1000 });
  await openSession(fixture.session);
  await visible(
    page
      .getByRole("region", { name: "Запись сессии" })
      .locator(".transcript-message")
      .first(),
  );
}

try {
  await login(page);
  await page.locator("#project").selectOption(fixture.project);
  await visible(
    page.getByRole("heading", { name: "Поиск по истории", exact: true }),
  );
  assert.equal(
    await page.getByRole("link", { name: "Работа", exact: true }).count(),
    0,
  );
  await visible(
    page.getByRole("heading", { name: "Недавние сессии", exact: true }),
  );
  await auditUI(page, "history-home", { enabled: true });
  const first = await api("/v1/context/browse", {
    project_id: fixture.project,
    kind: "session",
    order: "recent",
    limit: 6,
  });
  assert.equal(first.status, 200);
  assert.equal(first.value.items.length, 6);
  assert(first.value.next.observed_at);
  await page.getByRole("button", { name: "Показать ещё", exact: true }).click();
  await page
    .getByRole("button")
    .filter({ hasText: "Archive marker" })
    .first()
    .waitFor();
  await openSession(fixture.session);
  const transcript = page.getByRole("region", {
    name: "Запись сессии",
    exact: true,
  });
  await visible(transcript.getByText("<img src=x", { exact: false }));
  assert.equal(await transcript.locator("img").count(), 0);
  assert.equal(await page.evaluate(() => window.untrustedExecuted), undefined);
  await transcript
    .getByRole("button", { name: "Следующие сообщения", exact: true })
    .click();
  await visible(transcript.getByText("QuotedToolMessage 24", { exact: true }));
  await transcript
    .getByRole("button", { name: "Предыдущие сообщения", exact: true })
    .click();
  await transcript.getByLabel("Ревизия сессии", { exact: true }).fill("1");
  await transcript
    .getByRole("button", { name: "Открыть версию", exact: true })
    .click();
  await visible(
    transcript.getByText("obsoleteWord refreshToken: старая версия", {
      exact: true,
    }),
  );
  await transcript
    .getByRole("button", { name: "Последняя версия", exact: true })
    .click();
  await visible(transcript.getByText("<img src=x", { exact: false }));
  await auditUI(page, "history-session", { enabled: true });
  await exerciseImportedOrigin();
  await click("Назад к поиску");
  await visible(page.getByRole("searchbox"));
  await page.getByRole("searchbox").fill("abcdef0");
  await click("Найти");
  await visible(
    page.getByRole("alert").filter({ hasText: "Вставьте полный SHA" }),
  );
  await page
    .getByRole("searchbox")
    .fill("https://unregistered.invalid/team/repo/pull/7");
  await click("Найти");
  await visible(
    page.getByRole("alert").filter({ hasText: "ещё не синхронизирован" }),
  );
  const retired = await api(`/v1/projects/${fixture.project}/tasktopia`);
  assert.equal(retired.status, 404);
  const token = await api("/v1/me/tokens", {
    request_id: randomUUID(),
    label: "History revoke",
    scopes: ["capture:read", "directory:read"],
    ttl_seconds: 300,
  });
  assert.equal(token.status, 201);
  const key = token.value.secret;
  const before = await api(
    "/v1/context/browse",
    { project_id: fixture.project, kind: "session" },
    "POST",
    key,
  );
  assert.equal(before.status, 200);
  assert.equal(
    (
      await api(`/v1/me/tokens/${token.value.token.id}/revoke`, {
        request_id: randomUUID(),
      })
    ).status,
    200,
  );
  assert.equal(
    (
      await api(
        "/v1/context/browse",
        { project_id: fixture.project, kind: "session" },
        "POST",
        key,
      )
    ).status,
    401,
  );
  assert.deepEqual(faults, []);
  assert.deepEqual(policy.unexpected(), []);
  console.log(
    JSON.stringify({
      result: "passed",
      checks: [
        "recent cursor",
        "session revisions",
        "message pagination",
        "XSS plain text",
        "imported provenance",
        "desktop/mobile light/dark accessibility",
        "URL not found",
        "removed task route",
        "revocation",
      ],
    }),
  );
} finally {
  await context.close();
  await browser.close();
}
