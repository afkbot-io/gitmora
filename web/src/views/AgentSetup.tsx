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
    <details className="my-4 border-t border-line pt-4">
      <summary className="text-sm font-medium text-accent">
        {translate("Подключить CLI и локальных агентов")}
      </summary>
      <div className="pt-5">
        <SetupGuide repositoryID={repositoryID} />
      </div>
    </details>
  );
}
export function SetupGuide({ repositoryID }: { repositoryID?: string }) {
  const [agent, setAgent] = useState("codex");
  const commands = onboardingCommands(location.origin, repositoryID);
  return (
    <div className="setup-guide grid gap-7">
      <section>
        <h2 className="text-lg">{translate("1. Установите CLI")}</h2>
        <CommandBlock
          label={translate("macOS и Linux")}
          text={clientInstallCommand(location.origin, "gitmora_darwin_arm64")}
        />
        <details className="text-sm text-muted">
          <summary>{translate("Системные требования и загрузки")}</summary>
          <p className="mt-3">
            {translate(
              "Нужны Git 2.43+ и curl. Для входа на Linux установите libsecret-tools и запустите Secret Service в своей пользовательской сессии.",
            )}
          </p>
          <div className="flex flex-wrap gap-4">
            {clientPlatforms.map((p) => (
              <a
                key={p.file}
                href={`/downloads/cli/${publicClientVersion}/${p.file}`}
                download
              >
                {p.label}
              </a>
            ))}
            <a href={`/downloads/cli/${publicClientVersion}/SHA256SUMS`}>
              SHA-256
            </a>
          </div>
        </details>
      </section>
      <section>
        <h2 className="text-lg">
          {translate("2. Войдите и подключите репозиторий")}
        </h2>
        <CommandBlock
          label={translate("Вход в Gitmora")}
          text={'export PATH="$HOME/.local/bin:$PATH"\n' + commands.login}
        />
        {repositoryID ? (
          <CommandBlock
            label={translate("В папке репозитория")}
            text={commands.init}
          />
        ) : (
          <p className="text-sm text-muted">
            {translate(
              "Выберите репозиторий в разделе «Репозитории», чтобы получить команду подключения.",
            )}
          </p>
        )}
      </section>
      <section>
        <h2 className="text-lg">{translate("3. Включите нужный агент")}</h2>
        <label className="max-w-80">
          {translate("Агент")}
          <select
            className="mt-2 w-full"
            aria-label={translate("Агент")}
            value={agent}
            onChange={(e) => setAgent(e.target.value)}
          >
            <option value="codex">Codex</option>
            <option value="opencode">OpenCode</option>
          </select>
        </label>
        <CommandBlock
          label={translate("Подключение агента")}
          text={`gitmora agent enable ${agent}`}
        />
        <p className="text-sm text-muted">
          {agent === "codex"
            ? translate(
                "Разрешите команды Gitmora через /hooks в Codex и перезапустите сессию.",
              )
            : translate("Перезапустите OpenCode после подключения.")}
        </p>
        <p className="text-sm text-muted">
          {translate(
            "Работайте как обычно: git commit и git push отправят историю в Gitmora.",
          )}
        </p>
      </section>
      <details className="border-t border-line pt-5">
        <summary className="text-sm font-medium">
          {translate("Поиск через MCP")}
        </summary>
        <div className="mt-4">
          <CommandBlock
            label="Codex"
            text={`codex mcp add gitmora -- "$HOME/.local/bin/gitmora" mcp --server '${location.origin}'`}
          />
          <CommandBlock
            label="OpenCode · opencode.json"
            text={commands.opencodeMCP}
          />
          <p className="text-sm text-muted">
            {translate(
              "MCP использует ваш вход в CLI. После настройки перезапустите агент.",
            )}
          </p>
        </div>
      </details>
      <details className="border-t border-line pt-5">
        <summary className="text-sm font-medium">
          {translate("Статус и отключение")}
        </summary>
        <div className="mt-4">
          <CommandBlock
            label={translate("Проверить подключение")}
            text="gitmora status\ngitmora sync --retry"
          />
          <CommandBlock
            label={translate("Остановить сбор в этом репозитории")}
            text="gitmora disable"
          />
        </div>
      </details>
    </div>
  );
}
