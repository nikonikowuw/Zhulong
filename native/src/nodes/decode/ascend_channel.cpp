/**
 * @file ascend_channel.cpp
 * @brief 昇腾 DVPP VDEC 通道实现（唯一包含 CANN 头文件的编译单元）
 *
 * 为什么是「编译期依赖头文件 + 运行期 dlopen 符号」：
 * MPP 后端敢手写 ABI 镜像，是因为 `native/vendor/mpp/include/rockchip/` vendored 了厂商头，
 * 并有 `native/tests/mpp_abi_conformance_test.cpp` 做 static_assert 比对。Ascend 没有可
 * vendored 的公开头，镜像错误会变成板端静默错帧/段错误，因此这里改为：结构体布局与常量由
 * **真头文件**在编译期保证（`ZHULONG_ASCEND_ROOT`），符号解析仍在运行期完成（dlopen），
 * 链接期依旧零依赖。
 *
 * 未定义 `ZHULONG_HAVE_ASCEND` 时本文件退化为桩：`create_vdec_channel()` 返回 nullptr，
 * `ascend_vdec_available()` 恒 false，`DecoderBackend::ascend_vdec` 因此 fail closed。
 */

#include "nodes/decode/ascend_vdec.hpp"
#include "common/log.hpp"

#ifdef ZHULONG_HAVE_ASCEND

#include <acl/acl.h>

#if __has_include(<acl/dvpp/hi_dvpp.h>)
#include <acl/dvpp/hi_dvpp.h>
#elif __has_include(<hi_dvpp.h>)
#include <hi_dvpp.h>
#else
#error "未找到 hi_dvpp.h：请把 ZHULONG_ASCEND_ROOT 指向 CANN 的 include 前缀（含 acl/ 与 hi_dvpp.h）"
#endif

#include <dlfcn.h>
#include <unistd.h>

#include <algorithm>
#include <cstdlib>
#include <limits>
#include <mutex>
#include <new>
#include <stdexcept>
#include <string>
#include <unordered_map>
#include <vector>

namespace zhulong::ascend {

namespace {

constexpr const char *log_module = "nodes.decode";

/** @brief 通道号上限：310P 单卡可创建 256 个 VDEC 通道，本进程远用不到这么多 */
constexpr int32_t max_channels = 128;
/** @brief 同时在途（已取出未归还）的设备帧上限，防调用方漏 release 时把通道缓冲吃干 */
constexpr size_t max_pending_frames = 64;
/** @brief 输入码流设备缓冲的最小容量（1 MiB），避免每帧重新 dvpp_malloc */
constexpr size_t min_input_capacity = 1u << 20;
/** @brief 缺失时间戳的内部哨兵（UINT64_MAX），避免将合法的有效 PTS=0 误判为无时间戳 */
constexpr uint64_t kNoPtsSentinel = UINT64_MAX;

/**
 * @brief 按位比较 hi_dvpp 错误码
 *
 * `HI_ERR_*` 宏在不同 CANN 版本里可能是 `int`、`unsigned int` 或带强制转换的表达式，
 * 直接与 `hi_s32` 比较会在部分版本上触发 -Wsign-compare。统一折成无符号再比。
 */
constexpr bool is_error_code(const hi_s32 code, const hi_u32 expected) noexcept {
    return static_cast<hi_u32>(code) == expected;
}

/**
 * @brief 运行期解析出的符号表
 *
 * 类型全部来自 CANN 头文件，因此签名错误在编译期就会暴露；这里只负责「在哪找到实现」。
 */
struct VdecApi {
    aclError (*acl_init)(const char *) = nullptr;
    aclError (*acl_finalize)() = nullptr;
    aclError (*set_device)(int32_t) = nullptr;
    aclError (*reset_device)(int32_t) = nullptr;
    aclError (*create_context)(aclrtContext *, int32_t) = nullptr;
    aclError (*destroy_context)(aclrtContext) = nullptr;
    aclError (*set_current_context)(aclrtContext) = nullptr;
    aclError (*memcpy_sync)(void *, size_t, const void *, size_t, aclrtMemcpyKind) = nullptr;

    hi_s32 (*sys_init)() = nullptr;
    hi_s32 (*sys_exit)() = nullptr;
    hi_s32 (*create_chn)(hi_s32, const hi_vdec_chn_attr *) = nullptr;
    hi_s32 (*destroy_chn)(hi_s32) = nullptr;
    hi_s32 (*start_recv)(hi_s32) = nullptr;
    hi_s32 (*stop_recv)(hi_s32) = nullptr;
    hi_s32 (*reset_chn)(hi_s32) = nullptr;
    hi_s32 (*send_stream)(hi_s32, const hi_vdec_stream *, const hi_vdec_pic_info *, hi_s32) = nullptr;
    hi_s32 (*get_frame)(hi_s32, hi_video_frame_info *, hi_vdec_stream *, hi_s32) = nullptr;
    hi_s32 (*release_frame)(hi_s32, const hi_video_frame_info *) = nullptr;
    hi_u32 (*pic_buf_size)(hi_payload_type, const hi_pic_buf_attr *) = nullptr;
    hi_u32 (*tmv_buf_size)(hi_payload_type, hi_u32, hi_u32) = nullptr;
    hi_s32 (*dvpp_malloc)(hi_u32, void **, hi_u64) = nullptr;
    hi_s32 (*dvpp_free)(void *) = nullptr;
};

template <typename Fn>
bool resolve(void *handle, const char *name, Fn &slot) noexcept {
    void *symbol = handle != nullptr ? ::dlsym(handle, name) : nullptr;
    if (symbol == nullptr) {
        symbol = ::dlsym(RTLD_DEFAULT, name);
    }
    if (symbol == nullptr) {
        return false;
    }
    slot = reinterpret_cast<Fn>(symbol);
    return true;
}

/**
 * @brief 符号解析单例（永不析构：dlopen 句柄在进程生命周期内保持有效）
 *
 * 与 `mpp_loader.hpp` 同一范式：`alignas` + placement-new + 删除析构函数，
 * 避免静态析构顺序在其它翻译单元的静态对象之后关闭句柄。
 */
class ApiHolder {
public:
    static ApiHolder &instance() noexcept {
        alignas(ApiHolder) static unsigned char storage[sizeof(ApiHolder)];
        static ApiHolder *holder = new (storage) ApiHolder();
        return *holder;
    }

    ~ApiHolder() = delete;

    /** @brief 懒加载；返回 nullptr 表示本机无法提供完整的 VDEC 符号集 */
    const VdecApi *get() {
        std::lock_guard<std::mutex> lock(mutex_);
        if (!attempted_) {
            attempted_ = true;
            load_locked();
        }
        return ready_ ? &api_ : nullptr;
    }

private:
    ApiHolder() = default;

    void load_locked() {
        // 310P 上 DVPP 媒体接口在 libacl_dvpp.so；AscendCL 运行库在 libascendcl.so。
        // 逐个 dlopen 并保留句柄（不 dlclose）：后续 dlsym 会跨候选库查找。
        static constexpr const char *candidates[] = {
            "libacl_dvpp.so", "libhi_dvpp.so", "libascendcl.so", "libdvpp_utils.so",
        };
        for (const char *name : candidates) {
            if (void *handle = ::dlopen(name, RTLD_NOW | RTLD_GLOBAL)) {
                handles_.push_back(handle);
            }
        }

        VdecApi api{};
        bool ok = true;
        auto bind = [&](const char *name, auto &slot) {
            bool found = false;
            for (void *handle : handles_) {
                if (resolve(handle, name, slot)) {
                    found = true;
                    break;
                }
            }
            if (!found) {
                found = resolve(nullptr, name, slot);
            }
            ok = found && ok;
        };

        bind("aclInit", api.acl_init);
        bind("aclFinalize", api.acl_finalize);
        bind("aclrtSetDevice", api.set_device);
        bind("aclrtResetDevice", api.reset_device);
        bind("aclrtCreateContext", api.create_context);
        bind("aclrtDestroyContext", api.destroy_context);
        bind("aclrtSetCurrentContext", api.set_current_context);
        bind("aclrtMemcpy", api.memcpy_sync);
        bind("hi_mpi_sys_init", api.sys_init);
        bind("hi_mpi_sys_exit", api.sys_exit);
        bind("hi_mpi_vdec_create_chn", api.create_chn);
        bind("hi_mpi_vdec_destroy_chn", api.destroy_chn);
        bind("hi_mpi_vdec_start_recv_stream", api.start_recv);
        bind("hi_mpi_vdec_stop_recv_stream", api.stop_recv);
        bind("hi_mpi_vdec_reset_chn", api.reset_chn);
        bind("hi_mpi_vdec_send_stream", api.send_stream);
        bind("hi_mpi_vdec_get_frame", api.get_frame);
        bind("hi_mpi_vdec_release_frame", api.release_frame);
        bind("hi_vdec_get_pic_buf_size", api.pic_buf_size);
        bind("hi_vdec_get_tmv_buf_size", api.tmv_buf_size);
        bind("hi_mpi_dvpp_malloc", api.dvpp_malloc);
        bind("hi_mpi_dvpp_free", api.dvpp_free);

        if (!ok) {
            // 不 dlclose：句柄可能已被其它翻译单元静态持有；失败即整体 fail closed
            api_ = VdecApi{};
            ready_ = false;
            return;
        }
        api_ = api;
        ready_ = true;
    }

    std::mutex mutex_;
    bool attempted_{false};
    bool ready_{false};
    VdecApi api_{};
    std::vector<void *> handles_;
};

/** @brief 进程级 ACL 运行时（aclInit / set_device / hi_mpi_sys_init 都是进程级且幂等性差） */
class Runtime {
public:
    static Runtime &instance() noexcept {
        alignas(Runtime) static unsigned char storage[sizeof(Runtime)];
        static Runtime *runtime = new (storage) Runtime();
        return *runtime;
    }

    ~Runtime() = delete;

    bool acquire() {
        std::lock_guard<std::mutex> lock(mutex_);
        if (refs_ > 0) {
            ++refs_;
            return true;
        }
        if (!ready_ && !init_locked()) {
            return false;
        }
        refs_ = 1;
        return true;
    }

    void release() {
        std::lock_guard<std::mutex> lock(mutex_);
        if (refs_ > 0) {
            --refs_;
        }
        // ⚠️ 进程级 ACL 运行时（aclInit / hi_mpi_sys_init）保持常驻：
        // CANN 架构下 aclInit/aclFinalize 对应进程生命周期；同进程内频繁 finalize
        // 并再次 init 会触发 ACL_ERROR_REPEAT_INITIALIZE 或内部上下文混乱。
        // 通道级资源（Channel / aclrtContext）已在各通道的 teardown() 中完整销毁。
    }

    [[nodiscard]] int32_t device_id() const noexcept { return device_id_; }

private:
    Runtime() = default;

    bool init_locked() {
        const VdecApi *api = ApiHolder::instance().get();
        if (api == nullptr) {
            return false;
        }
        api_ = api;

        // 多卡场景由 ASCEND_RT_VISIBLE_DEVICES 决定可见性，这里只选逻辑设备号
        if (const char *raw = std::getenv("ZHULONG_ASCEND_DEVICE")) {
            char *end = nullptr;
            const long parsed = std::strtol(raw, &end, 10);
            if (end != raw && parsed >= 0 && parsed < 64) {
                device_id_ = static_cast<int32_t>(parsed);
            }
        }

        aclError error = api_->acl_init(nullptr);
        // 重复初始化不是错误：同一进程内可能有其它组件已经调用过
        if (error != ACL_SUCCESS && error != ACL_ERROR_REPEAT_INITIALIZE) {
            ZL_LOGE(0, log_module, "ascend_acl_init_failed error=%d", static_cast<int>(error));
            return false;
        }
        error = api_->set_device(device_id_);
        if (error != ACL_SUCCESS) {
            ZL_LOGE(0, log_module, "ascend_set_device_failed device=%d error=%d", device_id_,
                    static_cast<int>(error));
            api_->acl_finalize();
            return false;
        }
        if (api_->sys_init() != HI_SUCCESS) {
            ZL_LOGE(0, log_module, "ascend_sys_init_failed device=%d", device_id_);
            api_->reset_device(device_id_);
            api_->acl_finalize();
            return false;
        }
        ready_ = true;
        return true;
    }

    void teardown_locked() {
        if (!ready_ || api_ == nullptr) {
            return;
        }
        api_->sys_exit();
        api_->reset_device(device_id_);
        api_->acl_finalize();
        ready_ = false;
    }

    std::mutex mutex_;
    int refs_{0};
    bool ready_{false};
    int32_t device_id_{0};
    const VdecApi *api_{nullptr};
};

/** @brief 通道号分配器（hi_mpi_vdec 通道号是全局资源，必须显式回收） */
class ChannelIds {
public:
    static ChannelIds &instance() noexcept {
        alignas(ChannelIds) static unsigned char storage[sizeof(ChannelIds)];
        static ChannelIds *ids = new (storage) ChannelIds();
        return *ids;
    }

    ~ChannelIds() = delete;

    int32_t allocate() {
        std::lock_guard<std::mutex> lock(mutex_);
        for (size_t index = 0; index < used_.size(); ++index) {
            if (!used_[index]) {
                used_[index] = true;
                return static_cast<int32_t>(index);
            }
        }
        if (used_.size() >= static_cast<size_t>(max_channels)) {
            return -1;
        }
        used_.push_back(true);
        return static_cast<int32_t>(used_.size() - 1);
    }

    void release(int32_t id) {
        std::lock_guard<std::mutex> lock(mutex_);
        if (id >= 0 && static_cast<size_t>(id) < used_.size()) {
            used_[static_cast<size_t>(id)] = false;
        }
    }

private:
    ChannelIds() = default;

    std::mutex mutex_;
    std::vector<bool> used_;
};

[[nodiscard]] bool device_present() noexcept {
    return ::access("/dev/davinci_manager", F_OK) == 0 || ::access("/dev/davinci0", F_OK) == 0;
}

[[nodiscard]] hi_payload_type payload_type_for(VdecCodec codec) noexcept {
    return codec == VdecCodec::h265 ? HI_PT_H265 : HI_PT_H264;
}

/**
 * @brief 真实通道
 *
 * ⚠️ 只能在创建它的线程上使用：ACL context 是「线程当前态」，不是可传递的句柄。
 */
class CannVdecChannel final : public IVdecChannel {
public:
    CannVdecChannel(const VdecChannelConfig &config, const VdecApi *api);
    ~CannVdecChannel() override;

    int send(const uint8_t *data, size_t size, int64_t pts, int64_t dts) override;
    int send_eos() override;
    int receive(DecodedFrame &out) override;
    void release(void *dev_ptr) noexcept override;
    void reset() override;
    int copy_to_host(void *host, const void *dev, size_t size) override;
    [[nodiscard]] bool healthy() const noexcept override { return healthy_; }

private:
    bool setup();
    void teardown() noexcept;
    bool ensure_input_buffer(size_t size);
    void release_pending() noexcept;

    const VdecApi *api_{nullptr};
    VdecChannelConfig config_{};
    VdecGeometry geometry_{};
    aclrtContext context_{nullptr};
    int32_t chn_{-1};
    void *input_{nullptr};
    size_t input_capacity_{0};
    std::unordered_map<void *, hi_video_frame_info> pending_;
    bool healthy_{false};
    bool runtime_held_{false};
};

CannVdecChannel::CannVdecChannel(const VdecChannelConfig &config, const VdecApi *api)
    : api_(api), config_(config), geometry_(compute_vdec_geometry(config.width, config.height)) {
    if (api_ == nullptr) {
        throw std::runtime_error("ascend vdec: null api table");
    }
    if (geometry_.buffer_size == 0) {
        throw std::invalid_argument("ascend vdec: invalid geometry");
    }
    if (!Runtime::instance().acquire()) {
        throw std::runtime_error("ascend vdec: runtime init failed");
    }
    runtime_held_ = true;

    // ⚠️ 构造抛异常时析构函数不会执行，必须自己回滚（否则 ACL 运行时引用计数泄漏，
    //    后续所有通道都会因为 refs_ 永不归零而无法重新初始化）
    try {
        const aclError error = api_->create_context(&context_, Runtime::instance().device_id());
        if (error != ACL_SUCCESS) {
            ZL_LOGE(config_.stream_id, log_module, "ascend_create_context_failed error=%d",
                    static_cast<int>(error));
            throw std::runtime_error("ascend vdec: create context failed");
        }
        // 本线程后续所有 DVPP 调用都归属这个 context；设不进去就说明 context 不可用，
        // 继续跑只会让每个 DVPP 调用返回含义模糊的错码。
        const aclError bind_error = api_->set_current_context(context_);
        if (bind_error != ACL_SUCCESS) {
            ZL_LOGE(config_.stream_id, log_module, "ascend_set_current_context_failed error=%d",
                    static_cast<int>(bind_error));
            throw std::runtime_error("ascend vdec: set current context failed");
        }

        if (!setup()) {
            throw std::runtime_error("ascend vdec: channel setup failed");
        }
    } catch (...) {
        teardown();
        throw;
    }
}

CannVdecChannel::~CannVdecChannel() { teardown(); }

bool CannVdecChannel::setup() {
    chn_ = ChannelIds::instance().allocate();
    if (chn_ < 0) {
        ZL_LOGE(config_.stream_id, log_module, "ascend_vdec_channel_exhausted");
        return false;
    }

    const hi_payload_type type = payload_type_for(config_.codec);
    hi_vdec_chn_attr attr{};
    attr.type = type;
    attr.mode = HI_VDEC_SEND_MODE_FRAME;
    attr.pic_width = config_.width;
    attr.pic_height = config_.height;
    attr.stream_buf_size = static_cast<hi_u32>(config_.width * config_.height * 3 / 2);
    // 参考帧 + 显示帧 + 1 个在途帧：与官方样例一致
    attr.frame_buf_cnt = config_.ref_frame_num + config_.display_frame_num + 1;

    // ⚠️ 用聚合初始化而不是逐字段赋值：字段名随 CANN 版本有过调整，顺序是样例里固定的顺序
    const hi_pic_buf_attr buf_attr{config_.width, config_.height, 0, HI_DATA_BIT_WIDTH_8,
                                   HI_PIXEL_FORMAT_YUV_SEMIPLANAR_420, HI_COMPRESS_MODE_NONE};
    attr.frame_buf_size = api_->pic_buf_size(type, &buf_attr);

    attr.video_attr.ref_frame_num = config_.ref_frame_num;
    attr.video_attr.temporal_mvp_en = HI_TRUE;
    attr.video_attr.tmv_buf_size = api_->tmv_buf_size(type, config_.width, config_.height);

    hi_s32 result = api_->create_chn(chn_, &attr);
    if (result != HI_SUCCESS) {
        ZL_LOGE(config_.stream_id, log_module, "ascend_vdec_create_chn_failed code=%d w=%u h=%u",
                static_cast<int>(result), config_.width, config_.height);
        ChannelIds::instance().release(chn_);
        chn_ = -1;
        return false;
    }
    result = api_->start_recv(chn_);
    if (result != HI_SUCCESS) {
        ZL_LOGE(config_.stream_id, log_module, "ascend_vdec_start_recv_failed code=%d", static_cast<int>(result));
        (void)api_->destroy_chn(chn_);
        ChannelIds::instance().release(chn_);
        chn_ = -1;
        return false;
    }
    healthy_ = true;
    return true;
}

void CannVdecChannel::teardown() noexcept {
    release_pending();
    if (chn_ >= 0 && api_ != nullptr) {
        (void)api_->stop_recv(chn_);
        (void)api_->destroy_chn(chn_);
        ChannelIds::instance().release(chn_);
        chn_ = -1;
    }
    if (input_ != nullptr && api_ != nullptr) {
        (void)api_->dvpp_free(input_);
        input_ = nullptr;
        input_capacity_ = 0;
    }
    if (context_ != nullptr && api_ != nullptr) {
        (void)api_->destroy_context(context_);
        context_ = nullptr;
    }
    if (runtime_held_) {
        Runtime::instance().release();
        runtime_held_ = false;
    }
    healthy_ = false;
}

void CannVdecChannel::release_pending() noexcept {
    if (api_ == nullptr || chn_ < 0) {
        pending_.clear();
        return;
    }
    for (auto &entry : pending_) {
        (void)api_->release_frame(chn_, &entry.second);
    }
    pending_.clear();
}

bool CannVdecChannel::ensure_input_buffer(size_t size) {
    if (input_ != nullptr && input_capacity_ >= size) {
        return true;
    }
    if (input_ != nullptr) {
        (void)api_->dvpp_free(input_);
        input_ = nullptr;
        input_capacity_ = 0;
    }
    const size_t capacity = std::max<size_t>(size, min_input_capacity);
    if (capacity > std::numeric_limits<hi_u32>::max()) {
        return false;
    }
    void *buffer = nullptr;
    if (api_->dvpp_malloc(static_cast<hi_u32>(Runtime::instance().device_id()), &buffer,
                          static_cast<hi_u64>(capacity)) != HI_SUCCESS) {
        ZL_LOGE(config_.stream_id, log_module, "ascend_dvpp_malloc_failed size=%zu", capacity);
        return false;
    }
    input_ = buffer;
    input_capacity_ = capacity;
    return true;
}

int CannVdecChannel::send(const uint8_t *data, size_t size, int64_t pts, int64_t dts) {
    (void)dts;
    if (!healthy_ || data == nullptr || size == 0) {
        return decode_backend_failure;
    }
    if (size > std::numeric_limits<hi_u32>::max() || !ensure_input_buffer(size)) {
        return decode_backend_failure;
    }
    // 码流必须先落到设备内存：hi_mpi_vdec_send_stream 只接受设备地址
    const aclError copy_error = api_->memcpy_sync(input_, input_capacity_, data, size, ACL_MEMCPY_HOST_TO_DEVICE);
    if (copy_error != ACL_SUCCESS) {
        ZL_LOGE(config_.stream_id, log_module, "ascend_h2d_failed size=%zu error=%d", size, static_cast<int>(copy_error));
        healthy_ = false;
        return decode_backend_failure;
    }

    hi_vdec_stream stream{};
    stream.addr = static_cast<hi_u8 *>(input_);
    stream.len = static_cast<hi_u32>(size);
    // 约定用 kNoPtsSentinel 标记缺失 PTS (AV_NOPTS_VALUE)；0 是合法的有效首帧时间戳
    const bool has_valid_pts = (pts != std::numeric_limits<int64_t>::min() &&
                                static_cast<uint64_t>(pts) != kNoPtsSentinel);
    stream.pts = static_cast<decltype(stream.pts)>(has_valid_pts ? static_cast<uint64_t>(pts) : kNoPtsSentinel);
    stream.end_of_stream = HI_FALSE;

    hi_vdec_pic_info pic{};
    pic.width = geometry_.width;
    pic.height = geometry_.height;
    pic.width_stride = geometry_.width_stride;
    pic.height_stride = geometry_.height_stride;
    pic.buffer_size = static_cast<hi_u32>(geometry_.buffer_size);

    const hi_s32 result = api_->send_stream(chn_, &stream, &pic, 0);
    if (result == HI_SUCCESS) {
        return decode_ok;
    }
    if (is_error_code(result, HI_ERR_VDEC_BUF_FULL)) {
        return decode_again;
    }
    ZL_LOGE(config_.stream_id, log_module, "ascend_vdec_send_stream_failed code=%d", static_cast<int>(result));
    healthy_ = false;
    return decode_backend_failure;
}

int CannVdecChannel::send_eos() {
    if (!healthy_) {
        return decode_backend_failure;
    }
    hi_vdec_stream stream{};
    stream.addr = nullptr;
    stream.len = 0;
    stream.pts = 0;
    stream.end_of_stream = HI_TRUE;

    // 非绑定模式下按帧发送必须提供合法 pic_info，避免部分 CANN 版本抛 HI_ERR_VDEC_ILLEGAL_PARAM
    hi_vdec_pic_info pic{};
    pic.width = geometry_.width;
    pic.height = geometry_.height;
    pic.width_stride = geometry_.width_stride;
    pic.height_stride = geometry_.height_stride;
    pic.buffer_size = static_cast<hi_u32>(geometry_.buffer_size);

    const hi_s32 result = api_->send_stream(chn_, &stream, &pic, 0);
    if (result == HI_SUCCESS) {
        return decode_ok;
    }
    if (is_error_code(result, HI_ERR_VDEC_BUF_FULL)) {
        return decode_again;
    }
    ZL_LOGE(config_.stream_id, log_module, "ascend_vdec_send_eos_failed code=%d", static_cast<int>(result));
    healthy_ = false;
    return decode_backend_failure;
}

int CannVdecChannel::receive(DecodedFrame &out) {
    if (!healthy_) {
        return decode_backend_failure;
    }
    hi_video_frame_info info{};
    hi_vdec_stream stream{};
    const hi_s32 result = api_->get_frame(chn_, &info, &stream, 0);
    if (is_error_code(result, HI_ERR_VDEC_BUF_EMPTY)) {
        return decode_again;
    }
    if (result != HI_SUCCESS) {
        ZL_LOGE(config_.stream_id, log_module, "ascend_vdec_get_frame_failed code=%d", static_cast<int>(result));
        healthy_ = false;
        return decode_backend_failure;
    }

    void *const device = info.v_frame.virt_addr[0];
    if (device == nullptr) {
        // 取帧成功却没有设备地址：属于驱动异常，不能当成「暂时没帧」让上层空转
        (void)api_->release_frame(chn_, &info);
        ZL_LOGE(config_.stream_id, log_module, "ascend_vdec_null_frame_addr");
        healthy_ = false;
        return decode_backend_failure;
    }
    if (pending_.size() >= max_pending_frames) {
        // 调用方漏了 release：立刻归还并让上层稍后重试，绝不无限增长
        (void)api_->release_frame(chn_, &info);
        ZL_LOGE(config_.stream_id, log_module, "ascend_vdec_pending_overflow count=%zu", pending_.size());
        return decode_again;
    }

    // 几何以驱动实际产出为准：驱动可能选择更大的对齐粒度
    out.dev_ptr = device;
    out.geometry.width = info.v_frame.width;
    out.geometry.height = info.v_frame.height;
    out.geometry.width_stride = info.v_frame.width_stride[0];
    out.geometry.height_stride = info.v_frame.height_stride[0];
    out.geometry.buffer_size =
        static_cast<size_t>(out.geometry.width_stride) * out.geometry.height_stride * 3 / 2;
    out.has_pts = (static_cast<uint64_t>(stream.pts) != kNoPtsSentinel);
    out.pts = out.has_pts ? static_cast<int64_t>(stream.pts) : 0;

    pending_.emplace(device, info);
    return decode_ok;
}
}

void CannVdecChannel::release(void *dev_ptr) noexcept {
    if (dev_ptr == nullptr) {
        return;
    }
    const auto entry = pending_.find(dev_ptr);
    if (entry == pending_.end()) {
        return;
    }
    if (api_ != nullptr && chn_ >= 0) {
        (void)api_->release_frame(chn_, &entry->second);
    }
    pending_.erase(entry);
}

void CannVdecChannel::reset() {
    // flush 同时承担「从传输错误里恢复」的职责，所以不看 healthy_：
    // 只要通道还在，就停收 -> 复位 -> 重收，成功即重新可用。
    if (chn_ < 0 || api_ == nullptr) {
        return;
    }
    release_pending();
    (void)api_->stop_recv(chn_);
    (void)api_->reset_chn(chn_);
    if (api_->start_recv(chn_) != HI_SUCCESS) {
        ZL_LOGE(config_.stream_id, log_module, "ascend_vdec_restart_failed");
        healthy_ = false;
        return;
    }
    healthy_ = true;
}

int CannVdecChannel::copy_to_host(void *host, const void *dev, size_t size) {
    if (host == nullptr || dev == nullptr || size == 0) {
        return decode_backend_failure;
    }
    const aclError error = api_->memcpy_sync(host, size, dev, size, ACL_MEMCPY_DEVICE_TO_HOST);
    if (error != ACL_SUCCESS) {
        return decode_backend_failure;
    }
    return decode_ok;
}

} // namespace

bool ascend_vdec_compiled() noexcept { return true; }

bool ascend_vdec_available() noexcept {
    if (!device_present()) {
        return false;
    }
    return ApiHolder::instance().get() != nullptr;
}

IVdecChannel::Ptr create_vdec_channel(const VdecChannelConfig &config) {
    const VdecApi *api = ApiHolder::instance().get();
    if (api == nullptr) {
        ZL_LOGE(config.stream_id, log_module, "ascend_vdec_symbols_missing");
        return nullptr;
    }
    try {
        return std::make_unique<CannVdecChannel>(config, api);
    } catch (const std::exception &error) {
        ZL_LOGE(config.stream_id, log_module, "ascend_vdec_channel_init_failed reason=%s", error.what());
        return nullptr;
    }
}

} // namespace zhulong::ascend

#else // !ZHULONG_HAVE_ASCEND

namespace zhulong::ascend {

bool ascend_vdec_compiled() noexcept { return false; }

bool ascend_vdec_available() noexcept { return false; }

IVdecChannel::Ptr create_vdec_channel(const VdecChannelConfig &) { return nullptr; }

} // namespace zhulong::ascend

#endif // ZHULONG_HAVE_ASCEND
