import type { ReactNode } from "react";

import { Badge } from "./Badge";
import { Button } from "./Button";

export type StatusNotice = Readonly<{
  id: string;
  tone: "info" | "success" | "warning" | "danger";
  title: string;
  body: string;
  actionLabel?: string;
  onAction?: () => void;
  dismissible?: boolean;
}>;

export type StatusSurfaceProps = Readonly<{
  notices: readonly StatusNotice[];
  dismissLabel: string;
  children?: ReactNode;
  onDismiss: (id: string) => void;
}>;

export function StatusSurface({ notices, dismissLabel, children, onDismiss }: StatusSurfaceProps) {
  return (
    <>
      {children}
      <div
        aria-live="polite"
        className="app-region-no-drag fixed right-[var(--space-4)] bottom-[var(--space-4)] z-[60] grid w-[min(380px,calc(100vw-32px))] gap-[var(--space-3)]"
      >
        {notices.map((notice) => (
          <article
            className="island-glass grid animate-[surface-reveal_var(--motion-normal)] gap-[var(--space-2)] rounded-[var(--radius-l)] p-[var(--space-3)]"
            key={notice.id}
          >
            <Badge tone={notice.tone}>{notice.title}</Badge>
            <p className="m-0 text-sm text-[var(--color-on-island)]">{notice.body}</p>
            <div className="flex flex-wrap justify-end gap-[var(--space-2)]">
              {notice.actionLabel !== undefined && notice.onAction !== undefined ? (
                <Button onClick={notice.onAction} variant="ghost">
                  {notice.actionLabel}
                </Button>
              ) : null}
              {notice.dismissible === false ? null : (
                <Button
                  onClick={() => {
                    onDismiss(notice.id);
                  }}
                  variant="ghost"
                >
                  {dismissLabel}
                </Button>
              )}
            </div>
          </article>
        ))}
      </div>
    </>
  );
}
