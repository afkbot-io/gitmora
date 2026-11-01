/** Authored UI translations. Source text and user content are never translated. */
export const english: Readonly<Record<string, string>> = {
  " — недоступно родителю": " — unavailable to this connection",
  "(пустое сообщение)": "(empty message)",
  ". Родитель:": ". Parent:",
  ". Учётная запись:": ". Account:",
  ". Фрагмент": ". Part",
  "1. Установите CLI": "1. Install the CLI",
  "2. Войдите и подключите репозиторий":
    "2. Sign in and connect your repository",
  "3. Включите нужный агент": "3. Enable your agent",
  "API-ключ": "API key",
  "CLI и MCP": "CLI and MCP",
  "Contents, Pull requests и Actions: Read-only. Выберите только этот репозиторий.":
    "Contents, Pull requests and Actions: Read-only. Select only this repository.",
  "GitHub → Settings → Developer settings → Fine-grained tokens. Выберите репозиторий и разрешите Contents, Pull requests и Actions: Read-only.":
    "GitHub → Settings → Developer settings → Fine-grained tokens. Select the repository and allow Contents, Pull requests and Actions: Read-only.",
  "GitLab → Settings → Access tokens. Создайте токен с правом read_api и доступом к этому проекту.":
    "GitLab → Settings → Access tokens. Create a token with read_api permission and access to this project.",
  "Gitmora связывает сессии агентов с коммитами и MR/PR. Находите решения команды и продолжайте работу с нужным контекстом.":
    "Gitmora connects agent sessions to commits and merge requests. Find your team’s decisions and continue with the context you need.",
  "Gitmora — история разработки": "Gitmora — development history",
  "IID превышает точный числовой диапазон интерфейса.":
    "The IID exceeds the interface's exact numeric range.",
  "MCP использует ваш вход в CLI. После настройки перезапустите агент.":
    "MCP uses your CLI sign-in. Restart the agent after setup.",
  "MR и PR": "Merge and pull requests",
  "MR/PR ещё не синхронизирован. Проверьте подключение репозитория.":
    "This MR/PR has not synced yet. Check the repository connection.",
  "MR/PR пока недоступен. Проверьте подключение репозитория.":
    "This MR/PR is unavailable. Check the repository connection.",
  "MR/PR появятся после синхронизации репозитория.":
    "Merge and pull requests appear after the repository syncs.",
  "UUID пользователя": "User UUID",
  "macOS и Linux": "macOS and Linux",
  "{0} дн.": "{0} days",
  "{0} мин.": "{0} min",
  Агент: "Agent",
  "Агент в источнике:": "Reported agent:",
  Администратор: "Administrator",
  "Администратор Gitmora должен разрешить подключение к этому серверу.":
    "A Gitmora administrator needs to allow connections to this server.",
  "Администратор сервера должен настроить шифрование и разрешённые адреса интеграций.":
    "Ask the server administrator to configure encryption and allowed integration servers.",
  "Адрес вебхука": "Webhook URL",
  "Без токена доступен небольшой общий лимит GitHub API.":
    "Without a token, GitHub applies a small shared API limit.",
  "Блоки вне текстового импорта:": "Blocks outside this text import:",
  "Браузерная сессия": "Browser session",
  "Будет удалено членство в этом проекте. Учётная запись и доступ к другим проектам сохранятся.":
    "Project membership will be removed. The account and other project access will remain.",
  "Будут отозваны этот ключ и все его дочерние ключи.":
    "This key and all its child keys will be revoked.",
  "В MR/PR появились новые коммиты.": "This MR/PR has new commits.",
  "В папке репозитория": "Inside your repository folder",
  "В этой части совпадений нет.": "No matches in this part.",
  "В этой части только действия инструментов.":
    "This part only contains tool activity.",
  "Введите название ключа длиной до 120 байт UTF-8.":
    "Enter a key name of up to 120 UTF-8 bytes.",
  "Введите номер ревизии от 1 до 1000000000.":
    "Enter a revision number from 1 to 1000000000.",
  "Введите поисковый запрос или полный SHA коммита.":
    "Enter a search query or the full commit SHA.",
  "Введите полный код из терминала: XXXX-XXXX.":
    "Enter the complete code from your terminal: XXXX-XXXX.",
  "Введите срок целым положительным числом минут.":
    "Enter a positive whole number of minutes.",
  Вебхуки: "Webhooks",
  "Вернуться в Gitmora": "Back to Gitmora",
  Версия: "Version",
  "Версия или идентификатор операции изменились. Обновите данные перед новым изменением.":
    "The version or operation ID changed. Refresh the data before editing.",
  "Включите запись сессий в CLI": "Enable session recording in the CLI",
  Войти: "Sign in",
  "Войти через организацию заново": "Sign in again",
  "Впервые здесь?": "New here?",
  "Время неизвестно": "Unknown time",
  "Все доступные коммиты": "All available commits",
  "Все источники": "All sources",
  "Все репозитории": "All repositories",
  "Вставьте полный SHA коммита.": "Paste the full commit SHA.",
  "Вставьте ссылку на репозиторий. Адрес сервера и ID определим автоматически.":
    "Paste a repository URL. We will detect its server and ID.",
  "Вход CLI через организацию не настроен на этом сервере.":
    "CLI sign-in is not configured on this server.",
  "Вход в Gitmora": "Sign in to Gitmora",
  "Вход не настроен. Обратитесь к администратору сервера.":
    "Sign-in is not configured. Contact the server administrator.",
  "Вход пока недоступен. Обратитесь к администратору команды.":
    "Sign-in is unavailable. Contact your team administrator.",
  Вы: "You",
  "Выберите поддерживаемую платформу CLI.": "Select a supported CLI platform.",
  "Выберите проект": "Choose a project",
  "Выберите проект, к которому у вас есть доступ.":
    "Choose a project you can access.",
  "Выберите репозиторий в разделе «Репозитории», чтобы получить команду подключения.":
    "Choose a repository under Repositories to get its connection command.",
  "Выберите хотя бы одно разрешение.": "Select at least one permission.",
  "Выданный ключ истёк или был отозван. Результат выпуска скрыт.":
    "The issued key expired or was revoked. Its secret has been hidden.",
  "Выделите команду и скопируйте вручную.":
    "Select the command and copy it manually.",
  Выйти: "Sign out",
  "Выполняем…": "Working…",
  Выполняется: "Running",
  "Выпускать и отзывать свои ключи": "Issue and revoke your keys",
  "Выпустить ключ": "Issue key",
  "Выпустить личный ключ": "Issue personal key",
  "Готово к проверке": "Ready for review",
  "Данные изменились. Обновите страницу и повторите действие.":
    "The data has changed. Refresh and try again.",
  "Данные источника. Авторство указано внешним инструментом; строки без меток имеют неизвестное происхождение.":
    "Source data. Attribution comes from an external tool; unmarked lines have unknown authorship.",
  "Действия агента: {0}": "Agent actions: {0}",
  "Действия проекта": "Project actions",
  Действует: "Active",
  "Действует до": "Expires",
  "Диалог с агентом": "Conversation with agent",
  "Диалоги к этому изменению": "Conversations behind this change",
  "Диапазоны относятся к коммиту": "Ranges refer to commit",
  "Диапазоны, указанные источником:": "Source-reported ranges:",
  "Для закрытого репозитория нужен токен чтения.":
    "A private repository requires a read token.",
  "Для новой попытки начните новый вход в терминале.":
    "Start a new sign-in from your terminal to try again.",
  "Для подтверждения CLI войдите через организацию заново по ссылке из терминала.":
    "Sign in again using the link from your terminal to approve CLI access.",
  "Для этих коммитов ещё нет записанных сессий.":
    "No sessions have been recorded for these commits.",
  "Добавить репозиторий": "Add repository",
  "Добавить участника": "Add member",
  "Добавьте GitHub или GitLab, чтобы сохранять историю разработки.":
    "Connect GitHub or GitLab to start saving development history.",
  "Добавьте коллегу по нику или UUID.": "Add a teammate by username or UUID.",
  Дополнительно: "Advanced",
  "Достигнут лимит API сервиса. Проверьте токен или дождитесь следующей попытки.":
    "The service API limit was reached. Check the token or wait for the next attempt.",
  "Достигнут общий лимит GitHub. Подключите токен чтения для регулярной синхронизации.":
    "GitHub's shared limit has been reached. Connect a read token for regular synchronization.",
  "Доступ терминала сохранится после выхода из браузера. Отозвать его можно в личных настройках.":
    "Terminal access remains active after browser sign-out. Revoke it in personal settings.",
  "За доступом обратитесь к администратору команды.":
    "Contact your team administrator for access.",
  "Загружаем проекты…": "Loading projects…",
  "Загружаем репозитории…": "Loading repositories…",
  "Загружаем фрагмент…": "Loading fragment…",
  "Загружено сообщений: {0}": "Messages loaded: {0}",
  "Загрузить ещё": "Load more",
  "Загрузить ещё ключи": "Load more keys",
  "Загрузить ещё репозитории": "Load more repositories",
  "Загрузить ещё связи": "Load more links",
  "Загрузить ещё сообщения": "Load more messages",
  "Загрузить ещё участников": "Load more members",
  "Загрузка подтверждения CLI…": "Loading CLI confirmation…",
  "Загрузка…": "Loading…",
  Закрыт: "Closed",
  "Запись не найдена или больше недоступна.":
    "The record was not found or is no longer available.",
  "Запись сессии": "Session record",
  "Запрашиваемые права": "Requested permissions",
  "Запрос CLI изменился или уже завершён. Обновите его состояние перед новым решением.":
    "The CLI request changed or has ended. Refresh its status before deciding.",
  "Запрос CLI истёк, отменён или ключ больше не действует. Проверьте состояние в терминале.":
    "The CLI request expired, was cancelled or is no longer valid. Check your terminal.",
  "Запрос CLI не совпал с поддерживаемыми условиями подключения.":
    "This CLI request uses unsupported connection settings.",
  "Запрос должен занимать не более 512 байт UTF-8.":
    "The query must be no longer than 512 UTF-8 bytes.",
  "Запрос или ссылка на MR/PR": "Query or MR/PR URL",
  "Запрос отклонён. Ключ не создан.": "Request denied. No key was created.",
  "Запрос отменён в терминале. Ключ не создан.":
    "Request cancelled in the terminal. No key was created.",
  "Запрос превышает допустимый размер.": "The request exceeds the size limit.",
  "Запрошенный срок превышает 90 дней или оставшийся срок текущего ключа или сессии.":
    "The requested lifetime exceeds 90 days or the remaining connection lifetime.",
  Идентификатор: "Identifier",
  "Идентификатор проекта": "Project ID",
  "Идентификаторы источника": "Source identifiers",
  "Изменения в коде": "Code changes",
  "Изменить доступ": "Edit access",
  "Импорт:": "Import:",
  "Индекс обновляется": "Index updating",
  "Инструкция сервиса": "Provider instructions",
  "Историческая версия": "Historical version",
  "Исторические версии": "Historical versions",
  "История Gitmora будет удалена. Репозитории в GitHub и GitLab сохранятся.":
    "Gitmora history will be deleted. Your GitHub and GitLab repositories will remain.",
  "История разработки под рукой.": "Your development history, at hand.",
  Источник: "Source",
  "Источник SHA-256": "Source SHA-256",
  "Источник не содержит однозначной области навигации.":
    "The source does not identify a single navigation scope.",
  "Источник ссылается на внешние transcripts. Их содержимое в эту запись не загружалось.":
    "The source references external transcripts. Their contents were not imported into this record.",
  "Источник указал AI: {0} / {1}": "Source reports AI: {0} / {1}",
  "Источник указал человека: {0}": "Source reports human: {0}",
  "Источник:": "Source:",
  Истёк: "Expired",
  "Исходная запись": "Original record",
  "Ищем…": "Searching…",
  "К содержимому": "Skip to content",
  "Как получить токен": "How to create a token",
  "Клиенты с этими ключами потеряют доступ.":
    "Clients using these keys will lose access.",
  "Ключ действует до": "Key expires",
  "Ключ создан, секрет недоступен": "Key created; secret unavailable",
  "Ключи для автоматизации": "Automation keys",
  "Ключи и браузерные сессии вашей учётной записи. Доступ к проектам проверяется отдельно.":
    "Keys and browser sessions for your account. Project access is checked separately.",
  "Код входа CLI": "CLI sign-in code",
  "Код коммита": "Commit code",
  "Код репозитория": "Repository code",
  "Код:": "Code:",
  "Команда / входные данные": "Command / input",
  Коммит: "Commit",
  "Коммит ещё не проверен": "Commit not verified yet",
  "Коммит репозитория": "Repository commit",
  Коммиты: "Commits",
  "Коммиты, указанные агентом:": "Agent-reported commits:",
  "Контекст MR/PR": "MR/PR context",
  "Контекст коммита": "Commit context",
  "Контекст не совпал с выбранным источником.":
    "The context does not match the selected source.",
  Копировать: "Copy",
  "Копировать: {0}": "Copy: {0}",
  "Корневой ключ": "Root key",
  "Личные ключи": "Personal keys",
  "Личные настройки": "Personal settings",
  "Назад к поиску": "Back to search",
  "Название в Gitmora": "Name in Gitmora",
  "Название и удаление": "Name and deletion",
  "Название ключа": "Key name",
  "Название проекта": "Project name",
  "Название сохранено.": "Name saved.",
  "Найдено фрагментов: {0}": "Excerpts found: {0}",
  Найти: "Search",
  "Найти в загруженных частях": "Find in loaded parts",
  "Например, alex…": "For example, alex…",
  "Например, локальный сборщик": "For example, local collector",
  "Например, платформа или продукт…": "For example, a platform or product…",
  "Настраивать интеграции проекта": "Configure project integrations",
  Настроить: "Configure",
  "Настроить CLI и MCP": "Set up CLI and MCP",
  "Настройки проекта": "Project settings",
  "Не удалось войти. Повторите попытку или обратитесь к администратору.":
    "Could not sign in. Try again or contact your administrator.",
  "Не удалось выполнить запрос. Повторите попытку.":
    "The request failed. Try again.",
  "Не удалось завершить сессию.": "Could not end your session.",
  "Не удалось проверить доступ к внешнему сервису. Проверьте адрес, ключ и его права.":
    "Could not verify external service access. Check the address, key and permissions.",
  "Не указана": "Not specified",
  "Недавние сессии": "Recent sessions",
  "Недопустимый адрес подключения или идентификатор репозитория.":
    "Invalid server address or repository ID.",
  "Недопустимый идентификатор репозитория.": "Invalid repository ID.",
  "Недостаточно прав для этого действия.":
    "You do not have permission for this action.",
  "Некорректная область списка источников.": "Invalid source list scope.",
  "Нельзя передать больше прав, чем у текущего ключа.":
    "You cannot grant more permissions than this connection has.",
  "Нет доступа к контексту": "No context access",
  "Нет доступа к сессиям.": "You do not have access to sessions.",
  "Ник для приглашений": "Invitation username",
  "Ник или UUID пользователя": "Username or user UUID",
  "Ничего не найдено": "No results",
  "Новое название репозитория": "New repository name",
  "Новые данные перестанут поступать. Сохранённые сессии останутся в Gitmora.":
    "New data will stop arriving. Saved sessions will remain in Gitmora.",
  "Новые сообщения появятся ниже.": "New messages will appear below.",
  "Новый API-ключ": "New API key",
  "Новый ключ превышает полномочия, срок или глубину текущего подключения. Проверьте ограничения родителя.":
    "The key exceeds this connection's permissions or lifetime. Check its parent limits.",
  "Новый проект": "New project",
  "Нужны Git 2.43+ и curl. Для входа на Linux установите libsecret-tools и запустите Secret Service в своей пользовательской сессии.":
    "Requires Git 2.43+ and curl. For Linux sign-in, install libsecret-tools and run Secret Service in your user session.",
  "Нужны read_api и доступ к этому проекту GitLab.":
    "Requires read_api and access to this GitLab project.",
  "Обновите список или выпустите ограниченный ключ.":
    "Refresh the list or issue a restricted key.",
  Обновить: "Refresh",
  "Обновить запрос CLI": "Refresh CLI request",
  "Обновить состояние": "Refresh status",
  "Обновить список ключей": "Refresh keys",
  "Обновить участников": "Refresh members",
  Обновлено: "Updated",
  "Обсуждение и изменения": "Discussion and changes",
  "Объедините репозитории и команду в одном проекте.":
    "Bring your repositories and team together in one project.",
  Объект: "Object",
  Ожидает: "Pending",
  "Окно подтверждения": "Confirmation deadline",
  "Окно подтверждения истекло. Проверьте состояние входа в терминале.":
    "The confirmation has expired. Check sign-in status in your terminal.",
  "Остановить сбор в этом репозитории": "Stop collection in this repository",
  "Ответ не совпал с выбранной сессией и ревизией.":
    "The response does not match this session and revision.",
  "Ответ не совпал с выбранным источником.":
    "The response does not match the selected source.",
  "Ответ не совпал с подтверждённым запросом.":
    "The response does not match the confirmed request.",
  "Отклонить запрос CLI…": "Deny CLI request…",
  "Отклонить запрос терминала?": "Deny terminal request?",
  Отключено: "Disconnected",
  "Отключить {0}": "Disconnect {0}",
  "Отключить {0}?": "Disconnect {0}?",
  Открыт: "Open",
  "Открыть в {0}": "Open in {0}",
  "Открыть версию": "Open version",
  "Открыть контекст": "Open context",
  "Открыть последнюю версию": "Open latest version",
  "Открыть проект": "Open project",
  Отмена: "Cancel",
  Отменено: "Canceled",
  Отозван: "Revoked",
  "Отозвать {0}?": "Revoke {0}?",
  "Отозвать выданный ключ?": "Revoke issued key?",
  "Отозвать ключ": "Revoke key",
  "Отозвать ключ и потомков": "Revoke key and child keys",
  "Отозвать этот выданный ключ": "Revoke this issued key",
  "Отправлять сессии агента": "Upload agent sessions",
  Оформление: "Appearance",
  Ошибка: "Failed",
  "Ошибка в записи": "Recorded error",
  "Перезапустите OpenCode после подключения.":
    "Restart OpenCode after connecting.",
  "Повторить выход": "Retry sign-out",
  "Повторить загрузку": "Retry loading",
  "Повторить загрузку репозиториев": "Retry loading repositories",
  "Повторить чтение": "Retry loading",
  "Подключаемся…": "Connecting…",
  "Подключаем…": "Connecting…",
  "Подключение агента": "Connect your agent",
  "Подключение сохранено.": "Connection saved.",
  Подключено: "Connected",
  "Подключите CLI и запишите первую сессию агента.":
    "Connect the CLI and record your first agent session.",
  "Подключите GitHub или GitLab": "Connect GitHub or GitLab",
  "Подключите исходный репозиторий, чтобы открыть связанные сессии.":
    "Connect the source repository to open its sessions.",
  "Подключите первый репозиторий": "Connect your first repository",
  "Подключить CLI и локальных агентов": "Connect CLI and local agents",
  "Подключить репозиторий": "Connect repository",
  "Подключить терминал": "Connect terminal",
  "Подключить токен": "Connect token",
  "Подробности вызова": "Call details",
  Подтвердить: "Confirm",
  "Подтвердить вход CLI": "Confirm sign-in",
  "Подтвердить отказ CLI": "Confirm denial",
  "Подтверждение CLI не совпало с текущей учётной записью или состоянием запроса.":
    "The CLI confirmation no longer matches this account or request.",
  "Подтверждение входа CLI": "CLI sign-in confirmation",
  "Подтверждение принято. Проверьте завершение входа в терминале.":
    "Approved. Check that sign-in completes in your terminal.",
  "Подтверждено провайдером:": "Confirmed by provider:",
  "Подтверждён провайдером": "Verified by provider",
  Поиск: "Search",
  "Поиск по истории": "Search history",
  "Поиск через MCP": "Search through MCP",
  "Поиск…": "Searching…",
  "Показаны первые 50 коммитов.": "Showing the first 50 commits.",
  "Показать ещё": "Show more",
  "Попробуйте другие слова или полный SHA коммита.":
    "Try other words or a full commit SHA.",
  "Последняя версия": "Latest version",
  "Последняя сохранённая версия": "Latest saved version",
  "Права и срок ключа ограничены текущей сессией. Выход завершит работу выданных через неё ключей.":
    "Key permissions and lifetime are limited by this session. Signing out revokes keys issued through it.",
  "Права токена": "Token permissions",
  "Предыдущие диапазоны": "Previous ranges",
  "Привязка уже существует или её версия изменилась. Обновите OIDC-привязки перед новым изменением.":
    "The binding already exists or has changed. Refresh the list before editing.",
  "Пример: проект, сессия агента, коммит и merge request":
    "Example: project, agent session, commit and merge request",
  "Пример: работа над входом": "Example: updating sign-in",
  "Проверить подключение": "Check connection",
  "Проверки CI": "CI checks",
  "Проверьте введённые данные и параметры запроса.":
    "Check the entered details and request parameters.",
  "Проверьте данные репозитория.": "Check the repository details.",
  "Проверьте данные.": "Check the entered details.",
  "Проверьте параметры запроса.": "Check the request parameters.",
  "Проверяем…": "Checking…",
  "Продолжение диалога · часть {0}": "Conversation continued · part {0}",
  Проект: "Project",
  "Происхождение импорта": "Import provenance",
  Пропущено: "Skipped",
  Профиль: "Profile",
  "Профиль сохранён.": "Profile saved.",
  "Публичный репозиторий без токена": "Public repository without a token",
  "Пути, указанные агентом:": "Agent-reported paths:",
  "Работайте как обычно: git commit и git push отправят историю в Gitmora.":
    "Work as usual: git commit and git push will send your history to Gitmora.",
  "Развернуть сообщение": "Expand message",
  "Разделы проекта": "Project sections",
  "Разрешения нового ключа": "New key permissions",
  "Разрешите команды Gitmora через /hooks в Codex и перезапустите сессию.":
    "Approve Gitmora commands in Codex using /hooks, then restart the session.",
  "Разрешить вход CLI…": "Allow CLI sign-in…",
  "Разрешить вход терминала?": "Allow terminal sign-in?",
  Ревизия: "Revision",
  "Ревизия сессии": "Session revision",
  Результат: "Result",
  "Результат выпуска ключа": "Key issuance result",
  "Результаты поиска": "Search results",
  "Резюме агента": "Agent summary",
  Репозитории: "Repositories",
  Репозиторий: "Repository",
  "Репозиторий добавлен. Введите действующий токен, чтобы завершить подключение.":
    "Repository added. Enter a valid token to finish connecting.",
  "Репозиторий не найден. Проверьте ссылку и доступ токена.":
    "Repository not found. Check its URL and the token's access.",
  Родитель: "Parent",
  Роль: "Role",
  "Роль нового участника": "New member role",
  Русский: "Русский",
  "Сведения об источнике": "Source details",
  Свернуть: "Collapse",
  "Сверьте этот код с кодом в вашем терминале. Подтверждайте только вход, который начали сами.":
    "Compare this code with your terminal. Approve only a sign-in you started yourself.",
  "Связанные сессии": "Linked sessions",
  "Сейчас доступно не более": "Available now: up to",
  "Секрет вебхука": "Webhook secret",
  "Секретный заголовок": "Secret header",
  "Сервер вернул источник другого проекта.":
    "The server returned a source from another project.",
  "Сервер вернул источники другого проекта.":
    "The server returned sources from another project.",
  "Сервер вернул неполный ответ. Повторите запрос.":
    "The server returned an incomplete response. Try again.",
  "Сервер вернул репозитории другого проекта.":
    "The server returned repositories from another project.",
  "Сервер не ответил вовремя. Повторите запрос.":
    "The server did not respond in time. Try again.",
  "Сервер недоступен. Проверьте соединение и повторите запрос.":
    "The server is unavailable. Check your connection and try again.",
  "Сервер подтвердил повтор ранее принятой операции. Исходный секрет не хранится в восстанавливаемом виде. Отзовите этот ключ и выпустите новый, если первый ответ не был сохранён.":
    "This operation was already accepted. The secret cannot be retrieved again. Revoke the key and issue another if you did not save the first response.",
  Сервис: "Service",
  "Сервис вернул неподдерживаемый ответ. Проверьте адрес сервера и его версию.":
    "The service returned an unsupported response. Check the server URL and version.",
  "Сервис временно недоступен. Повторите позже.":
    "The service is temporarily unavailable. Try again later.",
  "Сервис входа временно недоступен. Повторите попытку позже.":
    "The sign-in service is unavailable. Try again later.",
  "Сессии агентов, коммиты и код проекта.":
    "Agent sessions, commits and project code.",
  "Сессии и история этого репозитория будут удалены из Gitmora.":
    "This repository’s sessions and history will be deleted from Gitmora.",
  Сессия: "Session",
  "Сессия агента": "Agent session",
  "Сессия завершена. Войдите снова.": "Your session has ended. Sign in again.",
  "Сессия истекла. Войдите снова.": "Your session has expired. Sign in again.",
  "Сессия источника:": "Source session:",
  "Сессия:": "Session:",
  "Символы {0}–{1}": "Characters {0}–{1}",
  "Синхронизация запущена.": "Synchronization started.",
  "Синхронизация недоступна. Проверьте подключение.":
    "Synchronization is unavailable. Check the connection.",
  Синхронизировать: "Synchronize",
  "Системные требования и загрузки": "Requirements and downloads",
  "Скопировано.": "Copied.",
  "Скопируйте его в свой менеджер секретов. После закрытия или перехода на другую страницу интерфейс больше не покажет этот секрет.":
    "Copy it to your password manager. The secret will not be shown again after closing this view or navigating away.",
  "Скрыть результат выпуска": "Close key result",
  "Скрыть уведомление": "Dismiss notification",
  "Следующая попытка": "Next attempt",
  "Следующие MR": "Next MRs",
  "Следующие диапазоны": "Next ranges",
  Слит: "Merged",
  "Слишком много запросов. Повторите позже.":
    "Too many requests. Try again later.",
  "Слишком много попыток входа. Повторите позже.":
    "Too many sign-in attempts. Try again later.",
  "Сначала назначьте другого действующего администратора проекта.":
    "Assign another active project administrator first.",
  "Сначала назначьте другого действующего оператора установки.":
    "Assign another active Gitmora administrator first.",
  "Сначала сохраните или отзовите ключ и скройте результат предыдущего выпуска.":
    "Save or revoke the previous key, then close its result before issuing another.",
  "Снимок ref": "Ref snapshot",
  "Создайте первый проект": "Create your first project",
  "Создайте проект": "Create a project",
  "Создать аккаунт": "Create account",
  "Создать проект": "Create project",
  "Создаём…": "Creating…",
  "Соответствующая браузерная сессия завершится.":
    "The associated browser session will end.",
  "Состав MR/PR изменился. Обновите контекст.":
    "MR/PR commits changed. Refresh the context.",
  "Состояние синхронизации": "Synchronization status",
  Сохранено: "Saved",
  "Сохраните ключ сейчас": "Save your key now",
  "Сохранить название": "Save name",
  "Сохранить подключение": "Save connection",
  "Сохранить профиль": "Save profile",
  "Сохранить роль": "Save role",
  "Список коммитов обновляется.": "The commit list is updating.",
  "Список пуст": "No keys",
  "Способ проверки": "Verification method",
  "Срок в минутах": "Lifetime in minutes",
  "Срок ключа": "Key lifetime",
  "Ссылка на MR/PR, полный SHA или текст…": "MR/PR URL, full SHA or text…",
  "Ссылка на код": "Code reference",
  "Ссылка на репозиторий": "Repository URL",
  "Ссылка относится к другому репозиторию. Смените фильтр.":
    "This URL belongs to another repository. Change the filter.",
  "Статус и отключение": "Status and disabling capture",
  "Строки {0}–{1}": "Lines {0}–{1}",
  "Текущая сессия не предоставляет все запрошенные права. Можно отклонить запрос.":
    "Your session does not have all requested permissions. You can deny the request.",
  "Текущее подключение": "Current connection",
  "Текущее подключение завершится.": "Your current connection will end.",
  "Терминал получит перечисленные права на указанный срок. Ключ останется отдельным от браузерной сессии.":
    "The terminal will receive these permissions for the stated lifetime, independently of your browser session.",
  "Токен не принят. Проверьте его срок действия и сохраните новый.":
    "The token was rejected. Check its expiry and save a new one.",
  "Токен чтения": "Read token",
  "Только API": "API only",
  "Только переписка": "Conversation only",
  "Точная версия источника": "Exact source version",
  "Точный источник": "Exact source",
  "Требует внимания": "Needs attention",
  "Тёмная тема": "Dark theme",
  "У токена нет доступа. Разрешите чтение репозитория, запросов на слияние и проверок CI.":
    "The token has no access. Allow read access to the repository, pull requests and CI checks.",
  "Убрать доступ": "Remove access",
  "Убрать доступ {0}?": "Remove access for {0}?",
  "Удаление проекта": "Delete project",
  "Удалить {0}?": "Delete {0}?",
  "Удалить проект": "Delete project",
  "Удалить проект {0}?": "Delete project {0}?",
  "Удалить проект и его данные": "Delete project and data",
  "Удалить репозиторий": "Delete repository",
  "Удалить репозиторий и его данные": "Delete repository and its data",
  "Удаляет историю и настройки проекта в Gitmora.":
    "Deletes this project's Gitmora history and settings.",
  "Управлять каталогом проекта": "Manage the project catalog",
  "Управлять своими ключами": "Manage your keys",
  "Управлять установкой при наличии роли оператора":
    "Manage Gitmora with an administrator role",
  Успешно: "Succeeded",
  "Устройство:": "Device:",
  "Уточните запрос, чтобы увидеть нужный результат.":
    "Refine your query to find the right result.",
  Участник: "Contributor",
  "Участник добавлен.": "Member added.",
  Участники: "Members",
  "Участников нет": "No members",
  "Участнику нужна учётная запись Gitmora.":
    "Your teammate needs a Gitmora account.",
  "Учётная запись выключена": "Account disabled",
  "Учётная запись:": "Account:",
  Файл: "File",
  Формат: "Format",
  "Фрагмент SHA-256": "Fragment SHA-256",
  "Фрагмент источника": "Source fragment",
  "Фрагмент прочитан до конца": "End of fragment",
  "Частей загружено: {0}": "Parts loaded: {0}",
  "Часть {0}": "Part {0}",
  "Читаем запись сессии…": "Loading session…",
  "Читаем условия подключения…": "Loading connection details…",
  "Читать данные репозиториев": "Read repository data",
  "Читать диалог": "Read conversation",
  "Читать доступные проекты и репозитории":
    "Read available projects and repositories",
  "Читать интеграции и внешние источники":
    "Read integrations and external sources",
  "Читать каталог и участников": "Read the catalog and members",
  "Читать сессии агента": "Read agent sessions",
  Чтение: "Read only",
  "Этот запрос уже завершён. Новый вход начинается в терминале.":
    "This request has ended. Start a new sign-in from your terminal.",
  "Этот идентификатор уже удалён. Создайте новую запись.":
    "This ID has been deleted. Create a new record.",
  "Этот ключ и все его дочерние ключи перестанут работать.":
    "This key and all its child keys will stop working.",
  "Этот ключ не добавляет членство в проектах и не меняет вашу роль.":
    "This key does not add project memberships or change your role.",
  "Этот ник уже занят. Выберите другой.":
    "This username is taken. Choose another one.",
  "Этот пользователь уже в проекте.": "This user is already a project member.",
  Язык: "Language",
  "Язык интерфейса": "Interface language",
  "вне точного диапазона интерфейса":
    "outside the interface's exact numeric range",
  из: "of",
  "корневой ключ": "root key",
  "полных минут. Браузерная сессия ограничивает срок её дочерних ключей; более долгий корневой ключ выдаётся отдельной доверенной процедурой.":
    "whole minutes. A browser session limits its child keys' lifetime; longer-lived root keys require trusted server administration.",
};
