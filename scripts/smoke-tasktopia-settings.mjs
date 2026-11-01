/** Project settings against the actual isolated Tasktopia MCP server. */
import assert from "node:assert/strict";
import { chromium } from "playwright";
import { signInAs, clickUI } from "./browser-auth.mjs";
import { auditUI } from "./audit-ui.mjs";
let raw = "";
for await (const chunk of process.stdin) raw += chunk;
const fixture = JSON.parse(raw);
raw = "";
const browser = await chromium.launch();
const context = await browser.newContext();
const page = await context.newPage();
try {
  await signInAs(page, fixture);
  await page.locator("#project").selectOption(fixture.project);
  await clickUI(page, "Настройки проекта");
  await page.getByRole("link", { name: "Интеграции", exact: true }).click();
  const section = page
    .locator("section")
    .filter({
      has: page.getByRole("heading", { name: "Tasktopia", exact: true }),
    })
    .last();
  await page
    .getByRole("button", { name: "Подключить Tasktopia", exact: true })
    .click();
  await page
    .getByLabel("URL Tasktopia", { exact: true })
    .fill(fixture.tasktopia.endpoint);
  await page
    .getByLabel("UUID пространства Tasktopia", { exact: true })
    .fill(fixture.tasktopia.country_id);
  await page
    .getByLabel("Ключ Tasktopia", { exact: true })
    .fill(fixture.tasktopia.token);
  await section
    .getByRole("button", { name: "Сохранить подключение", exact: true })
    .click();
  await page
    .getByRole("status")
    .filter({ hasText: "Tasktopia подключена" })
    .waitFor();
  const state = await page.evaluate(
    async (project) =>
      (await fetch(`/v1/projects/${project}/tasktopia`)).json(),
    fixture.project,
  );
  assert.equal(state.endpoint, fixture.tasktopia.endpoint);
  assert.equal(state.country_id, fixture.tasktopia.country_id);
  assert.equal(state.enabled, true);
  await auditUI(page, "tasktopia-connected", {
    enabled: true,
    widths: [1440, 390],
  });
  // Change only policy/credential, preserving the selected service and workspace.
  await section
    .getByRole("button", { name: "Изменить доступ", exact: true })
    .click();
  assert.equal(
    await page
      .getByLabel("URL Tasktopia", { exact: true })
      .getAttribute("readonly"),
    "",
  );
  await page
    .getByLabel("Ключ Tasktopia", { exact: true })
    .fill(fixture.tasktopia.token);
  await page
    .getByLabel("Добавлять ссылки на запросы слияния в задачи", { exact: true })
    .check();
  await section
    .getByRole("button", { name: "Сохранить подключение", exact: true })
    .click();
  await page
    .getByRole("status")
    .filter({ hasText: "Tasktopia подключена" })
    .waitFor();
  await page.reload();
  await section.getByText("Подключено", { exact: true }).waitFor();
  await section.getByText("Состояние подключения", { exact: true }).click();
  await section
    .getByRole("button", { name: "Отключить Tasktopia", exact: true })
    .click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Отключить Tasktopia", exact: true })
    .click();
  await section.getByText("Отключено", { exact: true }).waitFor();
  console.log(
    JSON.stringify({
      result: "passed",
      actual_tasktopia: true,
      custom_url: true,
      policy: true,
      disconnect: true,
    }),
  );
} finally {
  await context.close();
  await browser.close();
}
