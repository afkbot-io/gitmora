/** Browser consent sees only proposed authority and public request metadata, never device key/proof material. */
export interface CLIView {
  id: string;
  label: string;
  scopes: string[];
  ttl_seconds: number;
  created_at: string;
  expires_at: string;
  fingerprint: string;
  status: "pending" | "approved" | "denied" | "cancelled";
  user_id?: string;
  token_id?: string;
  credential_expires_at?: string;
}
export const cliScopeNames: Readonly<Record<string, string>> = {
  "capture:read": "Читать сессии агента",
  "capture:write": "Отправлять сессии агента",
  "integration:read": "Читать данные репозиториев",
  "directory:read": "Читать доступные проекты и репозитории",
  "credentials:manage": "Управлять своими ключами",
};
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const codePattern =
  /^[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{4}-?[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{4}$/;
/** Normalize the public printable code; no URL or recovery secret is accepted as a code. */
export function cliCode(value: string): string {
  value = value.trim().toUpperCase();
  if (!codePattern.test(value))
    throw new Error("Введите полный код из терминала: XXXX-XXXX.");
  value = value.replace("-", "");
  return value.slice(0, 4) + "-" + value.slice(4);
}
/** Only the fixed CLI fragment opts into consent; unrelated/error fragments retain their existing behavior. */
export function cliCodeFromHash(value: string): string {
  if (!value.startsWith("#cli=")) return "";
  try {
    return cliCode(value.slice(5));
  } catch {
    return "";
  }
}
/** Reauthentication can only target this application's dedicated fixed-origin continuation entry. */
export function cliLoginPath(code: string): string {
  return "/v1/auth/cli/login?code=" + cliCode(code);
}
/** Reject inconsistent or cross-account consent data before displaying any decided grant. */
export function checkCLIView(v: CLIView, user: string): void {
  const created = Date.parse(v.created_at),
    expires = Date.parse(v.expires_at);
  if (
    !uuid.test(v.id) ||
    !v.label.trim() ||
    new TextEncoder().encode(v.label).length > 120 ||
    !/^([0-9a-f]{64})$/.test(v.fingerprint) ||
    !["pending", "approved", "denied", "cancelled"].includes(v.status) ||
    !Number.isSafeInteger(v.ttl_seconds) ||
    v.ttl_seconds < 60 ||
    v.ttl_seconds > 90 * 86400 ||
    !Number.isFinite(created) ||
    !Number.isFinite(expires) ||
    expires <= created ||
    expires - created > 600001 ||
    v.scopes.length < 1 ||
    v.scopes.length > 16 ||
    new Set(v.scopes).size !== v.scopes.length ||
    !v.scopes.includes("credentials:manage") ||
    v.scopes.some((s) => !Object.hasOwn(cliScopeNames, s))
  )
    throw new Error(
      "Запрос CLI не совпал с поддерживаемыми условиями подключения.",
    );
  if (
    ((v.status === "pending" || v.status === "cancelled") &&
      (v.user_id || v.token_id || v.credential_expires_at)) ||
    ((v.status === "approved" || v.status === "denied") &&
      v.user_id !== user) ||
    (v.status === "approved" &&
      (!v.token_id ||
        !uuid.test(v.token_id) ||
        !v.credential_expires_at ||
        !Number.isFinite(Date.parse(v.credential_expires_at)))) ||
    (v.status === "denied" && (v.token_id || v.credential_expires_at))
  )
    throw new Error(
      "Подтверждение CLI не совпало с текущей учётной записью или состоянием запроса.",
    );
}
/** An explicit decision pins exactly the inspected immutable request; terminal records cannot be re-enrolled. */
export function cliDecision(
  code: string,
  v: CLIView,
  user: string,
  approve: boolean,
) {
  checkCLIView(v, user);
  if (v.status !== "pending")
    throw new Error(
      "Этот запрос уже завершён. Новый вход начинается в терминале.",
    );
  return {
    user_code: cliCode(code),
    expected_fingerprint: v.fingerprint,
    approve,
  };
}
