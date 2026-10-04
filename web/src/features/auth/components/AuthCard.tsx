import type { ReactNode } from "react";
import { AlertCircle } from "lucide-react";

interface AuthCardProps {
  icon: ReactNode;
  title: string;
  subtitle: string;
  error?: string | null;
  children: ReactNode;
}

export function AuthCard({ icon, title, subtitle, error, children }: AuthCardProps) {
  return (
    <div className="w-full max-w-[360px] rounded-2xl bg-[var(--surface)] p-6 sm:p-7 shadow-[0_4px_20px_rgba(0,0,0,0.06)] dark:shadow-[0_8px_30px_rgba(0,0,0,0.35)] ring-1 ring-black/[0.06] dark:ring-white/[0.08] transition-all duration-200">
      <div className="mb-6 flex flex-col items-center text-center">
        <div className="mb-3.5 flex h-12 w-12 items-center justify-center rounded-2xl bg-[var(--surface-muted)] text-[var(--accent)] ring-1 ring-black/[0.04] dark:ring-white/[0.08] shadow-[0_1px_3px_rgba(0,0,0,0.03)]">
          {icon}
        </div>
        <h2 className="text-lg font-semibold tracking-tight text-[var(--foreground)]">
          {title}
        </h2>
        <p className="mt-1 max-w-[32ch] text-xs text-[var(--muted)] leading-relaxed">
          {subtitle}
        </p>
      </div>

      {error && (
        <div
          className="mb-4 flex items-start gap-2 rounded-xl bg-[var(--danger-soft)] p-2.5 text-xs text-[var(--danger)] leading-normal"
          role="alert"
        >
          <AlertCircle size={15} className="mt-0.5 shrink-0" aria-hidden="true" />
          <div className="flex-1 font-medium">{error}</div>
        </div>
      )}

      {children}
    </div>
  );
}
