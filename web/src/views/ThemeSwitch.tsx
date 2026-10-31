import { t as translate } from "../domain/locale.js";
import { useSyncExternalStore } from "react";

function subscribe(changed: () => void) {
  window.addEventListener("gitmora:theme-change", changed);
  return () => window.removeEventListener("gitmora:theme-change", changed);
}
function currentTheme() {
  return document.documentElement.dataset.theme === "dark";
}

/** The same early bootstrap owns preference and persistence on both application and identity pages. */
export function ThemeSwitch() {
  const dark = useSyncExternalStore(subscribe, currentTheme);
  return (
    <button
      className="theme-switch"
      type="button"
      aria-label={translate("Тёмная тема")}
      aria-pressed={dark}
      onClick={() => window.dispatchEvent(new Event("gitmora:theme-toggle"))}
    >
      <svg
        width="18"
        height="18"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        {dark ? (
          <path d="M20.5 13A8.5 8.5 0 0 1 11 3.5 8.5 8.5 0 1 0 20.5 13Z" />
        ) : (
          <>
            <circle cx="12" cy="12" r="4" />
            <path d="M12 2v2m0 16v2M2 12h2m16 0h2M5 5l1.5 1.5m11 11L19 19M5 19l1.5-1.5m11-11L19 5" />
          </>
        )}
      </svg>
      <span>{translate("Тёмная тема")}</span>
    </button>
  );
}
