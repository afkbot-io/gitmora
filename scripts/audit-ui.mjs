/** Visual/accessibility matrix against a live page; restores the caller's theme and viewport. */
import assert from "node:assert/strict";
import { readFile, mkdir, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const axeSource = await readFile(
  require.resolve("axe-core/axe.min.js"),
  "utf8",
);
const label = process.env.GITMORA_UI_AUDIT_LABEL;
if (label && !/^[a-z0-9-]{1,40}$/.test(label))
  throw new Error("Invalid UI audit label");
const folder = label
  ? `output/playwright/release-${label}`
  : "output/playwright/design";

export async function auditUI(
  page,
  name,
  {
    enabled = process.env.GITMORA_UI_AUDIT === "1",
    widths = [1440, 390],
    fitViewport = false,
  } = {},
) {
  if (!enabled) return;
  // A visible server-rendered input can precede its stylesheet on a cold connection.
  await page.waitForLoadState("load");
  await mkdir(folder, { recursive: true });
  const viewport = page.viewportSize();
  const previous = await page.evaluate(() =>
    localStorage.getItem("gitmora.theme"),
  );
  try {
    for (const theme of ["light", "dark"]) {
      await page.evaluate((value) => {
        if (document.documentElement.dataset.theme !== value)
          window.dispatchEvent(new Event("gitmora:theme-toggle"));
      }, theme);
      for (const width of widths) {
        await page.setViewportSize({
          width,
          height: width === 390 ? 844 : 1000,
        });
        await page.evaluate(() => document.fonts.ready);
        // Inspect a painted theme/viewport, not WebKit's preceding computed-color cache.
        await page.evaluate(
          () =>
            new Promise((resolve) => {
              requestAnimationFrame(() => requestAnimationFrame(resolve));
            }),
        );
        await page.evaluate(axeSource);
        const scan = await page.evaluate(async () => {
          // Native modal content is the active accessibility scope; the rest of the page is inert.
          const modal = document.querySelector("dialog[open]");
          const result = await window.axe.run(modal ?? document, {
            runOnly: {
              type: "tag",
              values: [
                "wcag2a",
                "wcag2aa",
                "wcag21a",
                "wcag21aa",
                "wcag22a",
                "wcag22aa",
                "best-practice",
              ],
            },
          });
          return {
            scope: modal ? "modal" : "page",
            incomplete: result.incomplete.map(({ id, nodes }) => ({
              id,
              nodes: nodes.map(({ target }) => ({ target })),
            })),
            violations: result.violations.map(({ id, impact, nodes }) => ({
              id,
              impact,
              nodes: nodes.map(({ target, failureSummary }) => ({
                target,
                failureSummary,
              })),
            })),
          };
        });
        const { violations, incomplete, scope } = scan;
        const layout = await page.evaluate(() => ({
          theme: document.documentElement.dataset.theme,
          scheme: getComputedStyle(document.documentElement).colorScheme,
          background: getComputedStyle(document.documentElement)
            .backgroundColor,
          overflow: document.documentElement.scrollWidth > innerWidth,
          verticalOverflow: document.documentElement.scrollHeight > innerHeight,
          mobileLayout: matchMedia("(max-width: 700px)").matches,
          headings: [...document.querySelectorAll("h1")].filter(
            (e) => e.getClientRects().length > 0,
          ).length,
        }));
        const key = `${name}-${theme}-${width}`;
        await writeFile(
          `${folder}/${key}.json`,
          JSON.stringify(
            { name, width, violations, incomplete, scope, ...layout },
            null,
            2,
          ),
        );
        const masks = [page.locator(".secret-disclosure:visible")];
        for (const field of await page
          .locator('input[type="password"]:visible')
          .all()) {
          if (await field.inputValue()) masks.push(field);
        }
        await page.screenshot({
          caret: "initial",
          path: `${folder}/${key}.png`,
          fullPage: true,
          mask: masks,
        });
        assert.equal(layout.theme, theme, `${key}: theme did not apply`);
        assert.equal(
          layout.mobileLayout,
          width <= 700,
          `${key}: CSS layout viewport does not match the requested device`,
        );
        assert.equal(
          layout.scheme,
          theme,
          `${key}: native controls use the wrong scheme`,
        );
        assert.equal(
          layout.overflow,
          false,
          `${key}: page overflows horizontally`,
        );
        if (fitViewport)
          assert.equal(
            layout.verticalOverflow,
            false,
            `${key}: landing exceeds one viewport`,
          );
        assert.deepEqual(
          violations,
          [],
          `${key}: accessibility violations (see JSON)`,
        );
      }
    }
  } finally {
    await page.evaluate((value) => {
      if (value === null) localStorage.removeItem("gitmora.theme");
      else localStorage.setItem("gitmora.theme", value);
      window.dispatchEvent(
        new StorageEvent("storage", { key: "gitmora.theme", newValue: value }),
      );
    }, previous);
    if (viewport) await page.setViewportSize(viewport);
  }
}
