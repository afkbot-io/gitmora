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
  await page.locator(".session-source > summary").click();
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
  // Append keeps exact earlier messages and the scroll position, with tools collapsed.
  assert.equal(await transcript.locator(".tool-activity[open]").count(), 0);
  assert.equal(
    await transcript.locator('[data-message-index="20"]').count(),
    1,
  );
  const firstNode = await transcript
    .locator(".transcript-message")
    .first()
    .elementHandle();
  for (const end of [40, 60, 80]) {
    const more = transcript.getByRole("button", {
      name: "Загрузить ещё 20 сообщений",
      exact: true,
    });
    await more.scrollIntoViewIfNeeded();
    const before = await page.evaluate(() => scrollY);
    await more.click();
    await visible(
      transcript.getByText(`Загружено сообщений: ${end}`, { exact: true }),
    );
    if (end <= 65)
      assert.equal(
        await transcript.locator(`[data-message-index="${end}"]`).count(),
        1,
      );
    assert(await firstNode.evaluate((node) => node.isConnected));
    assert(
      Math.abs((await page.evaluate(() => scrollY)) - before) < 4,
      "append moved reading position",
    );
  }
  await transcript
    .locator(".tool-activity")
    .first()
    .locator(":scope > summary")
    .click();
  const tool = transcript.locator(".tool-entry").first();
  await tool.locator(":scope > summary").click();
  await visible(
    tool.locator("pre").filter({ hasText: /^git status --short$/ }),
  );
  await transcript.locator(".session-source > summary").click();
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
  await transcript.getByLabel("Только переписка", { exact: true }).check();
  assert.equal(await transcript.locator(".tool-activity").count(), 0);
  // Every click consumes 20 source records, including across one-message continuations.
  for (const end of [40, 60, 80]) {
    await transcript
      .getByRole("button", { name: "Загрузить ещё 20 сообщений", exact: true })
      .click();
    await visible(
      transcript.getByText(`Загружено сообщений: ${end}`, { exact: true }),
    );
  }
  await visible(transcript.getByText("Archive marker 15", { exact: true }));
  assert.equal(
    await transcript.getByText("Archive marker 16", { exact: true }).count(),
    0,
  );
  assert.equal(await transcript.locator(".conversation-part").count(), 16);
  await transcript.getByLabel("Только переписка", { exact: true }).uncheck();
  await auditUI(page, "history-session", { enabled: true });
  await page.screenshot({
    path: "output/playwright/history-conversation.png",
    fullPage: false,
  });
  // A short continuation initially fills 20 records; previous history follows exact parent edges.
  await openSession("eeeeeeee-eeee-4eee-8eee-000000000005");
  await visible(
    transcript.getByText("Загружено сообщений: 20", { exact: true }),
  );
  await visible(transcript.getByText("Archive marker 24", { exact: true }));
  assert.equal(
    await transcript.getByText("Archive marker 25", { exact: true }).count(),
    0,
  );
  assert.equal(
    await transcript.getByText("Archive marker 4", { exact: true }).count(),
    0,
  );
  const previousHistory = transcript.getByRole("region", {
    name: "Предыдущие сообщения сессии",
    exact: true,
  });
  const precedingButton = previousHistory.getByRole("button", {
    name: "Загрузить предыдущие 20 сообщений",
    exact: true,
  });
  await precedingButton.scrollIntoViewIfNeeded();
  const continuationMarker = transcript.getByText("Archive marker 5", {
    exact: true,
  });
  const continuationTop = await continuationMarker.evaluate(
    (node) => node.getBoundingClientRect().top,
  );
  await precedingButton.click();
  await poll(
    async () =>
      (await previousHistory.locator("[data-message-index]").count()) === 20,
  );
  await visible(previousHistory.getByText("Archive marker 4", { exact: true }));
  assert(
    Math.abs(
      (await continuationMarker.evaluate(
        (node) => node.getBoundingClientRect().top,
      )) - continuationTop,
    ) < 4,
    `earlier continuation moved the current message: before=${continuationTop}, after=${await continuationMarker.evaluate((node) => node.getBoundingClientRect().top)}, scroll=${JSON.stringify(await page.evaluate(() => ({ y: scrollY, max: document.documentElement.scrollHeight - innerHeight })))}`,
  );
  assert.equal(
    await previousHistory
      .locator('[data-message-session="eeeeeeee-eeee-4eee-8eee-000000000005"]')
      .count(),
    0,
  );
  // A PR with one conversation gets only its own commit interval.
  await page.goto(
    fixture.url +
      `/#view=context&project=${fixture.project}&repository=${fixture.repository}&mr=4`,
  );
  await visible(page.getByRole("heading", { name: "MR !4", exact: true }));
  await visible(page.getByRole("button", { name: /Читать диалог/ }));
  assert.equal(
    await page.getByText("PR boundary fixture 3", { exact: true }).count(),
    0,
  );
  assert.equal(
    await page
      .getByRole("button", { name: "Загрузить ещё связи", exact: true })
      .count(),
    0,
  );
  await page.getByRole("button", { name: /Читать диалог/ }).click();
  await visible(
    page.getByRole("heading", { name: "Диалог MR !4", exact: true }),
  );
  const scoped = page.getByRole("region", {
    name: "Диалог изменения",
    exact: true,
  });
  await visible(
    scoped
      .locator(
        '.scoped-current [data-message-index="21"] .long-message > summary',
      )
      .filter({ hasText: "PR4BoundaryMarker" }),
  );
  assert(
    new URLSearchParams(new URL(page.url()).hash.slice(1)).get("mr") === "4",
  );
  assert.equal(
    await scoped
      .getByText("Проверь авторизацию и обнови CLI.", { exact: false })
      .count(),
    0,
  );
  assert.equal(await page.locator(".mr-context").count(), 0);
  for (const count of [40, 45]) {
    const more = scoped.getByRole("button", {
      name: "Загрузить ещё 20 сообщений",
      exact: true,
    });
    await more.scrollIntoViewIfNeeded();
    const y = await page.evaluate(() => scrollY);
    await more.click();
    await visible(
      scoped.getByText(`Загружено сообщений: ${count}`, { exact: true }),
    );
    assert(
      Math.abs((await page.evaluate(() => scrollY)) - y) < 4,
      "PR append lost reading position",
    );
  }
  assert.equal(await scoped.locator('[data-message-index="65"]').count(), 1);
  assert.equal(await scoped.locator('[data-message-index="1"]').count(), 0);
  assert.equal(
    await page.evaluate(
      () => getComputedStyle(document.querySelector("main")).overflowY,
    ),
    "visible",
  );
  await auditUI(page, "history-pr-scoped", { enabled: true });
  const longUser = scoped.locator(
    '.scoped-current .transcript-message[data-message-index="21"] .long-message',
  );
  assert.equal(
    await longUser.getByText("FullUserTailMarker", { exact: false }).count(),
    0,
  );
  await longUser.locator(":scope > summary").click();
  await visible(longUser.getByText("FullUserTailMarker", { exact: false }));
  assert((await longUser.innerText()).length > 30000);
  await longUser.locator(":scope > summary").click();
  const earlier = scoped.getByRole("region", {
    name: "Предыдущие сообщения сессии",
    exact: true,
  });
  const previousButton = earlier.getByRole("button", {
    name: "Загрузить предыдущие 20 сообщений",
    exact: true,
  });
  await previousButton.scrollIntoViewIfNeeded();
  const currentMarker = scoped.locator(
    '.scoped-current [data-message-index="21"]',
  );
  const markerTop = await currentMarker.evaluate(
    (node) => node.getBoundingClientRect().top,
  );
  await previousButton.click();
  await poll(
    async () => (await earlier.locator("[data-message-index]").count()) === 20,
  );
  const afterTop = await currentMarker.evaluate(
    (node) => node.getBoundingClientRect().top,
  );
  const scrollState = await page.evaluate(() => ({
    y: scrollY,
    max: document.documentElement.scrollHeight - innerHeight,
  }));
  assert(
    Math.abs(afterTop - markerTop) < 4 ||
      (Math.abs(scrollState.y - scrollState.max) < 2 && afterTop > markerTop),
    `prepend did not preserve the anchor within the document limit: before=${markerTop}, after=${afterTop}, scroll=${JSON.stringify(scrollState)}`,
  );
  assert.equal(
    await scoped.locator('.scoped-current [data-message-index="1"]').count(),
    0,
  );
  await visible(
    earlier.getByText("Выше — предыдущая переписка, вне выбранного изменения", {
      exact: true,
    }),
  );
  assert.equal(
    new URLSearchParams(new URL(page.url()).hash.slice(1)).get("mr"),
    "4",
  );
  assert.equal(await page.evaluate(() => window.untrustedExecuted), undefined);
  // The same scoped cursor path is read through the real HTTP MCP adapter.
  await scoped
    .locator(".scoped-current .tool-activity > summary")
    .first()
    .click();
  const expandedTool = scoped.locator('.tool-entry[data-message-index="23"]');
  await expandedTool.locator(":scope > summary").click();
  await visible(
    expandedTool
      .locator("pre")
      .filter({ hasText: "FullToolTailMarker" })
      .first(),
  );
  assert(
    (
      await expandedTool
        .locator("pre")
        .filter({ hasText: "FullToolTailMarker" })
        .first()
        .innerText()
    ).length > 50000,
  );
  const rpcHeaders = {
    Authorization: "Bearer " + fixture.admin,
    "Content-Type": "application/json",
    Accept: "application/json, text/event-stream",
    "MCP-Protocol-Version": "2026-07-28",
    "Mcp-Method": "tools/call",
    "Mcp-Name": "context.pack",
  };
  let cursor;
  let mcpCount = 0;
  for (let i = 0; i < 8; i++) {
    const response = await fetch(fixture.url + "/mcp", {
      method: "POST",
      headers: rpcHeaders,
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: i + 1,
        method: "tools/call",
        params: {
          name: "context.pack",
          arguments: {
            project_id: fixture.project,
            repository_id: fixture.repository,
            merge_request_iid: 4,
            dialogue_id: fixture.session,
            message_view: "compact",
            ...(cursor ? { message_after: cursor } : {}),
          },
          _meta: {
            "io.modelcontextprotocol/protocolVersion": "2026-07-28",
            "io.modelcontextprotocol/clientInfo": {
              name: "scoped-history",
              version: "1",
            },
            "io.modelcontextprotocol/clientCapabilities": {},
          },
        },
      }),
    });
    assert.equal(response.status, 200);
    const wire = await response.json();
    assert(!wire.result.isError);
    const pack = wire.result.structuredContent.result;
    assert(pack.merge_requests.items.every((m) => m.iid === 4));
    assert(
      pack.captures.items.every((c) =>
        c.confirmed_commits.every((sha) => sha === "b".repeat(40)),
      ),
    );
    for (const m of pack.transcript.items) {
      assert(m.index >= 20 && m.index < 65);
      mcpCount++;
    }
    cursor = pack.transcript.next;
    if (!cursor) break;
  }
  assert.equal(mcpCount, 45);
  await page
    .getByRole("navigation", { name: "Навигация" })
    .getByRole("button", { name: "MR !4", exact: true })
    .click();
  await visible(page.getByRole("heading", { name: "MR !4", exact: true }));
  await openSession(fixture.session);
  await exerciseImportedOrigin();
  await click("Поиск");
  await visible(page.locator(".context-view #search"));
  await page.locator(".context-view #search").fill("abcdef0");
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
        "PR-scoped dialogue and MCP pagination",
        "single scroll owner and append position",
        "PR breadcrumb retained",
        "recent cursor",
        "session revisions",
        "message pagination",
        "continuation pagination",
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
