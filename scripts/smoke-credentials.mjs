/** Personal automation keys through the current signed project workspace. Synthetic data only. */
import assert from "node:assert/strict";
import { chromium } from "playwright";
import { signInAs, clickUI } from "./browser-auth.mjs";
import { auditUI } from "./audit-ui.mjs";
let raw = "";
for await (const chunk of process.stdin) raw += chunk;
const fixture = JSON.parse(raw);
raw = "";
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
const faults = [];
page.on("pageerror", (e) => faults.push(e.message));
async function status(key, path = "/v1/me") {
  const r = await fetch(fixture.url + path, {
    headers: { Authorization: "Bearer " + key },
  });
  return r.status;
}
async function issue(name) {
  await page.getByLabel("Название ключа", { exact: true }).fill(name);
  await page.getByLabel("Срок в минутах", { exact: true }).fill("5");
  await page.getByRole("checkbox", { name: /^Читать сессии агента/ }).check();
  await page
    .getByRole("button", { name: "Выпустить личный ключ", exact: true })
    .click();
}
try {
  await signInAs(page, fixture);
  await clickUI(page, "Личные настройки");
  await page.getByText("Ключи для автоматизации", { exact: true }).click();
  assert.equal(
    await page
      .locator(".sidebar")
      .getByRole("link", { name: "Учётные записи", exact: true })
      .count(),
    0,
  );
  await issue("Browser automation");
  const disclosure = page.locator(".secret-disclosure");
  await disclosure.waitFor();
  const key = await page
    .getByLabel("Новый API-ключ", { exact: true })
    .inputValue();
  assert.match(key, /^wg_/);
  assert.equal(await status(key), 200);
  assert.equal(await status(key, "/v1/projects"), 403);
  const values = await page.evaluate(() =>
    [
      ...Object.values(localStorage),
      ...Object.values(sessionStorage),
      location.href,
    ].join("\n"),
  );
  assert.ok(!values.includes(key));
  await auditUI(page, "personal-key-disclosure", { enabled: true });
  await page
    .getByRole("button", { name: "Отозвать этот выданный ключ", exact: true })
    .click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Отозвать ключ и потомков", exact: true })
    .click();
  await disclosure.waitFor({ state: "hidden" });
  assert.equal(await status(key), 401);
  let dropped = false;
  await page.route("**/v1/me/tokens", async (route) => {
    if (route.request().method() !== "POST" || dropped) return route.continue();
    dropped = true;
    await route.fetch();
    await route.fulfill({
      status: 503,
      json: { error: { code: "service_unavailable" } },
    });
  });
  await issue("Uncertain delivery");
  await page.getByRole("alert").waitFor();
  await page
    .getByRole("button", { name: "Выпустить личный ключ", exact: true })
    .click();
  await page
    .getByRole("heading", {
      name: "Ключ создан, секрет недоступен",
      exact: true,
    })
    .waitFor();
  assert.equal(
    await page.getByLabel("Новый API-ключ", { exact: true }).count(),
    0,
  );
  const listed = await page.evaluate(async () =>
    (await fetch("/v1/me/tokens")).json(),
  );
  assert.equal(
    listed.items.filter((x) => x.label === "Uncertain delivery").length,
    1,
  );
  assert.ok(listed.items.every((x) => !x.secret && !x.secret_hash));
  await page.unroute("**/v1/me/tokens");
  await page
    .getByRole("button", { name: "Скрыть результат выпуска", exact: true })
    .click();
  await issue("Logout descendant");
  await disclosure.waitFor();
  const child = await page
    .getByLabel("Новый API-ключ", { exact: true })
    .inputValue();
  assert.equal(await status(child), 200);
  await page.getByRole("button", { name: "Выйти", exact: true }).click();
  await page.getByRole("link", { name: "Войти", exact: true }).waitFor();
  assert.equal(await status(child), 401);
  await signInAs(page, fixture);
  await clickUI(page, "Личные настройки");
  await page.getByText("Ключи для автоматизации", { exact: true }).click();
  assert.equal(await page.locator(".secret-disclosure").count(), 0);
  await page.setViewportSize({ width: 390, height: 844 });
  await auditUI(page, "personal-keys-mobile", { enabled: true, widths: [390] });
  assert.deepEqual(faults, []);
  console.log(
    JSON.stringify({
      result: "passed",
      personal_keys: true,
      bounded_permissions: true,
      first_response_only: true,
      uncertain_retry_no_duplicate: true,
      revocation: true,
      logout_revokes_descendant: true,
      no_browser_secret_storage: true,
      mobile: true,
    }),
  );
} finally {
  await browser.close();
}
