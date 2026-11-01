import { t as translate } from "../../domain/locale.js";
import { buttonStyles, sharedStyles } from "../ui.js";
import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import type { Me, Page } from "../../domain/context.js";
import { readableError } from "../../domain/context.js";
import {
  keyInput,
  keyState,
  maximumLifetime,
  permissions,
} from "../../domain/credentials.js";
import type { IssuedKey, OwnedKey } from "../../domain/credentials.js";
import type { ApiClient } from "../../transport/api.js";
import { LatestRequest } from "../../application/latest.js";
import { useMutation } from "../../application/useMutation.js";
import {
  ConfirmAction,
  MutationForm,
  MutationNotice,
} from "../SettingsShared.js";
import { ErrorNotice, Empty, aborted, date } from "../shared.js";

/** Owned credentials are account-scoped and do not require project/catalog access. */
export function Keys({ client, me }: { client: ApiClient; me: Me }) {
  const [items, setItems] = useState<OwnedKey[]>([]);
  const [next, setNext] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [requests] = useState(() => new LatestRequest());
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(timer);
  }, []);
  async function refresh(after = "") {
    const request = requests.begin();
    setLoading(true);
    setError("");
    try {
      const page = await client.request<Page<OwnedKey>>(
        "/v1/me/tokens",
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
  }, [client, requests]);
  return (
    <section className="settings-view px-0 pb-8">
      <div className={sharedStyles.heading}>
        <div>
          <h2>{translate("Личные ключи")}</h2>
          <p className="muted text-muted">
            {translate(
              "Ключи и браузерные сессии вашей учётной записи. Доступ к проектам проверяется отдельно.",
            )}
          </p>
        </div>
        <button
          className={buttonStyles.secondary}
          disabled={loading}
          onClick={() => void refresh()}
        >
          {translate("Обновить список ключей")}
        </button>
      </div>
      <div
        className={`
        credential-parent border-t border-line border-b border-line px-0
        py-5 mt-5 max-w-220 mobile:[&_.connection-scope]:grid-cols-[95px_minmax(0,_1fr)]
      `}
      >
        <h2>{translate("Текущее подключение")}</h2>
        <dl className={sharedStyles.connectionScope}>
          <dt>{translate("Идентификатор")}</dt>
          <dd className="mono font-mono text-xs wrap-anywhere">
            {me.token_id}
          </dd>
          <dt>{translate("Действует до")}</dt>
          <dd>{date(me.expires_at)}</dd>
        </dl>
        <p className="field-help text-sm text-muted mx-0 mt-2 mb-5 leading-[1.5]">
          {translate(
            "Права и срок ключа ограничены текущей сессией. Выход завершит работу выданных через неё ключей.",
          )}
        </p>
      </div>
      <KeyIssue
        key={me.token_id}
        client={client}
        me={me}
        onIssued={() => refresh()}
        now={now}
        observed={items}
      />
      <ErrorNotice>{error}</ErrorNotice>
      {!items.length && !loading && !error && (
        <Empty title={translate("Список пуст")}>
          {translate("Обновите список или выпустите ограниченный ключ.")}
        </Empty>
      )}
      <div className="owned-keys">
        {items.map((item) => (
          <KeyRow
            key={item.id}
            item={item}
            currentID={me.token_id}
            client={client}
            onRevoked={() => refresh()}
            now={now}
          />
        ))}
      </div>
      {next && (
        <button
          className={buttonStyles.secondary}
          disabled={loading}
          onClick={() => void refresh(next)}
        >
          {translate("Загрузить ещё ключи")}
        </button>
      )}
    </section>
  );
}

function KeyIssue({
  client,
  me,
  onIssued,
  now,
  observed,
}: {
  client: ApiClient;
  me: Me;
  onIssued: () => Promise<void>;
  now: number;
  observed: OwnedKey[];
}) {
  const [label, setLabel] = useState("");
  const [minutes, setMinutes] = useState("15");
  const [selected, setSelected] = useState<string[]>([]);
  const [error, setError] = useState("");
  const [result, setResult] = useState<IssuedKey | null>(null);
  const action = useMutation(client);
  useEffect(() => {
    if (
      result &&
      (Date.parse(result.token.expires_at) <= now ||
        observed.some(
          (item) => item.id === result.token.id && !!item.revoked_at,
        ))
    ) {
      setResult(null);
      setError("Выданный ключ истёк или был отозван. Результат выпуска скрыт.");
    }
  }, [result, observed, now]);
  const retry = useRef<{
    fingerprint: string;
    body: ReturnType<typeof keyInput>;
  } | null>(null);
  function choose(scope: string, enabled: boolean) {
    setSelected((values) =>
      enabled ? [...values, scope] : values.filter((value) => value !== scope),
    );
  }
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (result) return;
    setError("");
    setResult(null);
    try {
      const fingerprint = JSON.stringify([
        me.token_id,
        label.trim(),
        [...new Set(selected)].sort(),
        minutes,
      ]);
      // An exact uncertain retry recovers a prior receipt even if the remaining parent lifetime has decreased.
      const body =
        retry.current?.fingerprint === fingerprint
          ? retry.current.body
          : keyInput(me, label, selected, minutes);
      retry.current = { fingerprint, body };
      const accepted = await action.run<IssuedKey>(
        "POST",
        "/v1/me/tokens",
        body,
        true,
      );
      if (accepted) {
        setResult(accepted.value);
        setLabel("");
        setSelected([]);
        retry.current = null;
        await onIssued();
      }
    } catch (e) {
      setError(readableError(e));
    }
  }
  const available = maximumLifetime(me.expires_at, now);
  return (
    <>
      <MutationForm
        pending={action.pending || !!result}
        onSubmit={(event) => void submit(event)}
        className={`${sharedStyles.setup} key-issue`}
      >
        <h2>{translate("Выпустить ключ")}</h2>
        {result && (
          <p className="field-help text-sm text-muted mx-0 mt-2 mb-5 leading-[1.5]">
            {translate(
              "Сначала сохраните или отзовите ключ и скройте результат предыдущего выпуска.",
            )}
          </p>
        )}
        <div className={sharedStyles.formGrid}>
          <label>
            {translate("Название ключа")}
            <input
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              required
              autoComplete="off"
              maxLength={120}
              placeholder={translate("Например, локальный сборщик")}
            />
          </label>
          <label>
            {translate("Срок в минутах")}
            <input
              type="number"
              min="1"
              step="1"
              value={minutes}
              onChange={(e) => setMinutes(e.target.value)}
              required
            />
          </label>
        </div>
        <p className="field-help text-sm text-muted mx-0 mt-2 mb-5 leading-[1.5]">
          {translate("Сейчас доступно не более")} {Math.floor(available / 60)}{" "}
          {translate(
            "полных минут. Браузерная сессия ограничивает срок её дочерних ключей; более долгий корневой ключ выдаётся отдельной доверенной процедурой.",
          )}
        </p>
        <fieldset
          className={`
          scope-options border-0 p-0 mx-0 my-5 grid grid-cols-[repeat(2,_minmax(0,_1fr))] [gap:12px_20px]
          min-w-0 [&_legend]:font-medium [&_legend]:text-base [&_legend]:mb-4 [&_input]:mt-1 [&_small]:block
          [&_small]:text-muted [&_small]:text-xs [&_small]:mt-1 [&_.checkbox:has(input:disabled)]:text-muted
          mobile:grid-cols-[1fr]
        `}
        >
          <legend>{translate("Разрешения нового ключа")}</legend>
          {permissions
            .filter((item) => item.id !== "platform:manage" || me.user.operator)
            .map((item) => (
              <label
                className="checkbox [&_input]:[accent-color:var(--color-accent)] [&_input]:w-4 [&_input]:min-h-4 [&_input]:m-0"
                key={item.id}
              >
                <input
                  type="checkbox"
                  checked={selected.includes(item.id)}
                  disabled={!me.scopes.includes(item.id)}
                  onChange={(e) => choose(item.id, e.target.checked)}
                />
                <span>
                  {translate(item.label)}
                  <small className="mono font-mono text-xs wrap-anywhere">
                    {item.id}
                    {!me.scopes.includes(item.id)
                      ? translate(" — недоступно родителю")
                      : ""}
                  </small>
                </span>
              </label>
            ))}
        </fieldset>
        <ErrorNotice>{error}</ErrorNotice>
        <MutationNotice action={action} />
        <button
          className={buttonStyles.primary}
          disabled={action.pending || (available < 60 && !retry.current)}
        >
          {translate("Выпустить личный ключ")}
        </button>
      </MutationForm>
      {result && (
        <KeyDisclosure
          value={result}
          close={() => setResult(null)}
          client={client}
          onRevoked={onIssued}
        />
      )}
    </>
  );
}

/** Only this mounted disclosure owns the first response's raw bearer; no storage or automatic clipboard. */
function KeyDisclosure({
  value,
  close,
  client,
  onRevoked,
}: {
  value: IssuedKey;
  close: () => void;
  client: ApiClient;
  onRevoked: () => Promise<void>;
}) {
  const action = useMutation(client);
  const [confirm, setConfirm] = useState(false);
  async function revoke() {
    if (
      await action.run(
        "POST",
        `/v1/me/tokens/${value.token.id}/revoke`,
        {},
        true,
      )
    ) {
      close();
      await onRevoked();
    }
  }
  return (
    <section
      className={`
        secret-disclosure p-6 [border:2px_solid_var(--color-accent)] bg-paper rounded-lg mx-0 my-6 max-w-220
        [&_input]:w-full [&_input]:mt-2 [&_p]:max-w-[70ch] [&_p]:text-sm [&_.small]:text-xs
        [&_.small]:text-muted [&_.small]:wrap-anywhere mobile:p-4
      `}
      aria-label={translate("Результат выпуска ключа")}
    >
      <div className={sharedStyles.heading}>
        <h2>
          {value.secret && value.created
            ? translate("Сохраните ключ сейчас")
            : translate("Ключ создан, секрет недоступен")}
        </h2>
        <button className={buttonStyles.secondary} onClick={close}>
          {translate("Скрыть результат выпуска")}
        </button>
      </div>
      <p className="mono font-mono text-xs wrap-anywhere">{value.token.id}</p>
      {value.secret && value.created ? (
        <>
          <label>
            {translate("Новый API-ключ")}
            <input
              className="mono font-mono text-xs wrap-anywhere"
              value={value.secret}
              readOnly
              autoComplete="off"
              spellCheck={false}
            />
          </label>
          <p>
            {translate(
              "Скопируйте его в свой менеджер секретов. После закрытия или перехода на другую страницу интерфейс больше не покажет этот секрет.",
            )}
          </p>
        </>
      ) : (
        <p>
          {translate(
            "Сервер подтвердил повтор ранее принятой операции. Исходный секрет не хранится в восстанавливаемом виде. Отзовите этот ключ и выпустите новый, если первый ответ не был сохранён.",
          )}
        </p>
      )}
      <p className="small text-sm">
        {translate("Действует до")} {date(value.token.expires_at)}
        {translate(". Родитель:")}{" "}
        <span className="mono font-mono text-xs wrap-anywhere">
          {value.token.parent_id ?? translate("корневой ключ")}
        </span>
        .
      </p>
      <MutationNotice action={action} />
      <button
        className={buttonStyles.dangerOutline}
        disabled={action.pending}
        onClick={() => setConfirm(true)}
      >
        {translate("Отозвать этот выданный ключ")}
      </button>
      {confirm && (
        <ConfirmAction
          title={translate("Отозвать выданный ключ?")}
          pending={action.pending}
          error={action.error}
          label={translate("Отозвать ключ и потомков")}
          onClose={() => setConfirm(false)}
          onConfirm={() => void revoke()}
        >
          <p>
            {translate(
              "Этот ключ и все его дочерние ключи перестанут работать.",
            )}
          </p>
        </ConfirmAction>
      )}
    </section>
  );
}

function KeyRow({
  item,
  currentID,
  client,
  onRevoked,
  now,
}: {
  item: OwnedKey;
  currentID: string;
  client: ApiClient;
  onRevoked: () => Promise<void>;
  now: number;
}) {
  const [confirm, setConfirm] = useState(false);
  const action = useMutation(client);
  const state = keyState(item, now);
  async function revoke() {
    if (await action.run("POST", `/v1/me/tokens/${item.id}/revoke`, {}, true)) {
      setConfirm(false);
      await onRevoked();
    }
  }
  return (
    <article
      className={`
        settings-resource key-row border-b border-line px-0 py-4 [&_.repository-row]:border-0
        [&_.repository-row]:px-0 [&_.repository-row]:py-4 [&_.repository-row_>_div:first-child]:[flex:1]
      `}
      data-key-id={item.id}
    >
      <div className={sharedStyles.heading}>
        <div>
          <h2>{item.label}</h2>
          <span className="state-chip inline-block text-muted text-xs bg-canvas rounded px-2 py-1 mr-2">
            {item.kind === "browser"
              ? translate("Браузерная сессия")
              : translate("API-ключ")}
          </span>
          <span className="state-chip inline-block text-muted text-xs bg-canvas rounded px-2 py-1 mr-2">
            {
              {
                active: translate("Действует"),
                expired: translate("Истёк"),
                revoked: translate("Отозван"),
              }[state]
            }
          </span>
          {item.id === currentID && (
            <span className="state-chip inline-block text-muted text-xs bg-canvas rounded px-2 py-1 mr-2">
              {translate("Текущее подключение")}
            </span>
          )}
        </div>
        {!item.revoked_at && (
          <button
            className={buttonStyles.dangerOutline}
            onClick={() => setConfirm(true)}
          >
            {translate("Отозвать ключ")}
          </button>
        )}
      </div>
      <dl className={sharedStyles.connectionScope}>
        <dt>{translate("Идентификатор")}</dt>
        <dd className="mono font-mono text-xs wrap-anywhere">{item.id}</dd>
        <dt>{translate("Родитель")}</dt>
        <dd className="mono font-mono text-xs wrap-anywhere">
          {item.parent_id ?? translate("Корневой ключ")}
        </dd>
        <dt>{translate("Действует до")}</dt>
        <dd>{date(item.expires_at)}</dd>
        {item.revoked_at && (
          <>
            <dt>{translate("Отозван")}</dt>
            <dd>{date(item.revoked_at)}</dd>
          </>
        )}
      </dl>
      <p className="mono key-scopes font-mono text-xs wrap-anywhere max-w-[90ch]">
        {item.scopes.join(", ")}
      </p>
      {confirm && (
        <ConfirmAction
          title={translate("Отозвать {0}?", item.label)}
          pending={action.pending}
          error={action.error}
          label={translate("Отозвать ключ и потомков")}
          onClose={() => setConfirm(false)}
          onConfirm={() => void revoke()}
        >
          <p>
            {translate("Будут отозваны этот ключ и все его дочерние ключи.")}{" "}
            {item.id === currentID
              ? translate("Текущее подключение завершится.")
              : item.kind === "browser"
                ? translate("Соответствующая браузерная сессия завершится.")
                : translate("Клиенты с этими ключами потеряют доступ.")}
          </p>
        </ConfirmAction>
      )}
    </article>
  );
}
