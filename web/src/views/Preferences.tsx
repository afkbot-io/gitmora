import { t as translate } from "../domain/locale.js";
import { LanguageSelect } from "./LanguageSelect.js";
import { useState } from "react";
import type { Me } from "../domain/context.js";
import type { ApiClient } from "../transport/api.js";
import { useMutation } from "../application/useMutation.js";
import { MutationForm, MutationNotice } from "./SettingsShared.js";
import { buttonStyles } from "./ui.js";
import { ThemeSwitch } from "./ThemeSwitch.js";

export function Preferences({
  client,
  me,
  onMe,
}: {
  client: ApiClient;
  me: Me;
  onMe: (value: Me) => void;
}) {
  const [username, setUsername] = useState(me.user.username ?? "");
  const action = useMutation(client);
  return (
    <section className="pb-8 max-w-180">
      <h1 className="mb-8">{translate("Личные настройки")}</h1>
      <section className="border-b border-line pb-8 mb-8">
        <h2>{translate("Профиль")}</h2>
        <MutationForm
          pending={action.pending}
          onSubmit={async (e) => {
            e.preventDefault();
            if (
              await action.run(
                "PUT",
                "/v1/me/profile",
                { username, expected_version: me.user.version },
                true,
              )
            ) {
              onMe(await client.request<Me>("/v1/me"));
              action.setMessage("Профиль сохранён.");
            }
          }}
        >
          <label>
            {translate("Ник для приглашений")}
            <input
              className="mt-2 w-full"
              name="username"
              value={username}
              required
              maxLength={80}
              pattern="[a-zA-Z0-9][a-zA-Z0-9._-]{0,79}"
              spellCheck={false}
              onChange={(e) => setUsername(e.target.value)}
            />
          </label>
          <MutationNotice action={action} />
          <button className={buttonStyles.primary}>
            {translate("Сохранить профиль")}
          </button>
        </MutationForm>
        <details className="mt-4 text-sm text-muted">
          <summary>{translate("UUID пользователя")}</summary>
          <code className="block mt-2 break-all">{me.user.id}</code>
        </details>
      </section>
      <section
        className="border-b border-line pb-8 mb-8"
        aria-labelledby="appearance-heading"
      >
        <h2 id="appearance-heading">{translate("Оформление")}</h2>
        <ThemeSwitch />
      </section>
      <section aria-labelledby="language-heading">
        <h2 id="language-heading">{translate("Язык интерфейса")}</h2>
        <LanguageSelect hideLabel />
      </section>
    </section>
  );
}
