import { t as translate } from "../domain/locale.js";
import { useState } from "react";
import { buttonStyles } from "./ui.js";

export function CommandBlock({ label, text }: { label: string; text: string }) {
  const [notice, setNotice] = useState({ source: "", message: "" });
  async function copy() {
    try {
      await navigator.clipboard.writeText(text);
      setNotice({ source: text, message: translate("Скопировано.") });
    } catch {
      setNotice({
        source: text,
        message: translate("Выделите команду и скопируйте вручную."),
      });
    }
  }
  return (
    <div className="my-4 min-w-0">
      <div className="flex flex-wrap items-center justify-between gap-2 mb-2">
        <span className="text-sm font-medium">{label}</span>
        <button
          type="button"
          className={buttonStyles.text}
          onClick={() => void copy()}
          aria-label={translate("Копировать: {0}", label)}
        >
          {translate("Копировать")}
        </button>
      </div>
      <pre className="m-0 border border-line rounded-lg bg-canvas p-4 font-mono text-xs leading-relaxed whitespace-pre-wrap wrap-anywhere">
        {text}
      </pre>
      <p role="status" className="text-xs text-muted mt-2 mb-0">
        {notice.source === text ? notice.message : ""}
      </p>
    </div>
  );
}
