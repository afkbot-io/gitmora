import { t as translate } from "../domain/locale.js";
import {
  clientPlatforms,
  clientInstallCommand,
  onboardingCommands,
  publicClientVersion,
} from "../domain/onboarding.js";
import { CommandBlock } from "./CommandBlock.js";
import { useState } from "react";

export function AgentSetup({ repositoryID }: { repositoryID: string }) {
  return (
    <details className="my-3 border border-line rounded-xl bg-paper [&_summary]:p-4 [&_summary]:font-medium [&_summary]:text-accent">
      <summary>{translate("Подключить CLI и локальных агентов")}</summary>
      <div className="px-5 pb-5 max-w-220 mobile:px-4">
        <SetupGuide repositoryID={repositoryID} />
      </div>
    </details>
  );
}

export function SetupGuide({ repositoryID }: { repositoryID?: string }) {
  const [platform, setPlatform] = useState<
    (typeof clientPlatforms)[number]["file"]
  >("gitmora_darwin_arm64");
  const commands = onboardingCommands(location.origin, repositoryID);
  const downloads = `/downloads/cli/${publicClientVersion}`;
  return (
    <>
      <p className="text-sm text-muted leading-relaxed">
        {translate(
          "Выполните команды на своём компьютере. Нужен доступ к репозиторию в Gitmora.",
        )}
      </p>
      <p className="text-sm text-muted leading-relaxed">
        {translate(
          "Нужны Git 2.43+, curl и учётная запись с доступом к проекту. На macOS вход хранится в Keychain. На Linux нужны",
        )}{" "}
        <code>secret-tool</code>{" "}
        {translate(
          "и разблокированный Secret Service в пользовательской сессии D-Bus (в Ubuntu пакет",
        )}{" "}
        <code>libsecret-tools</code>
        {translate("). Windows пока не поддерживается.")}
      </p>
      <h3 className="text-base mb-3">{translate("1. Установите CLI")}</h3>
      <label className="block max-w-100 mb-4">
        {translate("Система компьютера разработчика")}
        <select
          value={platform}
          onChange={(event) =>
            setPlatform(event.target.value as typeof platform)
          }
        >
          {clientPlatforms.map((item) => (
            <option value={item.file} key={item.file}>
              {item.label}
            </option>
          ))}
        </select>
      </label>
      <CommandBlock
        label={translate("Скачать, проверить и установить CLI")}
        text={clientInstallCommand(location.origin, platform)}
      />
      <div className="flex gap-x-5 gap-y-3 flex-wrap text-sm">
        {clientPlatforms.map((platform) => (
          <a
            key={platform.file}
            href={`${downloads}/${platform.file}`}
            download
          >
            {platform.label}
          </a>
        ))}
      </div>
      <p className="text-xs text-muted mt-3">
        {translate("Версия")} {publicClientVersion} ·{" "}
        <a href={`${downloads}/SHA256SUMS`}>SHA-256</a> ·{" "}
        <a href={`${downloads}/release.json`}>{translate("Состав сборки")}</a>
      </p>
      <p className="text-sm text-muted">
        {translate(
          "Команда проверяет два SHA-256 перед запуском. Проверяйте версию по составу сборки. Добавьте",
        )}{" "}
        <code>$HOME/.local/bin</code>{" "}
        {translate(
          "в PATH вашего shell-профиля для новых терминалов. Существующий CLI сохраняется; обновление требует",
        )}{" "}
        <code>--replace-sha256</code> {translate("с его проверенным хешем.")}
      </p>
      <h3 className="text-base mt-7 mb-3">
        {translate("2. Войдите и подключите репозиторий")}
      </h3>
      <CommandBlock
        label={translate("Единый вход Gitmora")}
        text={commands.login}
      />
      {repositoryID ? (
        <>
          <p className="text-sm text-muted">
            {translate(
              "Подтвердите вход в браузере той же учётной записью. Выполните следующую команду из локального Git-репозитория с настроенным origin.",
            )}
          </p>
          <CommandBlock
            label={translate("Подключение этого репозитория")}
            text={commands.init}
          />
        </>
      ) : (
        <p className="text-sm text-muted">
          {translate(
            "Для записи сессий откройте «Репозитории», выберите нужный репозиторий и скопируйте его команду подключения. Если проектов нет, оператор должен предоставить доступ.",
          )}
        </p>
      )}
      <h3 className="text-base mt-7 mb-3">
        {translate("3. Включите нужный агент")}
      </h3>
      <CommandBlock label="Codex" text="gitmora agent enable codex" />
      <p className="text-sm text-muted">
        {translate("Откройте")} <code>/hooks</code>{" "}
        {translate(
          "в Codex, проверьте команды Gitmora и разрешите их запуск. Если Codex отсутствует в PATH, укажите",
        )}{" "}
        <code>{translate("--codex /полный/путь/к/codex")}</code>.
      </p>
      <CommandBlock label="OpenCode" text="gitmora agent enable opencode" />
      <p className="text-sm text-muted">
        {translate(
          "Перезапустите агент после установки. Сбор начинается только в явно подключённых репозиториях. Обычные commit и push связывают и отправляют контекст; без связи с коммитом сессия остаётся локальной.",
        )}
      </p>
      <details className="border-t border-line mt-6 pt-4 [&_summary]:p-0">
        <summary>{translate("Поиск и контекст через MCP")}</summary>
        <p className="text-sm text-muted mt-4">
          {translate(
            "MCP использует сохранённый вход CLI. Токен в конфигурации агента не нужен. Доступны поиск, просмотр связей и точных источников; действия с данными также доступны через",
          )}{" "}
          <code>gitmora api</code>.
        </p>
        <CommandBlock label="Codex · config.toml" text={commands.codexMCP} />
        <p className="text-sm text-muted">
          {translate("Добавьте секцию в")} <code>~/.codex/config.toml</code>
          {translate(
            ". Если агент не видит PATH вашего терминала, замените",
          )}{" "}
          <code>gitmora</code>{" "}
          {translate("полным путём к установленному файлу.")}
        </p>
        <CommandBlock
          label="OpenCode · opencode.json"
          text={commands.opencodeMCP}
        />
        <p className="text-sm text-muted">
          {translate("Объедините поле")} <code>mcp.gitmora</code>{" "}
          {translate("с существующим")} <code>opencode.json</code>
          {translate(
            ", сохранив остальные настройки. Перезапустите агент. Начните с просьбы найти доступные сессии через",
          )}{" "}
          <code>context.browse</code>
          {translate(
            ", затем открыть точные источники. При истечении входа повторите",
          )}{" "}
          <code>gitmora auth login</code> {translate("в терминале.")}
        </p>
      </details>
      <details className="border-t border-line mt-5 pt-4 [&_summary]:p-0">
        <summary>{translate("Статус, офлайн-очередь и отключение")}</summary>
        <CommandBlock
          label={translate("Проверить сбор и повторить доставку")}
          text={"gitmora status\ngitmora sync --retry"}
        />
        <CommandBlock
          label={translate("Остановить сбор в этом репозитории")}
          text="gitmora disable"
        />
        <p className="text-sm text-muted">
          {translate(
            "Сохранённые данные и очередь остаются на месте. Возобновление:",
          )}{" "}
          <code>gitmora enable</code>
          {translate(
            ". Чтобы отключить только один агент здесь, используйте",
          )}{" "}
          <code>gitmora agent disable codex --local</code> {translate("или")}{" "}
          <code>gitmora agent disable opencode --local</code>.
        </p>
      </details>
    </>
  );
}
