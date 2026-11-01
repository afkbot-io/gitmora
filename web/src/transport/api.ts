const messages: Record<number, string> = {
  400: "Проверьте параметры запроса.",
  401: "Сессия завершена. Войдите снова.",
  403: "Недостаточно прав для этого действия.",
  404: "Запись не найдена или больше недоступна.",
  409: "Данные изменились. Обновите страницу и повторите действие.",
  413: "Запрос превышает допустимый размер.",
  422: "Проверьте введённые данные и параметры запроса.",
  429: "Слишком много запросов. Повторите позже.",
  502: "Не удалось проверить доступ к внешнему сервису. Проверьте адрес, ключ и его права.",
  503: "Сервис временно недоступен. Повторите позже.",
};
export const integrationErrors: Record<string, string> = {
  provider_rate_limited:
    "Достигнут лимит API сервиса. Проверьте токен или дождитесь следующей попытки.",
  provider_unauthorized:
    "Токен не принят. Проверьте его срок действия и сохраните новый.",
  provider_forbidden:
    "У токена нет доступа. Разрешите чтение репозитория, запросов на слияние и проверок CI.",
  provider_project_not_found:
    "Репозиторий не найден. Проверьте ссылку и доступ токена.",
  provider_private_repository: "Для закрытого репозитория нужен токен чтения.",
  provider_endpoint_not_allowed:
    "Администратор Gitmora должен разрешить подключение к этому серверу.",
  provider_invalid_response:
    "Сервис вернул неподдерживаемый ответ. Проверьте адрес сервера и его версию.",
};
const diagnostics: Record<string, string> = {
  ...integrationErrors,
  username_taken: "Этот ник уже занят. Выберите другой.",
  cli_fresh_login_required:
    "Для подтверждения CLI войдите через организацию заново по ссылке из терминала.",
  cli_enrollment_unavailable:
    "Запрос CLI истёк, отменён или ключ больше не действует. Проверьте состояние в терминале.",
  cli_login_unavailable:
    "Вход CLI через организацию не настроен на этом сервере.",
  cli_enrollment_conflict:
    "Запрос CLI изменился или уже завершён. Обновите его состояние перед новым решением.",

  identity_conflict:
    "Привязка уже существует или её версия изменилась. Обновите OIDC-привязки перед новым изменением.",
  identity_unavailable:
    "Вход не настроен. Обратитесь к администратору сервера.",
  credential_delegation_denied:
    "Новый ключ превышает полномочия, срок или глубину текущего подключения. Проверьте ограничения родителя.",
  last_project_administrator:
    "Сначала назначьте другого действующего администратора проекта.",
  last_installation_operator:
    "Сначала назначьте другого действующего оператора установки.",
  directory_identity_retired:
    "Этот идентификатор уже удалён. Создайте новую запись.",
  integration_unconfigured:
    "Администратор сервера должен настроить шифрование и разрешённые адреса интеграций.",
  directory_conflict:
    "Версия или идентификатор операции изменились. Обновите данные перед новым изменением.",
  task_integration_conflict:
    "Адрес и страна Tasktopia уже закреплены за проектом. Проверьте исходную привязку.",
};
export type MutationMethod = "POST" | "PUT" | "PATCH" | "DELETE";
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code = "",
  ) {
    super(
      diagnostics[code] ??
        messages[status] ??
        "Не удалось выполнить запрос. Повторите попытку.",
    );
    this.name = "ApiError";
  }
}

/** Exact-name and duplicate-cookie rules match the cookie gateway. */
export function csrfCookie(cookie: string, secure: boolean): string {
  const name = secure ? "__Host-workgraph_csrf" : "workgraph_csrf_dev";
  const matches = cookie
    .split(";")
    .map((x) => x.trim())
    .filter((x) => x.startsWith(name + "="));
  return matches.length === 1 ? matches[0]!.slice(name.length + 1) : "";
}

/** Cookie-only browser transport. Session changes fence all pending work. */
export class ApiClient {
  private generation = 0;
  private controller = new AbortController();
  onUnauthorized: (() => void) | undefined;
  constructor(
    private readonly fetcher: typeof fetch = globalThis.fetch.bind(globalThis),
    private readonly cookies: () => string = () => document.cookie,
    private readonly timeoutMS = 20_000,
  ) {}
  clear(): void {
    this.generation++;
    this.controller.abort();
    this.controller = new AbortController();
  }
  async request<T>(
    path: string,
    body?: object,
    signal?: AbortSignal,
    query?: { after_id?: string; limit?: number; revision?: number },
  ): Promise<T> {
    return this.perform<T>(
      body === undefined ? "GET" : "POST",
      path,
      body,
      signal,
      query,
    );
  }
  async mutate<T = void>(
    method: MutationMethod,
    path: string,
    body?: object,
    signal?: AbortSignal,
  ): Promise<T> {
    return this.perform<T>(method, path, body, signal);
  }
  private async perform<T>(
    method: "GET" | MutationMethod,
    path: string,
    body?: object,
    signal?: AbortSignal,
    query?: { after_id?: string; limit?: number; revision?: number },
  ): Promise<T> {
    if (
      !/^\/v1\/[A-Za-z0-9_/-]+$/.test(path) ||
      path.includes("..") ||
      path.includes("//")
    )
      throw new Error("Invalid API path");
    const generation = this.generation;
    const headers = new Headers({ Accept: "application/json" });
    if (method !== "GET") {
      const secure =
        typeof location !== "undefined" && location.protocol === "https:";
      headers.set("X-Workgraph-CSRF", csrfCookie(this.cookies(), secure));
    }
    if (body !== undefined) headers.set("Content-Type", "application/json");
    const timeout = AbortSignal.timeout(this.timeoutMS);
    const combined = AbortSignal.any([
      this.controller.signal,
      timeout,
      ...(signal ? [signal] : []),
    ]);
    const assertActive = () => {
      if (generation !== this.generation || signal?.aborted)
        throw new DOMException("Request superseded", "AbortError");
      if (timeout.aborted)
        throw new Error("Сервер не ответил вовремя. Повторите запрос.");
    };
    const params = new URLSearchParams();
    if (query?.after_id) params.set("after_id", query.after_id);
    if (query?.limit) params.set("limit", String(query.limit));
    if (query?.revision) params.set("revision", String(query.revision));
    let response: Response;
    try {
      response = await this.fetcher(
        path + (params.size ? "?" + params.toString() : ""),
        {
          method,
          headers,
          credentials: "same-origin",
          cache: "no-store",
          redirect: "error",
          signal: combined,
          ...(body === undefined ? {} : { body: JSON.stringify(body) }),
        },
      );
    } catch (error) {
      assertActive();
      if (error instanceof TypeError)
        throw new Error(
          "Сервер недоступен. Проверьте соединение и повторите запрос.",
        );
      throw error;
    }
    assertActive();
    if (!response.ok) {
      if (response.status === 401) {
        this.clear();
        this.onUnauthorized?.();
        throw new ApiError(response.status);
      }
      let code = "";
      try {
        const error = (await response.json()) as { error?: { code?: unknown } };
        if (
          typeof error.error?.code === "string" &&
          Object.hasOwn(diagnostics, error.error.code)
        )
          code = error.error.code;
      } catch {
        /* Unknown error bodies never become UI text. */
      }
      assertActive();
      throw new ApiError(response.status, code);
    }
    if (response.status === 204) return undefined as T;
    let result: T;
    try {
      result = (await response.json()) as T;
    } catch {
      assertActive();
      throw new Error("Сервер вернул неполный ответ. Повторите запрос.");
    }
    assertActive();
    return result;
  }
}
