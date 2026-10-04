import {
  useEffect,
  useState,
  type ReactNode,
} from "react";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { onUnauthorized } from "@/shared/api/client";
import { currentLanguage } from "@/shared/i18n";
import {
  getAuthStatus,
  getMe,
  initAdmin as apiInitAdmin,
  login as apiLogin,
  logout as apiLogout,
} from "../api/authApi";
import type {
  AuthPhase,
  InitAdminPayload,
  LoginPayload,
} from "../types";
import { AuthContext } from "./authContext";

export function AuthProvider({ children }: { children: ReactNode }) {
  const { t, i18n } = useTranslation();
  const queryClient = useQueryClient();
  const language = currentLanguage(i18n.resolvedLanguage ?? i18n.language);
  const [sessionError, setSessionError] = useState<string | null>(null);

  // 1. Query system initialization status
  const statusQuery = useQuery({
    queryKey: ["auth", "status", language],
    queryFn: ({ signal }) => getAuthStatus(language, signal),
    retry: false,
    staleTime: 30_000,
  });

  // 2. Query current authenticated user (only when system is initialized)
  const isInitialized = statusQuery.data?.initialized;
  const meQuery = useQuery({
    queryKey: ["auth", "me", language],
    queryFn: ({ signal }) => getMe(language, signal),
    enabled: isInitialized === true,
    retry: false,
    staleTime: 60_000,
  });

  // 3. Listen for global 401 unauthorized notifications on protected routes
  useEffect(() => {
    const unsubscribe = onUnauthorized(() => {
      queryClient.setQueryData(["auth", "me", language], null);
      setSessionError(t("auth.sessionExpired"));
    });
    return unsubscribe;
  }, [queryClient, language, t]);

  // 4. Derive authentication phase strictly from queries and state
  let phase: AuthPhase;
  if (statusQuery.isLoading || (isInitialized && meQuery.isLoading)) {
    phase = "loading";
  } else if (statusQuery.isError) {
    phase = "error";
  } else if (isInitialized === false) {
    phase = "uninitialized";
  } else if (meQuery.data) {
    phase = "authenticated";
  } else {
    phase = "unauthenticated";
  }

  const user = meQuery.data ?? null;
  const activeError =
    sessionError ||
    (statusQuery.error instanceof Error ? statusQuery.error.message : null);

  const clearError = () => {
    setSessionError(null);
  };

  const handleInitAdmin = async (payload: InitAdminPayload) => {
    setSessionError(null);
    const createdUser = await apiInitAdmin(payload, language);
    queryClient.setQueryData(["auth", "status", language], { initialized: true });
    queryClient.setQueryData(["auth", "me", language], createdUser);
  };

  const handleLogin = async (payload: LoginPayload) => {
    setSessionError(null);
    const loggedInUser = await apiLogin(payload, language);
    queryClient.setQueryData(["auth", "status", language], { initialized: true });
    queryClient.setQueryData(["auth", "me", language], loggedInUser);
  };

  const handleLogout = async () => {
    try {
      await apiLogout(language);
    } finally {
      queryClient.setQueryData(["auth", "me", language], null);
      setSessionError(null);
    }
  };

  const checkAuth = async () => {
    setSessionError(null);
    await queryClient.invalidateQueries({ queryKey: ["auth"] });
  };

  return (
    <AuthContext.Provider
      value={{
        phase,
        user,
        error: activeError,
        clearError,
        initAdmin: handleInitAdmin,
        login: handleLogin,
        logout: handleLogout,
        checkAuth,
      }}
    >
      {children}
    </AuthContext.Provider>
  );
}
