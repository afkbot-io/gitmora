import { english } from "./messages.js";

export type Locale = "ru" | "en";
export function currentLocale(): Locale {
  return typeof document !== "undefined" &&
    document.documentElement.lang === "en"
    ? "en"
    : "ru";
}
/** Only authored interface messages go through this catalog; source/user text stays untouched. */
export function t(
  message: string,
  ...values: (string | number | undefined)[]
): string {
  const text =
    currentLocale() === "en" ? (english[message] ?? message) : message;
  return text.replace(/\{(\d+)\}/g, (_match, index: string) =>
    String(values[Number(index)] ?? "—"),
  );
}
export function loginLocalePath(path: string, locale: Locale): string {
  return path + (path.includes("?") ? "&" : "?") + "locale=" + locale;
}
