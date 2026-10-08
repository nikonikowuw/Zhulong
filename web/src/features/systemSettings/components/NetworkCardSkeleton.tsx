import type { FC } from "react";

export const NetworkCardSkeleton: FC = () => {
  return (
    <div
      className="flex flex-col justify-between rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-5 shadow-xs animate-pulse"
      aria-hidden="true"
    >
      <div>
        {/* Header Skeleton */}
        <div className="flex items-center justify-between mb-4">
          <div className="flex items-center gap-3">
            <div className="h-10 w-10 rounded-xl bg-[var(--surface-muted)] shrink-0" />
            <div className="space-y-1.5">
              <div className="flex items-center gap-2">
                <div className="h-4 w-16 rounded bg-[var(--surface-muted)]" />
                <div className="h-4 w-20 rounded-full bg-[var(--surface-muted)]" />
              </div>
              <div className="h-3 w-28 rounded bg-[var(--surface-muted)]" />
            </div>
          </div>
          <div className="flex items-center gap-2">
            <div className="h-5 w-16 rounded-full bg-[var(--surface-muted)]" />
            <div className="h-5 w-14 rounded-md bg-[var(--surface-muted)]" />
          </div>
        </div>

        {/* Details Grid Skeleton */}
        <div className="grid grid-cols-2 gap-3 rounded-xl border border-[var(--border-subtle)] bg-[var(--surface-muted)]/60 p-3.5">
          <div className="space-y-1">
            <div className="h-2.5 w-12 rounded bg-[var(--border)]" />
            <div className="h-3.5 w-24 rounded bg-[var(--border)]" />
          </div>
          <div className="space-y-1">
            <div className="h-2.5 w-12 rounded bg-[var(--border)]" />
            <div className="h-3.5 w-20 rounded bg-[var(--border)]" />
          </div>
          <div className="col-span-2 space-y-1 pt-1">
            <div className="h-2.5 w-16 rounded bg-[var(--border)]" />
            <div className="h-3.5 w-36 rounded bg-[var(--border)]" />
          </div>
        </div>
      </div>

      {/* Footer Skeleton */}
      <div className="mt-4 flex items-center justify-end">
        <div className="h-8 w-20 rounded-xl bg-[var(--surface-muted)]" />
      </div>
    </div>
  );
};
