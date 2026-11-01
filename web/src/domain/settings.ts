import { t as translate } from "./locale.js";
/** Settings policy only shapes controls; every server operation rechecks current authority. */
export interface Actor {
  scopes: readonly string[];
  user: { operator: boolean };
}
export function capabilities(actor: Actor, project?: { role?: string }) {
  const has = (value: string) => actor.scopes.includes(value);
  const operator =
    actor.user.operator && has("platform:manage") && has("directory:read");
  return {
    createProject: operator,
    accounts: operator,
    catalog:
      has("directory:read") &&
      (operator || (project?.role === "admin" && has("directory:manage"))),
    members: has("directory:read") && (!!project?.role || operator),
    integrations: project?.role === "admin" && has("integration:manage"),
    integrationRead: !!project?.role && has("integration:read"),
  };
}

/** Keep one exact metadata command in memory so uncertain retries replay its server receipt. */
export class CommandDraft {
  private fingerprint = "";
  private value:
    Readonly<Record<string, unknown> & { request_id: string }> | undefined;
  constructor(
    private readonly uuid: () => string = () => crypto.randomUUID(),
  ) {}
  prepare(method: string, path: string, payload: Record<string, unknown>) {
    const ordered = Object.fromEntries(
      Object.keys(payload)
        .sort()
        .map((key) => [key, payload[key]]),
    );
    const fingerprint = JSON.stringify([method, path, ordered]);
    if (fingerprint !== this.fingerprint || !this.value) {
      this.fingerprint = fingerprint;
      this.value = Object.freeze({ ...ordered, request_id: this.uuid() });
    }
    return this.value;
  }
}

export function providerNumber(value: string): number {
  if (!/^[1-9][0-9]*$/.test(value) || !Number.isSafeInteger(Number(value)))
    throw new Error(
      "ID проекта GitLab должен быть точным положительным целым числом.",
    );
  return Number(value);
}

/** Legacy int64 catalog IDs can exceed JSON's exact number range; never display a rounded ID. */
export function providerLabel(value: number): string {
  return Number.isSafeInteger(value) && value > 0
    ? String(value)
    : translate("ID вне точного диапазона браузера");
}

export interface Receipt {
  action: string;
  entity_id: string;
  project_id?: string;
  version: number;
  sequence?: number;
}
export interface Member {
  user_id: string;
  display_name: string;
  enabled: boolean;
  role: string;
  version: number;
}
export interface StreamStatus {
  kind: string;
  watermark: string;
  next_page: number;
  active: boolean;
  last_complete_at?: string;
  retry_at: string;
  error_code?: string;
}
export interface ForgeStatus {
  anonymous?: boolean;
  provider?: "github";
  repository_id: string;
  enabled: boolean;
  generation: number;
  webhook_mode: "disabled" | "signature" | "legacy";
  webhook_path: string;
  last_error_code?: string;
  last_synced_at?: string;
  next_sync_at: string;
  blocked: boolean;
  streams: StreamStatus[];
}
export interface TasktopiaStatus {
  project_id: string;
  endpoint: string;
  country_id: string;
  enabled: boolean;
  generation: number;
  blocked: boolean;
  error_code?: string;
  auto_link_merge_requests: boolean;
}

/** Provider diagnostics are small protocol codes; never render arbitrary provider messages. */
export function diagnostic(value?: string): string {
  if (!value) return translate("Нет зарегистрированной ошибки");
  return /^[a-z0-9_]{1,80}$/.test(value)
    ? value
    : translate("Ошибка интеграции");
}
