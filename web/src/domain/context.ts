/** Read models mirror the versioned API; source text remains untrusted plain text. */
export interface User {
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
  provider: "github";
  provider_url: string;
  provider_repository_id: number;
  name: string;
}
export function repositoryURL(repository: Repository): string {
  return repository.provider === "github"
    ? repository.provider_url
    : repository.gitlab_url;
}
export function repositoryProviderID(repository: Repository): number {
  return repository.provider === "github"
    ? repository.provider_repository_id
    : repository.gitlab_project_id;
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
  kind: "capture" | "task" | "code";
  project_id: string;
  digest: string;
  repository_id?: string;
  session_id?: string;
  revision?: number;
  country_id?: string;
  task_id?: string;
  commit_sha?: string;
  file_path?: string;
  blob_id?: string;
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
  country_id?: string;
  task_id?: string;
}
export interface SearchResult {
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
}

export const sourceNames: Record<string, string> = {
  session: "Сессия агента",
  task: "Задача",
  document: "Документ задачи",
  code_reference: "Ссылка на код",
  code: "Код GitLab",
};

/** The server interprets a complete SHA as a confirmed-commit filter. */
export function searchQuery(
  project: string,
  repository: string,
  text: string,
  kind: string,
  history: boolean,
): SearchQuery {
  text = text.trim();
  if (!text)
    throw new Error("Введите поисковый запрос или полный SHA коммита.");
  if (new TextEncoder().encode(text).length > 512)
    throw new Error("Запрос должен занимать не более 512 байт UTF-8.");
  return {
    project_id: project,
    ...(repository ? { repository_id: repository } : {}),
    text,
    ...(kind ? { kinds: [kind] } : {}),
    history,
    limit: 20,
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
