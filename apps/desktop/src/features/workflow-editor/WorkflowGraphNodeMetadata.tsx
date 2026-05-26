import { useTranslation } from "react-i18next";

export type CopyText = (value: string) => Promise<void> | void;

export function WorkflowNodeInfoTooltipContent({
  nodeID,
  nodeKey,
  onCopyText,
}: Readonly<{ nodeID: string; nodeKey: string; onCopyText: CopyText }>) {
  const { t } = useTranslation();
  const keyLabel = t("workflowEditor.key");
  const idLabel = t("workflowEditor.id");
  return (
    <>
      <CopyableNodeValue
        copyLabel={t("workflowEditor.copyNodeMetadata", { label: keyLabel, value: nodeKey })}
        label={keyLabel}
        onCopyText={onCopyText}
        value={nodeKey}
      />
      <CopyableNodeValue
        copyLabel={t("workflowEditor.copyNodeMetadata", { label: idLabel, value: nodeID })}
        label={idLabel}
        onCopyText={onCopyText}
        value={nodeID}
      />
    </>
  );
}

function CopyableNodeValue({
  copyLabel,
  label,
  onCopyText,
  value,
}: Readonly<{ copyLabel: string; label: string; onCopyText: CopyText; value: string }>) {
  return (
    <button
      aria-label={copyLabel}
      className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] items-baseline gap-2 rounded-sm bg-transparent px-1.5 py-0.5 text-left outline-none hover:bg-[var(--color-island-2)] focus-visible:bg-[var(--color-island-2)] focus-visible:outline-none"
      onClick={(event) => {
        event.stopPropagation();
        copyNodeText(value, onCopyText);
      }}
      type="button"
    >
      <span className="text-[0.68rem] font-bold uppercase tracking-[0.14em] opacity-70">
        {label}
      </span>
      <span className="min-w-0 break-all font-mono text-sm">{value}</span>
    </button>
  );
}

function copyNodeText(value: string, onCopyText: CopyText): void {
  try {
    void Promise.resolve(onCopyText(value)).catch(() => undefined);
  } catch {
    return;
  }
}
