import { useEffect } from "react";
import type { NativeBridge } from "@builder/desktop-native-bridge";

export type PendingWorkflowGraphDeleteConfirmation = Readonly<{
  requestID: string;
}>;

export function useWorkflowGraphDeleteConfirmationListener<
  TPending extends PendingWorkflowGraphDeleteConfirmation,
>({
  nativeBridge,
  onConfirmed,
  pendingDeleteRef,
}: Readonly<{
  nativeBridge: NativeBridge;
  onConfirmed: (deleteRequest: TPending) => void;
  pendingDeleteRef: { current: TPending | null };
}>): void {
  useEffect(() => {
    let disposed = false;
    let unlisten: (() => void) | null = null;
    void nativeBridge.workflowEditor
      .onGraphDeleteConfirmed((confirmation) => {
        const deleteRequest = pendingDeleteRef.current;
        if (deleteRequest?.requestID !== confirmation.requestID) {
          return;
        }
        onConfirmed(deleteRequest);
      })
      .then((nextUnlisten) => {
        if (disposed) {
          nextUnlisten();
          return;
        }
        unlisten = nextUnlisten;
      })
      .catch(() => undefined);
    return () => {
      disposed = true;
      unlisten?.();
    };
  }, [nativeBridge.workflowEditor, onConfirmed, pendingDeleteRef]);
}
