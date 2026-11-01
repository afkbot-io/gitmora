import { auditUI } from "./audit-ui.mjs";
import { signInAs, clickUI } from "./browser-auth.mjs";
/** Regression exercise against real application processes; no mocked successful API responses. */
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdir } from "node:fs/promises";
import { chromium, webkit, firefox, devices } from "playwright";
import { watchBrowserPolicy } from "./browser-policy.mjs";

let raw = "";
for await (const chunk of process.stdin) raw += chunk;
const fixture = JSON.parse(raw);
raw = "";
const output = "output/playwright";
await mkdir(output, { recursive: true });
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
const page = await context.newPage();
const policy = await watchBrowserPolicy(page);
const faults = [];
const external = new Set();
page.on("pageerror", (error) => faults.push(error.message));
page.on("console", (event) => {
  if (
    /violates.*Content Security Policy|Refused to.*(?:script|style|font)/i.test(
      event.text(),
    )
  )
    if (engine !== webkit) faults.push(event.text());
});
page.on("request", (request) => {
  const url = new URL(request.url());
  if (
    ![new URL(fixture.url).origin, new URL(fixture.issuer).origin].includes(
      url.origin,
    )
  )
    external.add(url.origin);
});
async function visible(locator) {
  await locator.waitFor({ state: "visible", timeout: 10_000 });
}
async function call(path, body, method = "POST") {
  const response = await fetch(fixture.url + path, {
    method,
    headers: {
      Authorization: "Bearer " + fixture.admin,
      "Content-Type": "application/json",
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  assert(response.ok, "Synthetic administrative operation failed");
  return response.json();
}
async function search(text) {
  await page.getByRole("searchbox").fill(text);
  const project = await page.locator("#project").inputValue();
  const response = page.waitForResponse((r) => {
    if (
      !r.url().endsWith("/v1/context/search") ||
      r.request().method() !== "POST"
    )
      return false;
    const query = r.request().postDataJSON();
    return query.project_id === project && query.text === text;
  });
  await page.getByRole("button", { name: "Найти", exact: true }).click();
  const result = await response;
  assert.equal(result.status(), 200);
  return result.json();
}
try {
  await page.goto(fixture.url);
  await visible(
    page.getByRole("heading", { name: "История разработки под рукой." }),
  );
  await page.screenshot({
    caret: "initial",
    path: output + "/workgraph-login.png",
    fullPage: true,
  });
  await page.getByRole("link", { name: "Войти" }).click();
  await visible(page.getByRole("searchbox"));
  assert.equal(
    await page
      .locator('#project option:not([value="__new__"]):not([value=""])')
      .count(),
    1,
    "OIDC reader saw an unrelated project",
  );
  assert.match(await page.locator(".account").textContent(), /Инженер команды/);
  const found = await search("refreshToken");
  assert(
    found.items.length > 0 &&
      found.items.every((item) => item.source.revision === 2),
  );
  await page.locator(".result-row").first().click();
  await visible(page.locator(".source-quote"));
  // Selection stays visually identifiable while the pointer rests on the chosen source.
  const selectedRow = page.locator(".result-row.selected");
  await page.mouse.move(0, 0);
  const selectedBackground = await selectedRow.evaluate(
    (element) => getComputedStyle(element).backgroundColor,
  );
  await selectedRow.hover();
  assert.equal(
    await selectedRow.evaluate(
      (element) => getComputedStyle(element).backgroundColor,
    ),
    selectedBackground,
    "Hover erased the selected source background",
  );
  assert(
    await selectedRow.evaluate((element) => {
      const style = getComputedStyle(element);
      return (
        parseFloat(style.borderLeftWidth) > 0 &&
        style.borderLeftStyle === "solid"
      );
    }),
    "Selected source lost its visible edge marker",
  );
  assert(
    (await page.locator(".source-quote").textContent()).includes("<img src=x"),
  );
  assert.equal(await page.locator(".source-quote img").count(), 0);
  assert.equal(
    await page.evaluate(() => globalThis.untrustedExecuted),
    undefined,
  );
  await page.screenshot({
    caret: "initial",
    path: output + "/workgraph-evidence.png",
    fullPage: true,
  });
  await auditUI(page, "context-evidence");
  await page.getByRole("button", { name: "Следующая часть" }).click();
  await visible(page.getByRole("button", { name: "Начало фрагмента" }));
  await page.getByRole("button", { name: "Начало фрагмента" }).click();
  await visible(page.getByRole("button", { name: "Следующая часть" }));
  await page.setViewportSize({ width: 390, height: 844 });
  await page.evaluate(
    () =>
      new Promise((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(resolve)),
      ),
  );
  const mobileLayout = await page.evaluate(() => ({
    viewport: innerWidth,
    width: document.documentElement.scrollWidth,
    media: matchMedia("(max-width: 700px)").matches,
    client: document.documentElement.clientWidth,
    visual: window.visualViewport?.width,
    screen: screen.width,
    elements: [...document.querySelectorAll("body *")]
      .filter((node) => node.getBoundingClientRect().right > innerWidth + 1)
      .slice(0, 12)
      .map((node) => ({
        tag: node.tagName,
        class: node.getAttribute("class"),
        width: node.getBoundingClientRect().width,
        right: node.getBoundingClientRect().right,
      })),
  }));
  if (mobileLayout.width > mobileLayout.viewport)
    console.error(JSON.stringify(mobileLayout));
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
    "Mobile layout overflowed",
  );
  await page.screenshot({
    caret: "initial",
    path: output + "/workgraph-mobile.png",
    fullPage: true,
  });
  if (!mobile) await page.setViewportSize({ width: 1440, height: 1000 });
  await clickUI(page, "Репозитории");
  await visible(page.getByRole("heading", { name: "Platform", exact: true }));
  await clickUI(page, "Поиск");
  await page.route(
    "**/v1/context/search",
    (route) =>
      route.fulfill({
        status: 503,
        contentType: "application/json",
        body: '{"error":"synthetic-private-response"}',
      }),
    { times: 1 },
  );
  await page.getByRole("searchbox").fill("refreshToken");
  await page.getByRole("button", { name: "Найти" }).click();
  await visible(page.getByRole("alert"));
  assert.match(
    await page.getByRole("alert").textContent(),
    /временно недоступен/,
  );
  assert(
    !(await page.locator("body").textContent()).includes(
      "synthetic-private-response",
    ),
  );
  await page.unroute("**/v1/context/search");
  assert.equal((await search("obsoleteWord")).items.length, 0);
  await page.getByRole("checkbox", { name: "Исторические версии" }).check();
  const historical = await search("obsoleteWord");
  assert(
    historical.items.length > 0 &&
      historical.items.every((item) => !item.current),
  );
  const logoutCommitted = page.waitForResponse(
    (response) =>
      response.url() === fixture.url + "/v1/auth/logout" &&
      response.request().method() === "POST" &&
      response.status() === 200,
  );
  await page.getByRole("button", { name: "Выйти", exact: true }).click();
  await logoutCommitted;
  await visible(
    page.getByRole("heading", { name: "История разработки под рукой." }),
  );
  await page.reload();
  await visible(page.getByRole("link", { name: "Войти", exact: true }));
  assert.equal(await page.locator("#token").count(), 0);
  assert.equal(
    await page.getByRole("searchbox").count(),
    0,
    "Logout did not survive reload",
  );
  await signInAs(page, fixture);
  await visible(page.locator("#project"));
  await page.locator("#project").selectOption(fixture.metadata_project);
  await visible(page.getByRole("heading", { name: "Нет доступа к контексту" }));
  assert.equal(
    await page.getByRole("searchbox").count(),
    0,
    "Operator status bypassed project content membership in UI",
  );
  await page.locator("#project").selectOption(fixture.project);
  await search("refreshToken");
  await visible(page.locator(".result-row").first());
  // Delay an actual successful server response, switch scope, then release it.
  let release;
  const gate = new Promise((resolve) => {
    release = resolve;
  });
  let entered;
  const started = new Promise((resolve) => {
    entered = resolve;
  });
  await page.route("**/v1/context/search", async (route) => {
    const response = await route.fetch();
    entered();
    await gate;
    try {
      await route.fulfill({ response });
    } catch {
      /* canceled request is expected */
    }
  });
  await page.getByRole("searchbox").fill("refreshToken");
  await page.getByRole("button", { name: "Найти" }).click();
  await started;
  await page.locator("#project").selectOption(fixture.other_project);
  assert.equal(await page.locator(".result-row").count(), 0);
  release();
  await page.unroute("**/v1/context/search");
  await visible(page.getByRole("searchbox"));
  assert.equal(await page.locator(".source-quote").count(), 0);
  assert.equal((await search("refreshToken")).items.length, 0);
  const storage = await page.evaluate(() => ({
    local: Object.keys(localStorage).filter(
      (key) => !key.startsWith("gitmora.project."),
    ),
    session: Object.keys(sessionStorage),
    url: location.href,
  }));
  assert.deepEqual(storage.local, []);
  assert.deepEqual(storage.session, []);
  assert(!storage.url.includes(fixture.admin));
  await page.getByRole("button", { name: "Выйти", exact: true }).click();
  await visible(page.getByRole("link", { name: "Войти", exact: true }));
  const restored = await fetch(fixture.issuer + "/control", {
    method: "POST",
    body: JSON.stringify({ mode: "good", subject: "subject-reader" }),
  });
  assert.ok(restored.ok);
  await page.getByRole("link", { name: "Войти", exact: true }).click();
  await visible(page.getByRole("searchbox"));
  await search("refreshToken");
  await call(
    "/v1/admin/identities/" + fixture.binding,
    {
      request_id: randomUUID(),
      expected_version: fixture.binding_version,
      enabled: false,
    },
    "PUT",
  );
  await page.evaluate(() =>
    document.querySelector(".search-form")?.requestSubmit(),
  );
  await visible(
    page.getByRole("heading", { name: "История разработки под рукой." }),
  );
  assert.equal(
    await page.locator(".result-row, .source-quote").count(),
    0,
    "Revoked account evidence remained visible",
  );
  assert.equal(external.size, 0, "Browser contacted an external origin");
  assert.deepEqual(faults, [], "Browser runtime or CSP errors");
  assert.deepEqual(
    policy.unexpected(),
    [],
    "Unexpected enforced CSP violation",
  );
  console.log(
    `Actual ${engine.name()}: signed OIDC, cookie/CSRF search, exact quotes, inert source HTML, revisions, repository view, mobile layout, durable logout, cookie-only authentication, project race and live revocation passed.`,
  );
} finally {
  await context.close();
  await browser.close();
}
