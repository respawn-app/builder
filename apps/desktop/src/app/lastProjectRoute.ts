import { z } from "zod";

const lastProjectRouteStorageKey = "builder.desktop.lastProjectRoute";
const storedProjectRouteSchema = z.object({
  projectId: z.string(),
  workflowId: z.string(),
});

export type StoredProjectRoute = Readonly<{
  projectId: string;
  workflowId: string;
}>;

export function readLastProjectRoute(): StoredProjectRoute | null {
  const storage = safeStorage("local");
  const raw = storage?.getItem(lastProjectRouteStorageKey) ?? null;
  if (raw === null) {
    return null;
  }
  try {
    const parsed: unknown = JSON.parse(raw);
    const result = storedProjectRouteSchema.safeParse(parsed);
    if (!result.success) {
      return null;
    }
    return result.data;
  } catch {
    return null;
  }
}

export function writeLastProjectRoute(route: StoredProjectRoute): void {
  safeStorage("local")?.setItem(lastProjectRouteStorageKey, JSON.stringify(route));
}

export function clearLastProjectRouteForProject(projectID: string): void {
  const restored = readLastProjectRoute();
  if (restored?.projectId === projectID) {
    safeStorage("local")?.removeItem(lastProjectRouteStorageKey);
  }
}

function safeStorage(kind: "local" | "session"): Storage | null {
  try {
    if (kind === "local") {
      return globalThis.localStorage;
    }
    return globalThis.sessionStorage;
  } catch {
    return null;
  }
}
