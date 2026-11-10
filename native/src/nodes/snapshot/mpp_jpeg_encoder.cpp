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
#include "common/mpp/mpp_api_types.hpp"

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

constexpr const char *log_module = "nodes.snapshot.jpeg";
constexpr const char *caller_name = "ZhulongJpeg";
constexpr const char *group_tag = "ZhulongJpeg";

/** @brief 会话池容量上限：抓拍单事件最多 9 张图，8 个桶足够覆盖常见尺寸组合 */
constexpr size_t max_sessions = 8;

/** @brief 每次编码最多等待 200 x 5ms = 1s，超时即判失败回退软件编码 */
constexpr int packet_retry_count = 200;
constexpr unsigned packet_retry_sleep_us = 5000;

/** @brief MPP 的 `jpeg:q_factor` 取值范围，与 `quality` 的 [1,100] 不同 */
constexpr int q_factor_min = 1;
constexpr int q_factor_max = 99;

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
        static const char *lib_names[] = {"librockchip_mpp.so.1", "librockchip_mpp.so",
                                          "librockchip_mpp.so.0", "libmpp.so.1",
                                          "libmpp.so", nullptr};
        for (int i = 0; lib_names[i] != nullptr && out.handle == nullptr; ++i) {
            out.handle = dlopen(lib_names[i], RTLD_NOW | RTLD_LOCAL);
        }
        if (out.handle == nullptr) {
            return false;
        }

        // 3. 解析全部必需符号；任一缺失即整体判为不支持（fail closed）。
        // ⚠️ 不用 `ok && load(...)` 短路：那样第一个缺失符号之后就不再 dlsym，
        //    日志里永远只有「symbols_or_device_missing」而无法定位到底缺哪几个。
        //    与 nodes/decode/mpp_loader.cpp 同名宏的写法保持一致。
        bool ok = true;
#define ZL_LOAD_SYM(field) \
    out.field = reinterpret_cast<decltype(out.field)>(dlsym(out.handle, #field)); \
    ok = (out.field != nullptr) && ok
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
            // 整体重置：半解析的函数表不得留在调用方手里
            out = Symbols{};
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
    return (value + 15) & ~15;
}

/** @brief 一个待编码的像素矩形（已对齐并 clamp 进画面） */
struct Rect {
    int x{0};
    int y{0};
    int w{0};
    int h{0};
};

/**
 * @brief 把请求矩形对齐并 clamp，委托给 `normalize_crop_rect` 以确保软/硬编码几何完全一致
 */
bool normalize_rect(int x, int y, int w, int h, uint32_t frame_w, uint32_t frame_h, Rect &out) {
    return normalize_crop_rect(x, y, w, h, frame_w, frame_h, out.x, out.y, out.w, out.h);
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
    /**
     * @brief 一个编码会话及其专属输出缓冲（像素上传目标）
     *
     * ⚠️ 裸句柄由析构函数统一收尾（Core Guidelines R.1/R.11）：会话可能在任何一步初始化
     *    失败、被 LRU 淘汰、或 `sessions.emplace` 抛 `bad_alloc` 时被丢弃。用「每次都要
     *    记得手工释放」的配平方式，上述三条路径任一漏掉就是 ctx/group/buffer 泄漏。
     */
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
        Symbols *sym{nullptr}; /**< 收尾所需；必须在开始分配资源前就填好 */

        Session() = default;
        Session(const Session &) = delete;
        Session &operator=(const Session &) = delete;
        Session(Session &&) = delete;
        Session &operator=(Session &&) = delete;
        ~Session() { release(); }

        /** @brief 幂等收尾：归还缓冲 → 归还缓冲组 → 销毁上下文 */
        void release() noexcept {
            if (sym == nullptr) {
                return;
            }
            if (buffer != nullptr && sym->mpp_buffer_put_with_caller != nullptr) {
                sym->mpp_buffer_put_with_caller(buffer, caller_name);
                buffer = nullptr;
            }
            if (group != nullptr && sym->mpp_buffer_group_put != nullptr) {
                sym->mpp_buffer_group_put(group);
                group = nullptr;
            }
            if (ctx != nullptr && sym->mpp_destroy != nullptr) {
                sym->mpp_destroy(ctx);
                ctx = nullptr;
            }
            mpi = nullptr;
        }
    };

    Symbols sym;
    std::mutex mutex;
    /**
     * @brief 会话池：`unique_ptr` 保证指针稳定，且淘汰/异常路径由 `Session` 析构统一收尾
     */
    std::map<std::tuple<int, int, int, int>, std::unique_ptr<Session>> sessions;
    uint64_t use_counter{0};

    ~Impl() {
        // ⚠️ 顺序关键：先把会话全部销毁（dtor 还要回调 MPP 函数），最后才 dlclose。
        //    反过来会让“最后一批会话”调进已卸载的库。
        sessions.clear();
        sym.close();
    }

    /**
     * @brief 建立会话并按 `(w, h, hor_stride, ver_stride)` 分桶缓存
     *
     * ⚠️ key 必须含 stride：DMA-BUF 直通时 stride 由解码器决定（可能 2048），
     * 与 `align16(w)` 不同；把这两种配置混进同一个会话会让 VEPU 用错行宽，
     * 产出斜切/花屏图。
     *
     * @return 可用会话；**失败返回 nullptr**（调用方必须判空，不得直接解引用）
     */
    Session *acquire(int w, int h, int hor_stride, int ver_stride) {
        const auto key = std::make_tuple(w, h, hor_stride, ver_stride);
        auto it = sessions.find(key);
        if (it != sessions.end()) {
            it->second->last_use = ++use_counter;
            return it->second.get();
        }
        if (sessions.size() >= max_sessions && !evict_oldest()) {
            return nullptr;
        }
        auto session = std::make_unique<Session>();
        if (!open_session(*session, w, h, hor_stride, ver_stride)) {
            return nullptr; // 半初始化的句柄由 Session 析构回收
        }
        session->last_use = ++use_counter;
        Session *raw = session.get();
        // emplace 抛 bad_alloc 时 unique_ptr 析构仍会配平资源
        sessions.emplace(key, std::move(session));
        return raw;
    }

    bool evict_oldest() {
        if (sessions.empty()) {
            return false;
        }
        auto oldest = sessions.begin();
        for (auto it = sessions.begin(); it != sessions.end(); ++it) {
            if (it->second->last_use < oldest->second->last_use) {
                oldest = it;
            }
        }
        sessions.erase(oldest); // Session 析构完成归还
        return true;
    }

    bool open_session(Session &s, int w, int h, int hor_stride, int ver_stride) {
        // 先挂上收尾所需的符号表：后面任何一步失败，析构都能正确归还未 fully-built 的句柄
        s.sym = &sym;
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
                                     group_tag, caller_name) != MPP_OK ||
            s.group == nullptr) {
            if (sym.mpp_buffer_group_get(&s.group, MPP_BUFFER_TYPE_ION, MPP_BUFFER_INTERNAL, group_tag,
                                         caller_name) != MPP_OK ||
                s.group == nullptr) {
                return false;
            }
        }
        if (sym.mpp_buffer_get_with_tag(s.group, &s.buffer, size, group_tag, caller_name) != MPP_OK ||
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
        const int q = std::min(q_factor_max, std::max(q_factor_min, quality));
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
     * 这是上传（兼容）路径：整帧直通走 `encode_dma`（见 §7.6），但**裁剪框与软解帧**
     * 只能走这里，因为 MPP 没有可靠的源偏移语义，而软解帧根本没有 `dma_fd`。
     * CPU 拷贝只发生在本路径。
     */
    bool upload(Session &s, const CpuFrameView &src, const Rect &r) {
        auto *dst = static_cast<uint8_t *>(sym.mpp_buffer_get_ptr_with_caller(s.buffer, caller_name));
        if (dst == nullptr) {
            return false;
        }
        const size_t hstride = static_cast<size_t>(s.hor_stride);
        const size_t y_stride = src.strides[0] != 0 ? src.strides[0] : src.width;
        if (src.planes[0] == nullptr || src.planes[1] == nullptr || y_stride < src.width) {
            return false;
        }
        size_t u_stride = 0;
        size_t v_stride = 0;
        size_t uv_stride = 0;
        if (src.format == FRAME_FORMAT_YUV420P) {
            if (src.planes[2] == nullptr) {
                return false;
            }
            const size_t min_chroma_stride = src.width / 2;
            u_stride = src.strides[1] != 0 ? src.strides[1] : min_chroma_stride;
            v_stride = src.strides[2] != 0 ? src.strides[2] : min_chroma_stride;
            if (u_stride < min_chroma_stride || v_stride < min_chroma_stride) {
                return false;
            }
        } else {
            uv_stride = src.strides[1] != 0 ? src.strides[1] : y_stride;
            if (uv_stride < src.width) {
                return false;
            }
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
            // NV12：UV 交错；交错平面里一个色度样本占 2 字节，因此 x 偏移的字节数等于像素偏移。
            const int cy = r.y / 2;
            for (int row = 0; row < ch; ++row) {
                const uint8_t *uvp =
                    src.planes[1] + uv_stride * static_cast<size_t>(cy + row) + r.x;
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
        for (int i = 0; i < packet_retry_count; ++i) {
            MppPacket packet = nullptr;
            const int rc = s.mpi->encode_get_packet(s.ctx, &packet);
            if (rc == MPP_OK && packet != nullptr) {
                struct PacketGuard {
                    Symbols *sym{nullptr};
                    MppPacket pkt{nullptr};
                    ~PacketGuard() {
                        if (pkt != nullptr && sym != nullptr && sym->mpp_packet_deinit != nullptr) {
                            sym->mpp_packet_deinit(&pkt);
                        }
                    }
                } guard{&sym, packet};

                void *pos = sym.mpp_packet_get_pos(packet);
                const size_t len = sym.mpp_packet_get_length(packet);
                if (pos != nullptr && len > 0) {
                    out.assign(static_cast<uint8_t *>(pos), static_cast<uint8_t *>(pos) + len);
                }
                return !out.empty();
            }
            if (rc != MPP_OK && rc != MPP_NOK && rc != MPP_ERR_TIMEOUT) {
                if (packet != nullptr) {
                    sym.mpp_packet_deinit(&packet);
                }
                return false;
            }
            usleep(packet_retry_sleep_us);
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
        const auto key = std::make_tuple(s.width, s.height, s.hor_stride, s.ver_stride);
        const bool ok = encode_buffer(s, s.buffer, s.width, s.height, s.hor_stride, s.ver_stride, quality,
                                      out);
        if (!ok) {
            // ⚠️ 只要下发了 frame 但未能正常取出 packet，会话状态即被污染，必须强制淘汰，杜绝串包与死锁
            sessions.erase(key);
        }
        return ok;
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
        if (sym.mpp_buffer_import_with_tag(nullptr, &info, &imported, group_tag, caller_name) != MPP_OK ||
            imported == nullptr) {
            return false;
        }

        // ⚠️ 导入的缓冲由栈上守卫配平：无论 encode_buffer 正常返回、提前失败
        //    还是抛 std::bad_alloc（out.assign），都不会把导入的 DMA-BUF 留在 MPP 里。
        struct ImportGuard {
            Symbols *sym{nullptr};
            MppBuffer buffer{nullptr};
            ~ImportGuard() {
                if (buffer != nullptr && sym != nullptr &&
                    sym->mpp_buffer_put_with_caller != nullptr) {
                    sym->mpp_buffer_put_with_caller(buffer, caller_name);
                }
            }
        } guard{&sym, imported};

        // ⚠️ acquire() 在建会话失败时返回 nullptr（VPU 被占 / buffer_group 申请失败等），
        //    这里绝不能直接解引用：那会把“抓拍降级”变成进程段错误。
        Session *session = acquire(w, h, hor_stride, ver_stride);
        if (session == nullptr) {
            ZL_LOGW(0, log_module, "jpeg_dmabuf_session_unavailable: %dx%d", w, h);
            return false;
        }
        // 谁导入谁归还：ImportGuard 在本函数返回前（帧回解码器缓冲池之前）完成配平
        const auto key = std::make_tuple(w, h, hor_stride, ver_stride);
        const bool ok = encode_buffer(*session, imported, w, h, hor_stride, ver_stride, quality, out);
        if (!ok) {
            // ⚠️ 直通失败时同步淘汰，防止会话残留未消费的 frame 导致后续错帧
            sessions.erase(key);
        }
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
        ZL_LOGI(0, log_module, "mpp_jpeg_encoder_unavailable: symbols_or_device_missing");
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
        // 栈上会话：无论探针成功与否，离开作用域都会由 Session 析构归还句柄
        Impl::Session probe;
        if (!inner.open_session(probe, 64, 64, align_stride(64), align_stride(64))) {
            ZL_LOGW(0, log_module, "mpp_jpeg_encoder_unavailable: session_probe_failed");
            return nullptr;
        }
    }
    ZL_LOGI(0, log_module, "mpp_jpeg_encoder_ready");
    return encoder;
}

bool MppJpegEncoder::encode_dma_frame(const HardwareFrame &frame, int quality,
                                      std::vector<uint8_t> &out_jpeg) {
    out_jpeg.clear();
    if (frame.dma_fd < 0 || frame.format != FRAME_FORMAT_NV12 || frame.width < 2 ||
        frame.height < 2) {
        return false;
    }
    // ⚠️ stride 必须原样取自帧（见 hardware-backends.md §7.6）：解码器可能给 2048 这类
    //    非 16 对齐宽度的行跨度，绝不可用 align16(width) 顶替；若帧未填合法 stride，直接拒绝直通。
    const int w = static_cast<int>(frame.width) & ~1;
    const int h = static_cast<int>(frame.height) & ~1;
    if (frame.stride < static_cast<uint32_t>(w) ||
        (frame.vstride != 0 && frame.vstride < static_cast<uint32_t>(h))) {
        return false;
    }
    const int hor_stride = static_cast<int>(frame.stride);
    const int ver_stride = frame.vstride != 0 ? static_cast<int>(frame.vstride) : h;

    try {
        std::lock_guard<std::mutex> lock(impl_->mutex);
        return impl_->encode_dma(frame, w, h, hor_stride, ver_stride, quality, out_jpeg);
    } catch (...) {
        // ⚠️ 实现契约（mpp_jpeg_encoder.hpp）：绝不抛异常。out.assign 等堆分配失败
        //    在这里降级为「本帧失败」，由 JpegBackend 回退软件编码；
        //    资源配平由栈上 RAII 守卫完成。
        out_jpeg.clear();
        return false;
    }
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

    try {
        std::lock_guard<std::mutex> lock(impl_->mutex);
        Impl::Session *session = impl_->acquire(rect.w, rect.h, align_stride(rect.w),
                                                align_stride(rect.h));
        if (session == nullptr) {
            return false;
        }
        return impl_->encode_locked(*session, src, rect, quality, out_jpeg);
    } catch (...) {
        // 同 encode_dma_frame：不得让 bad_alloc 穿过本类的公开接口
        out_jpeg.clear();
        return false;
    }
}

bool MppJpegEncoder::self_test() {
    try {
        return self_test_impl();
    } catch (...) {
        // 自检只回答「该 BSP 的硬编路是否可信」；分配失败一律视为不可信
        return false;
    }
}

bool MppJpegEncoder::self_test_impl() {
    // 两张 32x32 合成 NV12：仅亮度不同，色度同为中性 128。
    constexpr int size = 32;
    constexpr size_t luma_bytes = static_cast<size_t>(size) * size;
    std::vector<uint8_t> dark(luma_bytes + luma_bytes / 2, 128);
    std::vector<uint8_t> bright(luma_bytes + luma_bytes / 2, 128);
    std::memset(dark.data(), 16, luma_bytes);
    std::memset(bright.data(), 235, luma_bytes);

    auto make_view = [](std::vector<uint8_t> &storage) {
        CpuFrameView view{};
        view.width = static_cast<uint32_t>(size);
        view.height = static_cast<uint32_t>(size);
        view.format = FRAME_FORMAT_NV12;
        view.planes[0] = storage.data();
        view.planes[1] = storage.data() + luma_bytes;
        view.strides[0] = static_cast<uint32_t>(size);
        return view;
    };

    std::vector<uint8_t> dark_jpeg;
    std::vector<uint8_t> bright_jpeg;
    {
        std::lock_guard<std::mutex> lock(impl_->mutex);
        CpuFrameView dark_view = make_view(dark);
        CpuFrameView bright_view = make_view(bright);
        Rect rect;
        if (!normalize_rect(0, 0, size, size, dark_view.width, dark_view.height, rect)) {
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
    if (!read_jpeg_sof_size(dark_jpeg, w, h) || w != size || h != size) {
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
