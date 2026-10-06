import { Check, Video, X } from 'lucide-react';
import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useCamerasQuery } from '../../camera/hooks/useCameras';
import type { CameraResponse, StreamResponse } from '../../camera/types';

interface LiveAssignModalProps {
  isOpen: boolean;
  onClose: () => void;
  onAssign: (cameraId: string, role: 'main' | 'sub', name: string) => void;
  currentCameraId?: string;
}

export const LiveAssignModal: React.FC<LiveAssignModalProps> = ({
  isOpen,
  onClose,
  onAssign,
  currentCameraId,
}) => {
  const { t } = useTranslation();
  const { data: cameras = [], isLoading } = useCamerasQuery();

  const [selectedCamId, setSelectedCamId] = useState<string>(currentCameraId || '');
  const [selectedRole, setSelectedRole] = useState<'main' | 'sub'>('main');

  if (!isOpen) return null;

  const selectedCam = cameras.find((c: CameraResponse) => c.id === selectedCamId);
  const hasSubStream = selectedCam?.streams?.some((s: StreamResponse) => s.role === 'sub');

  const handleConfirm = () => {
    if (!selectedCamId) return;
    const camName = selectedCam?.name || selectedCamId;
    onAssign(selectedCamId, selectedRole, camName);
    onClose();
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60 backdrop-blur-md animate-in fade-in duration-200"
      onClick={onClose}
    >
      <div
        className="relative w-full max-w-md bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-2xl shadow-2xl overflow-hidden flex flex-col"
        onClick={(e) => e.stopPropagation()}
      >
        {/* 头部 */}
        <div className="flex items-center justify-between px-6 py-4 border-b border-neutral-100 dark:border-neutral-800">
          <div className="flex items-center gap-2">
            <Video className="w-5 h-5 text-blue-500" />
            <h3 className="text-base font-semibold text-neutral-900 dark:text-neutral-100">
              {t('live.assignModalTitle', '分配视口监控流')}
            </h3>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="p-1 text-neutral-400 hover:text-neutral-600 dark:hover:text-neutral-200 rounded-lg transition-colors cursor-pointer"
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* 主体列表 */}
        <div className="p-6 flex flex-col gap-4 max-h-[60vh] overflow-y-auto">
          {isLoading ? (
            <div className="py-8 text-center text-sm text-neutral-400">
              {t('common.loading', '加载中...')}
            </div>
          ) : cameras.length === 0 ? (
            <div className="py-8 text-center text-sm text-neutral-400">
              {t('camera.empty', '暂无摄像机，请先在摄像机管理中添加')}
            </div>
          ) : (
            <div className="flex flex-col gap-2">
              <label className="text-xs font-semibold text-neutral-500 dark:text-neutral-400 uppercase tracking-wider">
                {t('live.selectCamera', '选择设备')}
              </label>
              <div className="flex flex-col gap-1.5">
                {cameras.map((cam: CameraResponse) => {
                  const isSelected = selectedCamId === cam.id;
                  const isOnline = cam.health === 'online';

                  return (
                    <button
                      key={cam.id}
                      type="button"
                      onClick={() => {
                        setSelectedCamId(cam.id);
                        if (!cam.streams?.some((s: StreamResponse) => s.role === 'sub')) {
                          setSelectedRole('main');
                        }
                      }}
                      className={`flex items-center justify-between p-3 rounded-xl border text-left transition-all cursor-pointer ${
                        isSelected
                          ? 'border-blue-500 bg-blue-50/50 dark:bg-blue-950/20 text-neutral-900 dark:text-white'
                          : 'border-neutral-200 dark:border-neutral-800 hover:bg-neutral-50 dark:hover:bg-neutral-800/50 text-neutral-700 dark:text-neutral-300'
                      }`}
                    >
                      <div className="flex items-center gap-2.5 truncate">
                        <span
                          className={`w-2 h-2 rounded-full flex-shrink-0 ${
                            isOnline ? 'bg-emerald-500' : 'bg-neutral-400'
                          }`}
                        />
                        <span className="font-medium text-sm truncate">{cam.name}</span>
                      </div>
                      {isSelected && <Check className="w-4 h-4 text-blue-500 flex-shrink-0" />}
                    </button>
                  );
                })}
              </div>
            </div>
          )}

          {/* 码流通道选择 */}
          {selectedCamId && (
            <div className="flex flex-col gap-2 pt-2 border-t border-neutral-100 dark:border-neutral-800">
              <label className="text-xs font-semibold text-neutral-500 dark:text-neutral-400 uppercase tracking-wider">
                {t('live.selectStreamRole', '选择流通道')}
              </label>
              <div className="grid grid-cols-2 gap-2">
                <button
                  type="button"
                  onClick={() => setSelectedRole('main')}
                  className={`py-2 px-3 text-xs font-medium rounded-xl border text-center transition-all cursor-pointer ${
                    selectedRole === 'main'
                      ? 'border-blue-500 bg-blue-50/60 dark:bg-blue-950/30 text-blue-600 dark:text-blue-400'
                      : 'border-neutral-200 dark:border-neutral-800 text-neutral-600 dark:text-neutral-400'
                  }`}
                >
                  {t('camera.mainStream', '主流 (高清)')}
                </button>
                <button
                  type="button"
                  disabled={!hasSubStream}
                  onClick={() => setSelectedRole('sub')}
                  className={`py-2 px-3 text-xs font-medium rounded-xl border text-center transition-all ${
                    !hasSubStream
                      ? 'opacity-40 cursor-not-allowed border-neutral-200 dark:border-neutral-800 text-neutral-400'
                      : selectedRole === 'sub'
                        ? 'border-blue-500 bg-blue-50/60 dark:bg-blue-950/30 text-blue-600 dark:text-blue-400 cursor-pointer'
                        : 'border-neutral-200 dark:border-neutral-800 text-neutral-600 dark:text-neutral-400 cursor-pointer'
                  }`}
                >
                  {t('camera.subStream', '子流 (标清/低功耗)')}
                </button>
              </div>
            </div>
          )}
        </div>

        {/* 底部操作栏 */}
        <div className="flex items-center justify-end gap-3 px-6 py-4 bg-neutral-50 dark:bg-neutral-800/40 border-t border-neutral-100 dark:border-neutral-800">
          <button
            type="button"
            onClick={onClose}
            className="px-4 py-2 text-xs font-medium text-neutral-600 dark:text-neutral-300 hover:bg-neutral-200 dark:hover:bg-neutral-700/50 rounded-xl transition-colors cursor-pointer"
          >
            {t('common.cancel', '取消')}
          </button>
          <button
            type="button"
            disabled={!selectedCamId}
            onClick={handleConfirm}
            className="px-4 py-2 text-xs font-semibold text-white bg-blue-600 hover:bg-blue-500 active:scale-95 disabled:opacity-40 disabled:cursor-not-allowed rounded-xl transition-all shadow-sm cursor-pointer"
          >
            {t('common.confirm', '确定分配')}
          </button>
        </div>
      </div>
    </div>
  );
};
