import { Grid2x2, Grid3x3, LayoutGrid, Square } from 'lucide-react';
import React, { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { useLiveLayout } from '../hooks/useLiveLayout';
import { LiveViewport } from './LiveViewport';

export const LiveDashboard: React.FC = () => {
  const { t } = useTranslation();
  const {
    mode,
    slots,
    fullscreenSlot,
    setMode,
    assignSlot,
    clearSlot,
    toggleRole,
    setFullscreenSlot,
  } = useLiveLayout();

  // 键盘快捷键监听：Esc 退出单视口全屏，1/4/9 切换布局
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && fullscreenSlot !== null) {
        setFullscreenSlot(null);
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [fullscreenSlot, setFullscreenSlot]);

  const slotCount = mode;
  const slotIndices = Array.from({ length: slotCount }, (_, i) => i);

  // 布局栅格类计算
  const getGridClass = () => {
    switch (mode) {
      case 1:
        return 'grid-cols-1';
      case 4:
        return 'grid-cols-1 sm:grid-cols-2';
      case 9:
        return 'grid-cols-1 sm:grid-cols-2 lg:grid-cols-3';
      default:
        return 'grid-cols-2';
    }
  };

  return (
    <div className="flex flex-col gap-4 w-full h-full">
      {/* 顶部控制栏 */}
      <div className="flex items-center justify-between pb-1">
        <div className="flex items-center gap-2">
          <LayoutGrid className="w-5 h-5 text-blue-500" />
          <h2 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100 tracking-tight">
            {t('live.title', '实时视频监控')}
          </h2>
        </div>

        {/* 宫格布局切换器 */}
        <div className="flex items-center gap-1.5 p-1 rounded-xl bg-neutral-100 dark:bg-neutral-800/80 border border-neutral-200/80 dark:border-neutral-700/60 shadow-inner">
          <button
            type="button"
            onClick={() => setMode(1)}
            title={t('live.grid1', '单视口 (1x1)')}
            className={`flex items-center gap-1 px-3 py-1.5 rounded-lg text-xs font-medium transition-all cursor-pointer ${
              mode === 1
                ? 'bg-white dark:bg-neutral-700 text-blue-600 dark:text-blue-400 shadow-sm'
                : 'text-neutral-600 dark:text-neutral-400 hover:text-neutral-900 dark:hover:text-white'
            }`}
          >
            <Square className="w-3.5 h-3.5" />
            <span>1</span>
          </button>
          <button
            type="button"
            onClick={() => setMode(4)}
            title={t('live.grid4', '四分屏 (2x2)')}
            className={`flex items-center gap-1 px-3 py-1.5 rounded-lg text-xs font-medium transition-all cursor-pointer ${
              mode === 4
                ? 'bg-white dark:bg-neutral-700 text-blue-600 dark:text-blue-400 shadow-sm'
                : 'text-neutral-600 dark:text-neutral-400 hover:text-neutral-900 dark:hover:text-white'
            }`}
          >
            <Grid2x2 className="w-3.5 h-3.5" />
            <span>4</span>
          </button>
          <button
            type="button"
            onClick={() => setMode(9)}
            title={t('live.grid9', '九分屏 (3x3)')}
            className={`flex items-center gap-1 px-3 py-1.5 rounded-lg text-xs font-medium transition-all cursor-pointer ${
              mode === 9
                ? 'bg-white dark:bg-neutral-700 text-blue-600 dark:text-blue-400 shadow-sm'
                : 'text-neutral-600 dark:text-neutral-400 hover:text-neutral-900 dark:hover:text-white'
            }`}
          >
            <Grid3x3 className="w-3.5 h-3.5" />
            <span>9</span>
          </button>
        </div>
      </div>

      {/* 监控宫格视口容器 */}
      <div
        className={`grid ${getGridClass()} gap-3 w-full h-[calc(100vh-13.5rem)] min-h-[500px] auto-rows-fr`}
      >
        {slotIndices.map((index) => {
          const binding = slots[index] || null;
          const isFullscreen = fullscreenSlot === index;

          // 若有视口正处于独立全屏状态，只渲染该视口
          if (fullscreenSlot !== null && !isFullscreen) {
            return null;
          }

          return (
            <LiveViewport
              key={index}
              slotIndex={index}
              binding={binding}
              isFullscreen={isFullscreen}
              onAssign={(newBinding) => assignSlot(index, newBinding)}
              onClear={() => clearSlot(index)}
              onToggleRole={() => toggleRole(index)}
              onToggleFullscreen={() =>
                setFullscreenSlot(isFullscreen ? null : index)
              }
            />
          );
        })}
      </div>
    </div>
  );
};
