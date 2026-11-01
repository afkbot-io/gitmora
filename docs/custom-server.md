# CLI и MCP со своим сервером Gitmora

Везде используйте **адрес самой Gitmora**, например `https://gitmora.company.ru`, без `/auth`, `/v1` или `/mcp`. Адрес GitLab/GitHub/Tasktopia вводится отдельно в интеграции проекта. Свой публичный сертификат HTTPS проверяется обычным способом; обход TLS не требуется.

## Установка и вход

Оператор сначала публикует на своём домене проверенные файлы CLI — [инструкция сервера](deployment.md#7-раздать-cli-и-подключить-агенты-к-своему-адресу).

```sh
curl -fsS --proto '=https' --tlsv1.2 https://gitmora.company.ru/install.sh \
  | sh -s -- --server https://gitmora.company.ru
export PATH="$HOME/.local/bin:$PATH"
gitmora version
gitmora auth login --server https://gitmora.company.ru
gitmora auth status --server https://gitmora.company.ru
gitmora api get /v1/projects --server https://gitmora.company.ru
```

Сравните код в браузере и терминале и подтвердите свой запрос. CLI не просит пароль платформы и не показывает API-ключ. macOS использует Keychain; Linux требует `secret-tool` и разблокированный Secret Service/D-Bus. При обычном интерактивном входе оставьте `WORKGRAPH_TOKEN` неустановленным: явный automation override имеет приоритет над OS-хранилищем. Headless-автоматизация с отдельным ограниченным ключом описана в [CLI auth](cli-auth.md).

Для нескольких Gitmora-серверов вход выполняется отдельно на каждый полный origin. Не копируйте credentials между адресами: `auth status/logout --server …` работает только с выбранным сервером. Для смены адреса существующего экземпляра нужен [контролируемый перенос](relocation.md), а не редактирование URL внутри старой записи OS-хранилища.

## Репозиторий и сбор

1. Создайте проект в своей Gitmora, подключите GitHub/GitLab и выдайте себе нужную роль.
2. В локально клонированном репозитории выполните команду из **CLI и MCP**:

```sh
gitmora init --server https://gitmora.company.ru --repository-id <UUID_РЕПОЗИТОРИЯ_GITMORA>
gitmora agent enable codex
# В Codex проверьте и разрешите команды через /hooks.
# Для OpenCode вместо этого:
# gitmora agent enable opencode
gitmora status
```

Один collector checkout/Git common directory закреплён за одним сервером и UUID репозитория. Если он уже подключён к старому экземпляру, не удаляйте его очередь ради смены URL: сначала завершите доставку/отключите старый сбор и используйте отдельный **новый клон** для независимого экземпляра. Worktree того же клона разделяет collector state. MCP-подключения к нескольким серверам можно использовать параллельно под разными именами.

Перезапустите агент. Работайте с обычными `git commit`/`git push`. UUID здесь принадлежит локальному каталогу Gitmora; это не числовой ID GitLab. URL `origin` остаётся вашим GitHub/GitLab. При разрыве связи очередь остаётся локально; `gitmora sync --retry` повторяет доставку. [Полный жизненный цикл и отключение](collector.md).

## Codex MCP

Укажите настоящий абсолютный путь: GUI-агент может не видеть shell PATH.

```sh
codex mcp add gitmora-company -- /absolute/path/to/gitmora mcp --server https://gitmora.company.ru
codex mcp list
```

Эквивалентная запись `~/.codex/config.toml`:

```toml
[mcp_servers.gitmora-company]
command = "/absolute/path/to/gitmora"
args = ["mcp", "--server", "https://gitmora.company.ru"]
```

Не добавляйте одновременно две копии одного подключения. Для другого экземпляра используйте другое имя MCP-сервера и его отдельный CLI-вход. Пользовательские Codex-настройки сохраняются; эти команды не требуют ключа модели Gitmora.

## OpenCode MCP

Добавьте запись в существующий OpenCode config, сохранив остальные настройки:

```json
{
  "mcp": {
    "gitmora-company": {
      "type": "local",
      "command": ["/absolute/path/to/gitmora", "mcp", "--server", "https://gitmora.company.ru"],
      "enabled": true
    }
  }
}
```

## Проверить работу

После перезапуска агента должны появиться шесть инструментов: `context.browse`, `context.search`, `context.pack`, `context.expand`, `context.task`, `context.file`. Сначала получите UUID проекта через `gitmora api get /v1/projects --server …`. Затем попросите агента вызвать `context.browse` с этим `project_id`, найти сессии через `context.search` и открыть источник через `context.expand`. Пустой новый проект возвращает пустую историю, а не ошибку подключения.

Строка запуска `gitmora mcp --server …` использует **stdio**, поэтому ручной запуск в терминале будет ждать MCP-сообщения. Его stdout — только протокол, без текста приветствия/логина. Отдельного HTTP OAuth-подключения через `codex mcp login` для рекомендуемого stdio-моста нет: сначала выполните обычный `gitmora auth login`.

| Проблема | Проверка |
| --- | --- |
| Скачивание 404 | Оператор выложил файлы указанной версии на **этот** домен; `/install.sh` не устанавливает сам сервер |
| MCP process не найден | Абсолютный путь, executable, запуск агента в правильной OS-сессии |
| `auth status` не авторизован | Совпадение полного `--server`, текущий OS-пользователь, доступ к Keychain/Secret Service, явный `WORKGRAPH_TOKEN` override |
| HTTP 401 | Срок/отзыв входа; повторить `auth login` на тот же origin |
| HTTP 403 | Членство проекта и scopes; успешный login сам по себе не выдаёт чужие проекты |
| Certificate error | Правильный hostname/full chain; для корпоративного CA установите его в trust store компьютера. На Linux для контролируемого процесса допустим полный PEM через `SSL_CERT_FILE`; контейнерная настройка CA не настраивает компьютер разработчика |
| API работает, но MCP не виден | Перезапуск агента, уникальное имя подключения, корректная TOML/JSON-структура, отсутствие stdout-логов |
| Новые данные не находятся | `status`/`sync --retry`, серверный worker/index, сохранённая интеграция и подтверждение коммита |

Сервисные GitHub/GitLab/Tasktopia токены и CLI-доступ разработчика независимы. В MCP-конфиге нет ни одного из этих секретов. [Справочник env](configuration.md), [формат JSON API/MCP](native-context.md).
