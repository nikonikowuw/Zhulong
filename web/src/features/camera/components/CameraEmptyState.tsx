import React from "react";
import { useTranslation } from "react-i18next";
import { Plus, RotateCcw, SearchX, Video } from "lucide-react";

export interface CameraEmptyStateProps {
  isFiltered?: boolean;
  onClearFilters?: () => void;
  onAddCamera?: () => void;
}

export const CameraEmptyState: React.FC<CameraEmptyStateProps> = ({
  isFiltered = false,
  onClearFilters,
  onAddCamera,
}) => {
  const { t } = useTranslation();

  if (isFiltered) {
    return (
      <div
        className="flex min-h-[300px] flex-col items-center justify-center rounded-2xl border border-dashed border-[var(--border)] p-8 text-center bg-[var(--surface-muted)]/50"
        role="status"
      >
        <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-[var(--surface)] text-[var(--muted)] border border-[var(--border)] shadow-xs">
          <SearchX size={22} aria-hidden="true" />
        </div>
        <h3 className="mt-4 text-sm font-semibold text-[var(--foreground)]">
          {t("camera.emptySearchTitle", "未找到匹配的摄像机")}
        </h3>
        <p className="mt-1 text-xs text-[var(--muted)] max-w-sm">
          {t("camera.emptySearchDesc", "请尝试调整搜索关键词或重置筛选条件。")}
        </p>
        {onClearFilters && (
          <button
            type="button"
            onClick={onClearFilters}
            className="mt-4 inline-flex items-center gap-1.5 rounded-full bg-[var(--surface)] border border-[var(--border)] px-4 py-1.5 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--surface-hover)] active:scale-98 transition-all cursor-pointer shadow-xs"
          >
            <RotateCcw size={13} aria-hidden="true" />
            <span>{t("camera.clearFilters", "清除筛选")}</span>
          </button>
        )}
      </div>
    );
  }

  return (
    <div
      className="flex min-h-[360px] flex-col items-center justify-center rounded-2xl border border-dashed border-[var(--border)] p-8 text-center bg-[var(--surface-muted)]/50"
      role="status"
    >
      <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-[var(--surface)] text-[var(--muted)] border border-[var(--border)] shadow-xs">
        <Video size={26} aria-hidden="true" />
      </div>
      <h3 className="mt-4 text-base font-semibold text-[var(--foreground)]">
        {t("camera.emptyTitle", "暂无摄像机")}
      </h3>
      <p className="mt-1.5 text-xs text-[var(--muted)] max-w-sm">
        {t("camera.emptyDescription", "请点击下方按钮添加第一台 RTSP 摄像机设备。")}
      </p>
      {onAddCamera && (
        <button
          type="button"
          onClick={onAddCamera}
          className="mt-5 inline-flex items-center gap-1.5 rounded-full bg-[var(--accent)] text-white px-5 py-2 text-xs font-semibold hover:brightness-105 active:scale-98 transition-all cursor-pointer shadow-xs"
        >
          <Plus size={15} aria-hidden="true" />
          <span>{t("camera.addCamera", "添加摄像机")}</span>
        </button>
      )}
    </div>
  );
};
