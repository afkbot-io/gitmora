/** Public artifacts are immutable; publish this version before rolling out its onboarding UI. */
export const publicClientVersion = "0.6.0";
export const clientPlatforms = [
  { label: "macOS · Apple Silicon", file: "gitmora_darwin_arm64" },
  { label: "macOS · Intel", file: "gitmora_darwin_amd64" },
  { label: "Linux · x86-64", file: "gitmora_linux_amd64" },
  { label: "Linux · ARM64", file: "gitmora_linux_arm64" },
] as const;

function shell(value: string): string {
  return "'" + value.replaceAll("'", "'\"'\"'") + "'";
}

function checkServer(server: string) {
  const url = new URL(server);
  if (
    url.origin !== server ||
    url.username ||
    url.password ||
    (url.protocol !== "https:" &&
      !(
        url.protocol === "http:" &&
        ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname)
      ))
  ) {
    throw new Error(
      "Недопустимый адрес подключения или идентификатор репозитория.",
    );
  }
}

/** A copied installer stops before execution if either named checksum is absent or invalid. */
export function clientInstallCommand(
  server: string,
  file: (typeof clientPlatforms)[number]["file"],
): string {
  checkServer(server);
  if (!clientPlatforms.some((platform) => platform.file === file))
    throw new Error("Выберите поддерживаемую платформу CLI.");
  return `curl -fsS --proto '=https' --tlsv1.2 ${shell(server + "/install.sh")} | sh -s -- --server ${shell(server)}`;
}

export function onboardingCommands(server: string, repository?: string) {
  checkServer(server);
  if (
    repository !== undefined &&
    !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(
      repository,
    )
  )
    throw new Error("Недопустимый идентификатор репозитория.");
  return {
    login: `gitmora auth login --server ${shell(server)}`,
    init: repository
      ? `gitmora init --server ${shell(server)} --repository-id ${repository}`
      : "",
    mcp: `gitmora mcp --server ${shell(server)}`,
    codexMCP: `[mcp_servers.gitmora]\ncommand = "gitmora"\nargs = ["mcp", "--server", ${JSON.stringify(server)}]`,
    opencodeMCP: JSON.stringify(
      {
        mcp: {
          gitmora: {
            type: "local",
            command: ["gitmora", "mcp", "--server", server],
            enabled: true,
          },
        },
      },
      null,
      2,
    ),
  };
}
