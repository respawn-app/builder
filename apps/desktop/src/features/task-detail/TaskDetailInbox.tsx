import { useTranslation } from "react-i18next";

import type { AttentionItem, TaskDetail } from "../../api";
import { Island } from "../../ui";
import { ApprovalBox, QuestionBox } from "./TaskDetailAttention";
import type { useTaskMutations } from "./useTaskDetailData";

export function TaskInbox({
  currentVersion,
  detail,
  disabled,
  mutations,
}: Readonly<{
  currentVersion: number;
  detail: TaskDetail;
  disabled: boolean;
  mutations: ReturnType<typeof useTaskMutations>;
}>) {
  return (
    <>
      {detail.attention.map((item) => (
        <InboxItem
          attention={item}
          currentVersion={currentVersion}
          disabled={disabled}
          key={item.id}
          mutations={mutations}
          taskId={detail.id}
          transitions={detail.transitions}
        />
      ))}
    </>
  );
}

function InboxItem({
  attention,
  currentVersion,
  disabled,
  mutations,
  taskId,
  transitions,
}: Readonly<{
  attention: AttentionItem;
  currentVersion: number;
  disabled: boolean;
  mutations: ReturnType<typeof useTaskMutations>;
  taskId: string;
  transitions: TaskDetail["transitions"];
}>) {
  const { t } = useTranslation();
  if (attention.kind === "question") {
    return <QuestionBox attention={attention} disabled={disabled} mutations={mutations} taskId={taskId} />;
  }
  if (attention.kind === "approval") {
    return (
      <ApprovalBox
        attention={attention}
        currentVersion={currentVersion}
        disabled={disabled}
        mutations={mutations}
        transitions={transitions}
      />
    );
  }
  return (
    <Island aria-label={attention.kind || t("task.inbox")} className="grid gap-[var(--space-2)]">
      <h3 className="m-0">{attention.kind || t("task.inbox")}</h3>
      <p className="m-0">{attention.message}</p>
    </Island>
  );
}
