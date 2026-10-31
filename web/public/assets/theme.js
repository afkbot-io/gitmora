/* Shared, CSP-compatible theme bootstrap for the application and hosted account forms. */
(() => {
  const key = "gitmora.theme";
  const valid = (value) => value === "light" || value === "dark";
  const system = window.matchMedia("(prefers-color-scheme: dark)");
  let choice = null;
  try {
    const saved = window.localStorage.getItem(key);
    if (valid(saved)) choice = saved;
  } catch {
    // Private/embedded browser contexts can deny storage; the current tab still works.
  }
  function apply() {
    const theme = choice ?? (system.matches ? "dark" : "light");
    document.documentElement.dataset.theme = theme;
    const meta = document.querySelector('meta[name="theme-color"]');
    if (meta) meta.content = theme === "dark" ? "#101827" : "#f5f7fb";
    window.dispatchEvent(new CustomEvent("gitmora:theme-change"));
  }
  function toggle() {
    choice =
      document.documentElement.dataset.theme === "dark" ? "light" : "dark";
    try {
      window.localStorage.setItem(key, choice);
    } catch {
      /* In-memory choice is retained. */
    }
    apply();
  }
  window.addEventListener("gitmora:theme-toggle", toggle);
  window.addEventListener("storage", (event) => {
    if (event.key === key || event.key === null) {
      choice = valid(event.newValue) ? event.newValue : null;
      apply();
    }
  });
  system.addEventListener("change", () => {
    if (choice === null) apply();
  });
  document.addEventListener("DOMContentLoaded", apply);
  apply();
})();
