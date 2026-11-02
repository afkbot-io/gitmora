/** Read models mirror the versioned API; source text remains untrusted plain text. */
export interface User {
  username?: string;
  id: string;
  display_name: string;
  enabled: boolean;
  operator: boolean;
  version: number;
  created_at: string;
  deleted_at?: string;
}
export interface Me {
  user: User;
  token_id: string;
  scopes: string[];
  expires_at: string;
}
export interface Project {
  id: string;
  name: string;
  role?: string;
  version: number;
  created_at: string;
}
interface RepositoryMetadata {
  id: string;
  project_id: string;
  name: string;
  version: number;
  created_at: string;
}
export type Repository = RepositoryMetadata &
  (
    | { provider?: ""; gitlab_url: string; gitlab_project_id: number }
    | {
        provider: "github";
        provider_url: string;
        provider_repository_id: number;
      }
  );
export interface ResolvedRepository {
  provider: "github" | "";
  provider_url: string;
  provider_repository_id: number;
  name: string;
}
export function repositoryURL(repository: Repository): string {
  return repository.provider === "github"
    ? repository.provider_url
    : repository.gitlab_url;
}

export interface Page<T> {
  items: T[];
  next_id?: string;
}
export interface AuthConfig {
  registration_enabled?: boolean;
  enabled: boolean;
  issuer?: string;
  login_path?: string;
}
export interface Source {
  kind: "capture" | "code";
  project_id: string;
  digest: string;
  repository_id?: string;
  session_id?: string;
  revision?: number;
  commit_sha?: string;
  file_path?: string;
  blob_id?: string;
}
export interface SourceMetadata {
  agent?: string;
  version?: string;
  model?: string;
  provider?: string;
  model_basis?: "message_metadata" | "session_configuration" | "import_report";
  role?: string;
  tool?: string;
  profile?: string;
  native_id?: string;
  turn_id?: string;
  relation?:
    | "message"
    | "tool_reported"
    | "session_reported"
    | "git_ai_reported"
    | "retained_code";
  reported_line_start?: number;
  reported_line_end?: number;
  attribution_kind?: "ai" | "known_human";
  reported_commit?: string;
}
export interface SearchCursor {
  resolved_commit_sha?: string;
  literal?: boolean;
  query_digest: string;
  indexed_before: string;
  score: number;
  observed_at: string;
  id: string;
}
export interface Hit {
  id: string;
  source: Source;
  kind: string;
  part: string;
  start: number;
  end: number;
  title: string;
  content: string;
  content_digest: string;
  current: boolean;
  observed_at: string;
  indexed_at: string;
  confirmed_commits: string[];
  confirmed_count: number;
  line_start?: number;
  line_end?: number;
  metadata?: SourceMetadata;
  file_paths?: string[];
  file_path_count?: number;
}
export interface SearchResult {
  next?: SearchCursor;
  resolved_commit_sha?: string;
  items: Hit[];
  has_more: boolean;
  pending_sources_in_project: number;
  failed_sources_in_project: number;
}
export interface Expanded extends Hit {
  next_offset?: number;
}
export interface SearchQuery {
  project_id: string;
  repository_id?: string;
  text: string;
  kinds?: string[];
  history: boolean;
  limit: number;
  agent?: string;
  model?: string;
  role?: string;
  file_path?: string;
  commit_sha?: string;
  session_id?: string;
  after?: SearchCursor;
}

export const sourceNames: Record<string, string> = {
  session: "Сессия агента",
  code_reference: "Ссылка на код",
  code: "Код репозитория",
};

/** The server interprets a complete SHA as a confirmed-commit filter. */
export function searchQuery(
  project: string,
  repository: string,
  text: string,
  kind: string,
  history: boolean,
  filters: {
    agent?: string;
    model?: string;
    role?: string;
    file_path?: string;
  } = {},
): SearchQuery {
  text = text.trim();
  if (!text && !Object.values(filters).some((value) => value?.trim()))
    throw new Error("Введите запрос или выберите фильтр.");
  if (filters.file_path && !repository)
    throw new Error("Выберите репозиторий для поиска по файлу.");
  if (new TextEncoder().encode(text).length > 512)
    throw new Error("Запрос должен занимать не более 512 байт UTF-8.");
  return {
    project_id: project,
    ...(repository ? { repository_id: repository } : {}),
    text,
    ...(kind ? { kinds: [kind] } : {}),
    history,
    limit: 20,
    ...Object.fromEntries(
      Object.entries(filters)
        .map(([key, value]) => [key, value?.trim()])
        .filter(([, value]) => value),
    ),
  };
}

/** Links must have an explicit HTTP(S) scheme and no embedded credentials. */
export function safeExternalURL(value: string): string | null {
  try {
    const url = new URL(value);
    return ["https:", "http:"].includes(url.protocol) &&
      !url.username &&
      !url.password
      ? url.href
      : null;
  } catch {
    return null;
  }
}

export function readableError(error: unknown): string {
  return error instanceof Error
    ? error.message
    : "Не удалось выполнить запрос. Повторите попытку.";
}
