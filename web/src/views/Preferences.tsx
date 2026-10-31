import { t as translate } from "../domain/locale.js";
import { LanguageSelect } from "./LanguageSelect.js";
import { ThemeSwitch } from "./ThemeSwitch.js";

export function Preferences() {
  return (
    <section className="pb-8 max-w-180">
      <h1 className="mb-8">{translate("Настройки")}</h1>
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
