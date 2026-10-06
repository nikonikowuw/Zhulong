package camera

import (
	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

func init() {
	httputil.RegisterMessages(map[string]map[string]string{
		"CAMERA_NOT_FOUND": {
			"en":      "Camera not found",
			"zh-Hans": "指定的摄像机不存在",
			"zh-Hant": "指定的攝影機不存在",
		},
		"CAMERA_ALREADY_EXISTS": {
			"en":      "Camera already exists",
			"zh-Hans": "摄像机已存在，禁止重复创建",
			"zh-Hant": "攝影機已存在，禁止重複建立",
		},
		"CAMERA_REVISION_CONFLICT": {
			"en":      "Camera configuration was modified concurrently; please refresh",
			"zh-Hans": "摄像机配置已被并发修改，请刷新后重试",
			"zh-Hant": "攝影機設定已被並行修改，請重新整理後重試",
		},
		"CAMERA_CONNECT_TIMEOUT": {
			"en":      "Connection to RTSP camera stream timed out",
			"zh-Hans": "连接摄像机 RTSP 流超时，请检查网络或地址",
			"zh-Hant": "連線攝影機 RTSP 串流逾時，請檢查網路或位址",
		},
		"CAMERA_AUTH_FAILED": {
			"en":      "Camera authentication failed; please check username and password",
			"zh-Hans": "摄像机鉴权失败，请检查用户名或密码",
			"zh-Hant": "攝影機身分驗證失敗，請檢查使用者名稱或密碼",
		},
		"CAMERA_UNSUPPORTED": {
			"en":      "Unsupported video codec or invalid stream format",
			"zh-Hans": "不支持的视频编码或无效的流格式（仅支持 H.264 / H.265）",
			"zh-Hant": "不支援的視訊編碼或無效的串流格式（僅支援 H.264 / H.265）",
		},
		"CAMERA_BUSY": {
			"en":      "Camera resource is busy, please try again later",
			"zh-Hans": "摄像机资源正忙，请稍后再试",
			"zh-Hant": "攝影機資源忙碌，請稍後再試",
		},
		"CAMERA_PROBE_FAILED": {
			"en":      "Failed to probe RTSP stream parameters",
			"zh-Hans": "探测 RTSP 视频流失败，无法提取有效元数据",
			"zh-Hant": "探測 RTSP 視訊串流失敗，無法擷取有效元資料",
		},
		"CAMERA_LIMIT_EXCEEDED": {
			"en":      "Maximum camera capacity reached (limit 128)",
			"zh-Hans": "已达到系统摄像机数量上限（上限 128 台）",
			"zh-Hant": "已達到系統攝影機數量上限（上限 128 台）",
		},
		"CAMERA_INVALID_PARAM": {
			"en":      "Invalid camera parameter",
			"zh-Hans": "摄像机参数无效",
			"zh-Hant": "攝影機參數無效",
		},
		"CAMERA_STREAM_REQUIRED": {
			"en":      "Main video stream is required",
			"zh-Hans": "主流配置为必填项",
			"zh-Hant": "主流設定為必填項",
		},
		"CAMERA_PROTOCOL_UNSUPPORTED": {
			"en":      "Unsupported protocol; currently only RTSP is supported",
			"zh-Hans": "不支持的视频协议，当前仅支持 RTSP",
			"zh-Hant": "不支援的視訊協定，目前僅支援 RTSP",
		},
	})
}
