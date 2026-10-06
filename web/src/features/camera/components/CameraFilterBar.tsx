import React from "react";
import { useTranslation } from "react-i18next";
import { ArrowUpDown, Search, X } from "lucide-react";
import type { CameraHealthFilter, CameraSortOption } from "../hooks/useCameraFilter";

export interface CameraFilterBarProps {
  keyword: string;
  onKeywordChange: (kw: string) => void;
  healthFilter: CameraHealthFilter;
  onHealthFilterChange: (filter: CameraHealthFilter) => void;
  sortOption: CameraSortOption;
  onSortOptionChange: (sort: CameraSortOption) => void;
  counts: {
    all: number;
    online: number;
    offline: number;
    abnormal: number;
  };
}

export const CameraFilterBar: React.FC<CameraFilterBarProps> = ({
  keyword,
  onKeywordChange,
  healthFilter,
  onHealthFilterChange,
  sortOption,
  onSortOptionChange,
  counts,
}) => {
  const { t } = useTranslation();

  const filterItems: { key: CameraHealthFilter; label: string; count: number; badgeColor?: string }[] = [
    { key: "all", label: t("camera.filter.all", "全部"), count: counts.all },
    { key: "online", label: t("camera.filter.online", "在线"), count: counts.online, badgeColor: "bg-[var(--positive)]" },
    { key: "offline", label: t("camera.filter.offline", "离线"), count: counts.offline, badgeColor: "bg-[var(--muted)]" },
    { key: "abnormal", label: t("camera.filter.abnormal", "异常"), count: counts.abnormal, badgeColor: "bg-[var(--danger)]" },
  ];

  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between mb-5">
      {/* 搜索框与状态筛选胶囊组 */}
      <div className="flex flex-1 flex-col gap-2.5 sm:flex-row sm:items-center">
        {/* 搜索输入框 */}
        <div className="relative min-w-[200px] max-w-full sm:max-w-xs">
          <label htmlFor="camera-search-input" className="sr-only">
            {t("camera.filter.searchPlaceholder", "搜索摄像机名称或 ID...")}
          </label>
          <Search
            className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[var(--muted)]"
            size={15}
            aria-hidden="true"
          />
          <input
            id="camera-search-input"
            type="text"
            value={keyword}
            onChange={(e) => onKeywordChange(e.target.value)}
            placeholder={t("camera.filter.searchPlaceholder", "搜索摄像机名称或 ID...")}
            className="w-full rounded-xl border border-[var(--border)] bg-[var(--surface)] py-1.5 pl-8 pr-7 text-xs text-[var(--foreground)] placeholder:text-[var(--muted)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] transition-all"
          />
          {keyword && (
            <button
              type="button"
              onClick={() => onKeywordChange("")}
              className="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-[var(--muted)] hover:text-[var(--foreground)] rounded-full transition-colors cursor-pointer"
              aria-label={t("common.clear", "清除")}
            >
              <X size={13} aria-hidden="true" />
            </button>
          )}
        </div>

        {/* 状态过滤胶囊 (Pills) */}
        <div
          className="flex items-center gap-1 overflow-x-auto py-0.5"
          role="radiogroup"
          aria-label={t("camera.filter.statusGroup", "状态筛选")}
        >
          {filterItems.map((item) => {
            const isActive = healthFilter === item.key;
            return (
              <button
                key={item.key}
                type="button"
                role="radio"
                aria-checked={isActive}
                onClick={() => onHealthFilterChange(item.key)}
                className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-xs font-medium transition-all cursor-pointer whitespace-nowrap ${
                  isActive
                    ? "bg-[var(--foreground)] text-[var(--background)] shadow-xs"
                    : "bg-[var(--surface-muted)] text-[var(--muted)] hover:text-[var(--foreground)] hover:bg-[var(--surface-hover)] border border-[var(--border)]"
                }`}
              >
                {item.badgeColor && (
                  <span
                    className={`h-1.5 w-1.5 rounded-full ${item.badgeColor}`}
                    aria-hidden="true"
                  />
                )}
                <span>{item.label}</span>
                <span
                  className={`text-[10px] px-1 rounded-sm ${
                    isActive
                      ? "bg-[var(--background)]/20 text-[var(--background)]"
                      : "bg-[var(--border)]/60 text-[var(--muted)]"
                  }`}
                >
                  {item.count}
                </span>
              </button>
            );
          })}
        </div>
      </div>

      {/* 排序下拉框 */}
      <div className="flex items-center gap-1.5 shrink-0 self-end sm:self-center">
        <label htmlFor="camera-sort-select" className="text-xs text-[var(--muted)] flex items-center gap-1">
          <ArrowUpDown size={13} aria-hidden="true" />
          <span>{t("camera.sort.label", "排序")}</span>
        </label>
        <select
          id="camera-sort-select"
          value={sortOption}
          onChange={(e) => onSortOptionChange(e.target.value as CameraSortOption)}
          className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-2 py-1 text-xs text-[var(--foreground)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] cursor-pointer"
        >
          <option value="health_priority">{t("camera.sort.health", "状态优先 (异常靠前)")}</option>
          <option value="name_asc">{t("camera.sort.nameAsc", "名称升序 (A-Z)")}</option>
          <option value="name_desc">{t("camera.sort.nameDesc", "名称降序 (Z-A)")}</option>
          <option value="updated_desc">{t("camera.sort.updatedDesc", "最近更新优先")}</option>
        </select>
      </div>
    </div>
  );
};
