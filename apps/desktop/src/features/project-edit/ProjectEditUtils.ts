import type { WorkspaceSummary } from "../../api";

export function projectNameErrors(value: string, t: (key: string) => string): readonly string[] {
  const errors: string[] = [];
  const visibleLength = value.trim().length;
  if (visibleLength < 1 || visibleLength > 80) {
    errors.push(t("form.projectNameLength"));
  }
  if (value !== value.trim()) {
    errors.push(t("form.noEdgeWhitespace"));
  }
  if (hasLineBreak(value)) {
    errors.push(t("form.singleLine"));
  }
  return errors;
}

export function findWorkspaceByPath(
  workspaces: readonly WorkspaceSummary[],
  path: string,
): WorkspaceSummary | undefined {
  return workspaces.find((workspace) => workspace.rootPath === path);
}

function hasLineBreak(value: string): boolean {
  for (const char of value) {
    if (char === "\n" || char === "\r") {
      return true;
    }
  }
  return false;
}
