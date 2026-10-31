import { t as translate } from "../../domain/locale.js";
import { buttonStyles, sharedStyles } from "../ui.js";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import type { AuthConfig, Page } from "../../domain/context.js";
import { readableError } from "../../domain/context.js";
import type { Binding } from "../../domain/credentials.js";
import type { ApiClient } from "../../transport/api.js";
import { LatestRequest } from "../../application/latest.js";
import { useMutation } from "../../application/useMutation.js";
import {
  ConfirmAction,
  MutationForm,
  MutationNotice,
} from "../SettingsShared.js";
import { Empty, ErrorNotice, aborted, date } from "../shared.js";

export function Bindings({
  client,
  config,
}: {
  client: ApiClient;
  config: AuthConfig;
}) {
  const [items, setItems] = useState<Binding[]>([]);
  const [next, setNext] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [requests] = useState(() => new LatestRequest());
  const [id, setID] = useState(() => crypto.randomUUID());
  const [user, setUser] = useState("");
  const [subject, setSubject] = useState("");
  const action = useMutation(client);
  async function refresh(after = "") {
    if (!config.enabled) return;
    const request = requests.begin();
    setLoading(true);
    setError("");
    try {
      const page = await client.request<Page<Binding>>(
        "/v1/admin/identities",
        undefined,
        request.signal,
        { limit: 100, ...(after ? { after_id: after } : {}) },
      );
      if (request.current()) {
        setItems((current) =>
          after ? [...current, ...page.items] : page.items,
        );
        setNext(page.next_id ?? "");
      }
    } catch (e) {
      if (request.current() && !aborted(e)) setError(readableError(e));
    } finally {
      if (request.current()) setLoading(false);
    }
  }
  useEffect(() => {
    void refresh();
    return () => requests.cancel();
  }, [client, config.enabled, requests]);
  async function create(event: FormEvent) {
    event.preventDefault();
    if (
      await action.run(
        "POST",
        "/v1/admin/identities",
        { id, user_id: user.trim(), subject },
        true,
      )
    ) {
      setID(crypto.randomUUID());
      setUser("");
      setSubject("");
      action.setMessage(
        "Привязка создана. Она не меняет членство или полномочия пользователя.",
      );
      await refresh();
    }
  }
  return (
    <section className="settings-view px-0 pb-8">
      <div className={sharedStyles.heading}>
        <div>
          <h1>{translate("Вход через OIDC")}</h1>
          <p className="muted text-muted">
            {translate(
              "Явные привязки внешней идентичности к существующей локальной учётной записи.",
            )}
          </p>
        </div>
        {config.enabled && (
          <button
            className={buttonStyles.secondary}
            disabled={loading}
            onClick={() => void refresh()}
          >
            {translate("Обновить OIDC-привязки")}
          </button>
        )}
      </div>
      {!config.enabled ? (
        <Empty title={translate("OIDC не настроен")}>
          {translate("Попросите администратора сервера настроить вход.")}
        </Empty>
      ) : (
        <>
          <dl className={sharedStyles.connectionScope}>
            <dt>{translate("Настроенный issuer")}</dt>
            <dd className="mono font-mono text-xs wrap-anywhere">
              {config.issuer}
            </dd>
          </dl>
          <MutationForm
            className={sharedStyles.setup}
            pending={action.pending}
            onSubmit={(event) => void create(event)}
          >
            <h2>{translate("Новая OIDC-привязка")}</h2>
            <div className={sharedStyles.formGrid}>
              <label>
                {translate("UUID локального пользователя")}
                <input
                  required
                  pattern="[0-9a-fA-F-]{36}"
                  value={user}
                  onChange={(e) => setUser(e.target.value)}
                  autoComplete="off"
                />
              </label>
              <label>
                {translate("Точный subject провайдера")}
                <input
                  required
                  maxLength={255}
                  pattern="[!-~]+"
                  value={subject}
                  onChange={(e) => setSubject(e.target.value)}
                  autoComplete="off"
                  spellCheck={false}
                />
              </label>
            </div>
            <p className="field-help text-sm text-muted mx-0 mt-2 mb-5 leading-[1.5]">
              {translate(
                "Укажите постоянный subject пользователя в провайдере входа. Исправить ошибочную привязку можно, отключив её и создав новую.",
              )}
            </p>
            <MutationNotice action={action} />
            <button className={buttonStyles.primary} disabled={action.pending}>
              {translate("Создать OIDC-привязку")}
            </button>
          </MutationForm>
          <ErrorNotice>{error}</ErrorNotice>
          {items.map((item) => (
            <BindingRow
              key={`${item.id}-${item.version}`}
              item={item}
              client={client}
              onSaved={() => refresh()}
            />
          ))}
          {!items.length && !loading && !error && (
            <Empty title={translate("Привязок пока нет")}>
              {translate(
                "Создайте привязку, чтобы пользователь мог войти через организацию.",
              )}
            </Empty>
          )}
          {next && (
            <button
              className={buttonStyles.secondary}
              disabled={loading}
              onClick={() => void refresh(next)}
            >
              {translate("Загрузить ещё OIDC-привязки")}
            </button>
          )}
        </>
      )}
    </section>
  );
}
function BindingRow({
  item,
  client,
  onSaved,
}: {
  item: Binding;
  client: ApiClient;
  onSaved: () => Promise<void>;
}) {
  const [confirm, setConfirm] = useState(false);
  const action = useMutation(client);
  async function change() {
    if (
      await action.run(
        "PUT",
        `/v1/admin/identities/${item.id}`,
        { expected_version: item.version, enabled: !item.enabled },
        true,
      )
    ) {
      setConfirm(false);
      await onSaved();
    }
  }
  return (
    <article
      className={`
        settings-resource binding-row border-b border-line px-0 py-4
        [&_.repository-row]:border-0 [&_.repository-row]:px-0 [&_.repository-row]:py-4
        [&_.repository-row_>_div:first-child]:[flex:1]
      `}
      data-binding-id={item.id}
    >
      <div className={sharedStyles.heading}>
        <div>
          <h2 className="mono binding-subject font-mono text-base wrap-anywhere max-w-[60ch]">
            {item.subject}
          </h2>
          <span className="state-chip inline-block text-muted text-xs bg-canvas rounded px-2 py-1 mr-2">
            {item.enabled ? translate("Включена") : translate("Выключена")}
          </span>
        </div>
        <button
          className={buttonStyles.secondary}
          onClick={() => setConfirm(true)}
        >
          {item.enabled
            ? translate("Выключить OIDC-привязку")
            : translate("Включить OIDC-привязку")}
        </button>
      </div>
      <dl className={sharedStyles.connectionScope}>
        <dt>{translate("Локальный пользователь")}</dt>
        <dd className="mono font-mono text-xs wrap-anywhere">{item.user_id}</dd>
        <dt>Issuer</dt>
        <dd className="mono font-mono text-xs wrap-anywhere">{item.issuer}</dd>
        <dt>{translate("Идентификатор")}</dt>
        <dd className="mono font-mono text-xs wrap-anywhere">{item.id}</dd>
        <dt>{translate("Создана")}</dt>
        <dd>{date(item.created_at)}</dd>
      </dl>
      {confirm && (
        <ConfirmAction
          title={translate(
            "{0} OIDC-привязку?",
            item.enabled ? translate("Выключить") : translate("Включить"),
          )}
          pending={action.pending}
          error={action.error}
          onClose={() => setConfirm(false)}
          onConfirm={() => void change()}
        >
          <p>
            {item.enabled
              ? translate(
                  "Действующие сессии этой привязки и их дочерние ключи будут отозваны. Самостоятельные API-ключи учётной записи не заменяются.",
                )
              : translate(
                  "Пользователь сможет войти заново. Старые сессии и их ключи не восстановятся.",
                )}
          </p>
        </ConfirmAction>
      )}
    </article>
  );
}
