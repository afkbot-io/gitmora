import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import type { ApiClient } from "../transport/api.js";
import { t as translate } from "../domain/locale.js";
import { useMutation } from "../application/useMutation.js";
import { MutationForm, MutationNotice } from "./SettingsShared.js";
import { readableError } from "../domain/context.js";
import { ErrorNotice } from "./shared.js";
import { buttonStyles } from "./ui.js";
export function CreateProject({
  client,
  onCreated,
  onClose,
}: {
  client: ApiClient;
  onCreated: (id: string) => Promise<void>;
  onClose?: () => void;
}) {
  const [error, setError] = useState("");
  const [opening, setOpening] = useState(false);
  const [createdID, setCreatedID] = useState("");
  const [name, setName] = useState("");
  const [id] = useState(() => crypto.randomUUID());
  const action = useMutation(client);
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    if (onClose) dialog.current?.showModal();
    return () => dialog.current?.close();
  }, []);
  async function create(e: FormEvent) {
    e.preventDefault();
    if (opening) return;
    setOpening(true);
    setError("");
    try {
      let projectID = createdID;
      if (!projectID) {
        const saved = await action.run<{ entity_id: string }>(
          "POST",
          "/v1/projects",
          { id, name: name.trim() },
          true,
        );
        if (!saved) return;
        projectID = saved.value.entity_id;
        setCreatedID(projectID);
      }
      await onCreated(projectID);
    } catch (e) {
      setError(readableError(e));
    } finally {
      setOpening(false);
    }
  }

  const form = (
    <MutationForm
      pending={action.pending || opening}
      onSubmit={(e) => void create(e)}
      className="grid gap-4"
    >
      <label>
        {translate("Название проекта")}
        <input
          className="mt-2 w-full"
          name="project_name"
          readOnly={!!createdID}
          required
          maxLength={200}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={translate("Например, платформа или продукт…")}
        />
      </label>
      <ErrorNotice>{error}</ErrorNotice>
      <MutationNotice action={action} />
      <div className="flex justify-end gap-3">
        {onClose && (
          <button
            type="button"
            className={buttonStyles.secondary}
            onClick={onClose}
          >
            {translate("Отмена")}
          </button>
        )}
        <button
          disabled={!name.trim() && !createdID}
          className={buttonStyles.primary + (onClose ? "" : " w-full")}
        >
          {action.pending || opening
            ? translate("Создаём…")
            : translate(createdID ? "Открыть проект" : "Создать проект")}
        </button>
      </div>
    </MutationForm>
  );
  return onClose ? (
    <dialog
      ref={dialog}
      aria-labelledby="new-project-title"
      onCancel={(e) => {
        if (action.pending || opening) e.preventDefault();
        else onClose();
      }}
      className="w-120 max-w-[calc(100vw-2rem)] rounded-lg border border-line p-6 shadow-dialog backdrop:bg-brand/60"
    >
      <h2 id="new-project-title" className="mb-2">
        {translate("Новый проект")}
      </h2>
      <p className="text-sm text-muted mb-5">
        {translate("Затем подключите репозиторий и пригласите команду.")}
      </p>
      {form}
    </dialog>
  ) : (
    form
  );
}
