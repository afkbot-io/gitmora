import { clickUI } from "./browser-auth.mjs";
/** All workspace areas against the real signed API/DB fixture, both themes and viewports. */
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { chromium, webkit, firefox, devices } from "playwright";
import { auditUI } from "./audit-ui.mjs";
import { watchBrowserPolicy } from "./browser-policy.mjs";
let input = "";
for await (const chunk of process.stdin) input += chunk;
const fixture = JSON.parse(input);
input = "";
const engine = { chromium, webkit, firefox }[
  process.env.GITMORA_BROWSER ?? "chromium"
];
assert.ok(engine, "Unsupported browser engine");
const mobile = process.env.GITMORA_MOBILE_DEVICE === "1";
const browser = await engine.launch({ headless: true });
const context = await browser.newContext({
  viewport: { width: 1440, height: 1000 },
  ...(mobile ? devices[engine === webkit ? "iPhone 13" : "Pixel 7"] : {}),
});
const page = await context.newPage();
const policy = await watchBrowserPolicy(page);
const faults = [];
page.on("pageerror", (e) => faults.push(e.message));
page.on("console", (m) => {
  if (
    /violates.*Content Security Policy|Refused to.*(?:script|style|font)/i.test(
      m.text(),
    )
  ) {
    if (engine !== webkit) faults.push("CSP failure");
  }
});
async function audit(name) {
  await auditUI(page, name, {
    enabled: true,
    fitViewport: name === "login" || name === "landing-en",
    ...(mobile ? { widths: [390] } : {}),
  });
}
async function nav(name) {
  await clickUI(page, name);
  await page.locator("#workspace-main h1").first().waitFor();
}
try {
  const seeded = await fetch(fixture.url + "/v1/projects", {
    method: "POST",
    headers: {
      Authorization: "Bearer " + fixture.admin,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      request_id: randomUUID(),
      id: randomUUID(),
      name: "L".repeat(190),
    }),
  });
  assert.equal(seeded.status, 201);
  await page.goto(fixture.url);
  await page
    .getByRole("heading", {
      name: "Задачи, код и решения вместе.",
      exact: true,
    })
    .waitFor();
  await audit("login");
  await page.getByLabel("Язык", { exact: true }).selectOption("en");
  await page
    .getByRole("heading", {
      name: "Tasks, code and decisions together.",
      exact: true,
    })
    .waitFor();
  assert.equal(
    await page
      .getByRole("link", { name: "Sign in", exact: true })
      .getAttribute("href"),
    "/v1/auth/login?locale=en",
  );
  await audit("landing-en");
  await page.getByLabel("Language", { exact: true }).selectOption("ru");

  assert.equal(await page.locator("#token").count(), 0);
  assert.equal(
    await page
      .getByRole("button", { name: "Тёмная тема", exact: true })
      .count(),
    0,
  );
  await page.getByRole("link", { name: "Войти", exact: true }).click();
  await page.getByRole("searchbox").waitFor();
  await nav("Настройки");
  await audit("preferences");
  const toggle = page.getByRole("button", { name: "Тёмная тема", exact: true });
  await toggle.focus();
  await page.keyboard.press("Enter");
  assert.equal(await toggle.getAttribute("aria-pressed"), "true");
  await page.reload();
  assert.equal(await page.locator("html").getAttribute("data-theme"), "dark");
  await nav("Контекст");
  await page
    .getByLabel("Проект", { exact: true })
    .selectOption(fixture.project);
  await audit("context-empty");
  await page.getByRole("searchbox").fill("нет-совпадений-проверка");
  await page
    .getByRole("button", { name: "Найти контекст", exact: true })
    .click();
  await page
    .getByRole("heading", { name: "Совпадений пока нет", exact: true })
    .waitFor();
  await audit("context-no-results");
  for (const [name, label] of [
    ["work", "Работа"],
    ["repositories", "Репозитории"],
    ["settings", "Настройки проекта"],
    ["catalog", "Проекты"],
    ["accounts", "Учётные записи"],
    ["bindings", "Привязки входа"],
    ["keys", "Личные ключи"],
    ["setup", "Подключение"],
  ]) {
    await nav(label);
    await audit(name);
    if (name === "setup") {
      await page
        .getByLabel("Система компьютера разработчика")
        .selectOption("gitmora_linux_amd64");
      assert.match(
        await page.getByText(/sha256sum -c selected.sha256/).textContent(),
        /gitmora_linux_amd64/,
      );
      await page
        .getByText("Поиск и контекст через MCP", { exact: true })
        .click();
      await audit("setup-mcp");
    }
    if (name === "repositories") {
      await page
        .getByText("Подключить CLI и локальных агентов", { exact: true })
        .click();
      await audit("agent-setup");
    }
  }
  await nav("Репозитории");
  await nav("Настройки проекта");
  await page.goBack();
  await page
    .getByRole("heading", { name: "Репозитории", exact: true })
    .waitFor();
  await page.goForward();
  await page
    .getByRole("heading", { name: "Настройки проекта", exact: true })
    .waitFor();
  await page
    .getByRole("button", { name: "Удалить проект", exact: true })
    .click();
  await page.getByRole("dialog").waitFor();
  await audit("confirm-dialog");
  await page.keyboard.press("Escape");
  assert.equal(await page.getByRole("dialog").count(), 0);

  const beforeLocale = await page.evaluate(async () =>
    (await fetch("/v1/me")).json(),
  );
  await nav("Настройки");
  await page.getByLabel("Язык", { exact: true }).selectOption("en");
  await page.getByRole("heading", { name: "Settings", exact: true }).waitFor();
  await page.reload();
  assert.equal(await page.locator("html").getAttribute("lang"), "en");
  await audit("preferences-en");
  for (const name of [
    "Context",
    "Work",
    "Repositories",
    "Project settings",
    "Projects",
    "Accounts",
    "Sign-in bindings",
    "Personal keys",
    "Setup",
  ]) {
    await nav(name);
    assert.equal(
      await page
        .locator("#workspace-main")
        .getByRole("button", { name: "Тёмная тема", exact: true })
        .count(),
      0,
    );
    await audit("en-" + name.toLowerCase().replaceAll(" ", "-"));
  }
  const afterLocale = await page.evaluate(async () =>
    (await fetch("/v1/me")).json(),
  );
  assert.equal(afterLocale.user.id, beforeLocale.user.id);
  assert.deepEqual(afterLocale.scopes, beforeLocale.scopes);
  await nav("Settings");
  await page.getByLabel("Language", { exact: true }).selectOption("ru");
  await page.setViewportSize({ width: mobile ? 390 : 320, height: 740 });
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
    false,
  );
  const menu = page.getByRole("button", { name: "Разделы", exact: true });
  await menu.focus();
  await page.keyboard.press("Enter");
  await page.getByRole("navigation", { name: "Разделы проекта" }).waitFor();
  await page.getByRole("link", { name: "Контекст", exact: true }).click();
  assert.equal(await menu.getAttribute("aria-expanded"), "false");
  assert.equal(
    await page
      .locator("#workspace-main")
      .evaluate((el) => el === document.activeElement),
    true,
  );
  await page.getByRole("button", { name: "Выйти", exact: true }).click();
  await page
    .getByRole("heading", {
      name: "Задачи, код и решения вместе.",
      exact: true,
    })
    .waitFor();
  assert.equal(
    await page.evaluate(() => localStorage.getItem("gitmora.theme")),
    "dark",
  );
  assert.deepEqual(
    await page.evaluate(() => Object.keys(localStorage).sort()),
    ["gitmora.locale", "gitmora.theme"],
  );
  assert.deepEqual(faults, []);
  assert.deepEqual(
    policy.unexpected(),
    [],
    "Unexpected enforced CSP violation",
  );
  const isolated = await browser.newContext({ colorScheme: "dark" });
  await isolated.addInitScript(() => {
    Storage.prototype.getItem = () => {
      throw new DOMException("blocked", "SecurityError");
    };
    Storage.prototype.setItem = () => {
      throw new DOMException("blocked", "SecurityError");
    };
  });
  const privatePage = await isolated.newPage();
  await privatePage.goto(fixture.url);
  await privatePage.getByRole("link", { name: "Войти", exact: true }).waitFor();
  assert.equal(
    await privatePage.locator("html").getAttribute("data-theme"),
    "dark",
  );
  await privatePage.getByRole("link", { name: "Войти", exact: true }).click();
  await privatePage.locator(".sidebar").waitFor();
  await clickUI(privatePage, "Настройки");
  await privatePage
    .getByRole("button", { name: "Тёмная тема", exact: true })
    .click();
  assert.equal(
    await privatePage.locator("html").getAttribute("data-theme"),
    "light",
  );
  await isolated.close();
  console.log(
    JSON.stringify({
      result: "passed",
      areas: 13,
      browser: engine.name(),
      mobileEmulation: mobile,
      nativeSelectDiagnostics: policy.nativeSelectDiagnostics(),
      policyChecked: true,
      screenshotDiagnostics: policy.screenshotDiagnostics(),
      themes: ["light", "dark"],
      viewports: mobile ? [390] : [1440, 390, 320],
      axe: true,
      persistence: true,
      blockedStorage: true,
      keyboard: true,
    }),
  );
} finally {
  await context.close();
  await browser.close();
}
