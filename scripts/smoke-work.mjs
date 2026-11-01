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
    value: response.status === 204 ? null : await response.json(),
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
  await click("Обновить источники");
  await page.locator(".work-row").filter({ hasText: id }).click();
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
  await screenshot("gitmora-import-origin-mobile");
  if (!mobile) await page.setViewportSize({ width: 1440, height: 1000 });
  await page.locator(".work-row").filter({ hasText: fixture.session }).click();
  await visible(
    page
      .getByRole("region", { name: "Запись сессии" })
      .locator(".transcript-message")
      .first(),
  );
}
async function screenshot(name) {
  const selected = page.locator(".work-row.selected").first();
  if (await selected.isVisible()) {
    await page.mouse.move(0, 0);
    const background = await selected.evaluate(
      (el) => getComputedStyle(el).backgroundColor,
    );
    await selected.hover();
    assert.equal(
      await selected.evaluate((el) => getComputedStyle(el).backgroundColor),
      background,
      "Hover erased the selected work item",
    );
  }
  await auditUI(page, name);
  await page.screenshot({
    caret: "initial",
    path: `output/playwright/${name}.png`,
    fullPage: false,
  });
}
try {
  if (fixture.tasktopia) {
    await login(page, fixture.admin);
    await page
      .getByLabel("Проект", { exact: true })
      .selectOption(fixture.project);
    await click("Работа");
    await page
      .locator(".work-row")
      .filter({ hasText: "Read and publish through real MCP" })
      .click();
    const task = page.getByRole("region", { name: "Задача Tasktopia" });
    await visible(task);
    for (const [summary, marker] of [
      ["Чек-лист:", "FixtureChecklistMarker"],
      ["Комментарии:", "FixtureCommentMarker"],
      ["Дефекты:", "FixtureDefectMarker"],
      ["Зависимости:", "FixtureDependencyMarker"],
    ]) {
      await task.locator("summary").filter({ hasText: summary }).click();
      await visible(task.getByText(marker, { exact: false }).first());
    }
    assert(!(await task.textContent()).includes("context-comment-secret"));
    await task.getByRole("button", { name: /FixtureDependencyMarker/ }).click();
    await visible(
      page.getByRole("heading", { name: "Задача пока недоступна" }),
    );
    await click("Назад к источнику");
    await visible(task);
    await page
      .getByRole("button", { name: "Открыть контекст MR", exact: true })
      .click();
    await visible(page.locator(".mr-context"));
    assert.equal(await page.locator(".head-mismatch").count(), 0);
    const query = {
      project_id: fixture.project,
      repository_id: fixture.repository,
      merge_request_iid: 7,
      expected_head_sha: "b".repeat(40),
    };
    assert(
      (await fetch(fixture.gitlab + "/__test/head", { method: "POST" })).ok,
    );
    assert.equal(
      (await api(`/v1/repositories/${fixture.repository}/forge/sync`, {}))
        .status,
      202,
    );
    await poll(async () => {
      const p = await api("/v1/context/pack", query);
      return (
        p.status === 200 &&
        p.value.merge_requests.items[0]?.observed_head_sha === "f".repeat(40) &&
        p.value.merge_requests.items[0]?.pipelines_for_observed_head.some(
          (item) => item.sha === "f".repeat(40) && item.status === "failed",
        )
      );
    });
    await click("Обновить контекст");
    await visible(page.locator(".head-mismatch"));
    assert(
      (await page.locator(".ci-observation").textContent()).includes("failed"),
    );
    assert(
      !(await page.locator(".ci-observation").textContent()).includes(
        "success",
      ),
    );
    assert(
      (await page.locator(".ci-observation").textContent()).includes(
        "f".repeat(40),
      ),
    );
    await screenshot("workgraph-work-tasktopia-head-change");
    await page
      .locator(".mr-context")
      .screenshot({ path: "output/playwright/workgraph-work-mr-detail.png" });
    await click("Открыть наблюдаемый head");
    await visible(page.locator(".mr-context"));
    assert.equal(await page.locator(".head-mismatch").count(), 0);
    await page
      .getByLabel("Вид источника", { exact: true })
      .selectOption("merge_request");
    await page.locator(".work-row").first().click();
    await visible(page.locator(".mr-context"));
    await page.setViewportSize({ width: 390, height: 844 });
    assert(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
      "MR mobile overflow",
    );
    await screenshot("workgraph-work-tasktopia-mobile");
    await page.locator(".mr-context").screenshot({
      caret: "initial",
      path: "output/playwright/workgraph-work-mr-detail-mobile.png",
    });
    if (!mobile) await page.setViewportSize({ width: 1440, height: 1000 });
    await page
      .getByLabel("Вид источника", { exact: true })
      .selectOption("task");
    await page
      .locator(".work-row")
      .filter({ hasText: "Read and publish" })
      .click();
    await visible(task);
    assert(
      (
        await fetch(fixture.tasktopia + "/__test/revoke", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: "{}",
        })
      ).ok,
    );
    assert.equal(
      (
        await api(
          `/v1/projects/${fixture.project}/tasktopia/tasks/${fixture.task}/sync`,
          {},
        )
      ).status,
      202,
    );
    await poll(async () => {
      const s = await api(`/v1/projects/${fixture.project}/tasktopia`);
      return s.status === 200 && s.value.blocked;
    });
    await click("Обновить контекст");
    await visible(
      page.getByRole("heading", { name: "Задача пока недоступна" }),
    );
    assert.equal(await task.count(), 0);
    await click("Обновить источники");
    await visible(
      page.getByRole("heading", { name: "Сохранённых источников пока нет" }),
    );
  } else {
    await page.goto(fixture.url);
    await page.getByRole("link", { name: "Войти" }).click();
    await visible(page.locator(".sidebar"));
    await page
      .getByLabel("Проект", { exact: true })
      .selectOption(fixture.project);
    await click("Работа");
    await visible(
      page.getByRole("heading", { name: "Сохранённых источников пока нет" }),
    );
    await page
      .getByLabel("Вид источника", { exact: true })
      .selectOption("session");
    await poll(async () => (await page.locator(".work-row").count()) === 20);
    await click("Загрузить ещё источники");
    await poll(async () => (await page.locator(".work-row").count()) === 22);
    const root = page.locator(".work-row").filter({ hasText: fixture.session });
    await root.click();
    const transcript = page.getByRole("region", { name: "Запись сессии" });
    await visible(transcript.locator(".transcript-message").first());
    assert.equal(await transcript.locator(".transcript-message").count(), 20);
    assert.equal(
      await page.evaluate(() => window.untrustedExecuted),
      undefined,
    );
    assert.equal(await transcript.locator("img").count(), 0);
    await click("Следующие сообщения");
    assert.equal(await transcript.locator(".transcript-message").count(), 6);
    await page.getByLabel("Ревизия сессии", { exact: true }).fill("1");
    await click("Открыть ревизию");
    await visible(transcript.getByText(/obsoleteWord/));
    assert.equal(await transcript.locator(".transcript-message").count(), 1);
    await click("Последняя ревизия");
    await visible(transcript.getByText(/refreshToken:/).first());
    await screenshot("workgraph-work-session");
    await click("Открыть задачу из привязки агента");
    await visible(
      page.getByRole("heading", { name: "Задача пока недоступна" }),
    );
    await click("Следующие окна");
    await visible(
      page.getByRole("button", { name: "Первые окна", exact: true }),
    );
    await click("Назад к источнику");
    await visible(transcript.locator(".transcript-message").first());
    await page.setViewportSize({ width: 390, height: 844 });
    assert(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
      "Session mobile overflow",
    );
    await screenshot("workgraph-work-mobile");
    if (!mobile) await page.setViewportSize({ width: 1440, height: 1000 });
    await exerciseImportedOrigin();
    let release;
    let entered;
    const requested = new Promise((r) => (entered = r));
    const gate = new Promise((r) => (release = r));
    await page.route("**/v1/context/pack", async (route) => {
      const result = await route.fetch();
      entered();
      await gate;
      await route.fulfill({ response: result }).catch(() => {});
    });
    await click("Обновить контекст");
    await requested;
    await page
      .getByLabel("Проект", { exact: true })
      .selectOption(fixture.other_project);
    await visible(
      page.getByRole("heading", { name: "Сохранённых источников пока нет" }),
    );
    release();
    await page.unroute("**/v1/context/pack");
    assert.equal(await transcript.count(), 0);
    assert(
      !(await page.locator(".work-inspector").textContent()).includes(
        fixture.session,
      ),
    );
    await page
      .getByLabel("Проект", { exact: true })
      .selectOption(fixture.project);
    await page
      .getByLabel("Вид источника", { exact: true })
      .selectOption("session");
    await page
      .locator(".work-row")
      .filter({ hasText: fixture.session })
      .click();
    await visible(transcript.locator(".transcript-message").first());
    const current = await api(`/v1/projects/${fixture.project}/members`);
    const member = current.value.items.find((x) => x.user_id === fixture.user);
    assert.equal(
      (
        await api(
          `/v1/projects/${fixture.project}/members/${fixture.user}`,
          { request_id: randomUUID(), expected_version: member.version },
          "DELETE",
        )
      ).status,
      200,
    );
    await click("Обновить источники");
    await visible(page.getByRole("alert"));
    assert.equal(await transcript.count(), 0);
    assert.equal(await page.locator(".work-row").count(), 0);
    const limitedSessions = await api(
      "/v1/context/browse",
      { project_id: fixture.other_project, kind: "session", limit: 1 },
      "POST",
      fixture.capture_only,
    );
    assert.equal(limitedSessions.status, 200);
    const limitedTasks = await api(
      "/v1/context/browse",
      { project_id: fixture.other_project, kind: "task", limit: 1 },
      "POST",
      fixture.capture_only,
    );
    assert.equal(limitedTasks.status, 403);
  }
  assert.deepEqual(
    await page.evaluate(() => [
      Object.keys(localStorage),
      Object.keys(sessionStorage),
    ]),
    [[], []],
  );
  assert.deepEqual(faults, []);
  assert.deepEqual(
    policy.unexpected(),
    [],
    "Unexpected enforced CSP violation",
  );
  console.log(
    fixture.tasktopia
      ? "Actual Tasktopia/Chromium work: cached rich task children, dependency navigation, confirmed MR context, provider head change and exact CI, expected-head adoption, mobile and revoked-source clearing passed."
      : `Actual signed ${engine.name()} work: accepted-source pagination, verified continuation navigation, exact/historical transcript and message pages, inert source text, missing-task state, project race, role loss, independent capture capability and mobile layout passed.`,
  );
} catch (error) {
  await page
    .screenshot({
      caret: "initial",
      path: "output/playwright/workgraph-work-failure.png",
      fullPage: true,
      mask: [page.locator('input[type="password"]')],
    })
    .catch(() => {});
  console.error(
    "Work headings at failure:",
    await page
      .getByRole("heading")
      .allTextContents()
      .catch(() => []),
  );
  throw error;
} finally {
  await context.close();
  await browser.close();
}
