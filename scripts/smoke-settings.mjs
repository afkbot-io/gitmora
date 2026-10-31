import { clickUI } from "./browser-auth.mjs";
import { auditUI } from "./audit-ui.mjs";
import { signInAs } from "./browser-auth.mjs";
/** Settings lifecycle through actual browser forms and server commands; all data is synthetic. */
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdir } from "node:fs/promises";
import { chromium } from "playwright";

let input = "";
for await (const chunk of process.stdin) input += chunk;
const fixture = JSON.parse(input);
input = "";
await mkdir("output/playwright", { recursive: true });
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({
  viewport: { width: 1440, height: 1000 },
});
const page = await context.newPage();
const faults = [];
page.on("pageerror", (error) => faults.push(error.message));
async function visible(locator) {
  await locator.waitFor({ state: "visible", timeout: 12_000 });
}
async function call(path, body, method = body === undefined ? "GET" : "POST") {
  const response = await fetch(fixture.url + path, {
    method,
    headers: {
      Authorization: "Bearer " + fixture.admin,
      "Content-Type": "application/json",
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  assert(
    response.ok,
    "Synthetic administrative request failed: " + response.status,
  );
  return response.status === 204 ? undefined : response.json();
}
async function click(name) {
  await clickUI(page, name);
}
async function confirm(name = "Подтвердить") {
  const dialog = page.getByRole("dialog");
  await visible(dialog);
  await dialog.getByRole("button", { name, exact: true }).click();
  await dialog.waitFor({ state: "hidden" });
}
async function settings() {
  await click("Настройки проекта");
  await visible(
    page.getByRole("heading", { name: "Настройки проекта", exact: true }),
  );
}
function workers(count = 8) {
  for (let i = 0; i < count; i++)
    execFileSync("bin/workgraphd", ["worker", "--once"], {
      timeout: 30_000,
      stdio: "pipe",
    });
}
try {
  await page.goto(fixture.url);
  await signInAs(page, fixture);
  await visible(page.locator("#project"));
  await page.locator("#project").selectOption(fixture.project);
  await click("Репозитории");
  await visible(
    page.getByRole("heading", {
      name: fixture.oidc ? "Исходный репозиторий" : "task-test",
      exact: true,
    }),
  );
  await page
    .getByText("Подключить CLI и локальных агентов", { exact: true })
    .click();
  const setup = page
    .locator("details")
    .filter({
      has: page.getByText("Подключить CLI и локальных агентов", {
        exact: true,
      }),
    })
    .first();
  const init = await setup
    .locator("pre")
    .filter({ hasText: "gitmora init --server" })
    .textContent();
  assert(
    init.includes(fixture.url) && init.includes(fixture.repository),
    "CLI setup targets another server/repository",
  );
  assert.equal(await setup.locator("a[download]").count(), 4);
  await setup.getByText("Поиск и контекст через MCP", { exact: true }).click();
  assert(
    (
      await setup
        .locator("pre")
        .filter({ hasText: "[mcp_servers.gitmora]" })
        .textContent()
    ).includes(fixture.url),
  );
  await setup
    .getByText("Статус, офлайн-очередь и отключение", { exact: true })
    .click();
  assert.equal(
    await setup
      .locator("pre")
      .filter({ hasText: "gitmora status" })
      .textContent(),
    "gitmora status\ngitmora sync --retry",
  );
  await setup.screenshot({ path: "output/playwright/gitmora-agent-setup.png" });
  await page.setViewportSize({ width: 390, height: 844 });
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
    "Agent setup overflows mobile viewport",
  );
  await setup.screenshot({
    path: "output/playwright/gitmora-agent-setup-mobile.png",
  });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await click("Удалить репозиторий");
  await confirm("Удалить локальные данные");
  await click("Проекты");
  await page
    .getByLabel("Название проекта", { exact: true })
    .fill("Сервис согласования");
  await click("Создать проект");
  await visible(page.getByRole("searchbox"));
  const project = await page.locator("#project").inputValue();
  assert.notEqual(project, fixture.project);
  await settings();
  const renameForm = page
    .locator("form")
    .filter({ has: page.getByLabel("Название проекта", { exact: true }) });
  let requestID = "";
  await page.route("**/v1/projects/" + project, async (route) => {
    if (route.request().method() !== "PATCH") {
      await route.continue();
      return;
    }
    requestID = route.request().postDataJSON().request_id;
    assert.equal((await route.fetch()).status(), 200);
    await route.abort("failed");
  });
  await page
    .getByLabel("Название проекта", { exact: true })
    .fill("Платформа согласования");
  await click("Сохранить название");
  await visible(renameForm.getByRole("alert"));
  await page.unroute("**/v1/projects/" + project);
  let replayID = "";
  page.on("request", (request) => {
    if (
      request.method() === "PATCH" &&
      request.url() === fixture.url + "/v1/projects/" + project
    )
      replayID = request.postDataJSON().request_id;
  });
  await click("Сохранить название");
  await page
    .locator("#project option", { hasText: "Платформа согласования" })
    .waitFor({ state: "attached" });
  assert.equal(
    replayID,
    requestID,
    "Uncertain retry allocated a second command identity",
  );
  const events = await call(
    `/v1/directory/events?project_id=${project}&limit=100`,
  );
  assert.equal(
    events.items.filter((item) => item.request_id === requestID).length,
    1,
  );
  const current = await call("/v1/projects/" + project);
  await call(
    "/v1/projects/" + project,
    {
      request_id: randomUUID(),
      expected_version: current.version,
      name: "Внешнее изменение",
    },
    "PATCH",
  );
  await page
    .getByLabel("Название проекта", { exact: true })
    .fill("Следующая версия");
  await click("Сохранить название");
  await visible(renameForm.getByRole("alert"));
  assert.match(await renameForm.getByRole("alert").textContent(), /Версия/);
  await click("Обновить проект");
  await page
    .getByLabel("Название проекта", { exact: true })
    .fill("Платформа согласования");
  await click("Сохранить название");
  await page
    .locator("#project option", { hasText: "Платформа согласования" })
    .waitFor({ state: "attached" });
  const selfRow = page
    .locator(".member-row")
    .filter({ hasText: fixture.admin_user });
  await selfRow.getByRole("combobox").selectOption("reader");
  await selfRow.getByRole("button", { name: "Сохранить роль" }).click();
  await visible(selfRow.getByRole("alert"));
  assert.match(
    await selfRow.getByRole("alert").textContent(),
    /другого.*администратора/,
  );
  await click("Учётные записи");
  const userHelp = page
    .locator("summary")
    .filter({ hasText: "Новые пользователи" });
  await visible(userHelp);
  assert.equal(await userHelp.locator("..").getAttribute("open"), null);
  await userHelp.click();
  await visible(
    page.locator("pre").filter({ hasText: "gitmorad users create" }),
  );
  assert.equal(
    await page
      .getByRole("button", { name: "Создать учётную запись", exact: true })
      .count(),
    0,
  );
  assert(
    (
      await page
        .locator("pre")
        .filter({ hasText: "gitmorad users create" })
        .textContent()
    ).includes("--installation /srv/gitmora/state"),
  );
  await call("/v1/admin/users", {
    request_id: randomUUID(),
    id: randomUUID(),
    display_name: "Новый инженер",
  });
  await click("Обновить учётные записи");
  let account = page.locator(".account-resource").filter({
    has: page.getByRole("heading", { name: "Новый инженер", exact: true }),
  });
  await visible(account);
  const user = (await account.locator(".mono").first().textContent()).trim();
  await account
    .getByLabel("Имя Новый инженер", { exact: true })
    .fill("Инженер платформы");
  await account.getByRole("button", { name: "Сохранить имя" }).click();
  account = page.locator(".account-resource").filter({
    has: page.getByRole("heading", {
      name: "Инженер платформы",
      exact: true,
    }),
  });
  await visible(account);
  await account.getByRole("button", { name: "Назначить оператором" }).click();
  await confirm();
  await visible(
    account.getByRole("button", { name: "Снять полномочия оператора" }),
  );
  await account
    .getByRole("button", { name: "Снять полномочия оператора" })
    .click();
  await confirm();
  await account
    .getByRole("button", { name: "Выключить учётную запись" })
    .click();
  await confirm();
  await visible(
    account.getByRole("button", { name: "Включить учётную запись" }),
  );
  await account
    .getByRole("button", { name: "Включить учётную запись" })
    .click();
  await confirm();
  await page.locator("#project").selectOption(project);
  await settings();
  await page.getByLabel("UUID учётной записи", { exact: true }).fill(user);
  await page
    .getByRole("combobox", { name: "Роль нового участника", exact: true })
    .selectOption("writer");
  await click("Добавить участника");
  const member = page.locator(".member-row").filter({ hasText: user });
  await visible(member);
  await member.getByRole("combobox").selectOption("reader");
  await member.getByRole("button", { name: "Сохранить роль" }).click();
  await page
    .getByLabel("UUID учётной записи", { exact: true })
    .fill(fixture.reader);
  await page
    .getByRole("combobox", { name: "Роль нового участника", exact: true })
    .selectOption("reader");
  await click("Добавить участника");
  await click("Репозитории");
  await click("Добавить репозиторий");
  await page
    .getByLabel("Название репозитория", { exact: true })
    .fill("Сервис API");
  await page
    .getByLabel("Адрес сервера GitLab", { exact: true })
    .fill(fixture.gitlab);
  await page
    .getByLabel("Числовой ID проекта GitLab", { exact: true })
    .fill("42");
  await click("Зарегистрировать репозиторий");
  await visible(page.getByRole("heading", { name: "Сервис API", exact: true }));
  await click("Переименовать");
  await page
    .getByLabel("Новое название репозитория", { exact: true })
    .fill("Backend API");
  await click("Сохранить название");
  await visible(
    page.getByRole("heading", { name: "Backend API", exact: true }),
  );
  await settings();
  const repository = await page
    .getByRole("combobox", { name: "Репозиторий интеграции" })
    .inputValue();
  await click("Настроить GitLab");
  const connectionMode = page.getByRole("combobox", {
    name: "Режим подключения GitLab",
    exact: true,
  });
  assert.equal(await connectionMode.inputValue(), "disabled");
  assert.equal(
    await page.getByLabel("Signing token GitLab", { exact: true }).count(),
    0,
  );
  await page
    .getByLabel("API-ключ GitLab", { exact: true })
    .fill("synthetic-gitlab-token");
  await click("Проверить и сохранить GitLab");
  await visible(page.getByRole("button", { name: "Запросить сверку GitLab" }));
  assert.equal(
    await page.getByLabel("API-ключ GitLab", { exact: true }).count(),
    0,
  );
  await click("Запросить сверку GitLab");
  workers();
  await click("Обновить состояние GitLab");
  await visible(
    page
      .getByRole("region", { name: "Потоки синхронизации GitLab" })
      .or(page.locator(".stream-list")),
  );
  assert((await call(`/v1/repositories/${repository}/gitlab`)).last_synced_at);
  const apiOnly = await call(`/v1/repositories/${repository}/gitlab`);
  assert.equal(apiOnly.webhook_mode, "disabled");
  assert.equal(apiOnly.webhook_path, "");
  await visible(
    page.getByText("Только API, вебхуки отключены", { exact: true }),
  );
  await click("Заменить ключи GitLab");
  assert.equal(await connectionMode.inputValue(), "disabled");
  await page
    .getByLabel("API-ключ GitLab", { exact: true })
    .fill("synthetic-gitlab-token");
  await connectionMode.selectOption("legacy");
  await page
    .getByLabel("Секрет вебхука GitLab", { exact: true })
    .fill("synthetic-webhook-secret");
  await click("Проверить и сохранить GitLab");
  await visible(page.getByText("Секретный заголовок", { exact: true }));
  const withHook = await call(`/v1/repositories/${repository}/gitlab`);
  assert.equal(withHook.webhook_mode, "legacy");
  assert(withHook.webhook_path);
  await click("Заменить ключи GitLab");
  assert.equal(await connectionMode.inputValue(), "legacy");
  await page
    .getByLabel("API-ключ GitLab", { exact: true })
    .fill("synthetic-gitlab-token");
  await page
    .getByLabel("Секрет вебхука GitLab", { exact: true })
    .fill("secret-that-must-not-be-sent");
  await connectionMode.selectOption("disabled");
  assert.equal(
    await page.getByLabel("Секрет вебхука GitLab", { exact: true }).count(),
    0,
  );
  const replacement = page.waitForRequest(
    (request) =>
      request.method() === "PUT" &&
      request.url().endsWith(`/v1/repositories/${repository}/gitlab`),
  );
  await click("Проверить и сохранить GitLab");
  assert.deepEqual((await replacement).postDataJSON(), {
    api_token: "synthetic-gitlab-token",
    webhook_mode: "disabled",
  });
  await visible(
    page.getByText("Только API, вебхуки отключены", { exact: true }),
  );
  assert.equal(
    (await call(`/v1/repositories/${repository}/gitlab`)).webhook_path,
    "",
  );
  if (fixture.tasktopia) {
    await click("Настроить Tasktopia");
    await page
      .getByLabel("HTTP MCP endpoint", { exact: true })
      .fill(fixture.tasktopia.endpoint);
    await page
      .getByLabel("UUID страны Tasktopia", { exact: true })
      .fill(fixture.tasktopia.country_id);
    await page
      .getByLabel("MCP-ключ Tasktopia", { exact: true })
      .fill(fixture.tasktopia.token);
    await click("Проверить и сохранить Tasktopia");
    await visible(page.getByRole("button", { name: "Изменить Tasktopia" }));
    await click("Изменить Tasktopia");
    assert(
      (await page
        .getByLabel("UUID страны Tasktopia")
        .getAttribute("readonly")) !== null,
    );
    await page
      .getByRole("checkbox", {
        name: "Добавлять подтверждённые MR в задачи автоматически",
      })
      .check();
    await page
      .getByLabel("MCP-ключ Tasktopia", { exact: true })
      .fill(fixture.tasktopia.token);
    await click("Проверить и сохранить Tasktopia");
    await visible(
      page.getByText("Включено для подтверждённых связей", { exact: true }),
    );
    await page
      .getByLabel("UUID задачи для обновления", { exact: true })
      .fill(fixture.tasktopia.task_id);
    await click("Обновить задачу Tasktopia");
    workers(2);
    assert(
      (
        await call(
          `/v1/projects/${project}/tasktopia/tasks/${fixture.tasktopia.task_id}`,
        )
      ).available,
    );
    await click("Отключить Tasktopia");
    await confirm("Отключить Tasktopia");
    assert.equal(
      (await call(`/v1/projects/${project}/tasktopia`)).enabled,
      false,
    );
  } else {
    assert(
      (await page.getByRole("alert").allTextContents()).some((text) =>
        /шифрование|разрешённые адреса/.test(text),
      ),
    );
  }
  await auditUI(page, "settings-connected-errors");
  await page.screenshot({
    path: "output/playwright/workgraph-settings.png",
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
    "Settings overflow at mobile width",
  );
  await page.screenshot({
    path: "output/playwright/workgraph-settings-mobile.png",
    fullPage: true,
  });
  await page.setViewportSize({ width: 1440, height: 1000 });
  const readerContext = await browser.newContext();
  const readerPage = await readerContext.newPage();
  await signInAs(readerPage, fixture, fixture.reader_token);
  await visible(readerPage.locator("#project"));
  await readerPage.locator("#project").selectOption(project);
  await readerPage
    .getByRole("link", { name: "Настройки проекта", exact: true })
    .click();
  assert.equal(
    await readerPage
      .getByRole("button", {
        name: /Настроить GitLab|Заменить ключи GitLab|Изменить Tasktopia|Добавить участника|Удалить проект/,
      })
      .count(),
    0,
  );
  await readerPage
    .getByRole("link", { name: "Репозитории", exact: true })
    .click();
  assert.equal(
    await readerPage
      .getByRole("button", { name: "Добавить репозиторий" })
      .count(),
    0,
  );
  await readerContext.close();

  await click("Заменить ключи GitLab");
  await page
    .getByLabel("API-ключ GitLab", { exact: true })
    .fill("secret-that-must-clear");
  const memberships = (await call(`/v1/projects/${project}/members`)).items;
  await call(
    `/v1/projects/${project}/members/${user}`,
    {
      request_id: randomUUID(),
      expected_version: memberships.find((item) => item.user_id === user)
        .version,
      role: "admin",
    },
    "PUT",
  );
  await call(
    `/v1/projects/${project}/members/${fixture.admin_user}`,
    {
      request_id: randomUUID(),
      expected_version: memberships.find(
        (item) => item.user_id === fixture.admin_user,
      ).version,
      role: "writer",
    },
    "PUT",
  );
  await click("Обновить проект");
  await page
    .getByLabel("API-ключ GitLab", { exact: true })
    .waitFor({ state: "detached" });
  const latest = (await call(`/v1/projects/${project}/members`)).items.find(
    (item) => item.user_id === fixture.admin_user,
  );
  await call(
    `/v1/projects/${project}/members/${fixture.admin_user}`,
    {
      request_id: randomUUID(),
      expected_version: latest.version,
      role: "admin",
    },
    "PUT",
  );
  await click("Обновить проект");
  await click("Отключить GitLab");
  await confirm("Отключить GitLab");
  assert.equal(
    (await call(`/v1/repositories/${repository}/gitlab`)).enabled,
    false,
  );
  assert(
    !(await page.locator("body").textContent()).includes(
      "synthetic-gitlab-token",
    ),
  );
  assert.deepEqual(
    await page.evaluate(() => [
      Object.keys(localStorage),
      Object.keys(sessionStorage),
    ]),
    [[], []],
  );
  await click("Репозитории");
  await click("Удалить репозиторий");
  await confirm("Удалить локальные данные");
  await settings();
  await click("Удалить проект");
  await page.keyboard.press("Escape");
  assert((await call("/v1/projects/" + project)).id === project);
  await click("Удалить проект");
  await confirm("Удалить проект и его данные");
  await visible(page.getByRole("heading", { name: "Каталог проектов" }));
  assert.equal(
    await page.locator(`#project option[value="${project}"]`).count(),
    0,
  );
  await click("Учётные записи");
  account = page.locator(".account-resource").filter({
    has: page.getByRole("heading", {
      name: "Инженер платформы",
      exact: true,
    }),
  });
  await account.getByRole("button", { name: "Удалить учётную запись" }).click();
  await confirm();
  assert((await call("/v1/admin/users/" + user)).deleted_at);
  assert.deepEqual(faults, []);
  console.log(
    "Actual Chromium settings: project/repository/account/member lifecycle, exact uncertain replay, version/last-admin conflicts, scoped provider setup/health/retry/disconnect, secret clearing, role loss, confirmations and mobile layout passed" +
      (fixture.tasktopia
        ? "; actual Tasktopia MCP connection/policy/task refresh passed."
        : "; Tasktopia server-unconfigured state verified."),
  );
} catch (error) {
  await page
    .screenshot({
      path: "output/playwright/workgraph-settings-failure.png",
      fullPage: true,
    })
    .catch(() => {});
  console.error(
    "Settings screen at failure:",
    (
      await page
        .locator("main")
        .innerText()
        .catch(() => "unavailable")
    ).slice(0, 2400),
  );
  throw error;
} finally {
  await context.close();
  await browser.close();
}
