/**
 * @file mpp_jpeg_encoder.cpp
 * @brief Rockchip MPP VEPU JPEG 硬件编码器实现
 *
 * ⚠️ 内存所有权（谁分配谁释放）：
 *   - 会话（`MppCtx` / `MppBufferGroup` / `MppBuffer`）由本类分配，随会话池
 *     淘汰或对象析构释放，**不依赖 MPP 内部回收**；
 *   - `MppFrame` / `MppPacket` 每次编码分配，函数返回前必定释放（含全部失败分支）；
 *   - 输出的 JPEG 字节被**拷贝**进调用方的 `std::vector`，MPP 的 packet 缓冲
 *     不对外暴露，因此不存在跨调用生命周期问题。
 *
 * ⚠️ 并发：`Impl::mutex_` 串行化整个编码调用。VEPU 是单实例硬件，串行化不会
 *   降低吞吐，但能保证同一会话不被两个线程同时 put_frame。
 */

#include "nodes/snapshot/mpp_jpeg_encoder.hpp"

#include "Zhulong/frame.hpp"
#include "common/log.hpp"
#include "nodes/decode/mpp_api_types.hpp"

#include <dlfcn.h>
#include <unistd.h>

#include <algorithm>
#include <cstdio>
#include <cstring>
#include <map>
#include <mutex>
#include <tuple>
#include <utility>

namespace zhulong {
namespace {

using namespace zhulong::mpp;

constexpr const char *kModule = "nodes.snapshot.jpeg";
constexpr const char *kCaller = "ZhulongJpeg";
constexpr const char *kTag = "ZhulongJpeg";

/** @brief 会话池容量上限：抓拍单事件最多 9 张图，8 个桶足够覆盖常见尺寸组合 */
constexpr size_t kMaxSessions = 8;

/** @brief 每次编码最多等待 200 x 5ms = 1s，超时即判失败回退软件编码 */
constexpr int kPacketRetryCount = 200;
constexpr unsigned kPacketRetrySleepUs = 5000;

/** @brief MPP 的 `jpeg:q_factor` 取值范围，与 `quality` 的 [1,100] 不同 */
constexpr int kQFactorMin = 1;
constexpr int kQFactorMax = 99;

/**
 * @brief `MppBufferInfo_t` 的本地镜像（aarch64 布局：int32 + pad + size_t + 3 x ptr/int）
 *
 * 厂商头文件不进入本节点，故按 ABI 手工镜像；字段顺序必须与 `mpp_buffer.h` 一致。
 */
struct BufferInfo {
    int32_t type{0};
    size_t size{0};
    void *ptr{nullptr};
    void *hnd{nullptr};
    int fd{-1};
    int index{0};
};

/**
 * @brief MPP 符号表（dlopen 装载）
 *
 * 与 `nodes/decode/mpp_loader` 保持**独立**的符号集合：解码要求「全符号就绪」，
 * 若把编码符号并入，会让一个缺失的 enc 符号把硬件解码一并判为不可用。
 */
struct Symbols {
    void *handle{nullptr};

    int (*mpp_create)(MppCtx *, MppApi **){nullptr};
    int (*mpp_init)(MppCtx, MppCtxType, MppCodingType){nullptr};
    int (*mpp_destroy)(MppCtx){nullptr};

    int (*mpp_enc_cfg_init)(MppEncCfg *){nullptr};
    int (*mpp_enc_cfg_deinit)(MppEncCfg){nullptr};
    int (*mpp_enc_cfg_set_s32)(MppEncCfg, const char *, int32_t){nullptr};

    int (*mpp_frame_init)(MppFrame *){nullptr};
    int (*mpp_frame_deinit)(MppFrame *){nullptr};
    void (*mpp_frame_set_width)(MppFrame, uint32_t){nullptr};
    void (*mpp_frame_set_height)(MppFrame, uint32_t){nullptr};
    void (*mpp_frame_set_hor_stride)(MppFrame, uint32_t){nullptr};
    void (*mpp_frame_set_ver_stride)(MppFrame, uint32_t){nullptr};
    void (*mpp_frame_set_fmt)(MppFrame, MppFrameFormat){nullptr};
    void (*mpp_frame_set_buffer)(MppFrame, MppBuffer){nullptr};
    void (*mpp_frame_set_pts)(MppFrame, int64_t){nullptr};

    int (*mpp_packet_init)(MppPacket *, void *, size_t){nullptr};
    int (*mpp_packet_deinit)(MppPacket *){nullptr};
    void *(*mpp_packet_get_pos)(MppPacket){nullptr};
    size_t (*mpp_packet_get_length)(MppPacket){nullptr};

    int (*mpp_buffer_group_get)(MppBufferGroup *, MppBufferType, MppBufferMode, const char *,
                                const char *){nullptr};
    int (*mpp_buffer_group_put)(MppBufferGroup){nullptr};
    int (*mpp_buffer_import_with_tag)(MppBufferGroup, void *, MppBuffer *, const char *,
                                      const char *){nullptr};
    int (*mpp_buffer_get_with_tag)(MppBufferGroup, MppBuffer *, size_t, const char *,
                                   const char *){nullptr};
    int (*mpp_buffer_put_with_caller)(MppBuffer, const char *){nullptr};
    void *(*mpp_buffer_get_ptr_with_caller)(MppBuffer, const char *){nullptr};

    static bool load(Symbols &out) {
        // 1. 硬件节点探针：无 /dev/mpp_service 时无需尝试装载
        if (access("/dev/mpp_service", R_OK | W_OK) != 0) {
            return false;
        }

        // 2. 库名按板端实际存在顺序尝试（Debian 包为 librockchip_mpp.so.1）
        static const char *kLibNames[] = {"librockchip_mpp.so.1", "librockchip_mpp.so",
                                          "librockchip_mpp.so.0", "libmpp.so.1",
                                          "libmpp.so", nullptr};
        for (int i = 0; kLibNames[i] != nullptr && out.handle == nullptr; ++i) {
            out.handle = dlopen(kLibNames[i], RTLD_NOW | RTLD_LOCAL);
        }
        if (out.handle == nullptr) {
            return false;
        }

        // 3. 解析全部必需符号；任一缺失即整体判为不支持（fail closed）
        bool ok = true;
#define ZL_LOAD_SYM(field) \
    ok = ok && ((out.field = reinterpret_cast<decltype(out.field)>(dlsym(out.handle, #field))) != nullptr)
        ZL_LOAD_SYM(mpp_create);
        ZL_LOAD_SYM(mpp_init);
        ZL_LOAD_SYM(mpp_destroy);

        ZL_LOAD_SYM(mpp_enc_cfg_init);
        ZL_LOAD_SYM(mpp_enc_cfg_deinit);
        ZL_LOAD_SYM(mpp_enc_cfg_set_s32);

        ZL_LOAD_SYM(mpp_frame_init);
        ZL_LOAD_SYM(mpp_frame_deinit);
        ZL_LOAD_SYM(mpp_frame_set_width);
        ZL_LOAD_SYM(mpp_frame_set_height);
        ZL_LOAD_SYM(mpp_frame_set_hor_stride);
        ZL_LOAD_SYM(mpp_frame_set_ver_stride);
        ZL_LOAD_SYM(mpp_frame_set_fmt);
        ZL_LOAD_SYM(mpp_frame_set_buffer);
        ZL_LOAD_SYM(mpp_frame_set_pts);

        ZL_LOAD_SYM(mpp_packet_init);
        ZL_LOAD_SYM(mpp_packet_deinit);
        ZL_LOAD_SYM(mpp_packet_get_pos);
        ZL_LOAD_SYM(mpp_packet_get_length);

        ZL_LOAD_SYM(mpp_buffer_group_get);
        ZL_LOAD_SYM(mpp_buffer_group_put);
        ZL_LOAD_SYM(mpp_buffer_import_with_tag);
        ZL_LOAD_SYM(mpp_buffer_get_with_tag);
        ZL_LOAD_SYM(mpp_buffer_put_with_caller);
        ZL_LOAD_SYM(mpp_buffer_get_ptr_with_caller);
#undef ZL_LOAD_SYM

        if (!ok) {
            dlclose(out.handle);
            out.handle = nullptr;
        }
        return ok;
    }

    void close() noexcept {
        if (handle != nullptr) {
            dlclose(handle);
            handle = nullptr;
        }
    }
};

/** @brief 16 字节对齐（VEPU 的行/垂直 stride 要求），并对齐到偶数以满足 4:2:0 下采样 */
int align_stride(int value) {
    return (value + 15) / 16 * 16;
}

int floor_even(int value) {
    return value < 0 ? 0 : (value & ~1);
}

int ceil_even(int value) {
    return value <= 0 ? 0 : ((value + 1) & ~1);
}

/** @brief 一个待编码的像素矩形（已对齐并 clamp 进画面） */
struct Rect {
    int x{0};
    int y{0};
    int w{0};
    int h{0};
};

/**
 * @brief 把请求矩形对齐并 clamp，语义与 `extract_yuv_region` 完全一致
 *
 * 两条路径（硬件 / 软件）必须产出**同尺寸**图像，否则调用方的 box_index →
 * 图像尺寸约定会因后端不同而漂移。
 */
bool normalize_rect(int x, int y, int w, int h, uint32_t frame_w, uint32_t frame_h, Rect &out) {
    if (w <= 0 || h <= 0 || frame_w < 2 || frame_h < 2) {
        return false;
    }
    int x0 = floor_even(x < 0 ? 0 : x);
    int y0 = floor_even(y < 0 ? 0 : y);
    int x1 = ceil_even(x + w);
    int y1 = ceil_even(y + h);
    if (x1 > static_cast<int>(frame_w)) {
        x1 = floor_even(static_cast<int>(frame_w));
    }
    if (y1 > static_cast<int>(frame_h)) {
        y1 = floor_even(static_cast<int>(frame_h));
    }
    if (x1 <= x0 || y1 <= y0) {
        return false;
    }
    out = Rect{x0, y0, x1 - x0, y1 - y0};
    return out.w >= 2 && out.h >= 2;
}

/**
 * @brief 从 JPEG 的 SOF 段读出编码尺寸
 *
 * 自检要断言「产物尺寸就是请求尺寸」；解析段结构比链接解码器便宜得多，
 * 也正好覆盖浏览器解图真正依赖的那部分结构。
 */
bool read_jpeg_sof_size(const std::vector<uint8_t> &jpeg, int &out_w, int &out_h) {
    size_t pos = 2; // 跳过 SOI
    while (pos + 4 <= jpeg.size()) {
        if (jpeg[pos] != 0xFF) {
            return false;
        }
        const uint8_t marker = jpeg[pos + 1];
        if (marker == 0xD8 || marker == 0xD9 || (marker >= 0xD0 && marker <= 0xD7) ||
            marker == 0x01) {
            pos += 2;
            continue;
        }
        if (pos + 4 > jpeg.size()) {
            return false;
        }
        const size_t seg_len = (static_cast<size_t>(jpeg[pos + 2]) << 8) | jpeg[pos + 3];
        if (seg_len < 2 || pos + 2 + seg_len > jpeg.size()) {
            return false;
        }
        const bool is_sof = marker >= 0xC0 && marker <= 0xCF && marker != 0xC4 &&
                            marker != 0xC8 && marker != 0xCC;
        if (is_sof) {
            if (seg_len < 8) {
                return false;
            }
            out_h = (static_cast<int>(jpeg[pos + 5]) << 8) | jpeg[pos + 6];
            out_w = (static_cast<int>(jpeg[pos + 7]) << 8) | jpeg[pos + 8];
            return out_w > 0 && out_h > 0;
        }
        pos += 2 + seg_len;
    }
    return false;
}

bool jpeg_envelope_ok(const std::vector<uint8_t> &jpeg) {
    return jpeg.size() > 4 && jpeg[0] == 0xFF && jpeg[1] == 0xD8 &&
           jpeg[jpeg.size() - 2] == 0xFF && jpeg[jpeg.size() - 1] == 0xD9;
}

} // namespace

// ============================================================================
//                                Impl
// ============================================================================

struct MppJpegEncoder::Impl {
    /** @brief 一个编码会话及其专属输出缓冲（像素上传目标） */
    struct Session {
        MppCtx ctx{nullptr};
        MppApi *mpi{nullptr};
        MppBufferGroup group{nullptr};
        MppBuffer buffer{nullptr};
        int width{0};
        int height{0};
        int hor_stride{0};
        int ver_stride{0};
        uint64_t last_use{0};
    };

    Symbols sym;
    std::mutex mutex;
    std::map<std::tuple<int, int, int, int>, Session> sessions;
    uint64_t use_counter{0};

    ~Impl() {
        // 显式释放顺序：先归还缓冲，再销毁上下文；异常/崩溃都不能穿越到调用方
        for (auto &entry : sessions) {
            Session &s = entry.second;
            if (s.buffer != nullptr && sym.mpp_buffer_put_with_caller != nullptr) {
                sym.mpp_buffer_put_with_caller(s.buffer, kCaller);
            }
            if (s.group != nullptr && sym.mpp_buffer_group_put != nullptr) {
                sym.mpp_buffer_group_put(s.group);
            }
            if (s.ctx != nullptr && sym.mpp_destroy != nullptr) {
                sym.mpp_destroy(s.ctx);
            }
        }
        sessions.clear();
        sym.close();
    }

    /**
     * @brief 建立会话并按 `(w, h, hor_stride, ver_stride)` 分桶缓存
     *
     * ⚠️ key 必须含 stride：DMA-BUF 直通时 stride 由解码器决定（可能 2048），
     * 与 `align16(w)` 不同；把这两种配置混进同一个会话会让 VEPU 用错行宽，
     * 产出斜切/花屏图。
     */
    Session *acquire(int w, int h, int hor_stride, int ver_stride) {
        const auto key = std::make_tuple(w, h, hor_stride, ver_stride);
        auto it = sessions.find(key);
        if (it != sessions.end()) {
            it->second.last_use = ++use_counter;
            return &it->second;
        }
        if (sessions.size() >= kMaxSessions && !evict_oldest()) {
            return nullptr;
        }
        Session session;
        if (!open_session(session, w, h, hor_stride, ver_stride)) {
            close_session(session);
            return nullptr;
        }
        session.last_use = ++use_counter;
        auto inserted = sessions.emplace(key, session);
        return &inserted.first->second;
    }

    bool evict_oldest() {
        if (sessions.empty()) {
            return false;
        }
        auto oldest = sessions.begin();
        for (auto it = sessions.begin(); it != sessions.end(); ++it) {
            if (it->second.last_use < oldest->second.last_use) {
                oldest = it;
            }
        }
        close_session(oldest->second);
        sessions.erase(oldest);
        return true;
    }

    void close_session(Session &s) noexcept {
        if (s.buffer != nullptr && sym.mpp_buffer_put_with_caller != nullptr) {
            sym.mpp_buffer_put_with_caller(s.buffer, kCaller);
            s.buffer = nullptr;
        }
        if (s.group != nullptr && sym.mpp_buffer_group_put != nullptr) {
            sym.mpp_buffer_group_put(s.group);
            s.group = nullptr;
        }
        if (s.ctx != nullptr && sym.mpp_destroy != nullptr) {
            sym.mpp_destroy(s.ctx);
            s.ctx = nullptr;
        }
        s.mpi = nullptr;
    }

    bool open_session(Session &s, int w, int h, int hor_stride, int ver_stride) {
        s.width = w;
        s.height = h;
        s.hor_stride = hor_stride;
        s.ver_stride = ver_stride;

        if (sym.mpp_create(&s.ctx, &s.mpi) != MPP_OK || s.ctx == nullptr || s.mpi == nullptr) {
            return false;
        }
        if (sym.mpp_init(s.ctx, MPP_CTX_ENC, MPP_VIDEO_CodingMJPEG) != MPP_OK) {
            return false;
        }
        if (!apply_cfg(s, s.width, s.height, s.hor_stride, s.ver_stride, 85)) {
            return false;
        }

        // 输出缓冲：一次申请、整会话复用。类型选 DMA_HEAP（板端普遍可用），
        // 失败则退到 ION 以兼容老 BSP。
        size_t size = static_cast<size_t>(s.hor_stride) * static_cast<size_t>(s.ver_stride) * 3 / 2;
        if (sym.mpp_buffer_group_get(&s.group, MPP_BUFFER_TYPE_DMA_HEAP, MPP_BUFFER_INTERNAL,
                                     kTag, kCaller) != MPP_OK ||
            s.group == nullptr) {
            if (sym.mpp_buffer_group_get(&s.group, MPP_BUFFER_TYPE_ION, MPP_BUFFER_INTERNAL, kTag,
                                         kCaller) != MPP_OK ||
                s.group == nullptr) {
                return false;
            }
        }
        if (sym.mpp_buffer_get_with_tag(s.group, &s.buffer, size, kTag, kCaller) != MPP_OK ||
            s.buffer == nullptr) {
            return false;
        }
        return true;
    }

    /** @brief 下发一次完整编码配置（含质量），MJPEG 每帧独立，故可按帧改 */
    bool apply_cfg(Session &s, int w, int h, int hor_stride, int ver_stride, int quality) {
        MppEncCfg cfg = nullptr;
        if (sym.mpp_enc_cfg_init(&cfg) != MPP_OK || cfg == nullptr) {
            return false;
        }
        const int q = std::min(kQFactorMax, std::max(kQFactorMin, quality));
        bool ok = true;
        ok = ok && sym.mpp_enc_cfg_set_s32(cfg, "prep:width", w) == MPP_OK;
        ok = ok && sym.mpp_enc_cfg_set_s32(cfg, "prep:height", h) == MPP_OK;
        ok = ok && sym.mpp_enc_cfg_set_s32(cfg, "prep:hor_stride", hor_stride) == MPP_OK;
        ok = ok && sym.mpp_enc_cfg_set_s32(cfg, "prep:ver_stride", ver_stride) == MPP_OK;
        ok = ok && sym.mpp_enc_cfg_set_s32(cfg, "prep:format", MPP_FMT_YUV420SP) == MPP_OK;
        ok = ok && sym.mpp_enc_cfg_set_s32(cfg, "jpeg:q_factor", q) == MPP_OK;
        if (ok) {
            ok = s.mpi->control(s.ctx, MPP_ENC_SET_CFG, cfg) == MPP_OK;
        }
        sym.mpp_enc_cfg_deinit(cfg);
        return ok;
    }

    /**
     * @brief 把源帧的矩形区域按 NV12 紧凑布局上传到会话的 mpp_buffer
     *
     * ⚠️ DMA-BUF 直通（`mpp_buffer_import`）未启用：API 已验证可用，但像素
     *    正确性待板端复核，此处优先保证正确性。上传是唯一的一次 CPU 拷贝。
     */
    bool upload(Session &s, const CpuFrameView &src, const Rect &r) {
        auto *dst = static_cast<uint8_t *>(sym.mpp_buffer_get_ptr_with_caller(s.buffer, kCaller));
        if (dst == nullptr) {
            return false;
        }
        const size_t hstride = static_cast<size_t>(s.hor_stride);
        const size_t y_stride = src.strides[0] != 0 ? src.strides[0] : src.width;
        if (src.planes[0] == nullptr || src.planes[1] == nullptr) {
            return false;
        }

        // 1. 亮度平面
        const uint8_t *ysrc = src.planes[0] + y_stride * static_cast<size_t>(r.y) + r.x;
        for (int row = 0; row < r.h; ++row) {
            std::memcpy(dst + hstride * static_cast<size_t>(row),
                        ysrc + y_stride * static_cast<size_t>(row), static_cast<size_t>(r.w));
        }

        // 2. 色度平面（写入 NV12 交错布局）
        uint8_t *duv = dst + hstride * static_cast<size_t>(s.ver_stride);
        const int cw = r.w / 2;
        const int ch = r.h / 2;
        if (src.format == FRAME_FORMAT_YUV420P) {
            if (src.planes[2] == nullptr) {
                return false;
            }
            const size_t u_stride = src.strides[1] != 0 ? src.strides[1] : (src.width / 2);
            const size_t v_stride = src.strides[2] != 0 ? src.strides[2] : (src.width / 2);
            const int cx = r.x / 2;
            const int cy = r.y / 2;
            for (int row = 0; row < ch; ++row) {
                const uint8_t *up = src.planes[1] + u_stride * static_cast<size_t>(cy + row) + cx;
                const uint8_t *vp = src.planes[2] + v_stride * static_cast<size_t>(cy + row) + cx;
                uint8_t *out = duv + hstride * static_cast<size_t>(row);
                for (int i = 0; i < cw; ++i) {
                    out[i * 2] = up[i];
                    out[i * 2 + 1] = vp[i];
                }
            }
        } else {
            // NV12：UV 交错且与 Y 共用 stride；交错平面里一个色度样本占 2 字节，
            // 因此 x 偏移的字节数等于像素偏移。
            const int cy = r.y / 2;
            for (int row = 0; row < ch; ++row) {
                const uint8_t *uvp =
                    src.planes[1] + y_stride * static_cast<size_t>(cy + row) + r.x;
                std::memcpy(duv + hstride * static_cast<size_t>(row), uvp,
                            static_cast<size_t>(r.w));
            }
        }
        return true;
    }

    /**
     * @brief 对一份已就位的 `MppBuffer` 执行编码（不含任何像素搬运）
     *
     * @param buffer 像素来源：上传路径是会话自带缓冲，直通路径是导入的 DMA-BUF
     */
    bool encode_buffer(Session &s, MppBuffer buffer, int w, int h, int hor_stride, int ver_stride,
                       int quality, std::vector<uint8_t> &out) {
        out.clear();
        if (!apply_cfg(s, w, h, hor_stride, ver_stride, quality)) {
            return false;
        }

        MppFrame frame = nullptr;
        if (sym.mpp_frame_init(&frame) != MPP_OK || frame == nullptr) {
            return false;
        }
        sym.mpp_frame_set_width(frame, static_cast<uint32_t>(w));
        sym.mpp_frame_set_height(frame, static_cast<uint32_t>(h));
        sym.mpp_frame_set_hor_stride(frame, static_cast<uint32_t>(hor_stride));
        sym.mpp_frame_set_ver_stride(frame, static_cast<uint32_t>(ver_stride));
        sym.mpp_frame_set_fmt(frame, MPP_FMT_YUV420SP);
        sym.mpp_frame_set_buffer(frame, buffer);

        bool ok = s.mpi->encode_put_frame(s.ctx, frame) == MPP_OK;
        sym.mpp_frame_deinit(&frame);
        if (!ok) {
            return false;
        }

        // 有界等待：绝不无限阻塞抓拍 worker（软件编码是兜底，不是死等硬件）
        for (int i = 0; i < kPacketRetryCount; ++i) {
            MppPacket packet = nullptr;
            const int rc = s.mpi->encode_get_packet(s.ctx, &packet);
            if (rc == MPP_OK && packet != nullptr) {
                void *pos = sym.mpp_packet_get_pos(packet);
                const size_t len = sym.mpp_packet_get_length(packet);
                if (pos != nullptr && len > 0) {
                    out.assign(static_cast<uint8_t *>(pos), static_cast<uint8_t *>(pos) + len);
                }
                sym.mpp_packet_deinit(&packet);
                return !out.empty();
            }
            if (rc != MPP_OK && rc != MPP_NOK && rc != MPP_ERR_TIMEOUT) {
                if (packet != nullptr) {
                    sym.mpp_packet_deinit(&packet);
                }
                return false;
            }
            usleep(kPacketRetrySleepUs);
        }
        return false;
    }

    /** @brief 上传路径：把 CPU 可见帧的矩形写进会话缓冲后编码 */
    bool encode_locked(Session &s, const CpuFrameView &src, const Rect &r, int quality,
                       std::vector<uint8_t> &out) {
        out.clear();
        if (!upload(s, src, r)) {
            return false;
        }
        return encode_buffer(s, s.buffer, s.width, s.height, s.hor_stride, s.ver_stride, quality,
                             out);
    }

    /**
     * @brief 直通路径：把外部 DMA-BUF 导入成 `MppBuffer` 后编码
     *
     * ⚠️ 缓存一致性：本路径的**写者也是硬件**（解码器），CPU 侧没有脏行，
     *    因此不需要 `DMA_BUF_IOCTL_SYNC`。若将来出现「CPU 写完再硬编」的场景
     *    （例如叠加 OSD），必须在编码前显式 end-write 同步，否则会静默出灰图。
     */
    bool encode_dma(const HardwareFrame &frame, int w, int h, int hor_stride, int ver_stride,
                    int quality, std::vector<uint8_t> &out) {
        out.clear();
        if (frame.dma_fd < 0 || frame.format != FRAME_FORMAT_NV12) {
            return false;
        }
        const size_t size =
            static_cast<size_t>(hor_stride) * static_cast<size_t>(ver_stride) * 3 / 2;

        BufferInfo info;
        info.type = MPP_BUFFER_TYPE_EXT_DMA;
        info.size = size;
        info.fd = frame.dma_fd;
        MppBuffer imported = nullptr;
        if (sym.mpp_buffer_import_with_tag(nullptr, &info, &imported, kTag, kCaller) != MPP_OK ||
            imported == nullptr) {
            return false;
        }

        const bool ok = encode_buffer(*acquire(w, h, hor_stride, ver_stride), imported, w, h,
                                      hor_stride, ver_stride, quality, out);
        // 谁导入谁归还：必须在帧返回解码器缓冲池之前配平
        sym.mpp_buffer_put_with_caller(imported, kCaller);
        return ok;
    }
};

// ============================================================================
//                             MppJpegEncoder
// ============================================================================

MppJpegEncoder::MppJpegEncoder(std::unique_ptr<Impl> impl) : impl_(std::move(impl)) {}

MppJpegEncoder::~MppJpegEncoder() = default;

std::unique_ptr<MppJpegEncoder> MppJpegEncoder::create() {
    Symbols sym;
    if (!Symbols::load(sym)) {
        ZL_LOGI(0, kModule, "mpp_jpeg_encoder_unavailable: symbols_or_device_missing");
        return nullptr;
    }

    auto impl = std::make_unique<Impl>();
    impl->sym = sym;

    // 探针：真正建一个会话并下发编码配置，而不是「库能加载就算支持」。
    // 某些 BSP 上 mpp_init(MJPEG) 会成功但 MPP_ENC_SET_CFG 返回 MPP_ERR_VALUE。
    std::unique_ptr<MppJpegEncoder> encoder(new MppJpegEncoder(std::move(impl)));
    {
        Impl &inner = *encoder->impl_;
        std::lock_guard<std::mutex> lock(inner.mutex);
        Impl::Session probe;
        if (!inner.open_session(probe, 64, 64, align_stride(64), align_stride(64))) {
            inner.close_session(probe);
            ZL_LOGW(0, kModule, "mpp_jpeg_encoder_unavailable: session_probe_failed");
            return nullptr;
        }
        inner.close_session(probe);
    }
    ZL_LOGI(0, kModule, "mpp_jpeg_encoder_ready");
    return encoder;
}

bool MppJpegEncoder::encode_dma_frame(const HardwareFrame &frame, int quality,
                                      std::vector<uint8_t> &out_jpeg) {
    out_jpeg.clear();
    if (frame.dma_fd < 0 || frame.format != FRAME_FORMAT_NV12 || frame.width < 2 ||
        frame.height < 2) {
        return false;
    }
    // ⚠️ stride 必须原样取自帧：解码器可能给 2048 这类非 16 对齐宽度的行跨度，
    //    用 align16(width) 顶替会让 VEPU 按错误行宽读取，产出斜切图。
    const int w = static_cast<int>(frame.width) & ~1;
    const int h = static_cast<int>(frame.height) & ~1;
    const int hor_stride =
        frame.stride >= static_cast<uint32_t>(w) ? static_cast<int>(frame.stride) : align_stride(w);
    const int ver_stride =
        frame.vstride >= static_cast<uint32_t>(h) ? static_cast<int>(frame.vstride) : h;

    std::lock_guard<std::mutex> lock(impl_->mutex);
    return impl_->encode_dma(frame, w, h, hor_stride, ver_stride, quality, out_jpeg);
}

bool MppJpegEncoder::encode_region(const CpuFrameView &src, int x, int y, int w, int h,
                                   int quality, std::vector<uint8_t> &out_jpeg) {
    out_jpeg.clear();
    if (src.format != FRAME_FORMAT_NV12 && src.format != FRAME_FORMAT_YUV420P) {
        return false;
    }
    Rect rect;
    if (!normalize_rect(x, y, w, h, src.width, src.height, rect)) {
        return false;
    }

    std::lock_guard<std::mutex> lock(impl_->mutex);
    Impl::Session *session = impl_->acquire(rect.w, rect.h, align_stride(rect.w),
                                            align_stride(rect.h));
    if (session == nullptr) {
        return false;
    }
    return impl_->encode_locked(*session, src, rect, quality, out_jpeg);
}

bool MppJpegEncoder::self_test() {
    // 两张 32x32 合成 NV12：仅亮度不同，色度同为中性 128。
    constexpr int kSize = 32;
    constexpr size_t kY = static_cast<size_t>(kSize) * kSize;
    std::vector<uint8_t> dark(kY + kY / 2, 128);
    std::vector<uint8_t> bright(kY + kY / 2, 128);
    std::memset(dark.data(), 16, kY);
    std::memset(bright.data(), 235, kY);

    auto make_view = [](std::vector<uint8_t> &storage) {
        CpuFrameView view{};
        view.width = static_cast<uint32_t>(kSize);
        view.height = static_cast<uint32_t>(kSize);
        view.format = FRAME_FORMAT_NV12;
        view.planes[0] = storage.data();
        view.planes[1] = storage.data() + kY;
        view.strides[0] = static_cast<uint32_t>(kSize);
        return view;
    };

    std::vector<uint8_t> dark_jpeg;
    std::vector<uint8_t> bright_jpeg;
    {
        std::lock_guard<std::mutex> lock(impl_->mutex);
        CpuFrameView dark_view = make_view(dark);
        CpuFrameView bright_view = make_view(bright);
        Rect rect;
        if (!normalize_rect(0, 0, kSize, kSize, dark_view.width, dark_view.height, rect)) {
            return false;
        }
        Impl::Session *dark_session = impl_->acquire(rect.w, rect.h, align_stride(rect.w),
                                                     align_stride(rect.h));
        if (dark_session == nullptr ||
            !impl_->encode_locked(*dark_session, dark_view, rect, 85, dark_jpeg)) {
            return false;
        }
        Impl::Session *bright_session = impl_->acquire(rect.w, rect.h, align_stride(rect.w),
                                                       align_stride(rect.h));
        if (bright_session == nullptr ||
            !impl_->encode_locked(*bright_session, bright_view, rect, 85, bright_jpeg)) {
            return false;
        }
    }

    // 1. 结构完整
    if (!jpeg_envelope_ok(dark_jpeg) || !jpeg_envelope_ok(bright_jpeg)) {
        return false;
    }
    // 2. 尺寸就是请求尺寸（后端差异不能改变产物几何）
    int w = 0;
    int h = 0;
    if (!read_jpeg_sof_size(dark_jpeg, w, h) || w != kSize || h != kSize) {
        return false;
    }
    // 3. 亮度敏感性：若编码器忽略 Y 平面（某些 BSP 上出现「恒定灰图」），
    //    两份仅亮度不同的输入会产出完全相同的字节。
    if (dark_jpeg == bright_jpeg) {
        return false;
    }
    return true;
}

} // namespace zhulong
