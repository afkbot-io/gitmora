import { useLocale } from "./LanguageSelect.js";
import { t as translate } from "../domain/locale.js";
import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { readableError } from "../domain/context.js";
import { loginNotice } from "../domain/credentials.js";
import type { AuthConfig, Me } from "../domain/context.js";
import { ApiClient, ApiError } from "../transport/api.js";
import { aborted } from "./shared.js";
import { Login } from "./Login.js";
import { Workspace } from "./Workspace.js";
import { cliCodeFromHash, cliLoginPath } from "../domain/cli.js";

const CLIConsent = lazy(() =>
  import("./credentials/CLIConsent.js").then((module) => ({
    default: module.CLIConsent,
  })),
);

/** Bootstrap the same-origin account session. Credentials never enter browser storage. */

export function App() {
  const locale = useLocale();
  useEffect(() => {
    document.title = translate("Gitmora — история разработки");
  }, [locale]);
  const [logoutFailed, setLogoutFailed] = useState(false);
  const [client] = useState(() => new ApiClient());
  const [me, setMe] = useState<Me | null>(null);
  const [config, setConfig] = useState<AuthConfig>({ enabled: false });
  const [checking, setChecking] = useState(true);
  const [error, setError] = useState(() => loginNotice(location.hash));
  const [cliCode, setCLICode] = useState(() => cliCodeFromHash(location.hash));
  const authenticated = useRef(false);
  useEffect(() => {
    function read() {
      const code = cliCodeFromHash(location.hash);
      if (code) {
        setCLICode(code);
        history.replaceState(null, "", location.pathname + location.search);
      }
    }
    read();
    window.addEventListener("hashchange", read);
    return () => window.removeEventListener("hashchange", read);
  }, []);
  useEffect(() => {
    if (loginNotice(location.hash))
      history.replaceState(null, "", location.pathname + location.search);
    const controller = new AbortController();
    client.onUnauthorized = () => {
      setMe(null);
      setChecking(false);
      if (authenticated.current) setError("Сессия завершена. Войдите снова.");
      authenticated.current = false;
    };
    void (async () => {
      try {
        setConfig(
          await client.request<AuthConfig>(
            "/v1/auth/config",
            undefined,
            controller.signal,
          ),
        );
      } catch (e) {
        if (!aborted(e)) setError(readableError(e));
      }
      if (controller.signal.aborted) return;
      try {
        const current = await client.request<Me>(
          "/v1/me",
          undefined,
          controller.signal,
        );
        authenticated.current = true;
        setMe(current);
      } catch (e) {
        if (!aborted(e) && !(e instanceof ApiError && e.status === 401))
          setError(readableError(e));
      } finally {
        if (!controller.signal.aborted) setChecking(false);
      }
    })();
    return () => {
      controller.abort();
      client.onUnauthorized = undefined;
    };
  }, [client]);

  useEffect(() => {
    if (!me) return;
    let timer: ReturnType<typeof setTimeout>;
    const deadline = Date.parse(me.expires_at);
    function check() {
      const remaining = deadline - Date.now();
      if (!Number.isFinite(remaining) || remaining <= 0) {
        client.clear();
        authenticated.current = false;
        setMe(null);
        setError("Сессия истекла. Войдите снова.");
        return;
      }
      timer = setTimeout(check, Math.min(remaining, 2_147_483_647));
    }
    check();
    return () => clearTimeout(timer);
  }, [me, client]);

  async function logout(): Promise<void> {
    // Hide evidence immediately. A failed cookie logout remains visible and retryable.
    setMe(null);
    authenticated.current = false;
    setChecking(true);
    setLogoutFailed(false);
    setError("");
    try {
      client.clear();
      // Also close a cookie session left behind by a failed earlier logout.
      if (config.enabled) await client.request("/v1/auth/logout", {});
    } catch (e) {
      if (!aborted(e) && !(e instanceof ApiError && e.status === 401)) {
        setLogoutFailed(true);
        setError(readableError(e));
      }
    } finally {
      client.clear();
      setChecking(false);
    }
  }

  if (!me)
    return (
      <Login
        config={config}
        checking={checking}
        error={error}
        retryLogout={logoutFailed ? logout : undefined}
        {...(cliCode ? { loginHref: cliLoginPath(cliCode) } : {})}
      />
    );
  if (cliCode)
    return (
      <Suspense
        fallback={
          <p role="status" className="p-6 text-muted">
            {translate("Загрузка подтверждения CLI…")}
          </p>
        }
      >
        <CLIConsent
          key={me.token_id + ":" + cliCode}
          client={client}
          me={me}
          code={cliCode}
          onClose={() => setCLICode("")}
        />
      </Suspense>
    );
  return (
    <Workspace
      key={me.token_id}
      client={client}
      me={me}
      logout={logout}
      onMe={setMe}
      authConfig={config}
      notice={error}
      clearNotice={() => setError("")}
    />
  );
}
