import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";

import { queryKeys } from "./queryKeys";
import { useConnectionSnapshot } from "./useConnectionSnapshot";

export function useReconnectRefresh() {
  const connection = useConnectionSnapshot();
  const queryClient = useQueryClient();
  const sawDisconnectRef = useRef(false);

  useEffect(() => {
    if (connection.phase === "disconnected") {
      sawDisconnectRef.current = true;
      return;
    }
    if (connection.phase !== "connected" || !sawDisconnectRef.current) {
      return;
    }
    sawDisconnectRef.current = false;
    void refreshVisibleQueries(queryClient);
  }, [connection.generation, connection.phase, queryClient]);
}

async function refreshVisibleQueries(queryClient: ReturnType<typeof useQueryClient>): Promise<void> {
  await Promise.all([
    queryClient.invalidateQueries({ queryKey: queryKeys.projects }),
    queryClient.invalidateQueries({ queryKey: ["attention"] }),
    queryClient.invalidateQueries({ queryKey: ["board"] }),
    queryClient.invalidateQueries({ queryKey: ["project-edit"] }),
    queryClient.invalidateQueries({ queryKey: ["workspaces"] }),
    queryClient.invalidateQueries({ queryKey: ["task"] }),
    queryClient.invalidateQueries({ queryKey: ["activity"] }),
    queryClient.invalidateQueries({ queryKey: ["pending-asks"] }),
  ]);
}
