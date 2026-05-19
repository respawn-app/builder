import { useLayoutEffect, useRef, type ReactNode } from "react";

import { useAppServices } from "../app/useAppServices";
import { cx } from "./classes";

export type NativeDialogWindowProps = Readonly<{
  title: string;
  children: ReactNode;
  fitToContent?: boolean;
  contentMaxWidth?: string;
  contentPadding?: "none" | "chrome";
  showHeader?: boolean;
  surface?: "island" | "transparent";
}>;

export function NativeDialogWindow({
  title,
  children,
  fitToContent = true,
  contentMaxWidth = "var(--content-max-width-dialog)",
  contentPadding = "none",
  showHeader = true,
  surface = "island",
}: NativeDialogWindowProps) {
  const { logger, nativeBridge } = useAppServices();
  const shellRef = useRef<HTMLElement | null>(null);

  useLayoutEffect(() => {
    if (!fitToContent) {
      return undefined;
    }
    const shell = shellRef.current;
    if (shell === null) {
      return undefined;
    }
    let lastSize = "";
    let frame = 0;
    const fit = () => {
      const rect = shell.getBoundingClientRect();
      const width = Math.ceil(rect.width);
      const height = Math.ceil(rect.height);
      const key = `${width.toString()}x${height.toString()}`;
      if (key === lastSize || width <= 0 || height <= 0) {
        return;
      }
      lastSize = key;
      void nativeBridge.window.fitCurrentToContent({ height, width }).catch((error: unknown) => {
        void logger.append("warn", "Fit native dialog window failed.", {
          error: error instanceof Error ? error.message : "unknown",
        });
      });
    };
    const scheduleFit = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(fit);
    };
    scheduleFit();
    const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(scheduleFit);
    observer?.observe(shell);
    return () => {
      cancelAnimationFrame(frame);
      observer?.disconnect();
    };
  }, [fitToContent, logger, nativeBridge.window]);

  return (
    <main
      className={cx(
        "window-glass-fill grid",
        contentPadding === "chrome"
          ? "pt-[var(--native-titlebar-height)]"
          : "p-[var(--native-titlebar-height)_var(--space-2)_var(--space-2)]",
        fitToContent ? "w-max" : "h-screen w-screen",
      )}
      ref={shellRef}
    >
      <div
        className="app-region-drag fixed inset-x-0 top-0 h-[var(--native-titlebar-height)]"
        data-tauri-drag-region
      />
      <section
        aria-label={title}
        aria-modal="true"
        className={cx(
          "app-region-no-drag grid min-h-0 gap-[var(--space-3)]",
          surface === "island" && "island-glass rounded-[var(--radius-xl)] p-[var(--space-4)]",
          surface === "transparent" && "bg-transparent p-0 shadow-none",
          showHeader ? "grid-rows-[auto_minmax(0,1fr)]" : "grid-rows-[minmax(0,1fr)]",
          fitToContent ? "w-max" : "h-full",
        )}
        role="dialog"
      >
        <div
          className={cx(
            "mx-auto grid h-full min-h-0 w-full gap-[var(--space-3)]",
            showHeader ? "grid-rows-[auto_minmax(0,1fr)]" : "grid-rows-[minmax(0,1fr)]",
          )}
          data-testid="native-dialog-content"
          style={{ maxWidth: contentMaxWidth }}
        >
          {showHeader ? (
            <header className="min-w-0">
              <h1 className="m-0 text-[1.15rem] font-bold">{title}</h1>
            </header>
          ) : null}
          <div
            className={cx(
              "min-h-0 overflow-auto hide-scrollbar",
              contentPadding === "chrome" && "px-[var(--space-2)] pb-[var(--space-2)] pt-0",
            )}
            data-testid="native-dialog-scrollport"
          >
            {children}
          </div>
        </div>
      </section>
    </main>
  );
}
