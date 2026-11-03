/** Signed browser/API/PostgreSQL flows for the project workspace. Synthetic accounts only. */
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { writeFileSync } from "node:fs";
import { chromium, webkit, firefox, devices } from "playwright";
import { signInAs, signOutAndWait, clickUI } from "./browser-auth.mjs";
import { auditUI } from "./audit-ui.mjs";
import { watchBrowserPolicy } from "./browser-policy.mjs";

let raw = "";
for await (const chunk of process.stdin) raw += chunk;
const fixture = JSON.parse(raw);
raw = "";
const engine = { chromium, webkit, firefox }[
  process.env.GITMORA_BROWSER ?? "chromium"
];
assert.ok(engine);
const mobile = process.env.GITMORA_MOBILE_DEVICE === "1";
const browser = await engine.launch();
const context = await browser.newContext(
  mobile
    ? devices[engine === webkit ? "iPhone 13" : "Pixel 7"]
    : { viewport: { width: 1920, height: 1080 } },
);
const page = await context.newPage();
const faults = [];
let actorToken = fixture.reader_token;
page.on("pageerror", (e) => faults.push(e.message));
const policy = await watchBrowserPolicy(page);
async function nav(name) {
  await clickUI(page, name);
  await page.locator("#workspace-main h1").first().waitFor();
}
async function call(
  path,
  body,
  method = body ? "POST" : "GET",
  token = actorToken,
) {
  const r = await fetch(fixture.url + path, {
    method,
    headers: {
      Authorization: "Bearer " + token,
      "Content-Type": "application/json",
    },
    ...(body ? { body: JSON.stringify(body) } : {}),
  });
  assert.ok(r.ok, "Synthetic API operation failed: " + r.status);
  return r.status === 204 ? undefined : r.json();
}
let areas = 0;
async function audit(name) {
  await auditUI(page, "workspace-" + name, {
    enabled: true,
    widths: mobile ? [390] : [1920, 390],
  });
  areas++;
}
try {
  // Establish the public invitation handle through the same authenticated profile form.
  await signInAs(page, fixture);
  await nav("Личные настройки");
  await page
    .getByLabel("Ник для приглашений", { exact: true })
    .fill("fixture-owner");
  await page
    .getByRole("button", { name: "Сохранить профиль", exact: true })
    .click();
  await page
    .getByRole("status")
    .filter({ hasText: "Профиль сохранён" })
    .waitFor();
  // Hold the logout response to exercise a real pending transition rather than
  // relying on loopback timing before this fixture changes accounts.
  let releaseLogout;
  let observeLogout;
  const logoutGate = new Promise((resolve) => (releaseLogout = resolve));
  const logoutObserved = new Promise((resolve) => (observeLogout = resolve));
  const delayedLogout = async (route) => {
    observeLogout();
    await logoutGate;
    await route.continue();
  };
  await page.route("**/v1/auth/logout", delayedLogout);
  const leaving = signOutAndWait(page);
  await logoutObserved;
  try {
    assert.equal(await page.locator(".sidebar").count(), 0);
    assert.equal(
      await page.getByRole("link", { name: "Войти", exact: true }).count(),
      0,
    );
  } finally {
    releaseLogout();
  }
  await leaving;
  await page.unroute("**/v1/auth/logout", delayedLogout);
  await signInAs(page, fixture, fixture.reader_token);
  await page.locator("#project").selectOption(fixture.project);
  await nav("Участники");
  assert.equal(
    await page
      .getByRole("button", { name: "Добавить участника", exact: true })
      .count(),
    0,
  );
  for (const name of [
    "Проекты",
    "Учётные записи",
    "Привязки входа",
    "Личные ключи",
  ])
    assert.equal(
      await page
        .locator(".sidebar")
        .getByRole("link", { name, exact: true })
        .count(),
      0,
    );
  const globalStatus = await page.evaluate(
    async () => (await fetch("/v1/admin/users")).status,
  );
  assert.equal(globalStatus, 403);
  await signOutAndWait(page);
  actorToken = fixture.new_user_token;
  await signInAs(page, fixture, actorToken);
  await page
    .getByRole("heading", { name: "Создайте первый проект", exact: true })
    .waitFor();
  assert.equal(
    await page.evaluate(
      async (id) => (await fetch(`/v1/projects/${id}`)).status,
      fixture.project,
    ),
    403,
  );
  await audit("first-project");
  let failProjectOpen = true;
  await page.route("**/v1/projects/*", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (
      failProjectOpen &&
      route.request().method() === "GET" &&
      /^\/v1\/projects\/[0-9a-f-]{36}$/.test(path)
    ) {
      failProjectOpen = false;
      return route.fulfill({
        status: 503,
        contentType: "application/json",
        body: '{"error":"unavailable"}',
      });
    }
    await route.continue();
  });
  const projectName = "Browser team " + randomUUID().slice(0, 8);
  await page.getByLabel("Название проекта", { exact: true }).fill(projectName);
  await page
    .getByRole("button", { name: "Создать проект", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Открыть проект", exact: true })
    .waitFor();
  const afterCreate = await call("/v1/projects");
  assert.equal(
    afterCreate.items.filter((p) => p.name === projectName).length,
    1,
  );
  await page
    .getByRole("button", { name: "Открыть проект", exact: true })
    .click();
  await page.unroute("**/v1/projects/*");
  await page
    .getByRole("heading", {
      name: "Подключите первый репозиторий",
      exact: true,
    })
    .waitFor();
  const project = await page.locator("#project").inputValue();
  assert.notEqual(project, fixture.project);
  const me = await page.evaluate(async () => (await fetch("/v1/me")).json());
  assert.equal(me.user.operator, false);
  const own = await page.evaluate(
    async (id) => (await fetch(`/v1/projects/${id}`)).json(),
    project,
  );
  assert.equal(own.role, "admin");
  if (!mobile) {
    await page.setViewportSize({ width: 2560, height: 1440 });
    const box = await page.locator(".workspace").boundingBox();
    assert.equal(box.x, 0);
    assert.equal(box.width, 2560);
    await page.setViewportSize({ width: 1920, height: 1080 });
  }
  await audit("first-repository");
  const host = fixture.github ? "github" : "gitlab";
  await page.getByLabel("Сервис", { exact: true }).selectOption(host);
  await page
    .getByLabel("Ссылка на репозиторий", { exact: true })
    .fill(
      fixture.github
        ? "https://github.com/example/gitmora-fixture"
        : fixture.gitlab + "/example/project",
    );
  await page
    .getByLabel("Токен чтения", { exact: true })
    .fill("intentionally-rejected-token");
  await page
    .getByRole("button", { name: "Подключить репозиторий", exact: true })
    .click();
  await page
    .getByRole("alert")
    .filter({ hasText: "Токен не принят" })
    .waitFor();
  assert.equal(
    await page.getByLabel("Токен чтения", { exact: true }).inputValue(),
    "",
  );
  let failConnection = true;
  await page.route("**/v1/repositories/*/forge", async (route) => {
    if (failConnection && route.request().method() === "PUT") {
      failConnection = false;
      return route.fulfill({
        status: 503,
        contentType: "application/json",
        body: '{"error":"unavailable"}',
      });
    }
    await route.continue();
  });
  const empty = await call(`/v1/projects/${project}/repositories`);
  assert.equal(empty.items.length, 0);
  await page
    .getByLabel("Токен чтения", { exact: true })
    .fill(
      fixture.github ? "synthetic-github-read-token" : "synthetic-gitlab-token",
    );
  await page
    .getByRole("button", { name: "Подключить репозиторий", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Сохранить подключение", exact: true })
    .waitFor();
  await page
    .getByLabel("Токен чтения", { exact: true })
    .fill(
      fixture.github ? "synthetic-github-read-token" : "synthetic-gitlab-token",
    );
  assert.equal(
    (await call(`/v1/projects/${project}/repositories`)).items.length,
    1,
  );
  await page
    .getByRole("button", { name: "Сохранить подключение", exact: true })
    .click();
  await page.unroute("**/v1/repositories/*/forge");
  await page.getByRole("heading", { name: "CLI и MCP", exact: true }).waitFor();
  const catalog = await call(`/v1/projects/${project}/repositories`);
  assert.equal(catalog.items.length, 1);
  const repository = catalog.items[0];
  assert.equal(repository.provider ?? "", fixture.github ? "github" : "");
  await nav("Репозитории");
  await page.getByRole("button", { name: "Настроить", exact: true }).click();
  await page.getByText("Подключено", { exact: true }).waitFor();
  await audit("integrations");
  const current = await call(`/v1/repositories/${repository.id}/forge`);
  assert.equal(current.enabled, true);
  assert.equal(current.anonymous ?? false, false);
  await page
    .getByRole("button", { name: "Изменить доступ", exact: true })
    .first()
    .click();
  await page
    .getByLabel("Токен чтения", { exact: true })
    .fill("intentionally-rejected-token");
  await page
    .getByRole("button", { name: "Сохранить подключение", exact: true })
    .click();
  await page
    .getByRole("alert")
    .filter({ hasText: "Токен не принят" })
    .waitFor();
  const unchanged = await call(`/v1/repositories/${repository.id}/forge`);
  assert.equal(unchanged.generation, current.generation);
  assert.equal(
    await page.getByLabel("Токен чтения", { exact: true }).inputValue(),
    "",
  );
  await page.getByRole("button", { name: "Отмена", exact: true }).click();
  // Add a distinct repository using only a saved reference: no token is typed again.
  await page.getByRole("button", { name: "Свернуть", exact: true }).click();
  await page
    .getByRole("button", { name: "Добавить репозиторий", exact: true })
    .click();
  await page
    .getByLabel("Сервис", { exact: true })
    .selectOption(fixture.github ? "github" : "gitlab");
  await page
    .getByLabel("Ссылка на репозиторий", { exact: true })
    .fill(
      fixture.github
        ? "https://github.com/example/second"
        : fixture.gitlab + "/example/second",
    );
  const accessPicker = page.getByLabel("Доступ к репозиторию", { exact: true });
  await accessPicker
    .locator(`option[value="${repository.id}"]`)
    .waitFor({ state: "attached" });
  await accessPicker.selectOption(repository.id);
  assert.equal(
    await page.getByLabel("Токен чтения", { exact: true }).count(),
    0,
  );
  await audit("reuse-token");
  const connectedResponse = page.waitForResponse(
    (response) =>
      response.url().includes("/forge") &&
      response.request().method() === "PUT",
  );
  await page
    .getByRole("button", { name: "Подключить репозиторий", exact: true })
    .click();
  const connected = await connectedResponse;
  assert.equal(connected.status(), 200);
  const sent = connected.request().postDataJSON();
  assert.deepEqual(sent.credential, {
    repository_id: repository.id,
    generation: current.generation,
  });
  assert.equal(sent.api_token, undefined);
  await page
    .getByRole("status")
    .filter({ hasText: "Репозиторий подключён." })
    .waitFor();
  assert.equal(
    await page
      .getByRole("heading", { name: "Репозитории", exact: true })
      .count(),
    1,
  );
  assert.equal(
    (await call(`/v1/projects/${project}/repositories`)).items.length,
    2,
  );
  await audit("multiple-repositories");
  const originalRow = page.locator("article.settings-resource").filter({
    has: page.getByRole("heading", { name: repository.name, exact: true }),
  });
  await originalRow
    .getByRole("button", { name: "Настроить", exact: true })
    .click();
  const twoRepositories = await call(`/v1/projects/${project}/repositories`);
  const secondRepository = twoRepositories.items.find(
    (item) => item.id !== repository.id,
  );
  assert.ok(secondRepository);
  await originalRow
    .getByRole("button", { name: "Изменить доступ", exact: true })
    .click();
  const editAccess = page.getByLabel("Доступ к репозиторию", { exact: true });
  await editAccess
    .locator(`option[value="${secondRepository.id}"]`)
    .waitFor({ state: "attached" });
  await editAccess.selectOption(secondRepository.id);
  // Rotate the source after selection, then verify the form recovers with a fresh reference.
  await call(
    `/v1/repositories/${secondRepository.id}/forge`,
    {
      api_token: fixture.github
        ? "synthetic-github-read-token"
        : "synthetic-gitlab-token",
      webhook_mode: "disabled",
    },
    "PUT",
    fixture.new_user_manage_token,
  );
  await page
    .getByRole("button", { name: "Сохранить подключение", exact: true })
    .click();
  await page
    .getByRole("alert")
    .filter({ hasText: "Токен больше недоступен" })
    .waitFor();
  await editAccess.locator('option[value=""]').waitFor({ state: "attached" });
  assert.equal(
    (await call(`/v1/repositories/${repository.id}/forge`)).generation,
    current.generation,
  );
  await editAccess.selectOption(secondRepository.id);
  await page
    .getByRole("button", { name: "Сохранить подключение", exact: true })
    .click();
  await page
    .getByRole("status")
    .filter({ hasText: "Подключение сохранено" })
    .waitFor();
  assert.equal(
    (await call(`/v1/repositories/${repository.id}/forge`)).generation,
    current.generation + 1,
  );
  if (fixture.github) {
    await page
      .getByRole("button", { name: "Изменить доступ", exact: true })
      .first()
      .click();
    await page
      .getByLabel("Доступ к репозиторию", { exact: true })
      .selectOption("public");
    await page
      .getByRole("button", { name: "Сохранить подключение", exact: true })
      .click();
    await page
      .getByRole("status")
      .filter({ hasText: "Подключение сохранено" })
      .waitFor();
    const publicConnection = await call(
      `/v1/repositories/${repository.id}/forge`,
    );
    assert.equal(publicConnection.anonymous, true);
    assert.ok(publicConnection.generation > current.generation);
    await page.route(
      `**/v1/repositories/${repository.id}/forge`,
      async (route) => {
        if (route.request().method() !== "GET") return route.continue();
        const response = await route.fetch();
        await route.fulfill({
          json: {
            ...(await response.json()),
            anonymous: true,
            last_error_code: "provider_rate_limited",
          },
        });
      },
    );
    await page.reload();
    await originalRow
      .getByRole("button", { name: "Настроить", exact: true })
      .click();
    await page
      .getByRole("button", { name: "Подключить токен", exact: true })
      .waitFor();
    await audit("github-rate-limit");
    await page
      .getByRole("button", { name: "Подключить токен", exact: true })
      .click();
    assert.equal(
      await page
        .getByLabel("Доступ к репозиторию", { exact: true })
        .inputValue(),
      "new",
    );
    await page.unroute(`**/v1/repositories/${repository.id}/forge`);
    await page.getByRole("button", { name: "Отмена", exact: true }).click();
  }
  await nav("Участники");
  await audit("members");
  await page
    .getByLabel("Ник или UUID пользователя", { exact: true })
    .fill("@fixture-owner");
  await page
    .getByRole("button", { name: "Добавить участника", exact: true })
    .click();
  await page
    .locator(".member-row")
    .filter({ hasText: "fixture-owner" })
    .waitFor();
  await page
    .getByLabel("Ник или UUID пользователя", { exact: true })
    .fill("fixture-owner");
  await page
    .getByRole("button", { name: "Добавить участника", exact: true })
    .click();
  await page.getByRole("alert").filter({ hasText: "уже в проекте" }).waitFor();
  await nav("Поиск");
  await audit("history");
  assert.doesNotMatch(
    await page.locator(".main").innerText(),
    /Показаны принятые локальные|Пустой список не означает/,
  );
  await nav("Поиск");
  await audit("search");
  await page.getByRole("searchbox").fill("history");
  await page.getByRole("button", { name: "Найти", exact: true }).click();
  await page.getByText("Ничего не найдено", { exact: true }).waitFor();
  await nav("CLI и MCP");
  const agentGuide = page.getByRole("link", {
    name: "Инструкция для агента · ai.md",
    exact: true,
  });
  assert.equal(await agentGuide.getAttribute("href"), "/ai.md");
  const guideResponse = await fetch(fixture.url + "/ai.md");
  assert.equal(guideResponse.status, 200);
  assert.match(guideResponse.headers.get("content-type"), /^text\/markdown/);
  assert.match(await guideResponse.text(), /context\.search/);
  assert.match(await page.locator(".setup-guide").innerText(), /\/install.sh/);
  await audit("cli");
  await page.getByText("Поиск через MCP", { exact: true }).click();
  await audit("mcp");
  await nav("Личные настройки");
  await audit("preferences");
  await page.getByLabel("Язык", { exact: true }).selectOption("en");
  await page
    .getByRole("heading", { name: "Personal settings", exact: true })
    .waitFor();
  await page.reload();
  assert.equal(await page.locator("html").getAttribute("lang"), "en");
  await audit("preferences-en");
  for (const section of [
    "Members",
    "Repositories",
    "Search",
    "CLI and MCP",
    "Project settings",
  ]) {
    await nav(section);
    await audit("en-" + section.toLowerCase().replaceAll(" ", "-"));
  }
  await nav("Personal settings");
  await page.getByLabel("Language", { exact: true }).selectOption("ru");
  await nav("Настройки проекта");
  await page.getByText("Удаление проекта", { exact: true }).click();
  await page
    .getByRole("button", { name: "Удалить проект", exact: true })
    .click();
  await page.getByRole("dialog").waitFor();
  await audit("delete-project");
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Удалить проект и его данные", exact: true })
    .click();
  await page.getByRole("dialog").waitFor({ state: "hidden" });
  assert.equal(
    (await call("/v1/projects")).items.some((p) => p.id === project),
    false,
  );
  assert.deepEqual(faults, []);
  assert.deepEqual(policy.unexpected(), []);
  console.log(
    JSON.stringify({
      result: "passed",
      browser: engine.name(),
      mobile,
      provider: host,
      ordinary_user_created_project: true,
      global_accounts_hidden: true,
      member_by_handle: true,
      full_width: true,
      invalid_token_clear_and_no_rotation: true,
      saved_token_reuse: true,
      stale_selection_recovery: true,
      locale: true,
      areas,
    }),
  );
} catch (error) {
  const visible = await page
    .locator("body")
    .innerText({ timeout: 2000 })
    .catch(() => "Page body unavailable");
  writeFileSync(
    "build/workspace-browser-failure.txt",
    String(error.stack ?? error) + "\n\n" + page.url() + "\n\n" + visible,
  );
  throw error;
} finally {
  await browser.close();
}
