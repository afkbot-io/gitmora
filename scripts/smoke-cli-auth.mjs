import { auditUI } from "./audit-ui.mjs";
import { verifySavedMCP } from "./native-mcp-verification.mjs";
/** Real signed browser consent; optional native CLI uses only the real system secret store. */
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createHash, randomBytes, randomUUID } from "node:crypto";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { chromium } from "playwright";

let input = "";
for await (const chunk of process.stdin) input += chunk;
const fixture = JSON.parse(input);
input = "";
const binary = resolve(fixture.cli_binary ?? "bin/workgraph");
const env = {
  ...process.env,
  GIT_CONFIG_GLOBAL: "/dev/null",
  GIT_CONFIG_NOSYSTEM: "1",
  GIT_AUTHOR_NAME: "Verification",
  GIT_AUTHOR_EMAIL: "cli@example.invalid",
  GIT_COMMITTER_NAME: "Verification",
  GIT_COMMITTER_EMAIL: "cli@example.invalid",
};
assert(
  !Object.hasOwn(env, "WORKGRAPH_TOKEN"),
  "native delivery must have no environment bearer",
);
const children = new Set();
function launch(args, cwd, value) {
  const child = spawn(binary, args, {
    env,
    cwd,
    stdio: ["pipe", "pipe", "pipe"],
  });
  children.add(child);
  let out = "",
    diagnostic = "";
  let promptResolve;
  const prompt = new Promise((resolve) => {
    promptResolve = resolve;
  });
  child.stdout.on("data", (chunk) => {
    out += chunk;
  });
  child.stderr.on("data", (chunk) => {
    diagnostic += chunk;
    for (const line of diagnostic.split("\n")) {
      try {
        const v = JSON.parse(line);
        if (v.verification_uri && v.code) promptResolve(v);
      } catch {
        /* Static CLI diagnostics are not printed by this harness. */
      }
    }
  });
  child.stdin.end(value === undefined ? undefined : JSON.stringify(value));
  const done = new Promise((resolve, reject) => {
    child.on("error", () =>
      reject(new Error("Could not launch native verification CLI")),
    );
    child.on("close", (code) => {
      children.delete(child);
      resolve({ code, out, diagnostic });
    });
  });
  return {
    child,
    done,
    prompt: Promise.race([
      prompt,
      done.then(() => {
        throw new Error("CLI exited before public confirmation prompt");
      }),
    ]),
  };
}
async function cli(args, cwd, value, expected = 0) {
  const execution = launch(args, cwd, value);
  // A status/help command intentionally has no confirmation prompt.
  execution.prompt.catch(() => {});
  const result = await execution.done;
  assert(result.code === expected, "Unexpected native CLI status");
  assert(
    !/\b(?:wg_|wcd_|wcp_)[A-Za-z0-9_-]{43}\b/.test(
      result.out + result.diagnostic,
    ),
    "CLI printed private material",
  );
  return result.out.trim() ? JSON.parse(result.out) : null;
}
async function api(path, body, key = fixture.admin) {
  const response = await fetch(fixture.url + path, {
    method: body === undefined ? "GET" : "POST",
    redirect: "manual",
    headers: {
      Authorization: "Bearer " + key,
      "Content-Type": "application/json",
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  return { status: response.status, value: await response.json() };
}
async function publicAPI(path, body) {
  const response = await fetch(fixture.url + path, {
    method: "POST",
    redirect: "manual",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  return { status: response.status, value: await response.json() };
}
const hash = (v) => createHash("sha256").update(v).digest("hex");
function device() {
  const key = "wg_" + randomBytes(32).toString("base64url"),
    d = "wcd_" + randomBytes(32).toString("base64url"),
    proof = "wcp_" + randomBytes(32).toString("base64url");
  const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  const raw = [...randomBytes(8)].map((v) => alphabet[v & 31]).join("");
  const code = raw.slice(0, 4) + "-" + raw.slice(4);
  return {
    key,
    code,
    proofs: { id: randomUUID(), device_secret: d, proof },
    intent: {
      device_hash: hash(d),
      proof_hash: hash(proof),
      credential_hash: hash(key),
      code_hash: hash("cli-enrollment-v1\0code\0" + code),
      label: "Protocol-only terminal",
      scopes: [
        "capture:read",
        "capture:write",
        "credentials:manage",
        "directory:read",
      ],
      ttl_seconds: 172800,
    },
  };
}
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({
  viewport: { width: 1440, height: 1000 },
});
const page = await context.newPage();
const faults = [];
page.on("pageerror", () => faults.push("browser exception"));
page.on("console", (v) => {
  if (
    /violates.*Content Security Policy|Refused to.*(?:script|style|font)/i.test(
      v.text(),
    )
  )
    faults.push("CSP failure");
});
let root, nativeRun;
async function keycloakLogin() {
  if (!fixture.keycloak) return;
  await page.locator("#username").fill("gitmora.admin");
  await page.locator("#password").fill(fixture.keycloak.password);
  await page.locator("#kc-login").click();
  await auditUI(page, "identity-update-password");
  await page.locator("#password-new").fill(fixture.keycloak.replacement);
  await page.locator("#password-confirm").fill(fixture.keycloak.replacement);
  await page.locator("#kc-passwd-update-form [type=submit]").click();
  await auditUI(page, "identity-update-profile");
  await page.locator("#email").fill("gitmora-admin@example.invalid");
  await page.locator("#kc-update-profile-form [type=submit]").click();
}
try {
  if (fixture.cli_release) {
    const observed = await cli(["version"]);
    assert.deepEqual(
      { version: observed.version, revision: observed.revision },
      fixture.cli_release,
    );
    console.log(JSON.stringify({ native_release: observed }));
  }
  await page.goto(fixture.url);
  await page.getByRole("link", { name: "Войти", exact: true }).click();
  if (fixture.keycloak) {
    await keycloakLogin();
    await page.getByRole("link", { name: "Войти", exact: true }).waitFor();
    const denied = await context.request.get(fixture.url + "/v1/auth/session");
    assert(
      denied.status() === 401,
      "Unbound actual provider identity acquired a local session",
    );
    const bound = await api("/v1/admin/identities", {
      request_id: randomUUID(),
      id: fixture.binding,
      user_id: fixture.user,
      subject: fixture.keycloak.subject,
    });
    assert(bound.status === 201, "Explicit actual-provider binding failed");
    await page.getByRole("link", { name: "Войти", exact: true }).click();
  }
  await page.locator(".sidebar").waitFor();
  if (fixture.same_origin) {
    assert(
      new URL(fixture.issuer).origin === new URL(fixture.url).origin,
      "Provider moved to another origin",
    );
    for (const cookie of await context.cookies()) {
      if (
        !cookie.name.startsWith("workgraph_") &&
        !cookie.name.startsWith("__Host-workgraph_")
      ) {
        assert(
          cookie.path === "/auth" || cookie.path.startsWith("/auth/"),
          "Provider cookie escaped /auth",
        );
      }
    }
  }
  const before = (await context.cookies()).find(
    (v) => v.name === "workgraph_session_dev",
  );
  assert(before, "signed browser session missing");
  let prompt, proto;
  if (fixture.native) {
    nativeRun = launch([
      "auth",
      "login",
      "--server",
      fixture.url,
      "--label",
      "Native verification terminal",
      "--ttl",
      "48h",
      "--no-browser",
    ]);
    prompt = await nativeRun.prompt;
    const pending = await cli(["auth", "status", "--server", fixture.url]);
    assert(
      pending.state === "pending",
      "pending state not visible to a new CLI process",
    );
    await cli(
      ["auth", "login", "--server", fixture.url, "--no-browser"],
      undefined,
      undefined,
      1,
    );
    // Restart both the actual native process and API while the protected intent remains pending.
    nativeRun.child.kill("SIGINT");
    await nativeRun.done;
    const restart = await fetch(fixture.control_url + "/restart", {
      method: "POST",
      headers: { "X-Verification-Nonce": fixture.control_nonce },
      body: "{}",
    });
    assert(
      restart.status === 204,
      "test coordinator did not restart the actual API",
    );
    const firstPrompt = prompt;
    nativeRun = launch([
      "auth",
      "login",
      "--server",
      fixture.url,
      "--no-browser",
    ]);
    prompt = await nativeRun.prompt;
    assert(
      prompt.code === firstPrompt.code &&
        prompt.expires_at === firstPrompt.expires_at,
      "process/API restart changed the protected pending intent/deadline",
    );
  } else {
    proto = device();
    const prepared = await publicAPI("/v1/auth/cli/prepare", {
      id: proto.proofs.id,
      ...proto.intent,
    });
    assert(prepared.status === 202, "hash registration failed");
    prompt = {
      code: proto.code,
      verification_uri: prepared.value.verification_uri + "?code=" + proto.code,
    };
    assert(
      (await api("/v1/me", undefined, proto.key)).status === 401,
      "unapproved key became active",
    );
  }
  assert(
    prompt.verification_uri ===
      fixture.url + "/v1/auth/cli/login?code=" + prompt.code,
    "CLI verification destination changed",
  );
  await page.goto(prompt.verification_uri);
  await page
    .getByRole("heading", { name: "Подключить терминал", exact: true })
    .waitFor();
  await page
    .getByRole("button", { name: "Разрешить вход CLI…", exact: true })
    .waitFor();
  assert(
    (await page.locator(".cli-human-code").textContent()).trim() ===
      prompt.code,
    "browser code differs from originating terminal",
  );
  const after = (await context.cookies()).find(
    (v) => v.name === "workgraph_session_dev",
  );
  assert(
    after?.value === before.value && after.expires === before.expires,
    "CLI reauthentication rotated/extended the existing browser session",
  );
  await auditUI(page, "cli-consent");
  await mkdir("output/playwright", { recursive: true });
  await page.screenshot({
    path: "output/playwright/workgraph-cli-consent-desktop.png",
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
    "consent mobile overflow",
  );
  await page.screenshot({
    path: "output/playwright/workgraph-cli-consent-mobile.png",
    fullPage: true,
  });
  await page
    .getByRole("button", { name: "Разрешить вход CLI…", exact: true })
    .click();
  await page.getByRole("dialog").waitFor();
  assert(
    await page
      .getByRole("button", { name: "Отмена", exact: true })
      .evaluate((v) => v === document.activeElement),
    "consent dialog did not focus cancellation",
  );
  await page.keyboard.press("Escape");
  await page.getByRole("dialog").waitFor({ state: "hidden" });
  // The backend commits approval, but the browser loses its acknowledgement and retries exact intent.
  let acknowledgementLost = false;
  await page.route("**/v1/auth/cli/decide", async (route) => {
    if (!acknowledgementLost) {
      acknowledgementLost = true;
      await route.fetch();
      await route.abort("connectionfailed");
    } else await route.continue();
  });
  await page
    .getByRole("button", { name: "Разрешить вход CLI…", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Подтвердить вход CLI", exact: true })
    .click();
  await page.getByRole("alert").waitFor();
  await page
    .getByRole("button", { name: "Подтвердить вход CLI", exact: true })
    .click();
  await page.locator(".cli-receipt").waitFor();
  let account;
  if (fixture.native) {
    const result = await nativeRun.done;
    assert(
      result.code === 0 &&
        !/wg_|wcd_|wcp_/.test(result.out + result.diagnostic),
      "native activation failed or leaked a key",
    );
    account = JSON.parse(result.out).account;
  } else {
    const polled = await publicAPI("/v1/auth/cli/poll", proto.proofs);
    assert(
      polled.status === 200 && polled.value.status === "approved",
      "proof poll not approved",
    );
    const me = await api("/v1/me", undefined, proto.key);
    assert(me.status === 200, "approved candidate not authenticated");
    account = { token_id: me.value.token_id };
  }
  await page
    .getByRole("button", { name: "Вернуться в Gitmora", exact: true })
    .click();
  await page.getByRole("button", { name: "Выйти", exact: true }).click();
  await page.getByRole("link", { name: "Войти", exact: true }).waitFor();
  if (fixture.native) {
    assert(
      (await cli(["auth", "status", "--server", fixture.url])).state ===
        "active",
      "browser logout revoked independent root",
    );
    if (fixture.context_cli) {
      const projects = await cli([
        "api",
        "get",
        "/v1/projects",
        "--server",
        fixture.url,
      ]);
      assert(
        projects.status === 200 && projects.body.items.length > 0,
        "Saved account did not authorize native API",
      );
      await verifySavedMCP(binary, env, fixture.url, children);
      console.log(
        "Native API and stdio MCP loaded the enrolled OS-store account without environment or configuration secrets.",
      );
    }
    root = await mkdtemp(join(tmpdir(), "workgraph-native-auth-"));
    const remote = join(root, "remote.git"),
      work = join(root, "work");
    await mkdir(work);
    async function git(args, cwd = work) {
      const child = spawn("git", args, {
        env,
        cwd,
        stdio: ["ignore", "ignore", "pipe"],
      });
      children.add(child);
      const code = await new Promise((resolve) => child.on("close", resolve));
      children.delete(child);
      assert(code === 0, "Actual native-auth Git operation failed");
    }
    await git(["init", "--bare", remote], root);
    await git(["init", "-b", "main"]);
    await git(["remote", "add", "origin", remote]);
    await writeFile(join(work, "base.txt"), "base\n");
    await git(["add", "."]);
    await git(["commit", "-m", "base"]);
    await cli(
      ["init", "--server", fixture.url, "--repository-id", fixture.repository],
      work,
    );
    await writeFile(join(work, "native.txt"), "Native store delivery\n");
    const capture = await cli(["capture"], work, {
      schema_version: 1,
      source_session_id: "native-auth",
      event_id: randomUUID(),
      agent: { name: "local-verification", model: "synthetic" },
      messages: [
        { role: "assistant", text: "Native secret-store hook evidence" },
      ],
      changed_files: ["native.txt"],
    });
    await git(["add", "."]);
    await git(["commit", "-m", "native capture"]);
    await git(["push", "origin", "main"]);
    assert(
      (await cli(["status"], work)).queue.pending === 0,
      "native push did not drain queued evidence",
    );
    const delivered = await api(
      `/v1/repositories/${fixture.repository}/sessions/${capture.session_id}`,
    );
    assert(
      delivered.status === 200 &&
        JSON.stringify(delivered.value).includes(
          "Native secret-store hook evidence",
        ),
      "push was not delivered to the real API",
    );
    await cli(["auth", "logout", "--server", fixture.url]);
    const tokens = await api("/v1/me/tokens");
    assert(
      tokens.status === 200 &&
        tokens.value.items.find((v) => v.id === account.token_id)?.revoked_at,
      "native logout did not durably revoke root",
    );
    await writeFile(join(work, "native.txt"), "Queued without credential\n");
    await cli(["capture"], work, {
      schema_version: 1,
      source_session_id: "after-logout",
      event_id: randomUUID(),
      agent: { name: "local-verification", model: "synthetic" },
      messages: [
        { role: "assistant", text: "Must remain queued after logout" },
      ],
      changed_files: ["native.txt"],
    });
    await git(["add", "."]);
    await git(["commit", "-m", "queued after logout"]);
    await git(["push", "origin", "main"]);
    assert(
      (await cli(["status"], work)).queue.pending > 0,
      "credential-free push discarded pending evidence",
    );
    nativeRun = launch([
      "auth",
      "login",
      "--server",
      fixture.url,
      "--no-browser",
    ]);
    const cancelledPrompt = await nativeRun.prompt;
    nativeRun.child.kill("SIGINT");
    await nativeRun.done;
    await cli(["auth", "logout", "--server", fixture.url]);
    await page.goto(cancelledPrompt.verification_uri);
    await page
      .getByText("Запрос отменён в терминале. Ключ не создан.", { exact: true })
      .waitFor();
    assert(
      (await page
        .getByRole("button", { name: "Разрешить вход CLI…", exact: true })
        .count()) === 0,
      "cancelled device still approvable",
    );
  } else {
    assert(
      (await api("/v1/me", undefined, proto.key)).status === 200,
      "browser logout revoked protocol root",
    );
    const revoke = await api(
      `/v1/me/tokens/${account.token_id}/revoke`,
      { request_id: randomUUID() },
      proto.key,
    );
    assert(
      revoke.status === 200 &&
        (await api("/v1/me", undefined, proto.key)).status === 401,
      "explicit root revocation failed",
    );
  }
  assert(faults.length === 0, "Browser runtime/CSP failures");
  if (fixture.keycloak)
    console.log(
      "Actual Keycloak 26.8.1: forced initial password replacement, unbound-user denial, explicit subject binding and real signed browser/CLI flow passed.",
    );
  console.log(
    fixture.native
      ? "Actual native CLI + macOS/Linux OS store + signed browser + PostgreSQL + ordinary Git push/logout/cancellation passed."
      : "Actual signed Chromium CLI consent, session preservation, mobile/dialog, hash-only registration, independent root and revocation passed.",
  );
} catch (error) {
  if (fixture.keycloak) {
    await mkdir("output/playwright", { recursive: true });
    await page
      .screenshot({
        path: "output/playwright/keycloak-failure.png",
        fullPage: true,
      })
      .catch(() => {});
    const address = new URL(page.url());
    await writeFile(
      "build/keycloak-page-diagnosis.json",
      JSON.stringify({
        origin: address.origin,
        path: address.pathname,
        text: (await page.locator("body").innerText()).slice(0, 8000),
      }),
    ).catch(() => {});
  }
  throw error;
} finally {
  await Promise.all(
    [...children].map(
      (child) =>
        new Promise((resolve) => {
          const timer = setTimeout(() => child.kill("SIGKILL"), 6000);
          child.once("close", () => {
            clearTimeout(timer);
            resolve();
          });
          child.kill("SIGTERM");
        }),
    ),
  );
  await browser.close();
  if (fixture.native) {
    try {
      await cli(["auth", "logout", "--server", fixture.url, "--local-only"]);
    } catch {
      /* The synthetic schema is removed by the fixture owner. */
    }
  }
  if (root) await rm(root, { recursive: true, force: true });
}
