import { t as translate } from "../domain/locale.js";
import { buttonStyles, sharedStyles } from "./ui.js";
import { useEffect, useRef, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import type { ApiClient } from "../transport/api.js";
import { useMutation } from "../application/useMutation.js";
import { ErrorNotice } from "./shared.js";

/** Native disabled controls prevent editing a draft while its command is in flight. */
export function MutationForm({
  pending,
  children,
  className,
  onSubmit,
}: {
  pending: boolean;
  children: ReactNode;
  className?: string;
  onSubmit: (event: FormEvent) => void;
}) {
  return (
    <form className="min-w-0" onSubmit={onSubmit} autoComplete="off">
      <fieldset
        className={`mutation-fields ${className ?? ""}`}
        disabled={pending}
      >
        {children}
      </fieldset>
    </form>
  );
}

export function MutationNotice({
  action,
}: {
  action: Pick<ReturnType<typeof useMutation>, "error" | "message">;
}) {
  return (
    <>
      <ErrorNotice>{action.error}</ErrorNotice>
      {action.message && (
        <p
          className="notice success px-4 py-4 mx-0 my-3 bg-success-soft text-success rounded-md"
          role="status"
        >
          {translate(action.message)}
        </p>
      )}
    </>
  );
}

/** Native dialog keeps focus/escape behavior and never treats dismissal as confirmation. */
export function ConfirmAction({
  title,
  children,
  pending,
  error,
  onConfirm,
  onClose,
  label = translate("Подтвердить"),
  tone = "danger",
}: {
  title: string;
  children: ReactNode;
  pending: boolean;
  error: string;
  onConfirm: () => void;
  onClose: () => void;
  label?: string;
  tone?: "danger" | "primary";
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const node = dialog.current;
    node?.showModal();
    return () => node?.close();
  }, []);
  return (
    <dialog
      ref={dialog}
      className={`
        confirm-dialog border border-line rounded-lg p-7 max-w-[min(540px,_calc(100vw_-_36px))]
        bg-paper text-ink shadow-dialog [&::backdrop]:bg-brand/60 [&_h2]:text-2xl [&_h2]:mb-4
        [&_.actions]:mt-6 [&_.actions]:justify-end mobile:p-6
      `}
      aria-labelledby="confirm-title"
      onCancel={(event) => {
        if (pending) event.preventDefault();
        else onClose();
      }}
    >
      <h2 id="confirm-title">{title}</h2>
      <div
        className={`
        confirm-body max-w-[58ch] text-muted text-sm leading-[1.7] [&_.source-prose]:[font-family:inherit]
        [&_.source-prose]:[font-size:inherit] [&_.source-prose]:[font-weight:inherit] [&_.source-prose]:leading-[1.6]
        [&_.source-prose]:whitespace-pre-wrap [&_.source-prose]:wrap-anywhere [&_.source-prose]:max-w-[76ch]
      `}
      >
        {children}
      </div>
      <ErrorNotice>{error}</ErrorNotice>
      <div className="actions flex gap-3 flex-wrap items-center">
        <button
          className={buttonStyles.secondary}
          disabled={pending}
          autoFocus
          onClick={onClose}
        >
          {translate("Отмена")}
        </button>
        <button
          className={buttonStyles[tone]}
          disabled={pending}
          onClick={onConfirm}
        >
          {pending ? translate("Выполняем…") : label}
        </button>
      </div>
    </dialog>
  );
}

export function Rename({
  client,
  path,
  name,
  version,
  onSaved,
  label,
}: {
  client: ApiClient;
  path: string;
  name: string;
  version: number;
  onSaved: () => Promise<void>;
  label: string;
}) {
  const [value, setValue] = useState(name);
  const action = useMutation(client);
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (
      await action.run(
        "PATCH",
        path,
        { name: value.trim(), expected_version: version },
        true,
      )
    ) {
      action.setMessage("Название сохранено.");
      await onSaved();
    }
  }
  return (
    <MutationForm
      pending={action.pending}
      onSubmit={(event) => void submit(event)}
      className={sharedStyles.inlineForm}
    >
      <label>
        {label}
        <input
          value={value}
          onChange={(e) => setValue(e.target.value)}
          required
          maxLength={200}
        />
      </label>
      <button
        className={buttonStyles.secondary}
        disabled={action.pending || value.trim() === name || !value.trim()}
      >
        {translate("Сохранить название")}
      </button>
      <MutationNotice action={action} />
    </MutationForm>
  );
}
