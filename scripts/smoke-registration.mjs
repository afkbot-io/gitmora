import { auditUI } from "./audit-ui.mjs";
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { chromium } from "playwright";

const chunks = [];
for await (const chunk of process.stdin) chunks.push(chunk);
const fixture = JSON.parse(Buffer.concat(chunks).toString());
const browser = await chromium.launch({ headless: true });
const output = "output/playwright/core-accounts";
await mkdir(output, { recursive: true });
const faults = [];
const contexts = [];
async function pageFor() {
  const c = await browser.newContext();
  contexts.push(c);
  const p = await c.newPage();
  p.on("pageerror", () => faults.push("browser exception"));
  return p;
}
async function control(enabled) {
  const r = await fetch(
    fixture.control_url + "/registration/" + String(enabled),
    {
      method: "POST",
      headers: { "X-Verification-Nonce": fixture.control_nonce },
    },
  );
  assert.equal(r.status, 204);
}
async function me(page) {
  return page.evaluate(async () => {
    const r = await fetch("/v1/me");
    return { status: r.status, body: await r.json() };
  });
}
async function checkLocalTheme(page, name) {
  assert.ok(
    (
      await page
        .locator("[aria-invalid]")
        .evaluateAll((nodes) =>
          nodes.map((node) => node.getAttribute("aria-invalid")),
        )
    ).every((value) => value === "false" || value === "true"),
    "Account inputs require explicit boolean validity",
  );
  assert.equal(await page.locator('meta[name="viewport"]').count(), 1);
  assert.match(
    await page.locator('meta[name="viewport"]').getAttribute("content"),
    /width=device-width/,
  );
  await auditUI(page, "identity-" + name);
  await page
    .locator(name === "entry-mobile" ? ".landing" : ".auth-shell")
    .waitFor();
  await page.waitForLoadState("load");
  await page.evaluate(() => document.fonts.ready);
  const sheets = await page.evaluate(() =>
    [...document.querySelectorAll("link[rel=stylesheet]")].map((e) => e.href),
  );
  const assets = [];
  for (const href of sheets) {
    const r = await page.request.get(href);
    assets.push({
      path: new URL(href).pathname,
      status: r.status(),
      type: r.headers()["content-type"],
      length: (await r.body()).length,
    });
  }
  await writeFile(
    `build/registration-theme-assets-${name}.json`,
    JSON.stringify(assets),
  );
  await page.screenshot({ path: `${output}/${name}.png`, fullPage: true });
  assert.match(
    await page.locator("body").evaluate((e) => getComputedStyle(e).fontFamily),
    /IBM Plex Sans/,
  );
  assert.equal(
    await page
      .locator(name === "entry-mobile" ? ".landing-header" : ".auth-header")
      .count(),
    1,
  );
  assert.equal(await page.locator("[data-theme-toggle]").count(), 0);
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
    true,
    "horizontal overflow",
  );
  await page.screenshot({ path: `${output}/${name}.png`, fullPage: true });
}
try {
  const p = await pageFor();
  await p.goto(fixture.url);
  assert.equal(
    await p.getByRole("link", { name: "Создать аккаунт", exact: true }).count(),
    0,
  );
  const denied = await fetch(fixture.url + "/v1/auth/register");
  assert.equal(denied.status, 403);
  await p.getByLabel("Язык", { exact: true }).selectOption("en");
  await p.getByRole("link", { name: "Sign in", exact: true }).click();
  await p.locator("#username").waitFor();
  assert.equal(await p.locator("html").getAttribute("lang"), "en");
  await p
    .getByRole("heading", { name: "Sign in to Gitmora", exact: true })
    .waitFor();
  await p.locator(".auth-header summary").click();
  await p
    .locator(".auth-header nav")
    .getByRole("link", { name: /русский/i })
    .click();
  assert.equal(await p.locator("html").getAttribute("lang"), "ru");
  assert.equal(
    await p.evaluate(() => localStorage.getItem("gitmora.locale")),
    "ru",
  );

  await checkLocalTheme(p, "login-desktop");
  await p.locator("#username").fill(fixture.operator_account.username);
  await p.locator("#password").fill("incorrect-synthetic-password");
  await p.locator("#kc-login").click();
  await p.locator("#input-error").waitFor();
  await auditUI(p, "identity-login-error");
  assert.equal(
    await p.locator("#password").getAttribute("aria-invalid"),
    "true",
  );
  await p.locator("#password").fill(fixture.operator_account.password);
  await p.locator("#kc-login").click();
  // The provider may require completion of its default contact profile; no authorization derives from this email.
  await p.waitForURL(
    (url) =>
      url.origin === new URL(fixture.url).origin &&
      (url.pathname === "/" || url.pathname.includes("login-actions")),
  );
  if (await p.locator("#email").isVisible()) {
    assert.ok(
      (
        await p
          .locator("[aria-invalid]")
          .evaluateAll((nodes) =>
            nodes.map((node) => node.getAttribute("aria-invalid")),
          )
      ).every((value) => value === "false" || value === "true"),
    );
    await auditUI(p, "identity-profile");
    await p.locator("#email").fill("cli-engineer@example.invalid");
    if ((await p.locator("#lastName").inputValue()) === "")
      await p.locator("#lastName").fill("Engineer");
    await p.locator("#kc-update-profile-form [type=submit]").click();
  }
  await p.waitForURL(fixture.url + "/");
  const current = await me(p);
  assert.equal(current.status, 200);
  assert.equal(current.body.user.id, fixture.operator_account.user_id);
  assert.equal(current.body.user.operator, false);
  const projects = await p.evaluate(async () => {
    const r = await fetch("/v1/projects");
    return r.json();
  });
  assert.equal(projects.items.length, 0);
  await control(true);
  assert.equal(
    (await me(p)).status,
    200,
    "opening signup revoked existing login",
  );
  const signup = await pageFor();
  await signup.goto(fixture.url);
  await signup.getByLabel("Язык", { exact: true }).selectOption("en");
  await signup
    .getByRole("link", { name: "Create account", exact: true })
    .click();
  await signup.locator("#kc-register-form").waitFor();
  assert.equal(await signup.locator("html").getAttribute("lang"), "en");
  await checkLocalTheme(signup, "registration-desktop");
  const username = "signup." + Date.now();
  for (const [id, value] of Object.entries({
    username,
    email: username + "@example.invalid",
    firstName: "New",
    lastName: "Engineer",
    password: "synthetic-signup-password-2026",
    "password-confirm": "synthetic-signup-password-2026",
  }))
    await signup.locator("#" + id).fill(value);
  await signup.locator("#kc-register-form [type=submit]").click();
  await signup.waitForURL(fixture.url + "/");
  assert.equal(await signup.locator("html").getAttribute("lang"), "en");
  await signup.getByRole("link", { name: "Settings", exact: true }).waitFor();
  const created = await me(signup);
  assert.equal(created.status, 200);
  assert.equal(created.body.user.operator, false);
  const leaked = await signup.evaluate(async (project) => {
    const r = await fetch(`/v1/projects/${project}`);
    return r.status;
  }, fixture.project);
  assert.equal(leaked, 403);
  const pending = await pageFor();
  await pending.goto(fixture.url + "/v1/auth/register");
  await pending.locator("#kc-register-form").waitFor();
  await pending.setViewportSize({ width: 390, height: 844 });
  await checkLocalTheme(pending, "registration-mobile");
  await control(false);
  assert.equal(
    (await me(p)).status,
    200,
    "closing signup revoked existing login",
  );
  for (const [id, value] of Object.entries({
    username: "blocked." + Date.now(),
    email: "blocked@example.invalid",
    firstName: "Blocked",
    lastName: "Engineer",
    password: "synthetic-blocked-password-2026",
    "password-confirm": "synthetic-blocked-password-2026",
  }))
    await pending.locator("#" + id).fill(value);
  await pending.locator("#kc-register-form [type=submit]").click();
  assert.equal(
    (await me(pending)).status,
    401,
    "disabled provider registration published account",
  );
  const closed = await pageFor();
  await closed.setViewportSize({ width: 390, height: 844 });
  await closed.goto(fixture.url);
  await checkLocalTheme(closed, "entry-mobile");
  assert.equal(
    await closed
      .getByRole("link", { name: "Создать аккаунт", exact: true })
      .count(),
    0,
  );
  assert.equal((await fetch(fixture.url + "/v1/auth/register")).status, 403);
  assert.deepEqual(faults, []);
  console.log(
    "Actual Keycloak/PostgreSQL: hidden-password operator creation with signup disabled; password error/recovery; same-origin Tailwind login/registration; exact-subject account without project/operator rights; runtime env disable and stale form rejection passed.",
  );
} finally {
  for (const c of contexts) await c.close();
  await browser.close();
}
