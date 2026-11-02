/** Real browser/API/PostgreSQL and MCP search; signed local identity and synthetic provider observations. */
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { randomUUID } from "node:crypto";
import { chromium, webkit, firefox, devices } from "playwright";
import { signInAs, clickUI } from "./browser-auth.mjs";
import { auditUI } from "./audit-ui.mjs";
import { watchBrowserPolicy } from "./browser-policy.mjs";
let raw = "";
for await (const chunk of process.stdin) raw += chunk;
const fixture = JSON.parse(raw);
raw = "";
const engine = { chromium, webkit, firefox }[
  process.env.GITMORA_BROWSER ?? "chromium"
];
assert(engine);
const browser = await engine.launch({ headless: true });
const mobile = process.env.GITMORA_MOBILE_DEVICE === "1";
const context = await browser.newContext({
  viewport: { width: 1440, height: 1000 },
  ...(mobile ? devices[engine === webkit ? "iPhone 13" : "Pixel 7"] : {}),
});
const page = await context.newPage();
const faults = [];
page.on("pageerror", (error) => faults.push(error.message));
const policy = await watchBrowserPolicy(page);
await mkdir("output/playwright", { recursive: true });
async function api(
  path,
  body,
  token = fixture.admin,
  method = body ? "POST" : "GET",
) {
  const response = await fetch(fixture.url + path, {
    method,
    headers: {
      Authorization: "Bearer " + token,
      "Content-Type": "application/json",
    },
    ...(body ? { body: JSON.stringify(body) } : {}),
  });
  return {
    status: response.status,
    value: response.status === 204 ? null : await response.json(),
  };
}
async function search(text) {
  await page.getByRole("searchbox").fill(text);
  const next = page.waitForResponse(
    (response) =>
      response.url().endsWith("/v1/context/search") &&
      response.request().method() === "POST",
  );
  await page.getByRole("button", { name: "Найти", exact: true }).click();
  const response = await next;
  assert.equal(response.status(), 200);
  return response.json();
}
async function rows(count) {
  await page.waitForFunction(
    (n) => document.querySelectorAll(".result-row").length === n,
    count,
  );
}
try {
  await signInAs(page, fixture, fixture.read_token);
  await page.waitForFunction(
    (id) => document.querySelector("#project")?.value === id,
    fixture.project,
  );
  await page.getByRole("searchbox").waitFor();
  const first = await search("SearchScenarioMarker");
  assert.equal(first.items.length, 20);
  assert(first.next);
  await rows(20);
  const ids = new Set(first.items.map((hit) => hit.id));
  const more = page.getByRole("button", { name: "Показать ещё", exact: true });
  await more.scrollIntoViewIfNeeded();
  const before = await page
    .locator(".result-row")
    .nth(19)
    .evaluate((element) => element.getBoundingClientRect().top);
  const response = page.waitForResponse(
    (r) =>
      r.url().endsWith("/v1/context/search") &&
      Boolean(r.request().postDataJSON()?.after),
  );
  await more.click();
  const second = await (await response).json();
  await rows(40);
  assert(second.items.every((hit) => !ids.has(hit.id)));
  second.items.forEach((hit) => ids.add(hit.id));
  const after = await page
    .locator(".result-row")
    .nth(19)
    .evaluate((element) => element.getBoundingClientRect().top);
  assert(
    Math.abs(after - before) < 5,
    `Append changed reading position: ${before} to ${after}`,
  );
  await more.click();
  await rows(45);
  assert.equal(await more.count(), 0);
  await page.locator(".result-row").first().click();
  await page.locator(".source-quote").waitFor();
  await page.locator(".context-view > form details summary").click();
  await page.getByLabel("Модель", { exact: true }).fill("model-alpha");
  const filtered = await search("SearchScenarioMarker");
  assert(
    filtered.items.every(
      (hit) =>
        hit.metadata.model === "model-alpha" &&
        hit.metadata.model_basis === "message_metadata",
    ),
  );
  await rows(20);
  await page.getByRole("button", { name: "Показать ещё", exact: true }).click();
  await rows(25);
  await page.getByLabel("Тип сообщения", { exact: true }).selectOption("tool");
  const tools = await search("SearchScenarioMarker");
  assert.equal(tools.items.length, 9);
  assert(
    tools.items.every(
      (hit) =>
        hit.metadata.role === "tool" &&
        hit.metadata.relation === "tool_reported",
    ),
  );
  await auditUI(page, "search-model-filters");
  await page
    .getByRole("button", { name: "Сбросить фильтры", exact: true })
    .click();
  await page
    .getByRole("combobox", { name: "Репозиторий", exact: true })
    .selectOption(fixture.repository);
  const scoped = await search("AAAAAAA");
  assert.equal(scoped.resolved_commit_sha, "a".repeat(40));
  assert(
    scoped.items.every(
      (hit) =>
        hit.metadata.relation !== "message" ||
        hit.metadata.model === "model-alpha",
    ),
  );
  const session = await search(fixture.search_session);
  assert(
    session.items.every(
      (hit) => hit.source.session_id === fixture.search_session,
    ),
  );
  await page
    .getByRole("combobox", { name: "Источник", exact: true })
    .selectOption("code");
  const code = await search("SearchScenarioCode");
  assert.equal(code.items.length, 1);
  await page.locator(".result-row").click();
  await page.locator(".source-quote").waitFor();
  const provenance = page.locator(".inspector details").filter({
    has: page.locator("summary", { hasText: "Связанные изменения" }),
  });
  await provenance.locator("summary").click();
  await provenance.getByRole("button").first().waitFor();
  assert.equal(await provenance.getByRole("button").count(), 9);
  assert.match(await provenance.textContent(), /model-alpha/);
  assert.doesNotMatch(await provenance.textContent(), /model-beta/);
  await auditUI(page, "search-code-provenance");
  await page.screenshot({
    path: `output/playwright/search-provenance-${process.env.GITMORA_BROWSER ?? "chromium"}-${mobile ? "mobile" : "desktop"}.png`,
    fullPage: false,
  });
  await provenance.getByRole("button").first().click();
  await page
    .getByRole("region", { name: "Запись сессии", exact: true })
    .waitFor();
  assert.match(page.url(), /message=\d+/);
  assert.match(page.url(), /revision=1/);
  // Same new query fields work through the actual HTTP MCP endpoint.
  let sequence = 0;
  async function mcp(query) {
    const response = await fetch(fixture.url + "/mcp", {
      method: "POST",
      headers: {
        Authorization: "Bearer " + fixture.read_token,
        "Content-Type": "application/json",
        Accept: "application/json, text/event-stream",
        "MCP-Protocol-Version": "2026-07-28",
        "Mcp-Method": "tools/call",
        "Mcp-Name": "context.search",
      },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: ++sequence,
        method: "tools/call",
        params: {
          name: "context.search",
          arguments: query,
          _meta: {
            "io.modelcontextprotocol/protocolVersion": "2026-07-28",
            "io.modelcontextprotocol/clientInfo": {
              name: "search-case",
              version: "1",
            },
            "io.modelcontextprotocol/clientCapabilities": {},
          },
        },
      }),
    });
    assert.equal(response.status, 200);
    return (await response.json()).result;
  }
  const query = {
    project_id: fixture.project,
    repository_id: fixture.repository,
    text: "SearchScenarioMarker",
    model: "model-beta",
    limit: 7,
  };
  const one = await mcp(query);
  assert(!one.isError);
  const data = one.structuredContent.result;
  assert.equal(data.items.length, 7);
  assert(data.items.every((hit) => hit.metadata.model === "model-beta"));
  const two = await mcp({ ...query, after: data.next });
  assert(!two.isError);
  assert(
    two.structuredContent.result.items.every(
      (hit) => !data.items.some((prior) => prior.id === hit.id),
    ),
  );
  const wrong = await mcp({ ...query, model: "model-alpha", after: data.next });
  assert(wrong.isError);
  // Membership revocation invalidates both new search pages and exact evidence reads.
  const members = await api(`/v1/projects/${fixture.project}/members`);
  const member = members.value.items.find(
    (value) => value.user_id === fixture.user,
  );
  assert(member);
  const removed = await api(
    `/v1/projects/${fixture.project}/members/${fixture.user}`,
    { request_id: randomUUID(), expected_version: member.version },
    fixture.admin,
    "DELETE",
  );
  assert.equal(removed.status, 200);
  assert((await mcp(query)).isError);
  assert.equal(
    (
      await api(
        "/v1/context/expand",
        { evidence_id: code.items[0].id },
        fixture.read_token,
      )
    ).status,
    404,
  );
  assert.deepEqual(faults, []);
  assert.equal(policy.unexpected().length, 0);
  console.log(
    "Search acceptance passed: 20/40/45 append, stable reading position, per-message models, role/path scope, SHA prefix, session UUID, exact source navigation, code provenance, MCP pages and live membership revocation.",
  );
} catch (error) {
  await page
    .screenshot({
      path: "output/playwright/search-failure.png",
      fullPage: false,
    })
    .catch(() => {});
  await writeFile(
    "output/playwright/search-failure.txt",
    await page
      .locator("body")
      .innerText()
      .catch(() => "unavailable"),
  );
  throw error;
} finally {
  await browser.close();
}
