import { Link } from "@tanstack/react-router";
import { Home } from "lucide-react";
import type { PointerEvent, ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { useAppServices } from "./useAppServices";

export type AppChromeProps = Readonly<{
  children: ReactNode;
}>;

export function AppChrome({ children }: AppChromeProps) {
  const { t } = useTranslation();
  const { nativeBridge } = useAppServices();

  return (
    <main className="window-glass-fill grid h-screen w-screen overflow-hidden p-[var(--native-titlebar-height)_var(--space-2)_var(--space-2)]">
      <div
        className="app-region-drag fixed inset-x-0 top-0 z-20 h-[var(--native-titlebar-height)]"
        data-tauri-drag-region
        onPointerDown={(event) => {
          void startNativeWindowDrag(event, nativeBridge.window.startDragging);
        }}
      />
      <Link
        aria-label={t("app.home")}
        className={`app-region-no-drag fixed top-[8px] z-30 grid h-6 w-6 place-items-center rounded-full border border-transparent text-[var(--color-on-island)] ${isMacOS() ? "left-[var(--native-home-link-left-macos)]" : "left-[var(--native-home-link-left-default)]"}`}
        to="/"
      >
        <Home aria-hidden="true" size={16} strokeWidth={1.125} />
      </Link>
      <div className="app-region-no-drag min-h-0 overflow-hidden" data-testid="app-shell-content">
        {children}
      </div>
    </main>
  );
}

function isMacOS(): boolean {
  return typeof navigator !== "undefined" && /Mac OS|Macintosh/u.test(navigator.userAgent);
}

async function startNativeWindowDrag(
  event: PointerEvent<HTMLDivElement>,
  startDragging: () => Promise<void>,
): Promise<void> {
  if (event.button !== 0) {
    return;
  }
  event.preventDefault();
  await startDragging();
}
