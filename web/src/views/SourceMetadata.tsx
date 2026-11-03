import type { SourceMetadata as Metadata } from "../domain/context.js";
import { t } from "../domain/locale.js";

const agentNames: Readonly<Record<string, string>> = {
  codex: "Codex",
  opencode: "OpenCode",
};

/** Compact producer context; the tooltip distinguishes recorded execution from configuration. */
export function SourceMetadata({
  metadata,
}: {
  metadata: Metadata | undefined;
}) {
  if (metadata?.attribution_kind === "known_human")
    return <span className="text-xs text-muted">{t("Человек")}</span>;
  if (!metadata?.agent && !metadata?.model) return null;
  const basis =
    metadata.model_basis === "message_metadata"
      ? t("Модель сообщения")
      : metadata.model_basis === "import_report"
        ? t("Импортированный отчёт")
        : t("Настройка модели сессии");
  return (
    <span
      className="inline-flex min-w-0 flex-wrap gap-x-2 text-xs text-muted"
      title={metadata.model ? basis : undefined}
    >
      {metadata.agent && (
        <span className="wrap-anywhere">
          {agentNames[metadata.agent.toLowerCase()] ?? metadata.agent}
        </span>
      )}
      {metadata.model && (
        <span className="wrap-anywhere">{metadata.model}</span>
      )}
    </span>
  );
}
