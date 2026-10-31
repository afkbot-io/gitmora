/** Observe the enforced policy and identify the independently reproduced native-select diagnostic. */
export async function watchBrowserPolicy(page) {
  const events = [];
  await page.exposeFunction("gitmoraAuditPolicy", (event) =>
    events.push(event),
  );
  await page.addInitScript(() => {
    document.addEventListener("securitypolicyviolation", (event) => {
      window.gitmoraAuditPolicy({
        directive: event.effectiveDirective,
        inline: event.blockedURI === "inline",
        nativeSelectStyle: event.sample === "text-overflow:inherit",
        screenshotStyle: event.sample === "body {}" && event.sourceFile === "",
      });
    });
  });
  const webkit = page.context().browser()?.browserType().name() === "webkit";
  return {
    unexpected: () =>
      events.filter(
        (event) =>
          !(
            webkit &&
            event.inline &&
            ((event.directive === "style-src-attr" &&
              event.nativeSelectStyle) ||
              // Playwright's WebKit screenshot synchronizer inserts this empty rule.
              (event.directive === "style-src-elem" && event.screenshotStyle))
          ),
      ),
    nativeSelectDiagnostics: () =>
      events.filter((event) => event.nativeSelectStyle).length,
    screenshotDiagnostics: () =>
      events.filter((event) => event.screenshotStyle).length,
  };
}
