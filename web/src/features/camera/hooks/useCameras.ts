import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { currentLanguage } from "@/shared/i18n";
import {
  createCamera,
  deleteCamera,
  diagnoseCamera,
  getCamera,
  getCredentials,
  listCameras,
  updateCamera,
} from "../api/cameraApi";
import type { CameraResponse, CreateCameraInput, UpdateCameraInput } from "../types";

export const CAMERAS_QUERY_KEY = ["cameras"] as const;

export function useCamerasQuery() {
  const { i18n } = useTranslation();
  const language = currentLanguage(i18n.resolvedLanguage ?? i18n.language);

  return useQuery({
    queryKey: [...CAMERAS_QUERY_KEY, language],
    queryFn: ({ signal }) => listCameras(language, signal),
    staleTime: 10_000,
  });
}

export function useCameraDetailQuery(id: string | null) {
  const { i18n } = useTranslation();
  const language = currentLanguage(i18n.resolvedLanguage ?? i18n.language);

  return useQuery({
    queryKey: [...CAMERAS_QUERY_KEY, "detail", id, language],
    queryFn: ({ signal }) => (id ? getCamera(id, language, signal) : Promise.reject(new Error("No ID"))),
    enabled: Boolean(id),
  });
}

export function useCameraCredentialsQuery(id: string | null) {
  const { i18n } = useTranslation();
  const language = currentLanguage(i18n.resolvedLanguage ?? i18n.language);

  return useQuery({
    queryKey: [...CAMERAS_QUERY_KEY, "credentials", id, language],
    queryFn: ({ signal }) => (id ? getCredentials(id, language, signal) : Promise.reject(new Error("No ID"))),
    enabled: Boolean(id),
  });
}

export function useCreateCameraMutation() {
  const queryClient = useQueryClient();
  const { i18n } = useTranslation();
  const language = currentLanguage(i18n.resolvedLanguage ?? i18n.language);

  return useMutation({
    mutationFn: (input: CreateCameraInput) => createCamera(input, language),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: CAMERAS_QUERY_KEY });
    },
  });
}

export function useUpdateCameraMutation() {
  const queryClient = useQueryClient();
  const { i18n } = useTranslation();
  const language = currentLanguage(i18n.resolvedLanguage ?? i18n.language);

  return useMutation({
    mutationFn: ({ id, input }: { id: string; input: UpdateCameraInput }) => updateCamera(id, input, language),
    onSuccess: (updatedCamera) => {
      // Optimistically update lists and invalidate queries
      queryClient.setQueriesData<CameraResponse[]>({ queryKey: CAMERAS_QUERY_KEY }, (old) => {
        if (!old) return [updatedCamera];
        return old.map((cam) => (cam.id === updatedCamera.id ? updatedCamera : cam));
      });
      void queryClient.invalidateQueries({ queryKey: CAMERAS_QUERY_KEY });
    },
  });
}

export function useDeleteCameraMutation() {
  const queryClient = useQueryClient();
  const { i18n } = useTranslation();
  const language = currentLanguage(i18n.resolvedLanguage ?? i18n.language);

  return useMutation({
    mutationFn: (id: string) => deleteCamera(id, language),
    onSuccess: (_, deletedId) => {
      queryClient.setQueriesData<CameraResponse[]>({ queryKey: CAMERAS_QUERY_KEY }, (old) => {
        if (!old) return [];
        return old.filter((cam) => cam.id !== deletedId);
      });
      void queryClient.invalidateQueries({ queryKey: CAMERAS_QUERY_KEY });
    },
  });
}

export function useDiagnoseCameraMutation() {
  const { i18n } = useTranslation();
  const language = currentLanguage(i18n.resolvedLanguage ?? i18n.language);

  return useMutation({
    mutationFn: (id: string) => diagnoseCamera(id, language),
  });
}
