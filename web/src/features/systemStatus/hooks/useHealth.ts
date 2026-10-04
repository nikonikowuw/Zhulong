import { currentLanguage } from "@/shared/i18n";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { getHealth } from "../api/health";

export function useHealth() {
  const { i18n } = useTranslation();
  const language = currentLanguage(i18n.resolvedLanguage ?? i18n.language);

  return useQuery({
    queryKey: ["system", "health", language],
    queryFn: ({ signal }) => getHealth(language, signal),
    staleTime: 10_000,
    refetchInterval: (query) => query.state.status === "success" ? 30_000 : false,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: false,
    retry: false,
  });
}
