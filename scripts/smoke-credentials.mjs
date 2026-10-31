import { clickUI } from "./browser-auth.mjs";
import { auditUI } from "./audit-ui.mjs";
import { signInAs } from "./browser-auth.mjs";
/** Actual source/transport lifecycle; raw fixture bearers stay in process/browser memory only. */
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdir } from "node:fs/promises";
import { chromium } from "playwright";

let raw = "";
for await (const chunk of process.stdin) raw += chunk;
const fixture = JSON.parse(raw);
raw = "";
await mkdir("output/playwright", { recursive: true });
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({
  viewport: { width: 1440, height: 1000 },
});
const faults = [];
function watch(page) {
  page.on("pageerror", (error) => faults.push(error.message));
  page.on("console", (event) => {
    if (
      /violates.*Content Security Policy|Refused to.*(?:script|style|font)/i.test(
        event.text(),
      )
    )
      faults.push(event.text());
  });
}
context.on("page", watch);
const page = await context.newPage();
async function visible(locator) {
  await locator.waitFor({ state: "visible", timeout: 12_000 });
}
async function click(page, name) {
  await clickUI(page, name);
}
async function control(mode = "good", subject) {
  assert(
    (
      await fetch(fixture.issuer + "/control", {
        method: "POST",
        body: JSON.stringify({ mode, ...(subject ? { subject } : {}) }),
      })
    ).ok,
  );
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
async function sso(page) {
  await page.getByRole("link", { name: "Войти" }).click();
}
async function bind(user, subject) {
  await page
    .getByLabel("UUID локального пользователя", { exact: true })
    .fill(user);
  await page
    .getByLabel("Точный subject провайдера", { exact: true })
    .fill(subject);
  await click(page, "Создать OIDC-привязку");
  await visible(
    page.locator(".binding-row").filter({
      has: page.getByRole("heading", { name: subject, exact: true }),
    }),
  );
}
async function issue(page, label, scopes, minutes = "10") {
  await page.getByLabel("Название ключа", { exact: true }).fill(label);
  await page.getByLabel("Срок в минутах", { exact: true }).fill(minutes);
  for (const scope of scopes)
    await page
      .locator(".scope-options label")
      .filter({ hasText: scope })
      .getByRole("checkbox")
      .check();
  await click(page, "Выпустить личный ключ");
  const field = page.getByLabel("Новый API-ключ", { exact: true });
  await visible(field);
  await auditUI(page, "key-disclosure");
  const secret = await field.inputValue();
  const panel = page.getByRole("region", { name: "Результат выпуска ключа" });
  const id = (await panel.locator("p.mono").first().textContent()).trim();
  assert(
    await page
      .getByRole("button", { name: "Выпустить личный ключ", exact: true })
      .isDisabled(),
  );
  await click(page, "Скрыть результат выпуска");
  assert.equal(await field.count(), 0);
  return { id, secret };
}
async function confirm(page, label = "Подтвердить") {
  const dialog = page.getByRole("dialog");
  await visible(dialog);
  await dialog.getByRole("button", { name: label, exact: true }).click();
  await dialog.waitFor({ state: "hidden" });
}
try {
  await control("down");
  await page.goto(fixture.url);
  await sso(page);
  await visible(page.getByRole("alert"));
  assert.match(await page.getByRole("alert").textContent(), /недоступен/);
  assert.equal(page.url(), fixture.url + "/");
  const jsonFailure = await fetch(fixture.url + "/v1/auth/login", {
    redirect: "manual",
    headers: { Accept: "application/json" },
  });
  assert.equal(jsonFailure.status, 503);
  assert.equal(jsonFailure.headers.get("location"), null);
  await control();
  await sso(page);
  await visible(page.getByRole("alert"));
  assert.match(await page.getByRole("alert").textContent(), /администратору/);
  assert.equal(
    (await api("/v1/admin/users")).value.items.length,
    2,
    "Unmapped subject auto-provisioned a user",
  );
  await signInAs(page, fixture);
  await click(page, "Привязки входа");
  await bind(fixture.user, "subject-reader");
  await bind(fixture.reader, "subject-collaborator");
  const subject = "subject-<img-src=x>";
  await bind(fixture.reader, subject);
  assert.equal(await page.locator(".binding-row img").count(), 0);
  const binding = (await api("/v1/admin/identities")).value.items.find(
    (item) => item.subject === "subject-collaborator",
  );
  const row = page.locator(`[data-binding-id="${binding.id}"]`);
  const changed = await api(
    "/v1/admin/identities/" + binding.id,
    {
      request_id: randomUUID(),
      expected_version: binding.version,
      enabled: false,
    },
    "PUT",
  );
  assert.equal(changed.status, 200);
  await row
    .getByRole("button", { name: "Выключить OIDC-привязку", exact: true })
    .click();
  const conflict = page.getByRole("dialog");
  await conflict
    .getByRole("button", { name: "Подтвердить", exact: true })
    .click();
  await visible(conflict.getByRole("alert"));
  assert.match(await conflict.getByRole("alert").textContent(), /версия/);
  await conflict.getByRole("button", { name: "Отмена", exact: true }).click();
  await click(page, "Обновить OIDC-привязки");
  await visible(row.getByRole("button", { name: "Включить OIDC-привязку" }));
  await row.getByRole("button", { name: "Включить OIDC-привязку" }).click();
  await confirm(page);
  await click(page, "Выйти");
  await sso(page);
  await visible(page.locator(".sidebar"));
  await click(page, "Личные ключи");
  const form = page.locator(".key-issue");
  await page.getByLabel("Название ключа", { exact: true }).fill("Too long");
  await page.getByLabel("Срок в минутах", { exact: true }).fill("999999");
  await page
    .locator(".scope-options label")
    .filter({ hasText: "capture:read" })
    .getByRole("checkbox")
    .check();
  await click(page, "Выпустить личный ключ");
  await visible(form.getByRole("alert"));
  assert.match(await form.getByRole("alert").textContent(), /срок/);
  await page
    .getByLabel("Название ключа", { exact: true })
    .fill("Lost first response");
  await page.getByLabel("Срок в минутах", { exact: true }).fill("10");
  let lost;
  await page.route("**/v1/me/tokens", async (route) => {
    if (route.request().method() !== "POST") {
      await route.continue();
      return;
    }
    const response = await route.fetch();
    assert.equal(response.status(), 201);
    lost = await response.json();
    await route.abort("failed");
  });
  await click(page, "Выпустить личный ключ");
  await visible(form.getByRole("alert"));
  await page.unroute("**/v1/me/tokens");
  await click(page, "Выпустить личный ключ");
  await visible(
    page.getByRole("heading", { name: "Ключ создан, секрет недоступен" }),
  );
  assert.equal(
    await page.getByLabel("Новый API-ключ", { exact: true }).count(),
    0,
  );
  assert(lost.secret && lost.created);
  assert.equal(
    (await api("/v1/me", undefined, "GET", lost.secret)).status,
    200,
  );
  await click(page, "Отозвать этот выданный ключ");
  await confirm(page, "Отозвать ключ и потомков");
  assert.equal(
    (await api("/v1/me", undefined, "GET", lost.secret)).status,
    401,
  );
  const parent = await issue(page, "Agent parent", [
    "capture:read",
    "credentials:manage",
  ]);
  // Scoped API keys remain a CLI/API credential, never a browser sign-in method.
  assert.equal(
    (await api("/v1/projects", undefined, "GET", parent.secret)).status,
    403,
  );
  const wider = await api(
    "/v1/me/tokens",
    {
      request_id: randomUUID(),
      label: "Denied expansion",
      scopes: ["capture:write"],
      ttl_seconds: 60,
    },
    "POST",
    parent.secret,
  );
  assert.equal(wider.status, 403);
  const delegated = await api(
    "/v1/me/tokens",
    {
      request_id: randomUUID(),
      label: "Agent child",
      scopes: ["capture:read"],
      ttl_seconds: 60,
    },
    "POST",
    parent.secret,
  );
  assert.equal(delegated.status, 201);
  const child = {
    id: delegated.value.token.id,
    secret: delegated.value.secret,
  };
  assert.equal(
    (await api("/v1/me", undefined, "GET", child.secret)).status,
    200,
  );
  await click(page, "Обновить список ключей");
  const parentRow = page.locator(`[data-key-id="${parent.id}"]`);
  await visible(parentRow);
  await parentRow
    .getByRole("button", { name: "Отозвать ключ", exact: true })
    .click();
  await confirm(page, "Отозвать ключ и потомков");
  assert.equal(
    (await api("/v1/me", undefined, "GET", parent.secret)).status,
    401,
  );
  assert.equal(
    (await api("/v1/me", undefined, "GET", child.secret)).status,
    401,
  );
  const active = await issue(page, "K".repeat(120), ["capture:read"]);
  await page.screenshot({
    path: "output/playwright/workgraph-keys.png",
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  await page.screenshot({
    path: "output/playwright/workgraph-keys-mobile.png",
    fullPage: true,
  });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await control("down");
  await page.goto(fixture.url + "/v1/auth/login");
  await visible(page.locator(".sidebar"));
  await visible(page.getByRole("alert"));
  assert.match(await page.getByRole("alert").textContent(), /недоступен/);
  assert.equal(
    (await api("/v1/me", undefined, "GET", active.secret)).status,
    200,
  );
  await control();
  const marker = "provider-private-text";
  await page.goto(
    fixture.url +
      "/v1/auth/oidc/callback?state=private-proof&code=private-code&error_description=" +
      marker,
  );
  await visible(page.locator(".sidebar"));
  assert.equal(page.url(), fixture.url + "/");
  assert(!(await page.locator("body").innerText()).includes(marker));
  await click(page, "Скрыть уведомление");
  await click(page, "Личные ключи");
  const logoutCommitted = page.waitForResponse(
    (response) =>
      response.url() === fixture.url + "/v1/auth/logout" &&
      response.request().method() === "POST" &&
      response.status() === 200,
  );
  await click(page, "Выйти");
  await logoutCommitted;
  await visible(
    page.getByRole("heading", { name: "Задачи, код и решения вместе." }),
  );
  assert.equal(
    (await api("/v1/me", undefined, "GET", active.secret)).status,
    401,
  );
  await control("good", "subject-reader");
  await sso(page);
  await visible(page.locator(".sidebar"));
  await click(page, "Привязки входа");
  await control("good", "subject-collaborator");
  const readerContext = await browser.newContext();
  readerContext.on("page", watch);
  const readerPage = await readerContext.newPage();
  await readerPage.goto(fixture.url);
  await sso(readerPage);
  await visible(readerPage.locator(".sidebar"));
  assert.equal(
    await readerPage
      .getByRole("link", { name: "Привязки входа", exact: true })
      .count(),
    0,
  );
  const readerKeys = readerPage.waitForResponse(
    (response) =>
      response.url() === fixture.url + "/v1/me/tokens?limit=100" &&
      response.request().method() === "GET",
  );
  await click(readerPage, "Личные ключи");
  const own = await (await readerKeys).json();
  assert(
    !own.items.some(
      (item) => item.id === parent.id || item.label === "Agent parent",
    ),
    "Another account's credential metadata appeared",
  );
  const readerKey = await issue(readerPage, "Reader descendant", [
    "capture:read",
  ]);
  await row.getByRole("button", { name: "Выключить OIDC-привязку" }).click();
  await confirm(page);
  await click(readerPage, "Обновить список ключей");
  await visible(
    readerPage.getByRole("heading", { name: "Задачи, код и решения вместе." }),
  );
  assert.equal(
    (await api("/v1/me", undefined, "GET", readerKey.secret)).status,
    401,
  );
  await row.getByRole("button", { name: "Включить OIDC-привязку" }).click();
  await confirm(page);
  await readerPage.reload();
  await visible(
    readerPage.getByRole("heading", { name: "Задачи, код и решения вместе." }),
  );
  await sso(readerPage);
  await visible(readerPage.locator(".sidebar"));
  assert.equal(
    (await api("/v1/me", undefined, "GET", readerKey.secret)).status,
    401,
  );
  await readerContext.close();
  const clockContext = await browser.newContext();
  clockContext.on("page", watch);
  const clockPage = await clockContext.newPage();
  await clockPage.clock.install();
  await clockPage.goto(fixture.url);
  await signInAs(clockPage, fixture);
  const me = {
    value: await clockPage.evaluate(async () => (await fetch("/v1/me")).json()),
  };
  // This pinned Playwright clock coerces each fastForward step to a signed 32-bit duration.
  let remaining = Date.parse(me.value.expires_at) - Date.now() + 1000;
  while (remaining > 0) {
    const step = Math.min(remaining, 1_000_000_000);
    await clockPage.clock.fastForward(step);
    remaining -= step;
  }
  await visible(
    clockPage.getByRole("heading", { name: "Задачи, код и решения вместе." }),
  );
  await clockContext.close();
  assert.deepEqual(
    await page.evaluate(() => [
      Object.keys(localStorage),
      Object.keys(sessionStorage),
    ]),
    [[], []],
  );
  assert.deepEqual(faults, []);
  console.log(
    "Actual Chromium access: signed login recovery/no auto-provisioning, immutable/versioned OIDC binding UI, first-only/uncertain key delivery, bounded independent delegation, cookie-only account navigation, subtree/logout/binding revocation, no resurrection, declared-deadline cleanup and mobile layout passed.",
  );
} catch (error) {
  await page
    .screenshot({
      path: "output/playwright/workgraph-access-failure.png",
      fullPage: true,
      mask: [
        page.getByLabel("Новый API-ключ", { exact: true }),
        page.locator('input[type="password"]'),
      ],
    })
    .catch(() => {});
  console.error(
    "Access headings at failure:",
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
