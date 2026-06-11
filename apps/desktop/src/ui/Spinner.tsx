import { cx } from "./classes";

export type SpinnerProps = Readonly<{
  className?: string | undefined;
  size?: "sm" | "md";
  strokeWidth?: number | undefined;
  testID?: string | undefined;
}>;

export function Spinner({ className, size = "md", strokeWidth = 3, testID = "spinner" }: SpinnerProps) {
  return (
    <svg
      aria-hidden="true"
      className={cx(
        "motion-safe:animate-spin text-[var(--color-primary)]",
        size === "sm" ? "h-4 w-4" : "h-7 w-7",
        className,
      )}
      data-testid={testID}
      fill="none"
      viewBox="0 0 24 24"
    >
      <circle
        cx="12"
        cy="12"
        r="9"
        stroke="currentColor"
        strokeDasharray="42 18"
        strokeLinecap="round"
        strokeWidth={strokeWidth}
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  );
}
