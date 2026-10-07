import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { currentLanguage, type SupportedLanguage } from "@/shared/i18n";
import { clearAuditLogs, fetchAuditLogs } from "../api/auditApi";
import type { AuditLogFilterParams } from "../types";

export const AUDIT_LOGS_QUERY_KEY = ["auditLogs"] as const;

function useCurrentLanguage(): SupportedLanguage {
  const { i18n } = useTranslation();
  return currentLanguage(i18n.resolvedLanguage ?? i18n.language);
}

export function useAuditLogsQuery(params: AuditLogFilterParams) {
  const language = useCurrentLanguage();

  return useQuery({
    queryKey: [...AUDIT_LOGS_QUERY_KEY, params, language],
    queryFn: ({ signal }) => fetchAuditLogs(params, language, signal),
    staleTime: 5_000,
  });
}

export function useClearAuditLogsMutation() {
  const queryClient = useQueryClient();
  const language = useCurrentLanguage();

  return useMutation({
    mutationFn: (before?: string) => clearAuditLogs(language, before),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: AUDIT_LOGS_QUERY_KEY });
    },
  });
}
