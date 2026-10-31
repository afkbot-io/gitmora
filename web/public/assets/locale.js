/* Shared language preference for the application and hosted account forms. */
(() => {
  const key = "gitmora.locale";
  const valid = (value) => value === "ru" || value === "en";
  const account = document.documentElement.hasAttribute("data-account");
  let locale = "ru";
  try {
    const saved = localStorage.getItem(key);
    if (valid(saved)) locale = saved;
  } catch {
    /* The selected language still works without storage. */
  }
  if (account) locale = document.documentElement.lang === "en" ? "en" : "ru";
  function apply(value, persist) {
    if (!valid(value)) return;
    locale = value;
    if (!account) document.documentElement.lang = locale;
    if (persist) {
      try {
        localStorage.setItem(key, locale);
      } catch {
        /* In-memory selection. */
      }
    }
    window.dispatchEvent(new Event("gitmora:locale-change"));
  }
  window.addEventListener("gitmora:locale-select", (event) =>
    apply(event.detail, true),
  );
  window.addEventListener("storage", (event) => {
    if ((event.key === key || event.key === null) && !account)
      apply(valid(event.newValue) ? event.newValue : "ru", false);
  });
  apply(locale, account);
})();
