/** Authored English interface messages. */
export const english: Readonly<Record<string, string>> = {
  " / резюме агента": " / agent summary",
  " · Изменено в источнике": " · Updated at source",
  " Проверка ограничена глубиной.": " Verification depth is limited.",
  " — недоступно родителю": " — unavailable to this connection",
  "(пустое сообщение)": "(empty message)",
  ", открытых:": ", open:",
  ". Корень, указанный источником:": ". Source-reported root:",
  ". Ошибки индексации:": ". Indexing errors:",
  ". Подтверждённых коммитов этой ревизии:":
    ". Verified commits in this revision:",
  ". Полная запись доступна в источнике.":
    ". The full record is available from the source.",
  ". Родитель:": ". Parent:",
  ". Учётная запись:": ". Account:",
  ". Фрагмент": ". Part",
  ". Цитаты могут появиться позже.": ". Source fragments may appear later.",
  "1. Установите CLI": "1. Install the CLI",
  "2. Войдите и подключите репозиторий":
    "2. Sign in and connect your repository",
  "3. Включите нужный агент": "3. Enable your agent",
  "API-ключ": "API key",
  "CI для наблюдаемого head": "CI for observed head",
  "CLI и MCP": "CLI and MCP",
  "Contents, Pull requests и Actions: Read-only. Выберите только этот репозиторий.":
    "Contents, Pull requests and Actions: Read-only. Select only this repository.",
  "GitHub → Settings → Developer settings → Fine-grained tokens. Выберите репозиторий и разрешите Contents, Pull requests и Actions: Read-only.":
    "GitHub → Settings → Developer settings → Fine-grained tokens. Select the repository and allow Contents, Pull requests and Actions: Read-only.",
  "GitLab → Settings → Access tokens. Создайте токен с правом read_api и доступом к этому проекту.":
    "GitLab → Settings → Access tokens. Create a token with read_api permission and access to this project.",
  "Gitmora связывает сессии агентов, коммиты и задачи в одну историю. Находите решения команды и продолжайте работу с нужным контекстом.":
    "Gitmora connects agent sessions, commits and tasks into one history. Find your team's decisions and continue with the context you need.",
  "Gitmora — история разработки": "Gitmora — development history",
  "Head MR изменился относительно выбранного SHA. Приведённые CI относятся к наблюдаемому head, а не к ожидавшемуся.":
    "The MR head has changed from the selected SHA. These CI results apply to the observed head.",
  "ID репозитория источника": "Source repository ID",
  "IID превышает точный числовой диапазон интерфейса.":
    "The IID exceeds the interface's exact numeric range.",
  "MCP использует ваш вход в CLI. После настройки перезапустите агент.":
    "MCP uses your CLI sign-in. Restart the agent after setup.",
  "Tasktopia подключена.": "Tasktopia connected.",
  "UUID пользователя": "User UUID",
  "UUID пространства Tasktopia": "Tasktopia workspace UUID",
  "macOS и Linux": "macOS and Linux",
  "{0} дн.": "{0} days",
  "{0} мин.": "{0} min",
  "· Выполнение по Tasktopia:": "· Tasktopia completion:",
  Агент: "Agent",
  "Агент / модель": "Agent / model",
  "Агент в источнике:": "Reported agent:",
  Администратор: "Administrator",
  "Администратор Gitmora должен разрешить подключение к этому серверу.":
    "A Gitmora administrator needs to allow connections to this server.",
  "Администратор сервера должен настроить шифрование и разрешённые адреса интеграций.":
    "Ask the server administrator to configure encryption and allowed integration servers.",
  "Адрес вебхука": "Webhook URL",
  "Адрес и страна Tasktopia уже закреплены за проектом. Проверьте исходную привязку.":
    "This project's Tasktopia server and country are already fixed. Check the existing connection.",
  "Адрес репозитория недоступен": "Repository URL is unavailable",
  "Без токена доступен небольшой общий лимит GitHub API.":
    "Without a token, GitHub applies a small shared API limit.",
  "Блоки вне текстового импорта:": "Blocks outside this text import:",
  "Браузерная сессия": "Browser session",
  "Будет удалено членство в этом проекте. Учётная запись и доступ к другим проектам сохранятся.":
    "Project membership will be removed. The account and other project access will remain.",
  "Будут отозваны этот ключ и все его дочерние ключи.":
    "This key and all its child keys will be revoked.",
  "Будут удалены запись репозитория и связанные локальные сессии, индексы и результаты. Исходный репозиторий останется у вашего провайдера.":
    "The repository record and its local sessions, indexes and results will be deleted. The source repository stays with your provider.",
  "В Tasktopia откройте настройки → MCP-интеграция и создайте ключ с country:read и tasks:read. UUID страны можно получить через MCP country.list. Для добавления ссылок нужно tasks:write.":
    "In Tasktopia, open Settings → MCP integration and create a key with country:read and tasks:read. Use MCP country.list to find the country UUID. Adding links also requires tasks:write.",
  "В папке репозитория": "Inside your repository folder",
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
  Войти: "Sign in",
  "Войти через организацию заново": "Sign in again",
  "Впервые здесь?": "New here?",
  "Время коммита": "Commit time",
  "Время неизвестно": "Unknown time",
  "Все источники": "All sources",
  "Все репозитории": "All repositories",
  "Вход CLI через организацию не настроен на этом сервере.":
    "CLI sign-in is not configured on this server.",
  "Вход в Gitmora": "Sign in to Gitmora",
  "Вход не настроен. Обратитесь к администратору сервера.":
    "Sign-in is not configured. Contact the server administrator.",
  "Вход пока недоступен. Обратитесь к администратору команды.":
    "Sign-in is unavailable. Contact your team administrator.",
  "Выберите запись": "Select a record",
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
  Выполнено: "Complete",
  "Выполняем…": "Working…",
  "Выпускать и отзывать свои ключи": "Issue and revoke your keys",
  "Выпустить ключ": "Issue key",
  "Выпустить личный ключ": "Issue personal key",
  "Где найти ключ и UUID": "Where to find the key and UUID",
  "Готово к проверке": "Ready for review",
  "Данные Tasktopia": "Tasktopia data",
  "Данные изменились. Обновите страницу и повторите действие.":
    "The data has changed. Refresh and try again.",
  "Данные источника. Авторство указано внешним инструментом; строки без меток имеют неизвестное происхождение.":
    "Source data. Attribution comes from an external tool; unmarked lines have unknown authorship.",
  "Данные сервиса": "Service data",
  "Действия проекта": "Project actions",
  Действует: "Active",
  "Действует до": "Expires",
  "Дефекты:": "Defects:",
  "Диапазоны относятся к коммиту": "Ranges refer to commit",
  "Диапазоны, указанные источником:": "Source-reported ranges:",
  "Для закрытого репозитория нужен токен чтения.":
    "A private repository requires a read token.",
  "Для новой попытки начните новый вход в терминале.":
    "Start a new sign-in from your terminal to try again.",
  "Для подтверждения CLI войдите через организацию заново по ссылке из терминала.":
    "Sign in again using the link from your terminal to approve CLI access.",
  "Добавить репозиторий": "Add repository",
  "Добавить участника": "Add member",
  "Добавлять ссылки на запросы слияния в задачи":
    "Add pull request links to tasks",
  "Добавьте GitHub или GitLab, чтобы сохранять историю разработки.":
    "Connect GitHub or GitLab to start saving development history.",
  "Добавьте коллегу по нику или UUID.": "Add a teammate by username or UUID.",
  "Добро пожаловать в Gitmora": "Welcome to Gitmora",
  "Документ задачи": "Task document",
  "Документы:": "Documents:",
  Дополнительно: "Advanced",
  "Достигнут лимит API сервиса. Проверьте токен или дождитесь следующей попытки.":
    "The service API limit was reached. Check the token or wait for the next attempt.",
  "Достигнут общий лимит GitHub. Подключите токен чтения для регулярной синхронизации.":
    "GitHub's shared limit has been reached. Connect a read token for regular synchronization.",
  "Доступ терминала сохранится после выхода из браузера. Отозвать его можно в личных настройках.":
    "Terminal access remains active after browser sign-out. Revoke it in personal settings.",
  "Доступных цитат пока нет. Проверьте права и индексацию.":
    "No source fragments are available yet. Check access and indexing.",
  "Есть дополнительные MR, не вошедшие в контекст.":
    "More MRs exist outside this context.",
  "За доступом обратитесь к администратору команды.":
    "Contact your team administrator for access.",
  "Зависимости:": "Dependencies:",
  "Загружаем проекты…": "Loading projects…",
  "Загружаем репозитории…": "Loading repositories…",
  "Загружаем фрагмент…": "Loading fragment…",
  "Загрузить ещё": "Load more",
  "Загрузить ещё источники": "Load more sources",
  "Загрузить ещё ключи": "Load more keys",
  "Загрузить ещё репозитории": "Load more repositories",
  "Загрузить ещё участников": "Load more members",
  "Загрузка подтверждения CLI…": "Loading CLI confirmation…",
  "Загрузка…": "Loading…",
  Задача: "Task",
  "Задача Tasktopia": "Tasktopia task",
  "Задача пока недоступна": "Task unavailable",
  "Задачи Tasktopia": "Tasktopia tasks",
  "Задачи и связи с изменениями кода.": "Tasks and links to code changes.",
  "Задачи, код и решения вместе.": "Tasks, code and decisions together.",
  "Закрыть цитату": "Close fragment",
  "Запись агента": "Agent record",
  "Запись не найдена или больше недоступна.":
    "The record was not found or is no longer available.",
  "Запись сессии": "Session record",
  "Запись сессии агента": "Agent session record",
  "Запрашиваемые права": "Requested permissions",
  "Запрос CLI изменился или уже завершён. Обновите его состояние перед новым решением.":
    "The CLI request changed or has ended. Refresh its status before deciding.",
  "Запрос CLI истёк, отменён или ключ больше не действует. Проверьте состояние в терминале.":
    "The CLI request expired, was cancelled or is no longer valid. Check your terminal.",
  "Запрос CLI не совпал с поддерживаемыми условиями подключения.":
    "This CLI request uses unsupported connection settings.",
  "Запрос должен занимать не более 512 байт UTF-8.":
    "The query must be no longer than 512 UTF-8 bytes.",
  "Запрос или полный SHA коммита": "Search query or full commit SHA",
  "Запрос отклонён. Ключ не создан.": "Request denied. No key was created.",
  "Запрос отменён в терминале. Ключ не создан.":
    "Request cancelled in the terminal. No key was created.",
  "Запрос превышает допустимый размер.": "The request exceeds the size limit.",
  "Запрошенный срок превышает 90 дней или оставшийся срок текущего ключа или сессии.":
    "The requested lifetime exceeds 90 days or the remaining connection lifetime.",
  "Здесь появятся полный фрагмент, точная версия и происхождение источника.":
    "The source fragment, version and provenance will appear here.",
  Идентификатор: "Identifier",
  "Идентификатор проекта": "Project ID",
  "Идентификаторы источника": "Source identifiers",
  "Изменения в коде": "Code changes",
  "Изменить доступ": "Edit access",
  "Импорт:": "Import:",
  Интеграции: "Integrations",
  "Историческая версия": "Historical version",
  "Историческая запись": "Historical record",
  "Исторические версии": "Historical versions",
  "История Gitmora будет удалена. Исходный код и задачи во внешних сервисах сохранятся.":
    "Gitmora history will be deleted. Source code and tasks in external services will remain.",
  "История пока пуста": "No history yet",
  Источник: "Source",
  "Источник SHA-256": "Source SHA-256",
  "Источник не содержит однозначной области навигации.":
    "The source does not identify a single navigation scope.",
  "Источник относится к другому проекту.":
    "The source belongs to another project.",
  "Источник ссылается на внешние transcripts. Их содержимое в эту запись не загружалось.":
    "The source references external transcripts. Their contents were not imported into this record.",
  "Источник указал AI: {0} / {1}": "Source reports AI: {0} / {1}",
  "Источник указал человека: {0}": "Source reports human: {0}",
  "Источник:": "Source:",
  Истёк: "Expired",
  "Ищем…": "Searching…",
  "К содержимому": "Skip to content",
  "Как получить токен": "How to create a token",
  "Клиенты с этими ключами потеряют доступ.":
    "Clients using these keys will lose access.",
  "Ключ Tasktopia": "Tasktopia key",
  "Ключ действует до": "Key expires",
  "Ключ создан, секрет недоступен": "Key created; secret unavailable",
  "Ключи для автоматизации": "Automation keys",
  "Ключи и браузерные сессии вашей учётной записи. Доступ к проектам проверяется отдельно.":
    "Keys and browser sessions for your account. Project access is checked separately.",
  "Код входа CLI": "CLI sign-in code",
  "Код репозитория": "Repository code",
  "Код:": "Code:",
  "Комментарии:": "Comments:",
  Коммит: "Commit",
  "Коммит ещё не проверен": "Commit not verified yet",
  "Коммит репозитория": "Repository commit",
  "Коммиты и запросы на слияние.": "Commits and pull requests.",
  "Коммиты, указанные агентом:": "Agent-reported commits:",
  "Контекст источника": "Source context",
  "Контекст не совпал с выбранным источником.":
    "The context does not match the selected source.",
  "Контекст работы": "Work context",
  Копировать: "Copy",
  "Копировать: {0}": "Copy: {0}",
  "Корневой ключ": "Root key",
  "Критерии приёмки": "Acceptance criteria",
  "Личные ключи": "Personal keys",
  "Личные настройки": "Personal settings",
  "Наблюдаемый head": "Observed head",
  "Назад к источнику": "Back to source",
  "Название в Gitmora": "Name in Gitmora",
  "Название ключа": "Key name",
  "Название проекта": "Project name",
  "Название сохранено.": "Name saved.",
  "Найдите сессию, задачу или коммит по ключевым словам.":
    "Find a session, task or commit using keywords.",
  Найти: "Search",
  "Например, alex…": "For example, alex…",
  "Например, команда разработки…": "For example, Development team…",
  "Например, локальный сборщик": "For example, local collector",
  "Например, обновление токена доступа": "For example, refresh access token",
  "Настраивать интеграции проекта": "Configure project integrations",
  "Настройки проекта": "Project settings",
  Начало: "Started",
  "Начало фрагмента": "Start of fragment",
  "Не применяется к наблюдаемому head.": "Does not apply to the observed head.",
  "Не удалось войти. Повторите попытку или обратитесь к администратору.":
    "Could not sign in. Try again or contact your administrator.",
  "Не удалось выполнить запрос. Повторите попытку.":
    "The request failed. Try again.",
  "Не удалось завершить сессию.": "Could not end your session.",
  "Не удалось обновить задачи. Проверьте адрес и права ключа.":
    "Tasks could not be updated. Check the URL and key permissions.",
  "Не удалось проверить доступ к внешнему сервису. Проверьте адрес, ключ и его права.":
    "Could not verify external service access. Check the address, key and permissions.",
  "Не указана": "Not specified",
  "Недопустимый адрес подключения или идентификатор репозитория.":
    "Invalid server address or repository ID.",
  "Недопустимый идентификатор репозитория.": "Invalid repository ID.",
  "Недостаточно прав для этого действия.":
    "You do not have permission for this action.",
  "Некорректная область списка источников.": "Invalid source list scope.",
  "Нельзя передать больше прав, чем у текущего ключа.":
    "You cannot grant more permissions than this connection has.",
  "Нет доступа к контексту": "No context access",
  "Нет доступа к рабочим источникам": "No source access",
  "Нет наблюдаемого SHA": "No observed SHA",
  "Нет разрешения на данные задачи": "No task data access",
  "Ник для приглашений": "Invitation username",
  "Ник или UUID пользователя": "Username or user UUID",
  "Новое название репозитория": "New repository name",
  "Новые данные перестанут поступать. Сохранённые сессии останутся в Gitmora.":
    "New data will stop arriving. Saved sessions will remain in Gitmora.",
  "Новые задачи перестанут поступать в Gitmora.":
    "New tasks will stop arriving in Gitmora.",
  "Новый API-ключ": "New API key",
  "Новый ключ превышает полномочия, срок или глубину текущего подключения. Проверьте ограничения родителя.":
    "The key exceeds this connection's permissions or lifetime. Check its parent limits.",
  "Новый проект": "New project",
  "Нужно capture:read для сессий или integration:read для задач и MR.":
    "Sessions require capture:read; tasks and MRs require integration:read.",
  "Нужны Git 2.43+ и curl. Для входа на Linux установите libsecret-tools и запустите Secret Service в своей пользовательской сессии.":
    "Requires Git 2.43+ and curl. For Linux sign-in, install libsecret-tools and run Secret Service in your user session.",
  "Нужны read_api и доступ к этому проекту GitLab.":
    "Requires read_api and access to this GitLab project.",
  "Обновите список или выпустите ограниченный ключ.":
    "Refresh the list or issue a restricted key.",
  Обновить: "Refresh",
  "Обновить вход": "Update sign-in",
  "Обновить запрос CLI": "Refresh CLI request",
  "Обновить контекст": "Refresh context",
  "Обновить состояние": "Refresh status",
  "Обновить список ключей": "Refresh keys",
  "Обновить участников": "Refresh members",
  Обновлено: "Updated",
  "Обсуждение и изменения": "Discussion and changes",
  Объект: "Object",
  "Ожидают индексации:": "Awaiting indexing:",
  "Ожидают индексации: {0}. ": "Awaiting indexing: {0}. ",
  "Окна сессии": "Session windows",
  "Окно подтверждения": "Confirmation deadline",
  "Окно подтверждения истекло. Проверьте состояние входа в терминале.":
    "The confirmation has expired. Check sign-in status in your terminal.",
  Окончание: "Ended",
  Описание: "Description",
  "Основание связи:": "Link basis:",
  Основное: "General",
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
  "Отключить Tasktopia": "Disconnect Tasktopia",
  "Отключить Tasktopia?": "Disconnect Tasktopia?",
  "Отключить {0}": "Disconnect {0}",
  "Отключить {0}?": "Disconnect {0}?",
  "Откройте сессию, задачу или запрос на слияние из списка.":
    "Open a session, task or pull request from the list.",
  "Откройте фрагмент": "Open a fragment",
  Открыто: "Open",
  "Открыть в Tasktopia": "Open in Tasktopia",
  "Открыть версию": "Open version",
  "Открыть задачу из привязки агента": "Open agent-linked task",
  "Открыть источник": "Open source",
  "Открыть контекст MR": "Open MR context",
  "Открыть наблюдаемый head": "Open observed head",
  "Открыть окно": "Open window",
  Отмена: "Cancel",
  Отозван: "Revoked",
  "Отозвать {0}?": "Revoke {0}?",
  "Отозвать выданный ключ?": "Revoke issued key?",
  "Отозвать ключ": "Revoke key",
  "Отозвать ключ и потомков": "Revoke key and child keys",
  "Отозвать этот выданный ключ": "Revoke this issued key",
  "Отправлять сессии агента": "Upload agent sessions",
  Оформление: "Appearance",
  "Ошибки индексации: {0}. ": "Indexing errors: {0}. ",
  "Ошибок нет": "No errors",
  "Первые MR": "First MRs",
  "Первые окна": "First windows",
  "Перезапустите OpenCode после подключения.":
    "Restart OpenCode after connecting.",
  Переименовать: "Rename",
  "Повторить выход": "Retry sign-out",
  "Повторить загрузку": "Retry loading",
  "Повторить загрузку репозиториев": "Retry loading repositories",
  "Повторить список источников": "Retry loading sources",
  "Повторить чтение": "Retry loading",
  "Подключаемся…": "Connecting…",
  "Подключаем…": "Connecting…",
  "Подключение агента": "Connect your agent",
  "Подключение сохранено.": "Connection saved.",
  Подключено: "Connected",
  "Подключите CLI и выполните первый commit и push.":
    "Connect the CLI, then make your first commit and push.",
  "Подключите GitHub или GitLab, чтобы начать работу.":
    "Connect GitHub or GitLab to get started.",
  "Подключите первый репозиторий": "Connect your first repository",
  "Подключите репозиторий и пригласите команду.":
    "Connect a repository and invite your team.",
  "Подключить CLI и локальных агентов": "Connect CLI and local agents",
  "Подключить Tasktopia": "Connect Tasktopia",
  "Подключить репозиторий": "Connect repository",
  "Подключить терминал": "Connect terminal",
  "Подключить токен": "Connect token",
  Подтвердить: "Confirm",
  "Подтвердить вход CLI": "Confirm sign-in",
  "Подтвердить отказ CLI": "Confirm denial",
  "Подтверждение CLI не совпало с текущей учётной записью или состоянием запроса.":
    "The CLI confirmation no longer matches this account or request.",
  "Подтверждение входа CLI": "CLI sign-in confirmation",
  "Подтверждение принято. Проверьте завершение входа в терминале.":
    "Approved. Check that sign-in completes in your terminal.",
  "Подтверждено коммитов: {0}": "Confirmed commits: {0}",
  "Подтверждено провайдером:": "Confirmed by provider:",
  "Подтверждён провайдером": "Verified by provider",
  "Подтверждённое членство указанного коммита":
    "Verified membership of the selected commit",
  "Подтверждённые коммиты связанного потока сессии":
    "Verified commits in the linked session stream",
  "Подтверждённые результирующие SHA": "Verified resulting SHAs",
  "Подтверждённых связей с MR/PR пока нет. Проверьте состояние синхронизации репозитория.":
    "No verified MR/PR links yet. Check repository synchronization status.",
  "Подходящих принятых окон пока нет.": "No matching accepted windows yet.",
  Поиск: "Search",
  "Поиск через MCP": "Search through MCP",
  "Поиск…": "Searching…",
  "Показана часть deployment.": "Some deployment records are omitted.",
  "Показана часть подтверждённых SHA. Полная запись доступна в деталях сессии.":
    "Some verified SHAs are omitted. Open the session record for the full list.",
  "Показана часть проверок CI. Откройте источник, чтобы увидеть все.":
    "Some CI checks are shown. Open the source to see them all.",
  "Показана часть результатов. Уточните запрос.":
    "Only some results are shown. Refine your query.",
  "Показана часть цитат. Уточните запрос в разделе «Контекст» или откройте отдельный источник.":
    "Some fragments are omitted. Refine your Context query or open an individual source.",
  Показано: "Showing",
  Показать: "Show",
  "Полная цепочка родительских ссылок не подтверждена.":
    "The complete parent-reference chain is not verified.",
  "Последняя версия": "Latest version",
  "Последняя запись": "Latest record",
  "Последняя сохранённая версия": "Latest saved version",
  "Права и срок ключа ограничены текущей сессией. Выход завершит работу выданных через неё ключей.":
    "Key permissions and lifetime are limited by this session. Signing out revokes keys issued through it.",
  "Права токена": "Token permissions",
  "Предыдущие диапазоны": "Previous ranges",
  "Предыдущие сообщения": "Previous messages",
  "Привязка агента к задаче и подтверждённое членство коммита":
    "Agent task link and verified commit membership",
  "Привязка агента не подтверждает доступность задачи в Tasktopia. Проверьте синхронизацию и права.":
    "The agent's link does not verify Tasktopia task access. Check synchronization and permissions.",
  "Привязка уже существует или её версия изменилась. Обновите OIDC-привязки перед новым изменением.":
    "The binding already exists or has changed. Refresh the list before editing.",
  "Пример: задача, сессия агента, коммит и merge request связаны между собой":
    "Example: a task, agent session, commit and merge request linked together",
  "Пример: работа над входом": "Example: updating sign-in",
  "Проверен Git blob": "Verified Git blob",
  "Проверить подключение": "Check connection",
  "Проверок CI пока нет.": "No CI checks yet.",
  "Проверьте введённые данные и параметры запроса.":
    "Check the entered details and request parameters.",
  "Проверьте данные репозитория.": "Check the repository details.",
  "Проверьте данные.": "Check the entered details.",
  "Проверьте параметры запроса.": "Check the request parameters.",
  "Проверяем…": "Checking…",
  Проект: "Project",
  "Происхождение импорта": "Import provenance",
  Пространство: "Workspace",
  Профиль: "Profile",
  "Профиль сохранён.": "Profile saved.",
  Прочитано: "Read",
  "Публичный репозиторий без токена": "Public repository without a token",
  "Пути, указанные агентом:": "Agent-reported paths:",
  Работа: "Work",
  "Работайте как обычно: git commit и git push отправят историю в Gitmora.":
    "Work as usual: git commit and git push will send your history to Gitmora.",
  "Рабочие источники": "Work sources",
  "Развёртываний пока нет.": "No deployments yet.",
  Развёртывания: "Deployments",
  "Разделы проекта": "Project sections",
  "Разрешения нового ключа": "New key permissions",
  "Разрешите команды Gitmora через /hooks в Codex и перезапустите сессию.":
    "Approve Gitmora commands in Codex using /hooks, then restart the session.",
  "Разрешить вход CLI…": "Allow CLI sign-in…",
  "Разрешить вход терминала?": "Allow terminal sign-in?",
  Ревизия: "Revision",
  "Ревизия сессии": "Session revision",
  "Результат выпуска ключа": "Key issuance result",
  "Результаты поиска": "Search results",
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
  "Сверьте этот код с кодом в вашем терминале. Подтверждайте только вход, который начали сами.":
    "Compare this code with your terminal. Approve only a sign-in you started yourself.",
  "Связанные MR": "Linked MRs",
  "Связанные сессии": "Linked sessions",
  "Связи с MR скрыты: нужны разрешения на соответствующие источники.":
    "MR links are hidden because source access is required.",
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
  "Сессии агента": "Agent sessions",
  "Сессии скрыты: требуется capture:read.":
    "Sessions are hidden: capture:read is required.",
  "Сессии, задачи и код выбранного проекта.":
    "Sessions, tasks and code in this project.",
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
  Синхронизировано: "Synchronized",
  Синхронизировать: "Synchronize",
  "Системные требования и загрузки": "Requirements and downloads",
  "Скопировано.": "Copied.",
  "Скопируйте его в свой менеджер секретов. После закрытия или перехода на другую страницу интерфейс больше не покажет этот секрет.":
    "Copy it to your password manager. The secret will not be shown again after closing this view or navigating away.",
  "Скрыть результат выпуска": "Close key result",
  "Скрыть уведомление": "Dismiss notification",
  "Скрыть форму": "Hide form",
  "Следующая попытка": "Next attempt",
  "Следующая часть": "Next part",
  "Следующие MR": "Next MRs",
  "Следующие диапазоны": "Next ranges",
  "Следующие окна": "Next windows",
  "Следующие сообщения": "Next messages",
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
  "Совпадает с наблюдаемым head.": "Matches the observed head.",
  "Совпадений пока нет": "No matches yet",
  "Создайте первый проект": "Create your first project",
  "Создать аккаунт": "Create account",
  "Создать проект": "Create project",
  "Создаём…": "Creating…",
  Сообщения: "Messages",
  "Соответствующая браузерная сессия завершится.":
    "The associated browser session will end.",
  "Состояние подключения": "Connection status",
  "Состояние синхронизации": "Synchronization status",
  Сохранено: "Saved",
  "Сохраните ключ сейчас": "Save your key now",
  "Сохранить название": "Save name",
  "Сохранить подключение": "Save connection",
  "Сохранить профиль": "Save profile",
  "Сохранить роль": "Save role",
  "Список не совпал с выбранной областью.":
    "The list does not match the selected scope.",
  "Список пуст": "No keys",
  "Способ проверки": "Verification method",
  "Срок в минутах": "Lifetime in minutes",
  "Срок ключа": "Key lifetime",
  "Ссылка на код": "Code reference",
  "Ссылка на предыдущую ревизию:": "Previous revision reference:",
  "Ссылка на репозиторий": "Repository URL",
  Статус: "Status",
  "Статус и отключение": "Status and disabling capture",
  Страна: "Country",
  "Строки {0}–{1}": "Lines {0}–{1}",
  "Текст документов доступен через точные цитаты и поиск контекста.":
    "Read document text through source fragments and context search.",
  "Текст сокращён в контексте.": "Text is shortened in this context.",
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
  "Точная версия источника": "Exact source version",
  "Точные источники": "Exact sources",
  "Точный источник": "Exact source",
  "Требует внимания": "Needs attention",
  "Тёмная тема": "Dark theme",
  "У токена нет доступа. Разрешите чтение репозитория, запросов на слияние и проверок CI.":
    "The token has no access. Allow read access to the repository, pull requests and CI checks.",
  "Убрать доступ": "Remove access",
  "Убрать доступ {0}?": "Remove access for {0}?",
  "Удаление проекта": "Delete project",
  "Удалить {0}?": "Delete {0}?",
  "Удалить локальные данные": "Delete local data",
  "Удалить проект": "Delete project",
  "Удалить проект {0}?": "Delete project {0}?",
  "Удалить проект и его данные": "Delete project and data",
  "Удалить репозиторий": "Delete repository",
  "Удаляет историю и настройки проекта в Gitmora.":
    "Deletes this project's Gitmora history and settings.",
  "Управлять каталогом проекта": "Manage the project catalog",
  "Управлять своими ключами": "Manage your keys",
  "Управлять установкой при наличии роли оператора":
    "Manage Gitmora with an administrator role",
  "Устройство:": "Device:",
  "Уточните формулировку, проверьте фильтры или дождитесь индексации новых источников.":
    "Refine your query, check filters or wait for new sources to finish indexing.",
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
  "Фрагменты:": "Fragments:",
  "Цепочка точных родительских ссылок проверена.":
    "The exact parent-reference chain is verified.",
  "Цитаты контекста": "Context fragments",
  "Часть деталей сокращена до лимита контекста. Используйте точные источники и продолжение списков.":
    "Some detail is omitted to fit the context limit. Open the exact sources or continue the lists.",
  "Часть полей дочерних записей сокращена.":
    "Some child record fields have been shortened.",
  "Чек-лист:": "Checklist:",
  "Читаем доступные источники…": "Loading available sources…",
  "Читаем запись сессии…": "Loading session…",
  "Читаем условия подключения…": "Loading connection details…",
  "Читать доступные проекты и репозитории":
    "Read available projects and repositories",
  "Читать задачи и данные репозиториев": "Read tasks and repository data",
  "Читать интеграции и внешние источники":
    "Read integrations and external sources",
  "Читать каталог и участников": "Read the catalog and members",
  "Читать сессии агента": "Read agent sessions",
  "Членство исходных коммитов:": "Source commit membership:",
  Чтение: "Read only",
  "Это пути из записи агента; наличие файла в репозитории проверяется отдельно.":
    "These are paths from the agent record; file existence in the repository is checked separately.",
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
  "Явно выбранный MR/PR": "Explicitly selected MR/PR",
  Язык: "Language",
  "Язык интерфейса": "Interface language",
  "вне точного диапазона интерфейса":
    "outside the interface's exact numeric range",
  "заявление источника Git AI": "Git AI attribution",
  из: "of",
  "корневой ключ": "root key",
  "не подтверждена": "not verified",
  "полная сверка не завершена": "full reconciliation is incomplete",
  "полных минут. Браузерная сессия ограничивает срок её дочерних ключей; более долгий корневой ключ выдаётся отдельной доверенной процедурой.":
    "whole minutes. A browser session limits its child keys' lifetime; longer-lived root keys require trusted server administration.",
  проверена: "verified",
  "сведения об импорте": "import details",
  "сверено для этого code key": "verified for this code key",
  "＋ Создать проект": "＋ Create project",
};
