import { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";

import { useStatusController } from "../../app/useStatusController";

export type BoardMoveRunTracker = Readonly<{
  observeInterruptedRun: (input: Readonly<{ runID: string; taskID: string }>) => void;
  trackMoveRunIDs: (result: Readonly<{ runIDs: readonly string[] }>) => void;
}>;

export function useBoardMoveRunFeedback(): BoardMoveRunTracker {
  const { t } = useTranslation();
  const { push } = useStatusController();
  const [pendingMoveRunIDs, setPendingMoveRunIDs] = useState<ReadonlySet<string>>(() => new Set());

  const trackMoveRunIDs = useCallback((result: Readonly<{ runIDs: readonly string[] }>): void => {
    const runIDs = result.runIDs.map((runID) => runID.trim()).filter((runID) => runID.length > 0);
    if (runIDs.length === 0) {
      return;
    }
    setPendingMoveRunIDs((current) => new Set([...current, ...runIDs]));
  }, []);

  const observeInterruptedRun = useCallback(
    (input: Readonly<{ runID: string; taskID: string }>): void => {
      const runID = input.runID.trim();
      if (runID.length === 0) {
        return;
      }
      // Gate the notification on the membership check inside the state updater so
      // concurrent observations of the same runID can never both pass a stale
      // snapshot and queue duplicate notifications.
      let removed = false;
      setPendingMoveRunIDs((current) => {
        if (!current.has(runID)) {
          return current;
        }
        removed = true;
        const next = new Set(current);
        next.delete(runID);
        return next;
      });
      if (!removed) {
        return;
      }
      push({
        id: `board-move-run-interrupted-${runID}`,
        tone: "danger",
        title: t("board.moveRunInterrupted"),
        body: t("board.moveRunInterruptedBody", { runID, taskID: input.taskID }),
        dismissible: false,
      });
    },
    [push, t],
  );

  return { observeInterruptedRun, trackMoveRunIDs };
}
