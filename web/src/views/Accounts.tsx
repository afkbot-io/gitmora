import { t as translate } from "../domain/locale.js";
import { buttonStyles, sharedStyles } from "./ui.js";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import type { Page, User } from "../domain/context.js";
import { readableError } from "../domain/context.js";
import type { ApiClient } from "../transport/api.js";
import { LatestRequest } from "../application/latest.js";
import { useMutation } from "../application/useMutation.js";
import {
  ConfirmAction,
  MutationNotice,
  MutationForm,
} from "./SettingsShared.js";
import { CommandBlock } from "./CommandBlock.js";
import { ErrorNotice, aborted } from "./shared.js";

export function Accounts({
  client,
  refreshMe,
  registrationEnabled,
}: {
  client: ApiClient;
  refreshMe: () => Promise<void>;
  registrationEnabled: boolean;
}) {
  const [items, setItems] = useState<User[]>([]);
  const [next, setNext] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [requests] = useState(() => new LatestRequest());
  async function load(after = "") {
    const request = requests.begin();
    setError("");
    setLoading(true);
    try {
      const page = await client.request<Page<User>>(
        "/v1/admin/users",
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
    void load();
    return () => requests.cancel();
  }, [client, requests]);
  return (
    <section className="settings-view px-0 pb-8">
      <div className={sharedStyles.heading}>
        <div>
          <h1>{translate("Учётные записи")}</h1>
        </div>
        <button
          className={buttonStyles.secondary}
          disabled={loading}
          onClick={() => void load()}
        >
          {translate("Обновить учётные записи")}
        </button>
      </div>
      <details className="my-6 p-5 border border-line rounded-xl bg-paper max-w-220">
        <summary className="font-medium cursor-pointer">
          {translate("Новые пользователи")}
        </summary>
        <div className="pt-4">
          <p className="text-sm text-muted leading-relaxed">
            {registrationEnabled
              ? translate(
                  "Регистрация открыта: пользователь создаёт учётную запись на странице входа. После этого назначьте ему доступ к проекту.",
                )
              : translate(
                  "Регистрация закрыта. Оператор создаёт пользователя на сервере командой ниже, затем назначает ему доступ к проекту.",
                )}
          </p>
          <CommandBlock
            label={translate("Создание пользователя на сервере")}
            text={[
              "gitmorad users create",
              "  --directory /srv/gitmora/identity",
              "  --installation /srv/gitmora/state",
              "  --username engineer",
              translate("  --name 'Имя Фамилия'"),
            ].join(" ")}
          />
          <p className="text-sm text-muted">
            {translate(
              "Пароль вводится скрыто в терминале. Команда создаёт пользователя и настраивает его вход. При нестандартной установке укажите ваши каталоги.",
            )}
          </p>
          <p className="text-xs text-muted mb-0">
            {translate("Регистрация управляется на сервере:")}{" "}
            <code>WORKGRAPH_REGISTRATION_ENABLED=true</code> {translate("или")}{" "}
            <code>false</code>
            {translate(". Вход остаётся обязательным.")}
          </p>
        </div>
      </details>
      <ErrorNotice>{error}</ErrorNotice>
      {items.map((user) => (
        <AccountRow
          key={`${user.id}-${user.version}`}
          client={client}
          user={user}
          onSaved={async () => {
            await load();
            await refreshMe();
          }}
        />
      ))}
      {next && (
        <button
          className={buttonStyles.secondary}
          disabled={loading}
          onClick={() => void load(next)}
        >
          {translate("Загрузить ещё учётные записи")}
        </button>
      )}
    </section>
  );
}
function AccountRow({
  client,
  user,
  onSaved,
}: {
  client: ApiClient;
  user: User;
  onSaved: () => Promise<void>;
}) {
  const [name, setName] = useState(user.display_name);
  const [confirm, setConfirm] = useState<
    "disable" | "delete" | "operator" | null
  >(null);
  const action = useMutation(client);
  const path = `/v1/admin/users/${user.id}`;
  async function rename(event: FormEvent) {
    event.preventDefault();
    if (
      await action.run(
        "PATCH",
        path,
        { display_name: name.trim(), expected_version: user.version },
        true,
      )
    )
      await onSaved();
  }
  async function change(kind: "disable" | "delete" | "operator") {
    const method =
      kind === "delete" ? "DELETE" : kind === "operator" ? "PUT" : "PATCH";
    const body = {
      expected_version: user.version,
      ...(kind === "disable"
        ? { enabled: !user.enabled }
        : kind === "operator"
          ? { operator: !user.operator }
          : {}),
    };
    if (
      await action.run(
        method,
        path + (kind === "operator" ? "/operator" : ""),
        body,
        true,
      )
    ) {
      setConfirm(null);
      await onSaved();
    }
  }
  return (
    <article
      className={`
      settings-resource account-resource border-b border-line [&_.repository-row]:border-0
      [&_.repository-row]:px-0 [&_.repository-row]:py-4 [&_.repository-row_>_div:first-child]:[flex:1] px-0 py-6
    `}
    >
      <div className={sharedStyles.heading}>
        <div>
          <h2>{user.display_name}</h2>
          <p className="mono muted text-muted font-mono text-xs wrap-anywhere">
            {user.id}
          </p>
          <span className="state-chip inline-block text-muted text-xs bg-canvas rounded px-2 py-1 mr-2">
            {user.enabled ? translate("Включена") : translate("Выключена")}
          </span>
          {user.operator && (
            <span className="state-chip inline-block text-muted text-xs bg-canvas rounded px-2 py-1 mr-2">
              {translate("Оператор установки")}
            </span>
          )}
        </div>
      </div>
      {!user.deleted_at && (
        <>
          <MutationForm
            pending={action.pending}
            className={sharedStyles.inlineForm}
            onSubmit={(event) => void rename(event)}
          >
            <label>
              {translate("Имя")} {user.display_name}
              <input
                required
                maxLength={200}
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </label>
            <button
              className={buttonStyles.secondary}
              disabled={action.pending || name.trim() === user.display_name}
            >
              {translate("Сохранить имя")}
            </button>
          </MutationForm>
          <div className="actions flex gap-3 flex-wrap items-center">
            <button
              className={buttonStyles.secondary}
              onClick={() => setConfirm("disable")}
            >
              {user.enabled
                ? translate("Выключить учётную запись")
                : translate("Включить учётную запись")}
            </button>
            {user.enabled && (
              <button
                className={buttonStyles.secondary}
                onClick={() => setConfirm("operator")}
              >
                {user.operator
                  ? translate("Снять полномочия оператора")
                  : translate("Назначить оператором")}
              </button>
            )}
            <button
              className={sharedStyles.destructiveText}
              onClick={() => setConfirm("delete")}
            >
              {translate("Удалить учётную запись")}
            </button>
          </div>
        </>
      )}
      <MutationNotice action={action} />
      {confirm && (
        <ConfirmAction
          title={
            confirm === "delete"
              ? translate("Удалить {0}?", user.display_name)
              : confirm === "operator"
                ? translate("Изменить полномочия {0}?", user.display_name)
                : `${user.enabled ? translate("Выключить") : translate("Включить")} ${user.display_name}?`
          }
          pending={action.pending}
          error={action.error}
          onClose={() => setConfirm(null)}
          onConfirm={() => void change(confirm)}
        >
          <p>
            {confirm === "delete"
              ? translate(
                  "Запись будет отключена и исключена из проектов; ключи отозваны. Исторический идентификатор автора сохраняется.",
                )
              : confirm === "operator"
                ? translate("Доступ к проектам назначается отдельно.")
                : user.enabled
                  ? translate(
                      "Все ключи и браузерные сессии пользователя будут отозваны; полномочия оператора сняты.",
                    )
                  : translate(
                      "Старые ключи, сессии и полномочия оператора не восстанавливаются.",
                    )}
          </p>
        </ConfirmAction>
      )}
    </article>
  );
}
