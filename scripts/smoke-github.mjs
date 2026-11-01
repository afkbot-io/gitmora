/** GitHub URL lookup, explicit catalog identity and public/token connection UI over signed real API/DB. */
import assert from "node:assert/strict";
import { chromium, webkit, firefox, devices } from "playwright";
import { signInAs, clickUI } from "./browser-auth.mjs";
import { auditUI } from "./audit-ui.mjs";
const input = JSON.parse(
  await new Promise((resolve) => {
    let s = "";
    process.stdin.on("data", (x) => (s += x));
    process.stdin.on("end", () => resolve(s));
  }),
);
const engine = { chromium, webkit, firefox }[
  process.env.GITMORA_BROWSER ?? "chromium"
];
assert.ok(engine);
const mobile = process.env.GITMORA_MOBILE_DEVICE === "1";
const browser = await engine.launch();
const context = await browser.newContext(
  mobile
    ? devices[engine === webkit ? "iPhone 13" : "Pixel 7"]
    : { viewport: { width: 1440, height: 1000 } },
);
const page = await context.newPage();
try {
  await signInAs(page, input);
  await page.locator("#project").selectOption(input.project);
  await clickUI(page, "Репозитории");
  await clickUI(page, "Добавить репозиторий");
  await page
    .getByLabel("Хостинг репозитория", { exact: true })
    .selectOption("github");
  await page
    .getByLabel("Ссылка на репозиторий GitHub", { exact: true })
    .fill("https://github.com/example/gitmora-fixture");
  assert.equal(
    await page
      .getByLabel("Числовой ID проекта GitLab", { exact: true })
      .count(),
    0,
  );
  await clickUI(page, "Зарегистрировать репозиторий");
  const card = page
    .locator(".settings-resource")
    .filter({ hasText: "https://github.com/example/gitmora-fixture" });
  await card.waitFor();
  assert.match(await card.innerText(), /99/);
  const metadata = await page.evaluate(async (project) => {
    const r = await fetch(`/v1/projects/${project}/repositories`);
    return r.json();
  }, input.project);
  const repository = metadata.items.find((x) => x.provider === "github");
  assert.ok(repository);
  assert.equal(
    repository.provider_url,
    "https://github.com/example/gitmora-fixture",
  );
  assert.equal(repository.provider_repository_id, 99);
  assert.equal(repository.gitlab_url, undefined);
  await auditUI(page, "github-catalog", {
    enabled: true,
    ...(mobile ? { widths: [390] } : {}),
  });
  await clickUI(page, "Настройки проекта");
  await page
    .getByLabel("Репозиторий интеграции", { exact: true })
    .selectOption(repository.id);
  await page.getByRole("heading", { name: "GitHub", exact: true }).waitFor();
  await clickUI(page, "Настроить GitHub");
  assert.equal(
    await page
      .getByLabel("Читать публичный репозиторий без токена", { exact: true })
      .isChecked(),
    true,
  );
  assert.equal(
    await page.getByLabel("API-ключ GitHub", { exact: true }).count(),
    0,
  );
  assert.equal(
    await page.getByLabel("Режим подключения GitLab", { exact: true }).count(),
    0,
  );
  await clickUI(page, "Проверить и сохранить GitHub");
  await page
    .getByRole("button", { name: "Запросить сверку GitHub", exact: true })
    .waitFor();
  let status = await page.evaluate(
    async (id) => (await fetch(`/v1/repositories/${id}/forge`)).json(),
    repository.id,
  );
  assert.equal(status.provider, "github");
  assert.equal(status.anonymous, true);
  assert.equal(status.webhook_mode, "disabled");
  assert.equal(status.streams.length, 2);
  await auditUI(page, "github-public-connection", {
    enabled: true,
    ...(mobile ? { widths: [390] } : {}),
  });
  await clickUI(page, "Изменить соединение GitHub");
  assert.equal(
    await page
      .getByLabel("Читать публичный репозиторий без токена", { exact: true })
      .isChecked(),
    true,
  );
  await page
    .getByLabel("Читать публичный репозиторий без токена", { exact: true })
    .uncheck();
  await page
    .getByLabel("API-ключ GitHub", { exact: true })
    .fill("synthetic-github-read-token");
  await clickUI(page, "Проверить и сохранить GitHub");
  await page
    .getByRole("button", { name: "Запросить сверку GitHub", exact: true })
    .waitFor();
  await page
    .getByLabel("API-ключ GitHub", { exact: true })
    .waitFor({ state: "hidden" });
  assert.equal(
    await page.getByLabel("API-ключ GitHub", { exact: true }).count(),
    0,
  );
  status = await page.evaluate(
    async (id) => (await fetch(`/v1/repositories/${id}/forge`)).json(),
    repository.id,
  );
  assert.equal(status.anonymous, undefined);
  await clickUI(page, "Изменить соединение GitHub");
  assert.equal(
    await page
      .getByLabel("Читать публичный репозиторий без токена", { exact: true })
      .isChecked(),
    false,
  );
  await clickUI(page, "Отменить настройку");
  await clickUI(page, "Настройки");
  await page.getByLabel("Язык", { exact: true }).selectOption("en");
  await clickUI(page, "Project settings");
  await page
    .getByLabel("Integration repository", { exact: true })
    .selectOption(repository.id);
  await page
    .getByRole("button", { name: "Request GitHub sync", exact: true })
    .waitFor();
  await auditUI(page, "github-connection-en", {
    enabled: true,
    ...(mobile ? { widths: [390] } : {}),
  });
  console.log(
    JSON.stringify({
      result: "passed",
      provider: "github",
      signedCookieLogin: true,
      urlLookup: true,
      noGitLabMasquerading: true,
      publicAndTokenModes: true,
      secretClearing: true,
      locale: true,
      browser: engine.name(),
      mobile,
    }),
  );
} finally {
  await context.close();
  await browser.close();
}
