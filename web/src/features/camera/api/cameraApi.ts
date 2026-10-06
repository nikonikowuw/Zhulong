import type { SupportedLanguage } from "@/shared/i18n";
import { getApiData, postApiData, putApiData, deleteApiData } from "@/shared/api/client";
import { z } from "zod";
import {
  cameraListSchema,
  cameraResponseSchema,
  cameraCredentialsResponseSchema,
  diagnoseResponseSchema,
  type CameraResponse,
  type CameraCredentialsResponse,
  type DiagnoseResponse,
  type CreateCameraInput,
  type UpdateCameraInput,
} from "../types";

export function listCameras(
  language: SupportedLanguage,
  signal?: AbortSignal,
): Promise<CameraResponse[]> {
  return getApiData("/api/v1/cameras", cameraListSchema, language, signal);
}

export function getCamera(
  id: string,
  language: SupportedLanguage,
  signal?: AbortSignal,
): Promise<CameraResponse> {
  return getApiData(`/api/v1/cameras/${encodeURIComponent(id)}`, cameraResponseSchema, language, signal);
}

export function createCamera(
  payload: CreateCameraInput,
  language: SupportedLanguage,
  signal?: AbortSignal,
): Promise<CameraResponse> {
  return postApiData("/api/v1/cameras", payload, cameraResponseSchema, language, signal);
}

export function updateCamera(
  id: string,
  payload: UpdateCameraInput,
  language: SupportedLanguage,
  signal?: AbortSignal,
): Promise<CameraResponse> {
  return putApiData(`/api/v1/cameras/${encodeURIComponent(id)}`, payload, cameraResponseSchema, language, signal);
}

export function deleteCamera(
  id: string,
  language: SupportedLanguage,
  signal?: AbortSignal,
): Promise<unknown> {
  return deleteApiData(`/api/v1/cameras/${encodeURIComponent(id)}`, z.unknown(), language, signal);
}

export function diagnoseCamera(
  id: string,
  language: SupportedLanguage,
  signal?: AbortSignal,
): Promise<DiagnoseResponse> {
  return postApiData(`/api/v1/cameras/${encodeURIComponent(id)}/diagnose`, {}, diagnoseResponseSchema, language, signal);
}

export function getCredentials(
  id: string,
  language: SupportedLanguage,
  signal?: AbortSignal,
): Promise<CameraCredentialsResponse> {
  return getApiData(`/api/v1/cameras/${encodeURIComponent(id)}/credentials`, cameraCredentialsResponseSchema, language, signal);
}
