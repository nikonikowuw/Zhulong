import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { currentLanguage, type SupportedLanguage } from "@/shared/i18n";
import {
  applyNetworkConfig,
  confirmNetworkConfig,
  fetchNetworkInterfaces,
  fetchNetworkStatus,
  pingNetworkTarget,
  rollbackNetworkConfig,
} from "../api/networkApi";
import type { InterfaceConfig } from "../types";

export const NETWORK_INTERFACES_KEY = ["network", "interfaces"] as const;
export const NETWORK_STATUS_KEY = ["network", "status"] as const;

function useCurrentLanguage(): SupportedLanguage {
  const { i18n } = useTranslation();
  return currentLanguage(i18n.resolvedLanguage ?? i18n.language);
}

export function useNetworkInterfacesQuery() {
  const language = useCurrentLanguage();

  return useQuery({
    queryKey: [...NETWORK_INTERFACES_KEY, language],
    queryFn: ({ signal }) => fetchNetworkInterfaces(language, signal),
    staleTime: 5_000,
  });
}

export function useNetworkStatusQuery(refetchInterval: number | false = 3_000) {
  const language = useCurrentLanguage();

  return useQuery({
    queryKey: [...NETWORK_STATUS_KEY, language],
    queryFn: ({ signal }) => fetchNetworkStatus(language, signal),
    refetchInterval,
  });
}

export function useApplyNetworkConfigMutation() {
  const queryClient = useQueryClient();
  const language = useCurrentLanguage();

  return useMutation({
    mutationFn: ({ iface, config }: { iface: string; config: InterfaceConfig }) =>
      applyNetworkConfig(iface, config, language),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: NETWORK_INTERFACES_KEY });
      void queryClient.invalidateQueries({ queryKey: NETWORK_STATUS_KEY });
    },
  });
}

export function useConfirmNetworkMutation() {
  const queryClient = useQueryClient();
  const language = useCurrentLanguage();

  return useMutation({
    mutationFn: (token: string) => confirmNetworkConfig(token, language),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: NETWORK_INTERFACES_KEY });
      void queryClient.invalidateQueries({ queryKey: NETWORK_STATUS_KEY });
    },
  });
}

export function useRollbackNetworkMutation() {
  const queryClient = useQueryClient();
  const language = useCurrentLanguage();

  return useMutation({
    mutationFn: (token: string) => rollbackNetworkConfig(token, language),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: NETWORK_INTERFACES_KEY });
      void queryClient.invalidateQueries({ queryKey: NETWORK_STATUS_KEY });
    },
  });
}

export function usePingTargetMutation() {
  const language = useCurrentLanguage();

  return useMutation({
    mutationFn: (target: string) => pingNetworkTarget(target, language),
  });
}
