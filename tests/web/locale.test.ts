import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { runInNewContext } from "node:vm";
import { t, loginLocalePath } from "../../web/src/domain/locale.js";

function browser(
  saved: string | null = null,
  account = false,
  lang = "ru",
  blocked = false,
) {
  const window = new EventTarget();
  const root = { lang, hasAttribute: () => account };
  const storage = new Map(saved ? [["gitmora.locale", saved]] : []);
  const localStorage = {
    getItem(key: string) {
      if (blocked) throw new Error("Blocked");
      return storage.get(key) ?? null;
    },
    setItem(key: string, value: string) {
      if (blocked) throw new Error("Blocked");
      storage.set(key, value);
    },
  };
  runInNewContext(readFileSync("web/public/assets/locale.js", "utf8"), {
    window,
    document: { documentElement: root },
    localStorage,
    Event,
  });
  return {
    root,
    storage,
    select(value: string) {
      window.dispatchEvent(
        Object.assign(new Event("gitmora:locale-select"), { detail: value }),
      );
    },
    otherTab(value: string | null) {
      window.dispatchEvent(
        Object.assign(new Event("storage"), {
          key: "gitmora.locale",
          newValue: value,
        }),
      );
    },
  };
}

test("language persists before render, synchronizes tabs and rejects unsupported values", () => {
  const b = browser("en");
  assert.equal(b.root.lang, "en");
  b.select("ru");
  assert.equal(b.root.lang, "ru");
  assert.equal(b.storage.get("gitmora.locale"), "ru");
  b.select("javascript:alert(1)");
  assert.equal(b.root.lang, "ru");
  b.otherTab("en");
  assert.equal(b.root.lang, "en");
  b.otherTab(null);
  assert.equal(b.root.lang, "ru");
  assert.equal(browser("unknown").root.lang, "ru");
});
test("provider-rendered language wins and returns to the application; denied storage stays usable", () => {
  assert.equal(browser("ru", true, "en").storage.get("gitmora.locale"), "en");
  const b = browser(null, false, "ru", true);
  b.select("en");
  assert.equal(b.root.lang, "en");
});
test("authored messages localize and interpolate without interpreting values as message keys", () => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, "document");
  try {
    Object.defineProperty(globalThis, "document", {
      configurable: true,
      value: { documentElement: { lang: "en" } },
    });
    assert.equal(t("Личные настройки"), "Personal settings");
    assert.equal(t("Удалить {0}?", "Настройки"), "Delete Настройки?");
    assert.equal(t("Unlisted source text"), "Unlisted source text");
  } finally {
    if (previous) Object.defineProperty(globalThis, "document", previous);
    else Reflect.deleteProperty(globalThis, "document");
  }
});
test("account language does not change the local sign-in or CLI destination", () => {
  assert.equal(
    loginLocalePath("/v1/auth/login", "en"),
    "/v1/auth/login?locale=en",
  );
  assert.equal(
    loginLocalePath("/v1/auth/cli/login?code=ABCD-EFGH", "ru"),
    "/v1/auth/cli/login?code=ABCD-EFGH&locale=ru",
  );
});

test("validation messages retain their authored identity until displayed in the current language", async () => {
  const { ApiError } = await import("../../web/src/transport/api.js");
  const previous = Object.getOwnPropertyDescriptor(globalThis, "document");
  try {
    const root = { lang: "en" };
    Object.defineProperty(globalThis, "document", {
      configurable: true,
      value: { documentElement: root },
    });
    const error = new ApiError(403);
    assert.equal(
      t(error.message),
      "You do not have permission for this action.",
    );
    root.lang = "ru";
    assert.equal(t(error.message), "Недостаточно прав для этого действия.");
  } finally {
    if (previous) Object.defineProperty(globalThis, "document", previous);
    else Reflect.deleteProperty(globalThis, "document");
  }
});
