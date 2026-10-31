import { currentLocale, t as translate } from "../domain/locale.js";
import type { ReactNode } from "react";

export function Brand() {
  return (
    <div
      translate="no"
      className="brand flex items-center gap-3 text-2xl font-semibold tracking-[-0.6px]"
    >
      <svg
        width="30"
        height="30"
        viewBox="0 0 30 30"
        fill="none"
        aria-hidden="true"
      >
        <path d="M7 7L23 15L7 23V7Z" stroke="currentColor" strokeWidth="2" />
        <circle cx="7" cy="7" r="3" fill="currentColor" />
        <circle cx="23" cy="15" r="3" fill="currentColor" />
        <circle cx="7" cy="23" r="3" fill="currentColor" />
      </svg>
      <span>Gitmora</span>
    </div>
  );
}

export function ErrorNotice({ children }: { children: ReactNode }) {
  return children ? (
    <p
      className="notice error px-4 py-4 mx-0 my-3 bg-danger-soft [border-left:3px_solid_var(--color-danger)] text-danger rounded-[0_5px_5px_0] wrap-anywhere"
      role="alert"
    >
      {typeof children === "string" ? translate(children) : children}
    </p>
  ) : null;
}

export function Empty({
  title,
  children,
  heading = "h2",
}: {
  title: string;
  heading?: "h1" | "h2";
  children: ReactNode;
}) {
  const Heading = heading;
  return (
    <div className="empty px-7 py-10 text-muted [&_h2]:text-ink [&_h2]:text-xl [&_p]:max-w-[58ch] [&_p]:text-sm [&_p]:leading-[1.7]">
      <Heading>{title}</Heading>
      <p>{children}</p>
    </div>
  );
}

export function date(value: string): string {
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime())
    ? translate("Время неизвестно")
    : new Intl.DateTimeFormat(currentLocale() === "en" ? "en-GB" : "ru-RU", {
        dateStyle: "medium",
        timeStyle: "short",
      }).format(parsed);
}

export function aborted(error: unknown): boolean {
  return error instanceof Error && error.name === "AbortError";
}
