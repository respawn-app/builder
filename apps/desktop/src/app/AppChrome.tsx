import { Link } from "@tanstack/react-router";
import { Home } from "lucide-react";
import type { PointerEvent, ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { Island } from "../ui";
import { useAppServices } from "./useAppServices";

export type AppChromeProps = Readonly<{
  children: ReactNode;
}>;

export function AppChrome({ children }: AppChromeProps) {
  const { t } = useTranslation();
  const { nativeBridge } = useAppServices();

  return (
    <main className="app-shell">
      <div
        className="native-titlebar-drag-region"
        data-tauri-drag-region
        onPointerDown={(event) => {
          void startNativeWindowDrag(event, nativeBridge.window.startDragging);
        }}
      />
      <Link
        aria-label={t("app.home")}
        className={`native-home-link ${isMacOS() ? "native-home-link--macos" : "native-home-link--default"}`}
        to="/"
      >
        <Home aria-hidden="true" size={16} strokeWidth={1.125} />
      </Link>
      <Island className="app-surface" tone="primary">
        {children}
      </Island>
    </main>
  );
}

function isMacOS(): boolean {
  return typeof navigator !== "undefined" && /Mac OS|Macintosh/u.test(navigator.userAgent);
}

async function startNativeWindowDrag(event: PointerEvent<HTMLDivElement>, startDragging: () => Promise<void>): Promise<void> {
  if (event.button !== 0) {
    return;
  }
  event.preventDefault();
  await startDragging();
}
