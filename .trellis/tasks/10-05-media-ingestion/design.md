# 技术设计文档：媒体接入与流转管道

> 对应 PRD: `.trellis/tasks/10-05-media-ingestion/prd.md`
> 涉及包：`native/` (C++17), `internal/camera/` (Go), `internal/engine/` (CGO), `web/` (React/TypeScript)

## 实施来源与待修订边界（2026-10-05）

本文件下方保留早期全链路草案，不是当前可直接复制的 ABI/协议。Native 的现行签名以 `native/include/Zhulong/engine.h` 和已批准 Native 子设计为准；下一步 Go 桥接以 `../10-05-go-cgo-media-bridge/design.md` 的已批准方案为准。用户先选择 A 进入规划，后于 2026-10-05 审阅摘要并回复“开始实现”；该批准仅限桥接子任务，不批准全部旧草案落地。

- 旧错误码、probe 函数、512 字节 extradata、字符串 consumer ID、状态回调已与实际 ABI 不符；当前是动态 result-view、整数 ID、状态查询和 drain 语义。
- `unsafe.Slice` 不能跨回调持有。桥接拟用一次受限复制进入 Go-owned 队列；不在同步回调里做 WebSocket 网络写入。
- 现有认证是内存 Session + HttpOnly Cookie，不是 JWT。后续 WS 必须做 Origin/会话生命周期安全规划。
- 旧裸 URL LastIndex('@') + QueryEscape、鉴权“100% 成功”和单 uint64 PTS 的 wire 示例不可作为已验证保证；摄像机/WS 子任务需分别修订歧义解析与时间戳/参数集协议。
- 失败物理流在 grace 内重获取不会自动重启；业务重连不得通过 Stop 全局 Engine 影响其他相机。后续设计须解决失败流重试契约。
- 解码前随意丢 B/P 包与连续 GOP 要求冲突，仍禁止按下方早期草案直接实现。

---

## 1. 总体架构与数据流拓扑

```txt
┌─────────────────────────────────────────────────────────────────────────┐
│                          React 前端 (Web 控制台)                         │
│   CameraCard / LiveGrid ➔ useMediaStream(cameraId)                      │
│   Jessibuca / 自研三级降级播放器 (WebCodecs GPU ➔ MSE ➔ WASM SIMD 软解)    │
└────────────────────────────────────▲────────────────────────────────────┘
                                     │ WebSocket: /api/v1/cameras/:id/stream/ws
                                     │ SSE: /api/v1/telemetry/events (状态感知)
┌────────────────────────────────────▼────────────────────────────────────┐
│                         Go 业务应用宿主 (Host)                           │
│   internal/camera:                                                      │
│     - Service & Handler: CRUD, 凭据脱敏, 探测门禁 (Probe Gate)           │
│     - StateMachine: online / reconnecting / error / offline             │
│     - StreamHub: WebSocket 广播中心, 管理 Web 客户端连接生命周期         │
│   internal/engine:                                                      │
│     - CGO 门面封装, cgo.Handle 按需订阅, unsafe.Slice 零拷贝借用         │
└────────────────────────────────────▲────────────────────────────────────┘
                                     │ C ABI: include/Zhulong/engine.h
┌────────────────────────────────────▼────────────────────────────────────┐
│                    Native C++ 原生引擎 (ZhulongEngine)                  │
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐  │
│  │ StreamSourcePool (物理流单例连接池, Normalized RTSP URL 为 Key)   │  │
│  │ - 统一 RefCount = AI任务数 + Web预览数 + 录像数                     │  │
│  │ - 5~10s Grace Period 延时休眠防抖定时器                           │  │
│  └──────────────────┬────────────────────────────────────────────────┘  │
│                     │ 驱动物理连接 (0 ➔ 1 激活)                          │
│  ┌──────────────────▼────────────────────────────────────────────────┐  │
│  │ PhysicalStreamSession (单条物理流会话)                             │  │
│  │   CaptureNode (FFmpeg libavformat, TCP Interleaved, 超时中断)      │  │
│  │       │                                                           │  │
│  │       ├───────────────┬───────────────────────────────────────────┤  │
│  │       ▼ [录像通道]    ▼ [Web 预览通道]                            ▼  │
│  │   PacketQueue     CGO Callback (按需激活)                 DecodeNode │
│  │   (大容量无损)    (unsafe.Slice借用 ➔ Go WebSocket Hub)   (VPU 连续硬解)│
│  │       ▼                                                       │      │
│  │   StorageWriter                                               ▼      │
│  │   (MP4 切片)                                           std::shared_ptr│
│  │                                                        <const Frame> │
│  │                                                               │      │
│  │                                                               ▼      │
│  │                                                        Task Dispatch │
│  │                                                        (后级抽帧步长) │
│  │                                                               │      │
│  │                                                               ▼      │
│  │                                                        Preprocess/NPU│
│  └───────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## 2. C ABI 与 CGO 边界设计 (`include/Zhulong/engine.h`)

遵循 `spec/native/cgo-contract.md` 契约，C ABI 保持纯 C 符号，完全隐藏 C++ 类与模板。

### 2.1 头文件接口扩充

```c
#ifndef ZHULONG_ENGINE_H
#define ZHULONG_ENGINE_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct Zhulong_engine_t *Zhulong_engine_h;
typedef int32_t Zhulong_status_t;

enum {
    Zhulong_OK = 0,
    Zhulong_ERR_INVALID_ARGUMENT = -1,
    Zhulong_ERR_OUT_OF_MEMORY = -2,
    Zhulong_ERR_STREAM_NOT_FOUND = -3,
    Zhulong_ERR_STREAM_ALREADY_EXISTS = -4,
    Zhulong_ERR_CONNECTION_FAILED = -5,
    Zhulong_ERR_AUTH_FAILED = -6,
    Zhulong_ERR_TIMEOUT = -7,
    Zhulong_ERR_INTERNAL = -99
};

// 媒体格式元数据
typedef struct {
    int32_t codec_id;      // 1 = H264, 2 = H265
    int32_t width;
    int32_t height;
    float   fps;
    char    extra_data[512]; // SPS/PPS 参数集
    size_t  extra_data_len;
} Zhulong_stream_probe_info_t;

// 码流包回调函数签名 (用于 Web 预览按需借用)
// 内存所有权归 C 侧所有，Go 侧仅在回调执行期间借用，调用结束后 C 侧按需释放
typedef void (*Zhulong_stream_packet_cb)(
    uintptr_t go_handle,
    const uint8_t *data,
    size_t len,
    int64_t pts,
    int64_t dts,
    int32_t is_keyframe
);

// 状态变更回调函数签名 (用于上报 online / reconnecting / error 等状态)
typedef void (*Zhulong_stream_status_cb)(
    uintptr_t go_handle,
    int32_t status_code,
    const char *error_msg
);

// 1. 基础引擎生命周期
Zhulong_status_t Zhulong_engine_create(Zhulong_engine_h *out_engine);
Zhulong_status_t Zhulong_engine_start(Zhulong_engine_h engine);
Zhulong_status_t Zhulong_engine_stop(Zhulong_engine_h engine);
void Zhulong_engine_destroy(Zhulong_engine_h engine);

// 2. 独立探测接口 (Probe Gate, 严格限制单次超时)
Zhulong_status_t Zhulong_stream_probe(
    const char *rtsp_url,
    int32_t timeout_ms,
    Zhulong_stream_probe_info_t *out_info
);

// 3. 物理流会话管理 (引用计数维护)
// 若相同 url 已存在，直接复用底层 session 并增加 consumer_ref
Zhulong_status_t Zhulong_stream_acquire(
    Zhulong_engine_h engine,
    const char *canonical_url,
    const char *consumer_id,
    int32_t consumer_type, // 1 = AI, 2 = Preview, 3 = Recording
    uintptr_t status_go_handle,
    Zhulong_stream_status_cb status_cb
);

// 释放消费者引用计数，当计数归零触发 Grace Period 延时休眠
Zhulong_status_t Zhulong_stream_release(
    Zhulong_engine_h engine,
    const char *canonical_url,
    const char *consumer_id
);

// 4. Web 码流订阅 (按需借用)
Zhulong_status_t Zhulong_stream_subscribe_packets(
    Zhulong_engine_h engine,
    const char *canonical_url,
    uintptr_t packet_go_handle,
    Zhulong_stream_packet_cb packet_cb
);

Zhulong_status_t Zhulong_stream_unsubscribe_packets(
    Zhulong_engine_h engine,
    const char *canonical_url,
    uintptr_t packet_go_handle
);

#ifdef __cplusplus
}
#endif
#endif
```

### 2.2 Go 侧 CGO 桥接设计 (`internal/engine/`)

1. **零拷贝切片借用**：

   ```go
   //export onStreamPacketCallback
   func onStreamPacketCallback(handle C.uintptr_t, data *C.uint8_t, length C.size_t, pts C.int64_t, dts C.int64_t, isKey C.int32_t) {
       h := cgo.Handle(handle)
       sub, ok := h.Value().(*PacketSubscriber)
       if !ok || sub == nil {
           return
       }
       // 同步切片借用，底层指向 C 内存
       packetBytes := unsafe.Slice((*byte)(unsafe.Pointer(data)), int(length))
       // 广播至 WebSocket Hub (内存复制在写入连接 buffer 时发生)
       sub.Broadcast(packetBytes, int64(pts), isKey != 0)
   }
   ```

2. **生命周期防护**：
   - 订阅创建：`handle := cgo.NewHandle(subscriber)`，传递给 C；
   - 订阅注销：先调用 C 接口取消回调，再执行 `handle.Delete()`，防止悬挂引用。

---

## 3. C++ 内部核心类与数据流设计

### 3.1 `StreamSourcePool` (物理流单例连接池)

- **键（Key）**：`std::string canonical_url`（经过 URL 标准化处理：转换为小写协议、去除空格、明确默认 554 端口）。
- **映射**：`std::unordered_map<std::string, std::shared_ptr<PhysicalStreamSession>> sessions_`。
- **并发保护**：`std::mutex pool_mutex_`。
- **方法**：
  - `Acquire(url, consumer_id, type)`: 查找或创建 `PhysicalStreamSession`。如果已有，立即取消可能正在计时的 Grace Period 定时器，并增加引用计数；
  - `Release(url, consumer_id)`: 减少引用计数。若计数归零，启动 `asio::steady_timer` 或 C++11 条件变量倒计时 8 秒；若 8 秒到期仍无新入，从 map 移除并触发 Session 析构。

### 3.2 `PhysicalStreamSession` (单流物理会话)

- 封装单个 RTSP 输入流的完整拓扑：
  - `CaptureNode capture_`：持有 FFmpeg `AVFormatContext`，运行于独立的 `std::thread capture_thread_`；
  - `DecodeNode decode_`：持有 VPU/软解 `AVCodecContext`，运行于独立的 `std::thread decode_thread_`；
  - `BoundedQueue<AVPacketPtr> packet_queue_`：容量 60，背压丢弃 B/P 帧策略；
  - `SubscriberList packet_subscribers_`：维护 Web 预览的 CGO 回调列表（读写锁保护）。

### 3.3 防吊死与网络中断保护 (`AVIOInterruptCB`)

```cpp
struct InterruptContext {
    std::atomic<int64_t> last_packet_time_ms{0};
    int64_t timeout_threshold_ms{5000};
    std::atomic<bool> interrupted{false};
};

int InterruptCallback(void *opaque) {
    auto *ctx = static_cast<InterruptContext*>(opaque);
    if (ctx->interrupted.load(std::memory_order_relaxed)) return 1;
    int64_t now = GetMonotonicMs();
    if (now - ctx->last_packet_time_ms.load(std::memory_order_relaxed) > ctx->timeout_threshold_ms) {
        return 1; // 触发超时中断，强行跳出 av_read_frame
    }
    return 0;
}
```

---

## 4. Go 业务层模块设计 (`internal/camera/`)

### 4.1 实体模型与元数据持久化

- `Camera` 实体：
  - `ID`: 格式 `cam_<uuid/ulid>`
  - `Name`: 显示名称
  - `MainStreamURL`: 主流 RTSP（密码经过 AES-GCM 加密或脱敏存储）
  - `SubStreamURL`: 子流 RTSP
  - `Codec`: 探测确定的编码（`h264` / `h265`）
  - `Width`, `Height`, `FPS`
  - `Status`: `online` / `reconnecting` / `error` / `offline`
  - `LastError`: 错误描述（脱敏）

### 4.2 探测门禁工作流 (Probe Gate)

```txt
用户调用 POST /api/v1/cameras
       │
       ▼
CameraService.Create(req)
       │
       ▼
启动 goroutine 调用 C.Zhulong_stream_probe(url, timeout=3000ms)
       │
       ├─[失败: 鉴权错/超时] ➔ 返回 422 VALIDATION_FAILED / CAMERA_CONNECT_TIMEOUT (直接拦截，不落库!)
       │
       ▼[成功: 提取到格式与分辨率]
补全元数据 (width, height, codec) ➔ GORM 落库 ➔ 返回 200 OK
```

### 4.3 WebSocket 实时码流服务 (`/api/v1/cameras/:id/stream/ws`)

- **协议握手**：标准 WebSocket 握手，从 Cookie 或 Authorization Header 验证 JWT；
- **通道激活**：首个客户端连入时，调用 `engine.Subscribe(cam.SubStreamURL)`；
- **分发帧结构**：
  - 4 字节魔数 (`0x5A484C47` = "ZHLG")
  - 1 字节流类型 (1 = H264, 2 = H265)
  - 1 字节帧类型 (1 = KeyFrame, 0 = NonKey)
  - 8 字节单调 PTS (大端 uint64)
  - 4 字节载荷长度 N (大端 uint32)
  - N 字节原始 NALU / AVPacket 数据
- **断开自愈**：客户端断开后自动注销。若无其余客户端，触发底层延迟休眠。

### 4.4 RTSP 地址解析、特殊符号转义与凭据脱敏算法 (`ParseAndSanitizeRTSP`)

面对工业安防中包含 `@`, `:`, `/`, `?`, `#`, `!` 等特殊字符的摄像机密码，使用专用逆向消歧解析器：

```go
// internal/camera/url.go
package camera

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
)

// ParseAndSanitizeRTSP 解析裸 RTSP URL，消歧并完成 RFC 3986 百分号转义
// 返回值：
//   canonicalURL: 供底层 FFmpeg 拉流使用的转义规范 URL (如 rtsp://admin:Admin%40123%21@192.168.1.64:554/...)
//   maskedURL:    供 Zap 日志与异常堆栈记录的安全脱敏 URL (如 rtsp://admin:******@192.168.1.64:554/...)
//   username:     提取出的摄像机认证用户名
//   password:     提取出的原始密码 (用于 AES-GCM 安全落库与已认证管理员查询)
func ParseAndSanitizeRTSP(raw string) (canonicalURL, maskedURL, username, password string, err error) {
	trimmed := strings.TrimSpace(raw)
	prefix := "rtsp://"
	if !strings.HasPrefix(strings.ToLower(trimmed), prefix) {
		return "", "", "", "", apperr.New(apperr.KindInvalid, "INVALID_SCHEME", "URL 必须以 rtsp:// 开头", nil)
	}
	rest := trimmed[len(prefix):]

	// 1. 核心消歧逻辑：从后往前找最后一个 '@'，排除密码中可能包含的 '@'
	lastAt := strings.LastIndex(rest, "@")
	if lastAt == -1 {
		// 无凭据裸流 (如 rtsp://192.168.1.64:554/live)
		return trimmed, trimmed, "", "", nil
	}

	userInfo := rest[:lastAt]
	hostAndPath := rest[lastAt+1:]

	// 2. 在 userInfo 内部寻找第一个 ':' 区分用户名和密码
	colonIdx := strings.Index(userInfo, ":")
	if colonIdx == -1 {
		return "", "", "", "", apperr.New(apperr.KindInvalid, "INVALID_CREDENTIALS", "凭据格式无效，缺少密码分隔符", nil)
	}
	username = userInfo[:colonIdx]
	password = userInfo[colonIdx+1:]

	// 3. 执行 RFC 3986 百分号转义 (Percent-Encoding)
	// 将 '@' ➔ '%40', ':' ➔ '%3A', '!' ➔ '%21', '#' ➔ '%23' 等
	escapedUser := url.QueryEscape(username)
	escapedPass := url.QueryEscape(password)

	canonicalURL = fmt.Sprintf("rtsp://%s:%s@%s", escapedUser, escapedPass, hostAndPath)
	maskedURL = fmt.Sprintf("rtsp://%s:******@%s", username, hostAndPath)
	return canonicalURL, maskedURL, username, password, nil
}
```

- **FFmpeg 原生侧行为**：底层 FFmpeg `libavformat/rtsp.c` 接收转义后的 `canonicalURL`，在向 IPC 响应 401 Digest 认证挑战时，会自动对用户名与密码执行 Percent-Decode 逆转义，使用真实密码计算摘要响应值，保证握手 100% 成功。
- **持久化与管理员透明契约**：
  - **数据库持久化**：存储 `maskedURL` 与 `username`，真实密码经 AES-GCM 加密存储于 `encrypted_password` 字段；
  - **日志与错误追溯（强制单向脱敏）**：Zap 日志和抛给客户端的异常信息严格使用 `maskedURL`，绝对禁止在日志文件中输出明文密码，杜绝截图与日志打包排查时泄密；
  - **已登录管理员完全掌控**：在 `GET /api/v1/cameras/:id` 详情接口中解密并返回明文密码与完整可用 URL；前端编辑弹窗与详情卡片支持密码显隐切换（👁️）与「一键复制完整 RTSP 地址」按钮，方便单用户设备主人在 VLC/ffplay 等外部工具中排查调试，杜绝教条式黑盒屏蔽。

---

## 5. 前端功能切片与独立导航设计

为了彻底避免设备管理与视频监视耦合带来的臃肿体验，前端划分为两个独立的业务特性切片与独立菜单路由：

### 5.1 摄像机管理切片 (`web/src/features/camera/` ➔ 路由: `/cameras`)
- **定位**：系统管理员与运维人员的设备生命周期管理中枢；
- **核心组件**：
  - `CameraListView.tsx`：设备列表表格，展示摄像机名称、IP、规范化脱敏 URL、主/子码流分辨率与实时状态（在线/重连中/离线/错误），提供一键复制完整 RTSP 地址操作项；
  - `CameraFormDialog.tsx`：添加/编辑摄像机弹窗，支持结构化输入（IP、Port、User、Password）或单行 RTSP URL 粘贴，带前端即时格式校验；编辑模式下支持密码明文显隐切换（👁️）与完整 RTSP URL 快速复制；
  - `ProbeStatusIndicator.tsx`：入库前 3~5s 异步探测状态交互（探测中 Loading ➔ 成功解析并显示 Codec/分辨率 ➔ 失败展示友好错误文案）。

### 5.2 实时多画面预览切片 (`web/src/features/live/` ➔ 路由: `/live`)
- **定位**：安防值班人员的视频作战看板与大屏监视；
- **核心组件**：
  - `LiveDashboardView.tsx`：视频监视主视图，支持 1 画面、4 宫格、9 宫格自适应切换，顶部提供全屏切换与快速筛选栏；
  - `LivePlayer.tsx`：高复用通用播放器组件，内部封装三级自适应硬解：
    1. **WebCodecs 模式**：通过 `window.VideoDecoder` 构建流水线，`OffscreenCanvas` 渲染；
    2. **MSE 模式**：轻量 fMP4 解复用喂入 `MediaSource` 的 `SourceBuffer`；
    3. **WASM SIMD 兜底**：极端老机器动态加载 `jessibuca.wasm` 软解。
  - `RoiOverlayCanvas.tsx`：在视频画面上方根据 PTS 毫秒级叠加 AI 目标检测框与分类标签。

### 5.3 前端单例流连接池与多分屏引用计数 (`useCameraStream` & `StreamPool`)
- **设计动机**：当用户在 4 宫格或 9 宫格中将同一台摄像机分配给多个分屏视口时，如果各自建立独立 WebSocket，会导致单标签页网络带宽成倍浪费、服务端广播开销翻倍。因此在前端层实现纯客户端的**单例流复用池**，对后端完全透明。
- **架构实现**：
  ```typescript
  // web/src/features/live/model/streamPool.ts
  interface StreamSession {
    cameraId: string;
    socket: WebSocket | null;
    refCount: number;
    subscribers: Set<(frame: VideoFrameEvent) => void>;
    // 基础解码器实例与共享帧派发逻辑
  }

  class FrontendStreamPool {
    private sessions = new Map<string, StreamSession>();

    acquire(cameraId: string, onFrame: (frame: VideoFrameEvent) => void): () => void {
      let session = this.sessions.get(cameraId);
      if (!session) {
        session = this.createSession(cameraId);
        this.sessions.set(cameraId, session);
      }
      session.refCount++;
      session.subscribers.add(onFrame);

      // 返回退订清理函数
      return () => {
        session.subscribers.delete(onFrame);
        session.refCount--;
        if (session.refCount <= 0) {
          this.destroySession(cameraId, session);
          this.sessions.delete(cameraId);
        }
      };
    }
  }
  ```
- **React Hook 绑定 (`useCameraStream`)**：
  分屏单元组件（`LiveGridCell`）仅需调用 `useCameraStream(cameraId)`：
  - 挂载时自动 `acquire`，若当前页面已有分屏在播放该相机，**零延迟直接复用流并接入派发器**；
  - 切换或关闭时自动执行注销，当最后一个视口注销时，前端才会断开该相机的 WebSocket。

---

## 6. 测试与验证策略

1. **Native C++ 单元与集成测试 (`tests/`)**：
   - `stream_pool_test.cpp`: 验证多 consumer 对相同 URL 的引用计数增减与 Grace Period 延迟释放；
   - `interrupt_test.cpp`: 模拟 socket 阻塞，验证 `AVIOInterruptCB` 能在 5s 内安全解除阻塞；
   - `abi_probe_test.c`: 纯 C 编译器验证 ABI 兼容性与入参边界。
2. **Go CGO 与并发测试 (`internal/engine/`, `internal/camera/`)**：
   - 验证 `unsafe.Slice` 借用期间无指针越权与 GC 异常（开启 `-race`）；
   - 验证 WebSocket 广播中心多客户端并发连入/断开无 goroutine 泄漏。
3. **端到端集成**：
   - 使用本地测试 RTSP 服务（如 Mediamtx / 本地生成伪码流）验证 H.264 与 H.265 两路不同格式的秒级接入与 Web 播放。
