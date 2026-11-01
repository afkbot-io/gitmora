# Установка Gitmora на свой сервер и домен

Ниже — новая установка на Linux с Docker Engine/Compose, nginx и публичным HTTPS. Приложение и вход работают на **одном домене**: `https://gitmora.example.com`, провайдер входа — под `/auth/`. GPU и ключ модели не нужны. Для переноса существующих данных отдельно прочитайте [перенос и смену адреса](relocation.md).

## 1. Подготовить сервер и DNS

На сервере нужны Git, Docker Engine с Compose v2, nginx, Certbot, Python 3.11+, Node.js 22.22+/npm и Go из `go.mod` (для сборки). Серверу нужен исходящий доступ для получения исходников и образов. Порты **80/443** должны быть доступны для домена; **8080/18081 и PostgreSQL наружу не открываются**.

В DNS добавьте A-запись `gitmora` на публичный IPv4 вашего сервера. AAAA добавляйте только при рабочем IPv6. Если сертификат выдаётся через HTTP-01, запросы к `/.well-known/acme-challenge/` должны доходить до этого nginx; за внешним proxy/LB настройте этот маршрут или используйте соответствующий DNS challenge.

Compose задаёт отдельные лимиты API/worker/БД и входа. Для одного хоста учтите до 2 GiB на Keycloak, 1 GiB на БД приложения, 512 MiB на БД входа, по 512 MiB на API/worker и дополнительные gateway/relay/ОС; сборкам требуется запас. Это лимиты компонентов, а не гарантия числа пользователей.

Серверные команды ниже выполняются в root-shell (`sudo -i`) на **новом** сервере; команды раздела «На компьютере разработчика» — обычным пользователем. Пути `/srv/gitmora/state` и `/srv/gitmora/identity` ещё не должны существовать. Работающий экземпляр не переинициализируется.

## 2. Получить исходники и собрать образы

```sh
git clone https://github.com/afkbot-io/gitmora.git /opt/gitmora
cd /opt/gitmora
git checkout v0.5.1
npm ci
make build build-image build-identity-image \
  RELEASE_VERSION=0.5.1 RELEASE_REVISION="$(git rev-parse HEAD)"
docker tag gitmora:local gitmora:0.5.1
docker tag gitmora-identity:26.8.1-auth gitmora-identity:0.5.1-auth
```

Получаются host-команды `bin/gitmorad`/`bin/gitmora`, API/gateway-образ `gitmora:0.5.1`, оптимизированный `/auth`-образ входа и `gitmora-postgres:18.6-security1`. Образы собираются для архитектуры сервера. Не заменяйте тег уже работающей версии другими байтами. `make build-image` готовит PostgreSQL автоматически. В runtime нет Node/npm/CDN. [Сборка и проверки](development.md).

## 3. Создать приватную конфигурацию

```sh
install -d -m 0700 /srv/gitmora
bin/gitmorad deployment init \
  --directory /srv/gitmora/state \
  --compose /opt/gitmora/compose.yaml \
  --overlay /opt/gitmora/deploy/compose.identity.yaml \
  --overlay /opt/gitmora/deploy/compose.identity-auth.yaml \
  --overlay /opt/gitmora/deploy/compose.github-com.yaml \
  --overlay /opt/gitmora/deploy/compose.gitlab-com.yaml \
  --image gitmora:0.5.1 --port 8080
install -m 0600 .env.example /srv/gitmora/state/compose.env
install -m 0600 deploy/gitmora.runtime.env.example /srv/gitmora/state/runtime.env
```

Откройте `/srv/gitmora/state/compose.env` и замените **`GITMORA_PUBLIC_HOST=gitmora.example.com`** на свой домен. Проверьте, что `GITMORA_PRIVATE_SUBNET`/`GITMORA_PRIVATE_GATEWAY` не пересекаются с VPN и другими Docker-сетями. Домен автоматически используется API и issuer в выбранном overlay. `runtime.env` пока оставляет регистрацию закрытой.

CLI автоматически читает `compose.env` при последующих `deployment up/down/status/admin`; заново экспортировать адреса не нужно. Параметры `--image/--port`, пути и порядок overlays закреплены в `installation.json`. `runtime.env` задаёт настройки приложения, а не подстановки Compose. Все переменные и секретные файлы перечислены в [справочнике конфигурации](configuration.md).

В следующих командах используйте значения из своего проверенного несекретного файла:

```sh
. /srv/gitmora/state/compose.env
```

Этот пример файла состоит из обычных shell-совместимых присваиваний. `WORKGRAPH_*` runtime-файл подставлять в shell не нужно: его пути `/run/secrets/…` относятся к контейнерам.

GitHub.com/GitLab.com overlays можно не выбирать, если они не нужны. Для своего GitLab выберите `compose.gitlab-custom.yaml` **вместо** GitLab.com и заполните его переменные в `compose.env`. Enterprise GitHub может дополнять GitHub.com. Список поддержанных профилей — ниже.

## 4. Подготовить HTTPS

Сгенерируйте конфиги для своего домена и выбранных loopback-портов:

```sh
python3 scripts/render-nginx.py \
  --domain "$GITMORA_PUBLIC_HOST" --realm "$GITMORA_REALM" \
  --api-port 8080 --identity-port 18081 \
  --output /srv/gitmora/nginx-generated
install -d -m 0755 /srv/gitmora-public /srv/gitmora-public/acme/.well-known/acme-challenge /srv/gitmora-public/downloads/cli
install -m 0644 /srv/gitmora/nginx-generated/gitmora.http.conf /etc/nginx/sites-available/gitmora.conf
ln -s /etc/nginx/sites-available/gitmora.conf /etc/nginx/sites-enabled/gitmora.conf
nginx -t
systemctl reload nginx
certbot certonly --webroot -w /srv/gitmora-public/acme -d "$GITMORA_PUBLIC_HOST"
install -m 0644 /srv/gitmora/nginx-generated/gitmora.https.conf /etc/nginx/sites-available/gitmora.conf
nginx -t
systemctl reload nginx
install -d -m 0755 /etc/letsencrypt/renewal-hooks/deploy
install -m 0755 /srv/gitmora/nginx-generated/20-gitmora-reload /etc/letsencrypt/renewal-hooks/deploy/20-gitmora-reload
certbot renew --dry-run
```

Пример путей `sites-available/sites-enabled` относится к пакетной конфигурации Debian/Ubuntu. Если ваша система использует `conf.d`, установите файл в её `http`-контекст и не создавайте второй vhost. Существующий чужой конфиг/ссылку не заменяйте. Certbot запросит email и согласие с условиями. До запуска контейнеров HTTPS может возвращать 502 на приложение/вход — ACME-маршрут уже работает независимо от них.

ACME webroot и публичные downloads находятся в `/srv/gitmora-public` с доступом nginx на чтение; приватный `/srv/gitmora` остаётся `0700`.

Генератор только создаёт новые файлы и ничего не устанавливает. Для нестандартных путей есть `--acme-root` и `--downloads-root`. TLS-конфиг самодостаточен: не требует nginx helper-файлов, которые `certbot certonly --webroot` не создаёт. Продление на хосте должно быть включено штатным timer/cron Certbot; deploy hook проверяет nginx перед reload. См. [Certbot webroot/renewal](https://eff-certbot.readthedocs.io/en/stable/using.html#webroot), [nginx TLS](https://nginx.org/en/docs/http/ngx_http_ssl_module.html).

## 5. Запустить единый вход и приложение

```sh
bin/gitmorad identity-server init \
  --directory /srv/gitmora/identity \
  --compose /opt/gitmora/deploy/identity/compose.yaml \
  --public-url "https://$GITMORA_PUBLIC_HOST/auth" \
  --application-url "https://$GITMORA_PUBLIC_HOST" \
  --realm "$GITMORA_REALM" --client-id "$GITMORA_CLIENT_ID" \
  --identity-image gitmora-identity:0.5.1-auth \
  --gateway-image gitmora:0.5.1 --port 18081
bin/gitmorad identity-server up --directory /srv/gitmora/identity
bin/gitmorad identity-server finalize --directory /srv/gitmora/identity
install -m 0444 /srv/gitmora/identity/secrets/oidc_client_secret /srv/gitmora/state/secrets/oidc_client_secret
bin/gitmorad deployment up --directory /srv/gitmora/state
bin/gitmorad deployment status --directory /srv/gitmora/state
bin/gitmorad identity-server status --directory /srv/gitmora/identity
curl -fsS "https://$GITMORA_PUBLIC_HOST/health/ready"
curl -fsS "https://$GITMORA_PUBLIC_HOST/v1/auth/config"
```

`finalize` проверяет собственный provider, закрепляет приватное администрирование и выводит bootstrap из runtime. Обычный restart не сбрасывает пароли. API получает только файл client-secret; `operator.json` провайдера в API/worker не монтируется. Worker не получает OIDC-политику. [Подробнее о входе](identity-hosting.md).

Сетевая схема: публичный nginx → `127.0.0.1:8080` → gateway/API; `/auth/` → `127.0.0.1:18081` → отдельный provider. API обращается к своему HTTPS-домену через закреплённый private host gateway с обычной проверкой сертификата. Если firewall хоста блокирует 443 с выбранной Docker-подсети, разрешите **только этот маршрут**. Не открывайте API/worker общий исходящий доступ. Профиль рассчитан на Linux Docker Engine; Docker Desktop имеет другую host-сеть и используется в тестах через отдельные fixture-маршруты.

## 6. Создать своего первого администратора

Создайте обычный аккаунт с собственным логином через скрытый ввод пароля, затем явно назначьте ему роль оператора установки. Никакой bootstrap API-токен в браузер передавать не требуется:

```sh
umask 077
bin/gitmorad users create \
  --directory /srv/gitmora/identity --installation /srv/gitmora/state \
  --username admin --name 'Администратор' \
  > /srv/gitmora/admin-account.json
# Выведите только UUID созданного локального пользователя:
python3 -c 'import json; print(json.load(open("/srv/gitmora/admin-account.json"))["user_id"])'
bin/gitmorad deployment admin --directory /srv/gitmora/state -- \
  grant-operator --user-id <UUID_ИЗ_ПРЕДЫДУЩЕЙ_КОМАНДЫ>
```

Команду `users create` запускайте в терминале; по SSH нужен `ssh -t`. Она дважды спрашивает пароль без эха, связывает точный provider subject и работает с закрытой регистрацией. Если контактный профиль запрошен при первом входе, заполните его. Затем войдите на свой домен, создайте проект, подключите репозиторий и добавьте участников. Роль оператора не даёт читать чужие проекты без членства. [Создание пользователей и регистрация](accounts.md).

Провайдер также содержит сгенерированную первоначальную учётную запись `gitmora.admin`; она не получает роль оператора Gitmora от совпадения имени. Рекомендуемый сценарий выше создаёт ваш явный аккаунт. Его пароль не читается из provider recovery-файла.

Если нужна публичная регистрация, измените в `/srv/gitmora/state/runtime.env` строку на `WORKGRAPH_REGISTRATION_ENABLED=true`, затем:

```sh
WORKGRAPH_REGISTRATION_ENABLED=true bin/gitmorad identity-server configure --directory /srv/gitmora/identity
bin/gitmorad deployment up --directory /srv/gitmora/state
```

Обычный `identity-server up` без этой переменной сохраняет уже установленную политику provider. Явно переданное значение применяет её заново. Для закрытия регистрации примените `false` и к API runtime, и к provider configure/up. Вход остаётся обязательным.

## 7. Раздать CLI и подключить агенты к своему адресу

`/install.sh` устанавливает **клиент**, не сервер. Он скачивает файлы с указанного `--server`; скрытого fallback на чужую Gitmora нет. До выдачи команды пользователям разместите на своём домене проверенные файлы CLI 0.5.1:

```sh
gitmora_cli_download=$(mktemp -d)
for gitmora_asset in gitmora_darwin_arm64 gitmora_darwin_amd64 gitmora_linux_arm64 gitmora_linux_amd64 release.json SHA256SUMS; do
  curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --location --show-error \
    --output "$gitmora_cli_download/$gitmora_asset" \
    "https://github.com/afkbot-io/gitmora/releases/download/v0.5.1/$gitmora_asset" || exit 1
done
python3 scripts/publish-cli.py --release "$gitmora_cli_download" \
  --public-root /srv/gitmora-public --version 0.5.1
```

Публикация проверяет точный состав, размер и SHA-256 каждого artifact, manifest и отсутствие лишних файлов/симлинков; затем атомарно создаёт `downloads/cli/0.5.1/`. Повтор с теми же байтами безопасен, существующая другая версия файла не перезаписывается. Скрипт ничего не исполняет и не читает приватный state. В изолированной сети заранее доставьте эти шесть файлов с доверенного build/download-хоста и выполните ту же проверку. Контрольные суммы с того же HTTPS-источника не являются независимой подписью издателя. [Сборка своих проверенных бинарников](native-installation.md#build-and-verification).

В public root не кладите state, ключи, базы или backups. После этой подготовки установка клиента и работа Gitmora на вашем домене не требуют обращения к `gitmora.tasktopia.online`.

На компьютере разработчика:

```sh
curl -fsS --proto '=https' --tlsv1.2 "https://$GITMORA_PUBLIC_HOST/install.sh" \
  | sh -s -- --server "https://$GITMORA_PUBLIC_HOST"
export PATH="$HOME/.local/bin:$PATH"
gitmora auth login --server "https://$GITMORA_PUBLIC_HOST"
gitmora api get /v1/projects --server "https://$GITMORA_PUBLIC_HOST"
```

Замените shell-переменную фактическим адресом на компьютере разработчика. Внутри клонированного репозитория выполните показанный приложением `gitmora init --server … --repository-id …`; затем включите Codex/OpenCode. У Gitmora, GitHub/GitLab разные URL: `--server` всегда указывает на **Gitmora**, не на GitLab и не на `/mcp`.

В конфиге агента MCP:

```json
{"command":"/absolute/path/to/gitmora","args":["mcp","--server","https://gitmora.example.com"]}
```

Формы Codex TOML/OpenCode JSON, системное хранилище, несколько серверов, custom CA и проверочные команды описаны в [подключении к своему серверу](custom-server.md). Токен в конфиг агента не вставляется. [Пользовательская инструкция](getting-started.md).

## Опциональные источники

| Источник | Ordered overlay / настройки |
| --- | --- |
| GitHub.com | `deploy/compose.github-com.yaml`; отдельный read token в вебе для регулярной работы |
| GitLab.com | `deploy/compose.gitlab-com.yaml` |
| Свой GitLab | Вместо GitLab.com — `deploy/compose.gitlab-custom.yaml`; `GITMORA_GITLAB_*` в `compose.env`; [инструкция](gitlab-self-managed.md) |
| GitHub Enterprise | `deploy/compose.github-enterprise.example.yaml`; `GITMORA_GITHUB_ENTERPRISE_*` в `compose.env`; [инструкция](github.md) |
| Корпоративный CA | После выбранного provider overlay — `deploy/compose.gitlab-ca.yaml`; полный публичный PEM bundle для API/worker |

Для новой установки выбирайте overlays при `deployment init` в нужном порядке, до первого запуска. Для уже работающей сначала подготовьте и проверьте полный новый профиль, сохраните backup и закреплённые параметры; не запускайте `init` повторно. Форма веба не разрешает новый сетевой адрес. У API/worker нет default Internet route; наружу ходят только credential-free relay с точными destinations. VPN/private DNS/маршрут и доверенные CA готовит оператор.

## Обновления, backups и диагностика

Сохраните исходный revision, image IDs, ordered overlays, оба приватных state-каталога и **обе** БД: приложения и входа. Обычные `deployment down`/`identity-server down` сохраняют volumes. `down -v`, очистка shared volumes и image rollback после несовместимой миграции не являются восстановлением. Проверяйте восстановленную копию до запуска worker/public ingress. Recovery primitives не заменяют полный координатор backup/restore. [Перенос](relocation.md).

| Симптом | Что проверить |
| --- | --- |
| ACME/certificate не выдаётся | DNS A/AAAA, доступ к 80, webroot, внешний proxy и DNS challenge |
| 502 после HTTPS | Запущены ли gateway/API/identity, совпадают ли loopback-порты |
| Вход не открывается | Одинаковый домен у `compose.env`, API config, provider profile и сертификата; issuer discovery из private API-сети |
| Readiness 503 | БД/миграция, matching image/config; статус обоих Compose-проектов |
| CLI installer 404 | Шесть файлов CLI лежат в нужном `downloads/cli/0.5.1/`; это не установка сервера |
| Новый источник не разрешён | Allowlist, выбранный fixed relay, authority/порт/prefix, DNS/CA, `compose.env` |
| CLI/MCP 401/403 | Вход на тот же `--server`, membership/scopes, истечение/отзыв; [диагностика](custom-server.md) |

`make smoke-deployment`, `smoke-identity-proxy`, `smoke-gitlab-profile`, `smoke-self-host-config` и `smoke-custom-server` проверяют собственные изолированные ресурсы. Это не команды для проверки чужой production-БД. Число сотрудников и реальная нагрузка требуют отдельного измерения на вашем сервере.
