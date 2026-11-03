import { useEffect, useState } from "react";
import { LatestRequest } from "../../application/latest.js";
import { t as translate } from "../../domain/locale.js";
import { readableError, type Page } from "../../domain/context.js";
import {
  credentialMatches,
  type RepositoryAccessValue,
  type SavedRepositoryCredential,
} from "../../domain/repository-access.js";
import type { ApiClient } from "../../transport/api.js";
import { aborted, ErrorNotice } from "../shared.js";
import { buttonStyles } from "../ui.js";

/** One access selector for creation and settings. Only credential labels leave the server. */
export function RepositoryAccess({
  client,
  projectID,
  provider,
  url,
  repositoryID,
  value,
  onChange,
  refreshKey = 0,
}: {
  client: ApiClient;
  projectID: string;
  provider: "github" | "gitlab";
  url: string;
  repositoryID?: string;
  value: RepositoryAccessValue;
  onChange: (value: RepositoryAccessValue) => void;
  refreshKey?: number;
}) {
  const [items, setItems] = useState<SavedRepositoryCredential[]>([]);
  const [next, setNext] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [requests] = useState(() => new LatestRequest());
  async function load(after = "") {
    const request = requests.begin();
    setLoading(true);
    setError("");
    try {
      const page = await client.request<Page<SavedRepositoryCredential>>(
        `/v1/projects/${projectID}/forge/credentials`,
        undefined,
        request.signal,
        after ? { after_id: after } : undefined,
      );
      if (!request.current()) return;
      setItems((current) => (after ? [...current, ...page.items] : page.items));
      setNext(page.next_id ?? "");
    } catch (e) {
      if (request.current() && !aborted(e)) setError(readableError(e));
    } finally {
      if (request.current()) setLoading(false);
    }
  }
  useEffect(() => {
    void load();
    return () => requests.cancel();
  }, [client, projectID, requests, refreshKey]);
  const options = items.filter(
    (item) =>
      item.repository_id !== repositoryID &&
      credentialMatches(item, provider, url),
  );
  const selected =
    value.kind === "saved" ? value.credential.repository_id : value.kind;
  const invalidSelection =
    value.kind === "saved" &&
    !options.some(
      (item) =>
        item.repository_id === value.credential.repository_id &&
        item.generation === value.credential.generation,
    );
  return (
    <div className="grid gap-4 min-w-0">
      <label className="grid gap-1.5 mb-0 text-sm font-medium">
        {translate("Доступ к репозиторию")}
        <select
          className="w-full"
          aria-label={translate("Доступ к репозиторию")}
          required
          value={invalidSelection ? "" : selected}
          onChange={(event) => {
            if (event.target.value === "new")
              onChange({ kind: "new", token: "" });
            else if (event.target.value === "public")
              onChange({ kind: "public" });
            else {
              const credential = options.find(
                (item) => item.repository_id === event.target.value,
              );
              if (credential) onChange({ kind: "saved", credential });
            }
          }}
        >
          <option value="new">{translate("Новый токен")}</option>
          {options.map((item) => (
            <option key={item.repository_id} value={item.repository_id}>
              {translate("Токен из {0}", item.name)}
            </option>
          ))}
          {invalidSelection && (
            <option value="" disabled>
              {translate("Выберите токен заново")}
            </option>
          )}
          {provider === "github" && (
            <option value="public">
              {translate("Публичный репозиторий без токена")}
            </option>
          )}
        </select>
      </label>
      {value.kind === "new" && (
        <label className="grid gap-1.5 mb-0 text-sm font-medium">
          {translate("Токен чтения")}
          <input
            className="w-full"
            type="password"
            name="repository_token"
            required
            autoComplete="off"
            minLength={8}
            maxLength={4096}
            value={value.token}
            onChange={(event) =>
              onChange({ kind: "new", token: event.target.value })
            }
          />
        </label>
      )}
      {invalidSelection && (
        <p role="status" className="mb-0 text-sm text-danger">
          {translate("Токен больше не подходит. Выберите другой доступ.")}
        </p>
      )}
      {loading && (
        <p className="text-xs text-muted mb-0" role="status">
          {translate("Загружаем сохранённые токены…")}
        </p>
      )}
      <ErrorNotice>{error}</ErrorNotice>
      {(next || error || invalidSelection) && (
        <button
          type="button"
          className={buttonStyles.text}
          disabled={loading}
          onClick={() => void load(next && !error ? next : "")}
        >
          {translate(
            next && !error
              ? "Ещё сохранённые токены"
              : "Обновить список токенов",
          )}
        </button>
      )}
      {value.kind === "public" ? (
        <p className="text-sm text-muted mb-0">
          {translate("Без токена доступен небольшой общий лимит GitHub API.")}
        </p>
      ) : (
        <details className="text-sm text-muted">
          <summary>{translate("Как получить токен")}</summary>
          <p className="mt-3 mb-0">
            {provider === "github"
              ? translate(
                  "GitHub → Settings → Developer settings → Fine-grained tokens. Выберите нужные репозитории и разрешите Contents, Pull requests и Actions: Read-only.",
                )
              : translate(
                  "GitLab → Settings → Access tokens. Создайте токен с правом read_api и доступом к нужным проектам.",
                )}
          </p>
        </details>
      )}
    </div>
  );
}
