import { useTranslation } from "react-i18next";
import { Link2Off, Star, Unlink } from "lucide-react";

import type { WorkspaceSummary, WorkspaceUnlinkBlocker } from "../../api";
import { Button, Dialog } from "../../ui";
import { cx } from "../../ui/classes";

export type ProjectEditStatus = Readonly<{
  tone: "info" | "danger";
  message: string;
  blockers?: readonly WorkspaceUnlinkBlocker[] | undefined;
}>;

export function ProjectEditStatusMessage({ status }: Readonly<{ status: ProjectEditStatus }>) {
  return (
    <div
      className={cx(
        "rounded-[var(--radius-l)] border border-[var(--color-outline)] bg-[var(--color-island-1)] p-[var(--space-3)]",
        status.tone === "danger" && "text-[var(--color-error)]",
      )}
      role="status"
    >
      <p className="m-0">{status.message}</p>
      {status.blockers !== undefined && status.blockers.length > 0 ? (
        <ul className="m-0 mt-[var(--space-2)] grid gap-[var(--space-1)] pl-[var(--space-4)]">
          {status.blockers.map((blocker) => (
            <li key={blocker.code}>{blocker.message}</li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

export function WorkspaceRow({
  defaultWorkspaceID,
  disabled,
  highlighted,
  onUnlink,
  workspace,
}: Readonly<{
  defaultWorkspaceID: string;
  disabled: boolean;
  highlighted: boolean;
  onUnlink: () => void;
  workspace: WorkspaceSummary;
}>) {
  const { t } = useTranslation();
  const isDefault = workspace.id === defaultWorkspaceID;
  return (
    <article
      className={cx(
        "grid min-w-0 grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-[var(--space-2)] rounded-[var(--radius-l)] border border-[var(--color-outline)] bg-[var(--color-island-1)] p-[var(--space-3)]",
        highlighted && "outline outline-2 outline-[var(--color-primary)]",
      )}
      data-testid="workspace-row"
    >
      <span className="min-w-0 truncate font-mono text-sm">{workspace.rootPath}</span>
      {isDefault ? (
        <span
          aria-label={t("projectEdit.default")}
          className="inline-flex items-center text-[var(--color-secondary)]"
          title={t("projectEdit.default")}
        >
          <Star aria-hidden="true" size={16} strokeWidth={1.5} />
        </span>
      ) : (
        <span aria-hidden="true" />
      )}
      <button
        aria-label={t("projectEdit.unlinkWorkspace", { path: workspace.rootPath })}
        className="grid h-9 w-9 place-items-center rounded-full border border-[var(--color-outline)] bg-transparent text-[var(--color-on-island)] disabled:cursor-not-allowed disabled:opacity-55"
        disabled={disabled}
        onClick={onUnlink}
        type="button"
      >
        <Link2Off aria-hidden="true" size={18} strokeWidth={1.5} />
      </button>
    </article>
  );
}

export function WorkspaceUnlinkDialog({
  disabled,
  onClose,
  onConfirm,
  workspace,
}: Readonly<{
  disabled: boolean;
  onClose: () => void;
  onConfirm: (workspace: WorkspaceSummary) => void;
  workspace: WorkspaceSummary | null;
}>) {
  const { t } = useTranslation();
  return (
    <Dialog
      closeLabel={t("app.close")}
      onClose={onClose}
      open={workspace !== null}
      title={t("projectEdit.unlinkTitle")}
    >
      {workspace !== null ? (
        <div className="grid gap-[var(--space-3)]">
          <p className="m-0">{t("projectEdit.unlinkBody")}</p>
          <p className="m-0 rounded-[var(--radius-m)] border border-[var(--color-outline)] bg-[var(--color-island-1)] p-[var(--space-3)] font-mono text-sm">
            {workspace.rootPath}
          </p>
          <div className="flex flex-wrap justify-end gap-[var(--space-2)]">
            <Button disabled={disabled} onClick={onClose} variant="secondary">
              {t("app.cancel")}
            </Button>
            <Button
              disabled={disabled}
              onClick={() => {
                onConfirm(workspace);
              }}
              variant="danger"
            >
              <span className="inline-flex items-center gap-[var(--space-2)]">
                <Unlink aria-hidden="true" size={16} strokeWidth={1.5} />
                {t("projectEdit.unlinkConfirm")}
              </span>
            </Button>
          </div>
        </div>
      ) : null}
    </Dialog>
  );
}
