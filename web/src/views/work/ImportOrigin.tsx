import { t as translate } from "../../domain/locale.js";
import { useState } from "react";
import type { ImportOrigin } from "../../domain/work.js";
import { buttonStyles } from "../ui.js";

/** Displays external source identity and bounded claims without implying verified authorship. */
export function ImportOriginView({ origin }: { origin: ImportOrigin }) {
  const [offset, setOffset] = useState(0);
  const claims = origin.claims ?? [];
  return (
    <section
      className="my-6 border border-line bg-paper p-4 [&_code]:wrap-anywhere [&_code]:font-mono [&_code]:text-xs"
      aria-label={translate("Происхождение импорта")}
    >
      <h3>
        {translate("Импорт:")}{" "}
        {origin.system === "git-ai" ? "Git AI" : "Entire"}
      </h3>
      <p className="text-sm text-muted">
        {translate(
          "Данные источника. Авторство указано внешним инструментом; строки без меток имеют неизвестное происхождение.",
        )}
      </p>
      <p className="text-sm">
        {translate("Источник:")} <code>{origin.source_id}</code>
        {translate(". Фрагмент")} {origin.part} {translate("из")} {origin.parts}
        .
      </p>
      {origin.source_session_id && (
        <p className="text-sm">
          {translate("Сессия источника:")}{" "}
          <code>{origin.source_session_id}</code>
        </p>
      )}
      {origin.agent && (
        <p className="text-sm">
          {translate("Агент в источнике:")} {origin.agent.tool} /{" "}
          {origin.agent.model}
        </p>
      )}
      {!!origin.omitted_blocks && (
        <p className="text-sm">
          {translate("Блоки вне текстового импорта:")} {origin.omitted_blocks}.
        </p>
      )}
      {origin.external_transcripts && (
        <p className="text-sm">
          {translate(
            "Источник ссылается на внешние transcripts. Их содержимое в эту запись не загружалось.",
          )}
        </p>
      )}
      {claims.length > 0 && (
        <details>
          <summary>
            {translate("Диапазоны, указанные источником:")} {claims.length}
          </summary>
          <p className="text-sm">
            {translate("Диапазоны относятся к коммиту")}{" "}
            <code>{origin.target_commit}</code>.
          </p>
          <ul className="space-y-4">
            {claims.slice(offset, offset + 20).map((claim) => (
              <li key={`${claim.path}:${claim.start}:${claim.key}`}>
                <code>
                  {claim.path}:{claim.start}–{claim.end}
                </code>
                <p className="my-1 text-sm">
                  {claim.kind === "known_human"
                    ? translate("Источник указал человека: {0}", claim.author)
                    : translate(
                        "Источник указал AI: {0} / {1}",
                        claim.agent?.tool,
                        claim.agent?.model,
                      )}
                </p>
                {claim.agent && (
                  <p className="my-1 text-sm">
                    {translate("Сессия:")} <code>{claim.agent.session_id}</code>
                  </p>
                )}
                <code>{claim.key}</code>
              </li>
            ))}
          </ul>
          <div className="my-3 flex flex-wrap gap-2">
            {offset > 0 && (
              <button
                className={buttonStyles.secondary}
                onClick={() => setOffset(Math.max(0, offset - 20))}
              >
                {translate("Предыдущие диапазоны")}
              </button>
            )}
            {offset + 20 < claims.length && (
              <button
                className={buttonStyles.secondary}
                onClick={() => setOffset(offset + 20)}
              >
                {translate("Следующие диапазоны")}
              </button>
            )}
          </div>
        </details>
      )}
      <details>
        <summary>{translate("Точная версия источника")}</summary>
        <dl className="grid grid-cols-[100px_minmax(0,1fr)] gap-2 text-xs [&_dd]:m-0">
          <dt>{translate("Формат")}</dt>
          <dd>{origin.format}</dd>
          <dt>{translate("Версия")}</dt>
          <dd>{origin.producer_version ?? translate("Не указана")}</dd>
          <dt>Git ref</dt>
          <dd>
            <code>{origin.ref}</code>
          </dd>
          <dt>{translate("Снимок ref")}</dt>
          <dd>
            <code>{origin.ref_object}</code>
          </dd>
          <dt>{translate("Объект")}</dt>
          <dd>
            <code>{origin.object_id}</code>
          </dd>
          <dt>SHA-256</dt>
          <dd>
            <code>{origin.source_digest}</code>
          </dd>
        </dl>
      </details>
    </section>
  );
}
