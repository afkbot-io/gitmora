# Собственный GitLab

Поддерживаются точный HTTPS-адрес, нестандартный порт и подпуть установки, например `https://code.company.ru:8443/gitlab`. Gitmora вызывает API v4 относительно этого адреса. Форматы API конкретной старой версии GitLab могут отличаться: проверяйте здоровье всех четырёх потоков, а не только успешное сохранение токена.

## Оператор: сеть и TLS

Секреты остаются в API/worker на внутренней Docker-сети. Для одного своего экземпляра предусмотрен [отдельный overlay](../deploy/compose.gitlab-custom.yaml): credential-free CONNECT relay имеет единственную выбранную цель, проверяет все ответы DNS и передаёт зашифрованный TLS. Клиент проверяет имя GitLab и его сертификат. Входящие URL/вебхуки не могут перенастроить цель.

В shell оператора задайте **несекретную сетевую политику**. Эти значения нужны для каждой операции `deployment`, которая читает Compose; `runtime.env` не подставляет переменные в сам Compose:

```sh
export GITMORA_GITLAB_URL=https://code.company.ru:8443/gitlab
export GITMORA_GITLAB_AUTHORITY=code.company.ru:8443
export GITMORA_GITLAB_VISIBILITY=private
```

`private` разрешает private/loopback IP и отвергает public/mixed DNS. Для GitLab с публичным IPv4 выберите `public`; он отвергает private/loopback/link-local/reserved DNS. Нельзя использовать `any`, wildcard или URL проекта. DNS должен разрешаться с relay, а целевой адрес — быть доступным с его сети. VPN/private DNS/маршрут до корпоративной сети предоставляет оператор; overlay их не создаёт. Для VPN с отдельным Docker attachment адаптируйте только relay, оставив API/worker внутренними. Не устанавливайте глобальный HTTP-прокси.

Для **новой** установки передайте проверенный overlay в `init`:

```sh
gitmorad deployment init \
  --directory /absolute/private/gitmora-state \
  --compose /absolute/release/compose.yaml \
  --overlay /absolute/release/deploy/compose.gitlab-custom.yaml \
  --image gitmora:<reviewed-release-tag>
gitmorad deployment up --directory /absolute/private/gitmora-state
```

Используйте реальные абсолютные пути и предварительно собранные app/PostgreSQL images; [полная установка](deployment.md). GitLab.com overlay и custom overlay описывают одну службу relay: выбирайте один из них. Поддержка нескольких репозиториев одного экземпляра не требует отдельных relay. Для нескольких разных экземпляров нужен отдельный явно проверенный сетевой профиль; эта инструкция не даёт relay произвольные цели.

В существующей установке не запускайте `init` повторно. Сохраните резервную копию, подготовьте отдельный overlay по своей топологии и обновите закреплённый список overlays штатной процедурой оператора с проверкой профиля/доступности до открытия ingress. Изменение JSON профиля без проверки Compose не является автоматическим подключением. Текущая публичная установка сохраняет действующий GitLab.com profile; никаких неизвестных корпоративных адресов в ней не разрешено.

Если GitLab использует корпоративный CA, подготовьте PEM bundle с системными roots и корпоративным CA, **без приватного ключа**, и добавьте [CA overlay](../deploy/compose.gitlab-ca.yaml) после custom overlay:

```sh
export GITMORA_GITLAB_CA_BUNDLE=/absolute/operator/trusted-ca.pem
# В новой установке добавьте второй --overlay .../deploy/compose.gitlab-ca.yaml.
```

Файл должен существовать и быть читаемым UID 65532 в контейнере; родительский каталог на хосте защищайте как прочую конфигурацию. API и worker получают одинаковый read-only bundle через `SSL_CERT_FILE`. Relay расшифровывать HTTPS не должен. Не используйте `insecure`, `-k` или отключение проверки сертификатов. Один bundle используется всеми TLS-клиентами процесса: сохраните нужные им системные roots.

## Администратор проекта: токен и репозиторий

1. Создайте ограниченный по сроку Project Access Token в GitLab → проект → Settings → Access Tokens, роль **Reporter**, scopes **read_api** и **read_repository**. Если этот тип недоступен, используйте Personal Access Token участника с чтением нужного проекта. Для создания project token нужны Maintainer/Owner; на GitLab.com доступность зависит от тарифа, на Self-Managed он доступен с любой лицензией. [Документация GitLab](https://docs.gitlab.com/user/project/settings/project_access_tokens/), [значение scopes](https://docs.gitlab.com/security/tokens/access_token_scopes/).
2. В Gitmora → «Репозитории» введите тот же **базовый URL** и числовой **Project ID**. Namespace и название проекта не включайте в базовый URL. Привязка неизменяема.
3. В «Настройках проекта» → GitLab введите API-токен и выберите «Только API, без вебхуков». Gitmora проверит доступ и точный числовой ID перед сохранением зашифрованного секрета. Не отправляйте токен в чат или URL.
4. Запросите сверку и убедитесь, что завершились MR, pipelines, child pipelines и deployments. Отсутствие CI/deployments может быть ограничением прав/версии/содержимого проекта — это отражается отдельно.
5. Webhook необязателен. Для старых GitLab можно выбрать «Секретный заголовок GitLab», для поддерживающих Standard Webhooks — HMAC. Используйте показанный путь и соответствующий одинаковый секрет; подпись не заменяется legacy fallback. [Подробный протокол](gitlab.md).

Developer credentials из CLI и серверный GitLab token независимы. Список участников GitLab не копируется автоматически: доступ к локальной реплике задаётся членством Gitmora и сервисным доступом к источнику.

## Диагностика

| Код / симптом | Действие |
| --- | --- |
| `provider_endpoint_not_allowed` | Сверьте URL/порт/подпуть каталога и allowlist API/worker. |
| `provider_unavailable` | Проверьте DNS, достижимость с relay, выбранную private/public политику, сертификат/SNI, совпадение authority с URL. Не публикуйте приватные логи/ключи. |
| `provider_unauthorized` | Истечение/отзыв токена, роль и scopes. Замените ключ в настройках. |
| `provider_project_not_found` | Числовой ID, права токена; GitLab может скрывать недоступный проект через 404. |
| Relay ready, но GitLab не работает | Readiness relay проверяет процесс; успешный токен и завершённая сверка проверяют фактический доступ. |
| Метаданные обновились, но нет сессий агента | GitLab не передаёт диалоги. Подключите локальный CLI/адаптер отдельно. |

Проверки: `make smoke-forge-subpath` выполняет весь API/worker/PostgreSQL цикл ниже `/gitlab`; Go-тест `TestPrivateGitLabTLSCustomPortPrefixAndCorporateCA` поднимает настоящий TLS-сервер и CONNECT, проверяет custom port/prefix, недоверенный/доверенный CA и запрет другого подпути. Это реальные локальные компоненты с синтетическим provider API. Живой внешний GitLab проверен отдельно для выбранного GitLab.com; универсальная совместимость со всеми версиями Self-Managed не заявляется.
