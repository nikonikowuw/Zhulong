package asyncjob

import "github.com/nikonikowuw/Zhulong/internal/httputil"

// The job API emits its own machine codes, so it owns their catalog entries.
// Keeping them here (instead of in httputil) means the translations disappear
// with the package they belong to, and there is exactly one owner per code.
func init() {
	httputil.RegisterMessages(map[string]map[string]string{
		CodeJobNotFound: {
			"en":      "Background job not found",
			"zh-Hans": "后台任务不存在",
			"zh-Hant": "後台任務不存在",
		},
		CodeJobTypeUnsupported: {
			"en":      "Unsupported background job type",
			"zh-Hans": "不支持的后台任务类型",
			"zh-Hant": "不支援的後台任務類型",
		},
		CodeJobQueueFull: {
			"en":      "Background job queue is full, please retry later",
			"zh-Hans": "后台任务队列已满，请稍后重试",
			"zh-Hant": "後台任務佇列已滿，請稍後重試",
		},
		CodeJobNotCancellable: {
			"en":      "The job has already finished and cannot be cancelled",
			"zh-Hans": "任务已结束，无法取消",
			"zh-Hant": "任務已結束，無法取消",
		},
		CodeJobServiceStopped: {
			"en":      "Background job service is shutting down",
			"zh-Hans": "后台任务服务正在停止",
			"zh-Hant": "後台任務服務正在停止",
		},
		CodeJobExecutionFailed: {
			"en":      "The job failed while running",
			"zh-Hans": "任务执行失败",
			"zh-Hant": "任務執行失敗",
		},
		CodeJobPanicked: {
			"en":      "The job was aborted by an internal error",
			"zh-Hans": "任务因内部错误中止",
			"zh-Hant": "任務因內部錯誤中止",
		},
		CodeJobInterrupted: {
			"en":      "The host restarted before the job finished",
			"zh-Hans": "宿主重启导致任务中断",
			"zh-Hant": "宿主重啟導致任務中斷",
		},
		CodeJobShutdown: {
			"en":      "The job was stopped because the service is shutting down",
			"zh-Hans": "服务关闭，任务已终止",
			"zh-Hant": "服務關閉，任務已終止",
		},
	})
}
