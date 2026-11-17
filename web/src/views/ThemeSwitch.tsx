import { t as translate } from "../domain/locale.js";
import { useSyncExternalStore } from "react";

function subscribe(changed: () => void) {
  window.addEventListener("gitmora:theme-change", changed);
  return () => window.removeEventListener("gitmora:theme-change", changed);
}
function currentTheme() {
  return document.documentElement.dataset.theme === "dark";
}

/** Theme cards share the early bootstrap and never store a second preference. */
export function ThemeSwitch() {
  const dark = useSyncExternalStore(subscribe, currentTheme);
  return (
    <div
      className="grid max-w-lg grid-cols-2 gap-4"
      role="group"
      aria-label={translate("Оформление")}
    >
      {(["light", "dark"] as const).map((theme) => {
        const selected = dark === (theme === "dark");
        return (
          <button
            key={theme}
            className={`theme-switch flex flex-col items-start gap-3 p-3 text-left ${selected ? "border-accent ring-1 ring-accent text-ink" : ""}`}
            type="button"
            aria-pressed={selected}
            onClick={() => {
              if (!selected)
                window.dispatchEvent(new Event("gitmora:theme-toggle"));
            }}
          >
            <svg
              className="h-20 w-full rounded-md"
              viewBox="0 0 200 100"
              preserveAspectRatio="none"
              fill="none"
              aria-hidden="true"
            >
              <rect
                width="200"
                height="100"
                rx="6"
                fill={theme === "dark" ? "#101827" : "#f5f7fb"}
              />
              <path
                d="M0 6a6 6 0 0 1 6-6h188a6 6 0 0 1 6 6v16H0Z"
                fill={theme === "dark" ? "#182333" : "#fff"}
              />
              <path
                d="M0 23h46v77H6a6 6 0 0 1-6-6Z"
                fill={theme === "dark" ? "#182333" : "#fff"}
              />
              <rect
                x="8"
                y="33"
                width="30"
                height="8"
                rx="2"
                fill={theme === "dark" ? "#3d5ed8" : "#335cde"}
              />
              <path
                d="M57 34h81M57 45h114M57 64h122M57 76h122M57 88h122"
                stroke={theme === "dark" ? "#33445c" : "#dee5f0"}
                strokeWidth="4"
                strokeLinecap="round"
              />
            </svg>
            <span className="flex w-full items-center justify-between gap-2">
              {translate(theme === "dark" ? "Тёмная тема" : "Светлая тема")}
              <span
                className={`grid size-4 shrink-0 place-items-center rounded-full border ${selected ? "border-action bg-action text-white" : "border-control"}`}
                aria-hidden="true"
              >
                {selected && (
                  <svg
                    width="12"
                    height="12"
                    viewBox="0 0 16 16"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2"
                  >
                    <path d="m4 8 3 3 5-6" />
                  </svg>
                )}
              </span>
            </span>
          </button>
        );
      })}
    </div>
  );
}
