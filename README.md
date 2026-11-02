# Gitmora

Сервер для хранения и поиска контекста работы локальных агентов, связанного с коммитами GitHub и GitLab.

[Сайт](https://gitmora.tasktopia.online) · [Руководство пользователя](docs/getting-started.md) · [Установка на сервер](docs/deployment.md)

## Возможности

- Запись разрешённых сессий Codex и OpenCode в локальную очередь SQLite.
- Связь контекста с точными коммитами, запросами на слияние и результатами CI.
- Поиск исходных материалов через веб-интерфейс, CLI и MCP с проверкой прав доступа.
- Синхронизация с GitHub.com, GitHub Enterprise и GitLab, включая собственные серверы.

Доступ в проект Gitmora назначается отдельно от доступа в GitHub или GitLab.
Запись включается явно для каждого репозитория и агента. При недоступности
сервера очередь остаётся для повторной отправки. Агенты используют собственные
настройки моделей. Переписка не определяет построчное авторство кода.

## Развернуть свой сервер с нуля

Понадобятся Linux, домен с A-записью на ваш сервер, открытые 80/443,
Docker Engine с Compose v2, nginx, Certbot, Git 2.43+, Python 3.12+,
Node.js 22.22+/npm и Go из `go.mod`. GPU и ключ модели серверу не нужны.
Порты БД, API и сервиса входа остаются закрыты снаружи. Требования к памяти
и сети приведены в [подготовке сервера](docs/deployment.md#1-подготовить-сервер-и-dns).

Выполните [инструкцию установки](docs/deployment.md) по порядку:

1. [Получите исходники релиза и соберите образы](docs/deployment.md#2-получить-исходники-и-собрать-образы).
2. [Создайте приватную конфигурацию](docs/deployment.md#3-создать-приватную-конфигурацию):
   `deployment init`, затем `compose.env` и `runtime.env` из примеров.
3. [Подключите домен и HTTPS](docs/deployment.md#4-подготовить-https): nginx, сертификат и продление.
4. [Запустите сервис входа и приложение](docs/deployment.md#5-запустить-единый-вход-и-приложение),
   проверьте `/health/ready`. Вход работает на том же домене под `/auth/`.
5. [Создайте первого администратора](docs/deployment.md#6-создать-своего-первого-администратора).
   Публичная регистрация по умолчанию закрыта; её можно включить через env.
6. [Опубликуйте проверенные CLI-файлы на своём домене](docs/deployment.md#7-раздать-cli-и-подключить-агенты-к-своему-адресу)
   и подключите разработчиков по следующему разделу.

[`.env.example`](.env.example) задаёт подстановки Compose, а
[`deploy/gitmora.runtime.env.example`](deploy/gitmora.runtime.env.example) — параметры приложения.
Оба файла копируются **в приватный каталог установки**, не в корень репозитория.
Секреты создаются отдельными файлами. Все переменные, значения по умолчанию
и приоритеты перечислены в [справочнике конфигурации](docs/configuration.md).
Для identity-профиля `GITMORA_PUBLIC_HOST` обязателен; чужой адрес по умолчанию не используется.

Для своих GitLab/GitHub Enterprise заранее выберите
[нужные сетевые профили](docs/deployment.md#опциональные-источники), allowlist и доверенные CA.
После установки создайте проект в вебе, подключите репозиторий по URL и токену чтения,
добавьте коллег по нику или UUID. Ключи вводятся в настройках проекта.

## Подключиться к готовой Gitmora

Откройте адрес своей установки. Войдите или создайте аккаунт, если регистрация включена.
Если она закрыта, пользователя [создаёт администратор](docs/accounts.md#create-a-user-on-a-selected-docker-installation).

На компьютере разработчика замените адрес ниже на адрес **Gitmora**:

```sh
GITMORA_SERVER=https://gitmora.example.com
curl -fsS --proto '=https' --tlsv1.2 "$GITMORA_SERVER/install.sh" \
  | sh -s -- --server "$GITMORA_SERVER"
export PATH="$HOME/.local/bin:$PATH"
gitmora version
gitmora auth login --server "$GITMORA_SERVER"
gitmora auth status --server "$GITMORA_SERVER"
```

CLI поддерживает macOS/Linux и amd64/arm64. Нужны Git 2.43+, curl и системное
хранилище секретов: Keychain в macOS или `secret-tool` и разблокированный
Secret Service/D-Bus в Linux. Пакет Ubuntu — `libsecret-tools`.
Существующий CLI с другими байтами обновляется по
[отдельной процедуре](docs/native-installation.md#deliberate-updates-and-removal).

Внутри клона рабочего репозитория выполните команду `gitmora init`, которую
показывает раздел **CLI и MCP** выбранного репозитория. Она содержит адрес
вашей Gitmora и точный UUID репозитория. Затем включите используемый агент:

```sh
gitmora agent enable codex
# В Codex откройте /hooks и разрешите проверенные команды Gitmora.
# Для OpenCode вместо предыдущей команды: gitmora agent enable opencode
gitmora status
```

Перезапустите агент и работайте через обычные `git commit` / `git push`.
При недоступности сервера очередь сохраняется; после восстановления связи
выполните `gitmora sync --retry`. `gitmora disable` отключает сбор в репозитории,
сохраняя уже накопленные записи. [Полный пользовательский сценарий](docs/getting-started.md).

Для чтения контекста через MCP добавьте в существующую конфигурацию агента
команду `gitmora mcp --server <адрес Gitmora>` с абсолютным путём к CLI.
Пароль или токен в конфиг MCP не вставляется: используется сохранённый вход.
Проверка подключения, форматы конфигурации Codex/OpenCode, собственный CA
и несколько серверов описаны в [инструкции CLI/MCP](docs/custom-server.md).

После отправки и синхронизации найдите в вебе или через MCP полный SHA либо
ссылку на MR/PR. Агент использует `context.resolve`, `context.pack` и точные
источники. GitHub/GitLab должны подтвердить коммиты; успешный `push` сам по себе
не означает завершения индексации.

## Разработка и проверки

Клиент поддерживает macOS и Linux. Для работы с Git нужен Git 2.43+;
интеграционные проверки требуют Docker Compose и Python 3.12+. Для хранения
учётных данных CLI нужен Keychain в macOS или разблокированный Secret Service
в Linux.

```sh
git clone https://github.com/afkbot-io/gitmora.git
cd gitmora
npm ci
make build
npm run test:web
npm run test:agents
npm run check:format:web
make test
make vet
make integration
```

Проверки используют синтетические данные. Браузерные, серверные и проверки
восстановления перечислены в [руководстве разработчика](docs/development.md).
[GitHub Actions](https://github.com/afkbot-io/gitmora/actions/workflows/ci.yml) проверяет исходники на Linux/macOS,
а на Linux выполняет `make verify-release` и отдельный `make smoke-load`.
Чтобы повторить полный прогон локально, установите зависимости из
[руководства разработчика](docs/development.md) и выполните те же команды.

Внешняя почта, физические телефоны и нагрузочная ёмкость рабочей установки
проверяются отдельно.

## Документация

- [Пользовательский сценарий](docs/getting-started.md), [учётная запись CLI](docs/cli-auth.md) и [нативная установка](docs/native-installation.md).
- [Установка сервера](docs/deployment.md), [сервис входа](docs/identity-hosting.md) и [перенос данных](docs/relocation.md).
- [GitHub и Enterprise](docs/github.md), [собственный GitLab](docs/gitlab-self-managed.md).
- [Адаптеры агентов](docs/collector.md), [настройка Codex](docs/codex.md) и [совместимость форматов](docs/compatibility.md).
- [Архитектура](docs/system-analysis.md), [API](docs/api.md), [проверки](docs/development.md) и [изменения](CHANGELOG.md).
- [Проверка безопасности и остаточные риски релиза](docs/security-review.md).

Для нескольких серверов, собственного центра сертификации и настройки MCP
используйте [инструкцию подключения](docs/custom-server.md). Старые имена
`workgraph` и переменные `WORKGRAPH_*` сохраняются для совместимости.

## Лицензия

Собственный код проекта распространяется по [MIT](LICENSE).
Шрифты, шаблоны и зависимости сохраняют свои лицензии, перечисленные в
[уведомлениях о сторонних компонентах](THIRD_PARTY_NOTICES.md).
