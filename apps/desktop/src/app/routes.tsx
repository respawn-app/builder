/* eslint-disable react-refresh/only-export-components -- TanStack Router route config intentionally colocates route components with route definitions. */
import { createRoute, createRouter, createRootRoute, Outlet, useLocation } from "@tanstack/react-router";
import { useEffect } from "react";
import { z } from "zod";

import { BoardRoute } from "../features/board/BoardRoute";
import { HomeRoute } from "../features/home/HomeRoute";
import { ProjectCreateWindowRoute } from "../features/home/ProjectCreateForm";
import { ProjectEditRoute } from "../features/project-edit/ProjectEditRoute";
import { StandaloneTaskRoute, TaskDetailWindowRoute } from "../features/task-detail/StandaloneTaskRoute";
import { StartupGate } from "../features/startup/StartupGate";
import { AppChrome } from "./AppChrome";

const optionalSearchString = z.preprocess(
  (value: unknown) => (typeof value === "string" ? value : ""),
  z.string(),
);

const projectSearchSchema = z.object({
  workflowId: optionalSearchString,
  taskId: optionalSearchString,
  resumeRunId: optionalSearchString,
});

const projectCreateSearchSchema = z.object({
  name: optionalSearchString,
  key: optionalSearchString,
  workspaceRoot: optionalSearchString,
});

const taskDetailSearchSchema = z.object({
  taskId: optionalSearchString,
  resumeRunId: optionalSearchString,
});

const storedProjectRouteSchema = z.object({
  projectId: z.string(),
  workflowId: z.string(),
});

const lastProjectRouteStorageKey = "builder.desktop.lastProjectRoute";
const routeRestoreSessionKey = "builder.desktop.routeRestoreChecked";
let routeRestoreCheckedFallback = false;

const rootRoute = createRootRoute({
  component: RootRoute,
});

const homeRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: HomeRoute,
});

const projectRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/projects/$projectId",
  validateSearch: (search: Record<string, unknown>) => projectSearchSchema.parse(search),
  component: ProjectRoute,
});

const projectEditRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/projects/$projectId/edit",
  component: ProjectEditShellRoute,
});

const taskRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/tasks/$taskId",
  component: TaskRoute,
});

const projectCreateRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/native-dialog/project-create",
  validateSearch: (search: Record<string, unknown>) => projectCreateSearchSchema.parse(search),
  component: ProjectCreateRoute,
});

const taskDetailWindowRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/native-dialog/task-detail",
  validateSearch: (search: Record<string, unknown>) => taskDetailSearchSchema.parse(search),
  component: TaskDetailNativeRoute,
});

const routeTree = rootRoute.addChildren([
  homeRoute,
  projectRoute,
  projectEditRoute,
  taskRoute,
  projectCreateRoute,
  taskDetailWindowRoute,
]);

export function createAppRouter() {
  return createRouter({ routeTree });
}

export type AppRouter = ReturnType<typeof createAppRouter>;

function RootRoute() {
  const isNativeDialogWindow =
    typeof window !== "undefined" && window.location.pathname.startsWith("/native-dialog/");
  if (isNativeDialogWindow) {
    return (
      <StartupGate>
        <Outlet />
      </StartupGate>
    );
  }

  return (
    <AppChrome>
      <RoutePersistence />
      <StartupGate>
        <Outlet />
      </StartupGate>
    </AppChrome>
  );
}

function RoutePersistence() {
  const navigate = rootRoute.useNavigate();
  const location = useLocation();

  useEffect(() => {
    if (claimRouteRestoreCheck()) {
      const restored = readLastProjectRoute();
      if (location.pathname === "/" && restored !== null) {
        void navigate({
          to: "/projects/$projectId",
          params: { projectId: restored.projectId },
          search: { workflowId: restored.workflowId, taskId: "", resumeRunId: "" },
          replace: true,
        });
      }
    }
    const current = projectRouteState(location.pathname, location.searchStr);
    if (current !== null) {
      writeLastProjectRoute(current);
    }
  }, [location.pathname, location.searchStr, navigate]);

  return null;
}

function projectRouteState(
  pathname: string,
  searchStr: string,
): Readonly<{ projectId: string; workflowId: string }> | null {
  const segments = pathname.split("/").filter((segment) => segment.length > 0);
  if (segments.length !== 2 || segments[0] !== "projects") {
    return null;
  }
  const params = new URLSearchParams(searchStr);
  return {
    projectId: decodeURIComponent(segments[1] ?? ""),
    workflowId: params.get("workflowId") ?? "",
  };
}

function readLastProjectRoute(): Readonly<{ projectId: string; workflowId: string }> | null {
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

function writeLastProjectRoute(route: Readonly<{ projectId: string; workflowId: string }>): void {
  safeStorage("local")?.setItem(lastProjectRouteStorageKey, JSON.stringify(route));
}

function claimRouteRestoreCheck(): boolean {
  const storage = safeStorage("session");
  if (storage === null) {
    const shouldRestore = !routeRestoreCheckedFallback;
    routeRestoreCheckedFallback = true;
    return shouldRestore;
  }
  if (storage.getItem(routeRestoreSessionKey) !== null) {
    return false;
  }
  storage.setItem(routeRestoreSessionKey, "1");
  return true;
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

function ProjectRoute() {
  const params = projectRoute.useParams();
  const search = projectRoute.useSearch();
  return (
    <BoardRoute
      projectId={params.projectId}
      resumeRunId={search.resumeRunId}
      selectedTaskId={search.taskId}
      workflowId={search.workflowId}
    />
  );
}

function ProjectEditShellRoute() {
  const params = projectEditRoute.useParams();
  return <ProjectEditRoute projectId={params.projectId} />;
}

function TaskRoute() {
  const params = taskRoute.useParams();
  return <StandaloneTaskRoute taskId={params.taskId} />;
}

function ProjectCreateRoute() {
  const search = projectCreateRoute.useSearch();
  return <ProjectCreateWindowRoute draft={search} />;
}

function TaskDetailNativeRoute() {
  const search = taskDetailWindowRoute.useSearch();
  return <TaskDetailWindowRoute resumeRunId={search.resumeRunId} taskId={search.taskId} />;
}

declare module "@tanstack/react-router" {
  interface Register {
    router: AppRouter;
  }
}
