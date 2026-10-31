import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { test } from "node:test";
import { runInNewContext } from "node:vm";

function browser(saved: string | null = null, dark = false, blocked = false) {
  const window = new EventTarget();
  const media = Object.assign(new EventTarget(), { matches: dark });
  const data: Record<string, string> = {};
  const meta = { content: "" };
  const storage = new Map(saved === null ? [] : [["gitmora.theme", saved]]);
  const document = Object.assign(new EventTarget(), {
    documentElement: { dataset: data },
    querySelector: () => meta,
    querySelectorAll: () => [],
  });
  Object.assign(window, {
    matchMedia: () => media,
    localStorage: {
      getItem: (key: string) => {
        if (blocked) throw new Error("Storage unavailable");
        return storage.get(key) ?? null;
      },
      setItem: (key: string, value: string) => {
        if (blocked) throw new Error("Storage unavailable");
        storage.set(key, value);
      },
    },
  });
  runInNewContext(readFileSync(resolve("web/public/assets/theme.js"), "utf8"), {
    window,
    document,
    CustomEvent,
  });
  return {
    data,
    meta,
    storage,
    toggle: () => window.dispatchEvent(new Event("gitmora:theme-toggle")),
    system(value: boolean) {
      media.matches = value;
      media.dispatchEvent(new Event("change"));
    },
    otherTab(value: string | null) {
      window.dispatchEvent(
        Object.assign(new Event("storage"), {
          key: "gitmora.theme",
          newValue: value,
        }),
      );
    },
  };
}

test("theme uses the system default before the application renders", () => {
  assert.equal(browser(null, true).data.theme, "dark");
  assert.equal(browser().data.theme, "light");
});
test("theme restores an explicit choice across app and hosted login", () => {
  assert.equal(browser("light", true).data.theme, "light");
  assert.equal(browser("dark", false).data.theme, "dark");
});
test("theme ignores malformed stored values", () => {
  assert.equal(browser("unexpected", true).data.theme, "dark");
});
test("theme toggles, persists and updates browser chrome", () => {
  const b = browser();
  b.toggle();
  assert.equal(b.data.theme, "dark");
  assert.equal(b.storage.get("gitmora.theme"), "dark");
  assert.equal(b.meta.content, "#101827");
  b.toggle();
  assert.equal(b.data.theme, "light");
  assert.equal(b.meta.content, "#f5f7fb");
});
test("theme remains usable with browser storage blocked", () => {
  const b = browser(null, false, true);
  b.toggle();
  b.system(false);
  assert.equal(b.data.theme, "dark");
});
test("system changes apply only before an explicit selection", () => {
  const b = browser();
  b.system(true);
  assert.equal(b.data.theme, "dark");
  b.toggle();
  b.system(true);
  assert.equal(b.data.theme, "light");
});
test("theme changes synchronize between tabs and return to the system when cleared", () => {
  const b = browser(null, true);
  b.otherTab("light");
  assert.equal(b.data.theme, "light");
  b.otherTab(null);
  assert.equal(b.data.theme, "dark");
});
