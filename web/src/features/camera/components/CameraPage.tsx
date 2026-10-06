import { useState } from "react";
import {
  AlertTriangle,
  LoaderCircle,
  Plus,
  RefreshCw,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import {
  useCamerasQuery,
  useCreateCameraMutation,
  useUpdateCameraMutation,
  useDeleteCameraMutation,
  useDiagnoseCameraMutation,
} from "../hooks/useCameras";
import { useCameraEvents } from "../hooks/useCameraEvents";
import { useCameraFilter } from "../hooks/useCameraFilter";
import { CameraDashboard } from "./CameraDashboard";
import { CameraCard } from "./CameraCard";
import { CameraFilterBar } from "./CameraFilterBar";
import { CameraEmptyState } from "./CameraEmptyState";
import { CameraFormModal } from "./CameraFormModal";
import { CameraDiagnoseModal } from "./CameraDiagnoseModal";
import { DeleteConfirmModal } from "./DeleteConfirmModal";
import type { CameraResponse, CreateCameraInput, DiagnoseResponse, UpdateCameraInput } from "../types";

export function CameraPage() {
  const { t } = useTranslation();

  // SSE real-time state synchronization
  useCameraEvents(true);

  // Queries & Mutations
  const { data: cameras, isLoading, error, refetch, isFetching } = useCamerasQuery();
  const createMutation = useCreateCameraMutation();
  const updateMutation = useUpdateCameraMutation();
  const deleteMutation = useDeleteCameraMutation();
  const diagnoseMutation = useDiagnoseCameraMutation();

  // Filter, search and sorting state
  const {
    keyword,
    setKeyword,
    healthFilter,
    setHealthFilter,
    sortOption,
    setSortOption,
    filteredCameras,
    clearFilters,
    counts,
  } = useCameraFilter({ cameras: cameras ?? [] });

  // Modal States
  const [isFormOpen, setIsFormOpen] = useState(false);
  const [editingCamera, setEditingCamera] = useState<CameraResponse | null>(null);

  const [isDeleteOpen, setIsDeleteOpen] = useState(false);
  const [deletingCamera, setDeletingCamera] = useState<CameraResponse | null>(null);

  const [isDiagnoseOpen, setIsDiagnoseOpen] = useState(false);
  const [diagnosingCamera, setDiagnosingCamera] = useState<CameraResponse | null>(null);
  const [diagnoseResult, setDiagnoseResult] = useState<DiagnoseResponse | null>(null);
  const [isDiagnosing, setIsDiagnosing] = useState(false);

  const [togglingCameraId, setTogglingCameraId] = useState<string | null>(null);

  function handleOpenCreate() {
    setEditingCamera(null);
    setIsFormOpen(true);
  }

  function handleOpenEdit(camera: CameraResponse) {
    setEditingCamera(camera);
    setIsFormOpen(true);
  }

  function handleOpenDelete(camera: CameraResponse) {
    setDeletingCamera(camera);
    setIsDeleteOpen(true);
  }

  async function handleOpenDiagnose(camera: CameraResponse) {
    setDiagnosingCamera(camera);
    setDiagnoseResult(null);
    setIsDiagnoseOpen(true);
    setIsDiagnosing(true);
    try {
      const res = await diagnoseMutation.mutateAsync(camera.id);
      setDiagnoseResult(res);
    } catch {
      // Ignored: DiagnoseResponse will be null
    } finally {
      setIsDiagnosing(false);
    }
  }

  async function handleReDiagnose() {
    if (!diagnosingCamera) return;
    setIsDiagnosing(true);
    try {
      const res = await diagnoseMutation.mutateAsync(diagnosingCamera.id);
      setDiagnoseResult(res);
    } catch {
      // Ignored
    } finally {
      setIsDiagnosing(false);
    }
  }

  async function handleToggleEnabled(camera: CameraResponse) {
    setTogglingCameraId(camera.id);
    try {
      await updateMutation.mutateAsync({
        id: camera.id,
        input: {
          revision: camera.revision,
          enabled: !camera.enabled,
        },
      });
    } finally {
      setTogglingCameraId(null);
    }
  }

  return (
    <div className="camera-management-view">
      {/* Page Header */}
      <section className="page-heading flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4 mb-6" aria-labelledby="cameras-page-title">
        <div>
          <h1 id="cameras-page-title" className="text-2xl font-bold tracking-tight text-[var(--foreground)]">
            {t("camera.title")}
          </h1>
          <p className="page-description text-xs text-[var(--muted)] mt-1">
            {t("camera.description")}
          </p>
        </div>

        <div className="flex items-center gap-2.5">
          <button
            type="button"
            className="icon-button"
            onClick={() => void refetch()}
            disabled={isFetching}
            aria-label={t("camera.refresh")}
            title={t("camera.refresh")}
          >
            <RefreshCw className={isFetching ? "animate-spin" : undefined} size={16} />
          </button>

          <button
            type="button"
            className="inline-flex items-center gap-1.5 rounded-xl bg-[var(--button-background)] hover:bg-[var(--button-hover)] text-white px-4 py-2 text-xs font-medium shadow-xs transition-colors"
            onClick={handleOpenCreate}
          >
            <Plus size={16} />
            <span>{t("camera.addCamera")}</span>
          </button>
        </div>
      </section>

      {/* Metrics Dashboard */}
      <CameraDashboard cameras={cameras ?? []} />

      {/* Content State Handling */}
      {isLoading ? (
        <div className="flex flex-col items-center justify-center py-16 gap-3 rounded-2xl border border-[var(--border)] bg-[var(--surface)]" role="status">
          <LoaderCircle size={28} className="animate-spin text-[var(--accent)]" />
          <div className="text-xs text-[var(--muted)]">{t("common.loading", "Loading...")}</div>
        </div>
      ) : error ? (
        <div className="flex flex-col items-center justify-center py-12 gap-3 rounded-2xl border border-[var(--danger)]/30 bg-[var(--danger-soft)] p-6 text-center" role="alert">
          <AlertTriangle size={28} className="text-[var(--danger)]" />
          <div className="text-sm font-semibold text-[var(--danger)]">{t("camera.errorMessage")}</div>
          <p className="text-xs text-[var(--muted)] max-w-sm">
            {error instanceof Error ? error.message : String(error)}
          </p>
          <button
            type="button"
            onClick={() => void refetch()}
            className="mt-2 rounded-xl border border-[var(--border)] bg-[var(--surface)] px-4 py-2 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--surface-hover)] transition-colors"
          >
            {t("camera.refresh")}
          </button>
        </div>
      ) : !cameras || cameras.length === 0 ? (
        /* Empty State */
        <CameraEmptyState isFiltered={false} onAddCamera={handleOpenCreate} />
      ) : (
        /* Camera Filter Bar + Camera Grid */
        <>
          <CameraFilterBar
            keyword={keyword}
            onKeywordChange={setKeyword}
            healthFilter={healthFilter}
            onHealthFilterChange={setHealthFilter}
            sortOption={sortOption}
            onSortOptionChange={setSortOption}
            counts={counts}
          />

          {filteredCameras.length === 0 ? (
            <CameraEmptyState isFiltered={true} onClearFilters={clearFilters} />
          ) : (
            <div className="grid grid-cols-1 gap-4 lg:grid-cols-2" role="region" aria-label={t("camera.title")}>
              {filteredCameras.map((camera) => (
                <CameraCard
                  key={camera.id}
                  camera={camera}
                  onEdit={handleOpenEdit}
                  onDelete={handleOpenDelete}
                  onDiagnose={handleOpenDiagnose}
                  onToggleEnabled={handleToggleEnabled}
                  isToggling={togglingCameraId === camera.id}
                />
              ))}
            </div>
          )}
        </>
      )}

      {/* Modals */}
      <CameraFormModal
        isOpen={isFormOpen}
        onClose={() => setIsFormOpen(false)}
        onSubmitCreate={(input: CreateCameraInput) => createMutation.mutateAsync(input)}
        onSubmitUpdate={(id: string, input: UpdateCameraInput) => updateMutation.mutateAsync({ id, input })}
        editingCamera={editingCamera}
      />

      <CameraDiagnoseModal
        isOpen={isDiagnoseOpen}
        onClose={() => setIsDiagnoseOpen(false)}
        camera={diagnosingCamera}
        result={diagnoseResult}
        isLoading={isDiagnosing}
        onReDiagnose={() => void handleReDiagnose()}
      />

      <DeleteConfirmModal
        isOpen={isDeleteOpen}
        onClose={() => setIsDeleteOpen(false)}
        onConfirm={(id: string) => deleteMutation.mutateAsync(id)}
        camera={deletingCamera}
      />
    </div>
  );
}
