import type { SupportedLanguage } from "@/shared/i18n";
import { getApiData, postApiData } from "@/shared/api/client";
import { z } from "zod";
import {
  authStatusSchema,
  userSchema,
  type AuthStatus,
  type InitAdminPayload,
  type LoginPayload,
  type User,
} from "../types";

export function getAuthStatus(
  language: SupportedLanguage,
  signal?: AbortSignal,
): Promise<AuthStatus> {
  return getApiData("/api/v1/auth/status", authStatusSchema, language, signal);
}

export function initAdmin(
  payload: InitAdminPayload,
  language: SupportedLanguage,
  signal?: AbortSignal,
): Promise<User> {
  return postApiData("/api/v1/auth/init", payload, userSchema, language, signal);
}

export function login(
  payload: LoginPayload,
  language: SupportedLanguage,
  signal?: AbortSignal,
): Promise<User> {
  return postApiData("/api/v1/auth/login", payload, userSchema, language, signal);
}

const logoutResponseSchema = z.object({
  loggedOut: z.boolean().optional(),
});

export function logout(
  language: SupportedLanguage,
  signal?: AbortSignal,
): Promise<{ loggedOut?: boolean }> {
  return postApiData("/api/v1/auth/logout", {}, logoutResponseSchema, language, signal);
}

export function getMe(
  language: SupportedLanguage,
  signal?: AbortSignal,
): Promise<User> {
  return getApiData("/api/v1/auth/me", userSchema, language, signal);
}
