/** Account-level access read models; a browser session is never a reusable API secret. */
export interface OwnedKey {
  id: string;
  kind: "api" | "browser";
  parent_id?: string;
  label: string;
  scopes: string[];
  created_at: string;
  expires_at: string;
  revoked_at?: string;
}
export interface IssuedKey {
  token: OwnedKey;
  secret?: string;
  created: boolean;
}
export interface Binding {
  id: string;
  user_id: string;
  issuer: string;
  subject: string;
  enabled: boolean;
  version: number;
  created_at: string;
}

export const permissions: ReadonlyArray<{ id: string; label: string }> = [
  { id: "capture:read", label: "Читать сессии агента" },
  { id: "capture:write", label: "Отправлять сессии агента" },
  { id: "integration:read", label: "Читать интеграции и внешние источники" },
  { id: "integration:manage", label: "Настраивать интеграции проекта" },
  { id: "directory:read", label: "Читать каталог и участников" },
  { id: "directory:manage", label: "Управлять каталогом проекта" },
  {
    id: "platform:manage",
    label: "Управлять установкой при наличии роли оператора",
  },
  { id: "credentials:manage", label: "Выпускать и отзывать свои ключи" },
];

export function maximumLifetime(expires: string, now = Date.now()): number {
  const seconds = Math.floor((Date.parse(expires) - now) / 1000);
  return Number.isFinite(seconds)
    ? Math.max(0, Math.min(90 * 86400, seconds))
    : 0;
}

/** Validate an explicit delegation without silently increasing or reducing requested authority. */
export function keyInput(
  parent: { scopes: readonly string[]; expires_at: string },
  label: string,
  scopes: string[],
  minutes: string,
  now = Date.now(),
) {
  label = label.trim();
  if (
    !label ||
    new TextEncoder().encode(label).length > 120 ||
    /[\u0000-\u001f\u007f]/u.test(label)
  )
    throw new Error("Введите название ключа длиной до 120 байт UTF-8.");
  const selected = [...new Set(scopes)].sort();
  if (!selected.length) throw new Error("Выберите хотя бы одно разрешение.");
  if (
    selected.some(
      (value) =>
        !parent.scopes.includes(value) ||
        !permissions.some((item) => item.id === value),
    )
  )
    throw new Error("Нельзя передать больше прав, чем у текущего ключа.");
  if (!/^[1-9][0-9]*$/.test(minutes) || !Number.isSafeInteger(Number(minutes)))
    throw new Error("Введите срок целым положительным числом минут.");
  const seconds = Number(minutes) * 60;
  if (
    !Number.isSafeInteger(seconds) ||
    seconds < 60 ||
    seconds > maximumLifetime(parent.expires_at, now)
  )
    throw new Error(
      "Запрошенный срок превышает 90 дней или оставшийся срок текущего ключа или сессии.",
    );
  return { label, scopes: selected, ttl_seconds: seconds };
}

export function keyState(
  key: { expires_at: string; revoked_at?: string },
  now = Date.now(),
): "active" | "expired" | "revoked" {
  return key.revoked_at
    ? "revoked"
    : Date.parse(key.expires_at) <= now
      ? "expired"
      : "active";
}

const notices: Record<string, string> = {
  "#login=failed":
    "Не удалось войти. Повторите попытку или обратитесь к администратору.",
  "#login=unavailable":
    "Сервис входа временно недоступен. Повторите попытку позже.",
  "#login=busy": "Слишком много попыток входа. Повторите позже.",
};
export function loginNotice(hash: string): string {
  return Object.hasOwn(notices, hash) ? notices[hash]! : "";
}
