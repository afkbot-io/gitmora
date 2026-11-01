import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";

/** Wait for durable logout before a fixture changes identity or reloads the page. */
export async function signOutAndWait(page) {
  const response = page.waitForResponse(
    (r) =>
      new URL(r.url()).pathname === "/v1/auth/logout" &&
      r.request().method() === "POST",
  );
  await page.getByRole("button", { name: "Выйти", exact: true }).click();
  assert.ok((await response).ok(), "Synthetic logout was not acknowledged");
  await page.getByRole("link", { name: "Войти", exact: true }).waitFor();
}

/** Real signed local IdP enrollment for owned browser fixtures; never targets production accounts. */
export async function signInAs(page, fixture, token = fixture.admin) {
  for (const origin of [fixture.url, fixture.issuer])
    assert.ok(["localhost", "127.0.0.1"].includes(new URL(origin).hostname));
  const api = async (path, body, credential = fixture.admin) => {
    const response = await fetch(fixture.url + path, {
      method: body ? "POST" : "GET",
      headers: {
        Authorization: "Bearer " + credential,
        "Content-Type": "application/json",
      },
      ...(body ? { body: JSON.stringify(body) } : {}),
    });
    assert.ok(response.ok, "Synthetic account preparation failed");
    return response.json();
  };
  const me = await api("/v1/me", undefined, token);
  const subject = "browser-fixture-" + me.user.id;
  const bindings = await api("/v1/admin/identities");
  if (
    !bindings.items.some(
      (item) =>
        item.user_id === me.user.id && item.subject === subject && item.enabled,
    )
  )
    await api("/v1/admin/identities", {
      request_id: randomUUID(),
      id: randomUUID(),
      user_id: me.user.id,
      subject,
    });
  const control = await fetch(fixture.issuer + "/control", {
    method: "POST",
    body: JSON.stringify({ mode: "good", subject }),
  });
  assert.ok(control.ok);
  await page.goto(fixture.url);
  await page.getByRole("link", { name: "Войти", exact: true }).click();
  await page.locator(".sidebar").waitFor();
}

const workspaceSections = new Set([
  "Работа",
  "Поиск",
  "Участники",
  "CLI и MCP",
  "Личные настройки",
  "Search",
  "Members",
  "CLI and MCP",
  "Personal settings",
  "Контекст",
  "Репозитории",
  "Настройки проекта",
  "Проекты",
  "Учётные записи",
  "Привязки входа",
  "Личные ключи",
  "Подключение",
  "Настройки",
  "Work",
  "Context",
  "Repositories",
  "Project settings",
  "Projects",
  "Accounts",
  "Sign-in bindings",
  "Personal keys",
  "Setup",
  "Settings",
]);
export async function clickUI(page, name) {
  if (name === "Личные настройки" || name === "Personal settings") {
    await page.locator('.account a[href*="preferences"]').click();
    return;
  }
  if (workspaceSections.has(name)) {
    await page.locator(".sidebar").waitFor();
    const menu = page.getByRole("button", {
      name: /^(Разделы|Разделы проекта|Sections|Project sections)$/,
      exact: true,
    });
    if (
      (await menu.isVisible()) &&
      (await menu.getAttribute("aria-expanded")) === "false"
    )
      await menu.click();
    await page.getByRole("link", { name, exact: true }).click();
  } else await page.getByRole("button", { name, exact: true }).click();
}
