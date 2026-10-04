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
    <div className="flex min-h-[60vh] items-center justify-center p-4">
      <div className="w-full max-w-md rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-8 shadow-[var(--card-shadow)]">
        <div className="mb-6 flex flex-col items-center text-center">
          <div className="mb-3 flex h-12 w-12 items-center justify-center rounded-full bg-[var(--surface-muted)] text-[var(--accent)]">
            {icon}
          </div>
          <h2 className="text-2xl font-bold tracking-tight text-[var(--foreground)]">
            {title}
          </h2>
          <p className="mt-1 text-sm text-[var(--muted)]">{subtitle}</p>
        </div>

        {error && (
          <div
            className="mb-6 flex items-start gap-3 rounded-lg border border-[var(--danger)] bg-[var(--danger-soft)] p-3 text-sm text-[var(--danger)]"
            role="alert"
          >
            <AlertCircle size={18} className="mt-0.5 shrink-0" aria-hidden="true" />
            <div className="flex-1 font-medium">{error}</div>
          </div>
        )}

        {children}
      </div>
    </div>
  );
}
