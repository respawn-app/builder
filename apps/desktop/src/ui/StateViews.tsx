import type { ReactNode } from "react";

import { Button } from "./Button";
import { Island } from "./Island";

export type LoadingStateProps = Readonly<{
  title: string;
  body: string;
}>;

export function LoadingState({ title, body }: LoadingStateProps) {
  return (
    <Island
      className="grid animate-[surface-reveal_var(--motion-normal)] place-items-start gap-[var(--space-3)]"
      tone="floating"
    >
      <div
        className="h-6 w-6 motion-safe:animate-spin rounded-full border-[3px] border-[var(--color-outline)] border-t-[var(--color-primary)]"
        aria-hidden="true"
      />
      <h2>{title}</h2>
      <p>{body}</p>
    </Island>
  );
}

export type EmptyStateProps = Readonly<{
  title: string;
  body: string;
  action?: ReactNode;
}>;

export function EmptyState({ title, body, action }: EmptyStateProps) {
  return (
    <Island
      className="grid animate-[surface-reveal_var(--motion-normal)] place-items-start gap-[var(--space-3)]"
      tone="secondary"
    >
      <h2>{title}</h2>
      <p>{body}</p>
      {action !== undefined ? <div>{action}</div> : null}
    </Island>
  );
}

export type ErrorStateProps = Readonly<{
  title: string;
  body: string;
  retryLabel?: string;
  onRetry?: () => void;
  children?: ReactNode;
}>;

export function ErrorState({ title, body, retryLabel, onRetry, children }: ErrorStateProps) {
  return (
    <Island
      className="grid animate-[surface-reveal_var(--motion-normal)] place-items-start gap-[var(--space-3)]"
      tone="floating"
    >
      <h2>{title}</h2>
      <p>{body}</p>
      {children}
      {retryLabel !== undefined && onRetry !== undefined ? (
        <Button onClick={onRetry} variant="primary">
          {retryLabel}
        </Button>
      ) : null}
    </Island>
  );
}
