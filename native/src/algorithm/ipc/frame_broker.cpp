/**
 * @file frame_broker.cpp
 * @brief 共享内存帧分发宿主实现、多平面安全复制与代次防 ABA 租约回收
 */

#include "frame_broker.hpp"
#include "broker_resource_lock.hpp"
#include <algorithm>
#include <cstring>
#include <cstdio>
#include <cassert>
#include <limits>
#include "common/log.hpp"

namespace zhulong::ipc {

namespace {

bool checked_mul(size_t a, size_t b, size_t& out) noexcept {
    if (b != 0 && a > std::numeric_limits<size_t>::max() / b) {
        return false;
    }
    out = a * b;
    return true;
}

bool checked_add(size_t a, size_t b, size_t& out) noexcept {
    if (a > std::numeric_limits<size_t>::max() - b) {
        return false;
    }
    out = a + b;
    return true;
}

/**
 * @brief 计算给定帧写入单个共享内存槽位所需的字节数。
 *
 * 与 publish_frame 中各格式分支的 memcpy 布局严格一一对应：
 *  - NV12    : Y 平面（stride × (vstride > 0 ? vstride : height)）+ UV 交错平面
 *  - YUV420P : Y 平面 + U 平面 + V 平面（U/V 行跨度 = (stride + 1) / 2）
 *  - 其他    : 单平面 stride × height（BGR24 等打包格式）
 *
 * ⚠️ NV12 用 y_plane_size()（含垂直对齐跨度）而 YUV420P 用 stride × height，
 *    两者语义不同，**不可统一** —— 统一会改变既有测试固化的字节数。
 *
 * @return 所需字节数；0 表示尺寸无法表示或溢出（调用方必须据此拒绝发布）
 */
size_t frame_payload_bytes(const HardwareFrame& frame) {
    if (frame.width == 0 || frame.height == 0 || frame.stride == 0) {
        return 0;
    }
    switch (frame.format) {
        case FRAME_FORMAT_NV12: {
            const uint32_t eff_vstride = (frame.vstride > 0) ? std::max(frame.vstride, frame.height) : frame.height;
            size_t y_bytes = 0;
            size_t uv_bytes = 0;
            size_t total = 0;
            const size_t uv_rows = (static_cast<size_t>(frame.height) + 1) / 2;
            if (!checked_mul(frame.stride, eff_vstride, y_bytes) ||
                !checked_mul(frame.stride, uv_rows, uv_bytes) ||
                !checked_add(y_bytes, uv_bytes, total)) {
                return 0;
            }
            return total;
        }
        case FRAME_FORMAT_YUV420P: {
            const size_t u_stride = (static_cast<size_t>(frame.stride) + 1) / 2;
            const size_t uv_rows = (static_cast<size_t>(frame.height) + 1) / 2;
            size_t y_size = 0;
            size_t u_size = 0;
            size_t uv_total = 0;
            size_t total = 0;
            if (!checked_mul(frame.stride, frame.height, y_size) ||
                !checked_mul(u_stride, uv_rows, u_size) ||
                !checked_mul(u_size, 2, uv_total) ||
                !checked_add(y_size, uv_total, total)) {
                return 0;
            }
            return total;
        }
        default: {
            size_t total = 0;
            if (!checked_mul(frame.stride, frame.height, total)) {
                return 0;
            }
            return total;
        }
    }
}

} // namespace

FrameBroker::FrameBroker() = default;

FrameBroker::~FrameBroker() {
    stop();
}

bool FrameBroker::start(uint64_t stream_id, const std::string& base_dir,
                        BrokerOptions options, std::string* error_msg) {
    stop();
    if (options.slot_count == 0 || options.slot_size == 0 || options.max_in_flight_per_client == 0) {
        if (error_msg) *error_msg = "invalid broker options: slot_count, slot_size, and max_in_flight must be positive";
        return false;
    }
    stream_id_ = stream_id;
    options_ = options;

    shm_name_ = "/zhulong_shm_" + std::to_string(stream_id_);
    socket_path_ = base_dir + "/zhulong_broker_" + std::to_string(stream_id_) + ".sock";

    // 0. 与宿主启动清扫互斥
    //
    // 宿主（internal/algorunner）在每次启动时清扫上个进程进程遗留的 shm 段与 broker socket。
    // 它判断「无人使用」的依据是 /proc，而本函数创建的两类资源都存在「文件已存在、但尚不可用」
    // 的窗口：shm_open 之后才 mmap，bind 之后才 listen。落在窗口里的清扫会删掉活 broker 的资源。
    // 因此创建侧必须阻塞持有同一批目录锁（Go 侧 resource_lock_linux.go 为非阻塞）。
    //
    // ⚠️ 锁必须覆盖 shm 初始化与 socket bind/listen 全程，提前释放等于没有锁。
    //
    // 目录集合由 BrokerResourceLock::resource_dirs 集中定义：两侧必须锁同一批对象，
    // 而 shm 目录只在存在可枚举挂载点的平台上纳入。
    std::string lock_error;
    auto resource_lock = BrokerResourceLock::acquire(
        BrokerResourceLock::resource_dirs(base_dir), &lock_error);
    if (!resource_lock) {
        if (error_msg) *error_msg = "broker resource lock failed: " + lock_error;
        return false;
    }

    // 1. 初始化共享内存池 (主写模式)
    if (!shm_pool_.init(shm_name_, ShmMode::CREATE_READ_WRITE, options_.slot_count, options_.slot_size, error_msg)) {
        return false;
    }

    // 1.1 尝试探测并初始化硬件 DMA-BUF 槽位池
    //
    // ⚠️ 这是**允许降级但绝不允许静默**的一步：槽位池是 slot_count × slot_size
    //    （默认 8 × 4MB = 32MB）一次性向 CMA dma_heap 申请的。RK3568 实测
    //    CmaTotal≈16MB 且已被占用，必然分配失败 → 退回纯共享内存路径（帧要多一次
    //    拷贝），功能不受影响但性能/时延特性变了。没有这行日志，板端排查会误以为
    //    自己在零拷贝路径上跑（reference: hardware-backends §MPP/零拷贝）。
    dma_slots_.clear();
    has_dma_slots_ = false;
    auto& dma_allocator = zhulong::platform::DmaBufAllocator::instance();
    if (dma_allocator.is_supported()) {
        bool dma_ok = true;
        size_t allocated = 0;
        for (size_t i = 0; i < options_.slot_count; ++i) {
            auto buf = dma_allocator.allocate(options_.slot_size);
            if (!buf) {
                dma_ok = false;
                break;
            }
            dma_slots_.push_back(std::move(buf));
            ++allocated;
        }
        if (dma_ok && dma_slots_.size() == options_.slot_count) {
            has_dma_slots_ = true;
            ZL_LOGI(0, "ipc.broker", "dma-buf slot pool ready: allocator=%s slots=%zu slot_size=%zu",
                    dma_allocator.type_name(), dma_slots_.size(), options_.slot_size);
        } else {
            ZL_LOGW(0, "ipc.broker",
                    "dma-buf slot pool unavailable (%zu/%zu slots of %zu bytes allocated): %s; "
                    "falling back to shared-memory frames (extra copy per frame)",
                    allocated, options_.slot_count, options_.slot_size,
                    dma_allocator.last_error().c_str());
            dma_slots_.clear();
            has_dma_slots_ = false;
        }
    } else {
        ZL_LOGI(0, "ipc.broker", "dma-buf allocator unsupported on this host; shared-memory frames only");
    }

    // 2. 初始化槽位状态
    {
        std::lock_guard<std::mutex> lock(mutex_);
        slots_.resize(options_.slot_count);
        for (size_t i = 0; i < options_.slot_count; ++i) {
            slots_[i].slot_index = static_cast<uint32_t>(i);
            slots_[i].generation = 1;
            slots_[i].state = SlotState::FREE;
            slots_[i].active_leases.clear();
        }
        clients_.clear();
        task_config_payloads_.clear();
    }

    // 3. 配置并启动 Unix Domain Socket 服务端
    server_.set_on_message([this](uint32_t client_id, const IpcHeader& header, const uint8_t* payload) {
        handle_client_message(client_id, header, payload);
    });

    server_.set_on_disconnect([this](uint32_t client_id) {
        handle_client_disconnect(client_id);
    });

    if (!server_.start(socket_path_, error_msg)) {
        shm_pool_.close();
        {
            std::lock_guard<std::mutex> lock(mutex_);
            slots_.clear();
            clients_.clear();
            task_config_payloads_.clear();
        }
        dma_slots_.clear();
        has_dma_slots_ = false;
        return false;
    }

    {
        std::lock_guard<std::mutex> lk(publish_mutex_);
        running_.store(true, std::memory_order_release);
    }
    return true;
}

void FrameBroker::stop() {
    // 在 publish_mutex_ 下原子切换 running_ 并等待在途 publish_frame 全部退出，
    // 消除无锁双原子变量在弱序内存模型下的 Dekker 竞态，杜绝与 shm_pool_.close() 并发。
    {
        std::unique_lock<std::mutex> lk(publish_mutex_);
        if (!running_.exchange(false, std::memory_order_acq_rel)) {
            return;
        }
        publish_cv_.wait_for(lk, std::chrono::seconds(2), [this] {
            return publishing_count_.load(std::memory_order_relaxed) == 0;
        });
    }

    server_.stop();
    shm_pool_.close();

    std::lock_guard<std::mutex> lock(mutex_);
    slots_.clear();
    clients_.clear();
    task_config_payloads_.clear();
    dma_slots_.clear();
    has_dma_slots_ = false;
}

int FrameBroker::find_or_evict_free_slot(std::unique_lock<std::mutex>& lock) {
    assert(lock.owns_lock());
    // 1. 查找空闲槽位
    for (size_t i = 0; i < slots_.size(); ++i) {
        if (slots_[i].state == SlotState::FREE) {
            return static_cast<int>(i);
        }
    }

    // 2. 无空闲槽位，检查是否有超时槽位可驱逐
    auto now = std::chrono::steady_clock::now();
    int oldest_idx = -1;
    auto oldest_time = now;

    for (size_t i = 0; i < slots_.size(); ++i) {
        if (slots_[i].state == SlotState::LEASED) {
            auto elapsed = std::chrono::duration_cast<std::chrono::milliseconds>(now - slots_[i].dispatch_time);
            if (elapsed >= options_.lease_timeout) {
                if (oldest_idx == -1 || slots_[i].dispatch_time < oldest_time) {
                    oldest_idx = static_cast<int>(i);
                    oldest_time = slots_[i].dispatch_time;
                }
            }
        }
    }

    if (oldest_idx >= 0) {
        // 强制回收超时槽位：扣减在途计数并自增代次，使迟到的历史 ACK 失效
        auto& slot = slots_[oldest_idx];
        for (uint32_t cid : slot.active_leases) {
            auto it = clients_.find(cid);
            if (it != clients_.end() && it->second.in_flight_count > 0) {
                it->second.in_flight_count--;
            }
        }
        slot.active_leases.clear();
        slot.generation++;
        slot.state = SlotState::FREE;
        return oldest_idx;
    }

    return -1; // 全部繁忙且未超时
}

bool FrameBroker::publish_frame(const HardwareFrame& frame, const FrameMotionInfo& motion) {
    return publish_frame(frame, ViewWindow{}, motion, CropCopyFn{}, 0);
}

bool FrameBroker::publish_frame(const HardwareFrame& frame, const ViewWindow& view,
                                const FrameMotionInfo& motion, const CropCopyFn& crop,
                                int64_t capture_time_ms) {
    struct PublishGuard {
        std::atomic<uint32_t>& count;
        std::mutex& mtx;
        std::condition_variable& cv;
        bool active{false};
        ~PublishGuard() {
            if (active) {
                std::lock_guard<std::mutex> lk(mtx);
                count.fetch_sub(1, std::memory_order_relaxed);
                cv.notify_all();
            }
        }
    } guard{publishing_count_, publish_mutex_, publish_cv_};

    {
        std::lock_guard<std::mutex> lk(publish_mutex_);
        if (!running_.load(std::memory_order_relaxed)) {
            return false;
        }
        publishing_count_.fetch_add(1, std::memory_order_relaxed);
        guard.active = true;
    }

    int slot_idx = -1;
    uint64_t generation = 0;
    std::vector<uint32_t> target_clients;

    {
        std::unique_lock<std::mutex> lock(mutex_);
        slot_idx = find_or_evict_free_slot(lock);
        if (slot_idx < 0) {
            total_dropped_.fetch_add(1);
            return false;
        }

        auto& slot = slots_[slot_idx];
        slot.state = SlotState::WRITING;
        slot.generation++;
        generation = slot.generation;
        slot.dispatch_time = std::chrono::steady_clock::now();

        // 挑选接收该帧的合格客户端（慢消费者背压隔离：在途达上限则直接丢弃最新帧）
        for (auto& [cid, client] : clients_) {
            if (client.in_flight_count < options_.max_in_flight_per_client) {
                target_clients.push_back(cid);
                client.in_flight_count++;
                client.delivered_frames++;
                slot.active_leases.insert(cid);
            } else {
                client.dropped_frames++;
            }
        }
    }

    // 如果没有任何客户端订阅/合格，直接释放槽位
    if (target_clients.empty()) {
        std::lock_guard<std::mutex> lock(mutex_);
        slots_[slot_idx].state = SlotState::FREE;
        slots_[slot_idx].active_leases.clear();
        return true;
    }

    // ⚠️ 零拷贝分叉只允许在“不裁剪”时直通：窗口尺寸绝不能配全幅 fd 下发。
    const bool windowed = view.active;
    const bool is_zero_copy_frame = (frame.dma_fd >= 0) && !windowed;

    uint8_t* slot_data = nullptr;     // shm 槽位目标
    uint8_t* dma_slot_data = nullptr; // DMA 槽位目标
    int dma_fd_to_send = -1;

    if (is_zero_copy_frame) {
        // 全幅零拷贝直通：复用源帧 fd，绕过拷贝与槽位 CPU 映射。
        dma_fd_to_send = frame.dma_fd;
    } else if (windowed) {
        // 窗口物化：只写一个目标，绝不“先写 shm 再抄 dma”；若 DMA 槽位未映射则回退 SHM。
        if (has_dma_slots_) {
            void* mapped = dma_slots_[slot_idx]->mmap_addr();
            if (mapped != nullptr) {
                dma_slot_data = static_cast<uint8_t*>(mapped);
                dma_fd_to_send = dma_slots_[slot_idx]->fd();
            } else {
                slot_data = shm_pool_.slot_write_ptr(slot_idx);
            }
        } else {
            slot_data = shm_pool_.slot_write_ptr(slot_idx);
        }
    } else {
        slot_data = shm_pool_.slot_write_ptr(slot_idx);
        if (has_dma_slots_) {
            void* mapped = dma_slots_[slot_idx]->mmap_addr();
            if (mapped != nullptr) {
                dma_slot_data = static_cast<uint8_t*>(mapped);
                dma_fd_to_send = dma_slots_[slot_idx]->fd();
            }
        }
    }

    if (!is_zero_copy_frame && !slot_data && !dma_slot_data) {
        // 槽位获取失败：统一回滚在途配额与槽位状态
        return fail_publish(slot_idx, target_clients);
    }

    // ⚠️ 容量与源数据前置门禁：必须在任何 memcpy 之前完成，杜绝「部分写入 + 静默发旧帧」。
    //    零拷贝帧（frame.dma_fd >= 0 且未窗口化）不占槽位、不拷贝，因此不受此门禁约束。
    PackedLayout layout{};
    size_t payload_bytes = 0;
    if (windowed) {
        if (!crop) {
            return fail_publish(slot_idx, target_clients);
        }
        layout = packed_layout_for(view, frame.format);
        if (!layout.valid || layout.bytes == 0 || layout.bytes > options_.slot_size) {
            return fail_publish(slot_idx, target_clients);
        }
        if (dma_slot_data != nullptr && dma_slots_[slot_idx]->size() < layout.bytes) {
            dma_slot_data = nullptr;
            dma_fd_to_send = -1;
            slot_data = shm_pool_.slot_write_ptr(slot_idx);
            if (!slot_data) {
                return fail_publish(slot_idx, target_clients);
            }
        }
        // 防御性边界校验：几何已按帧尺寸计算，这里覆盖元数据/帧尺寸的瞬时不一致。
        if (view.x < 0 || view.y < 0 ||
            static_cast<uint64_t>(view.x) + static_cast<uint64_t>(view.w) > frame.width ||
            static_cast<uint64_t>(view.y) + static_cast<uint64_t>(view.h) > frame.height) {
            return fail_publish(slot_idx, target_clients);
        }
    } else if (is_zero_copy_frame) {
        if (frame.width == 0 || frame.height == 0 || frame.stride < frame.width) {
            return fail_publish(slot_idx, target_clients);
        }
    } else {
        payload_bytes = frame_payload_bytes(frame);
        if (payload_bytes == 0 || payload_bytes > options_.slot_size ||
            payload_bytes > std::numeric_limits<uint32_t>::max()) {
            return fail_publish(slot_idx, target_clients);
        }

        // 校验行跨度与源指针合法性，避免越界写槽位或发送未初始化的旧槽位字节
        if (frame.format == FRAME_FORMAT_NV12) {
            if (frame.stride < frame.width) {
                return fail_publish(slot_idx, target_clients);
            }
            const bool has_planes = (frame.host_planes[0] != nullptr && frame.host_planes[1] != nullptr);
            if (!has_planes && frame.host_ptr == nullptr) {
                return fail_publish(slot_idx, target_clients);
            }
            if (has_planes) {
                const uint32_t src_y_stride = frame.plane_strides[0] > 0 ? frame.plane_strides[0] : frame.stride;
                const uint32_t src_uv_stride = frame.plane_strides[1] > 0 ? frame.plane_strides[1] : frame.stride;
                if (src_y_stride < frame.width || src_uv_stride < frame.width) {
                    return fail_publish(slot_idx, target_clients);
                }
            }
        } else if (frame.format == FRAME_FORMAT_YUV420P) {
            if (frame.stride < frame.width) {
                return fail_publish(slot_idx, target_clients);
            }
            const size_t u_stride = (static_cast<size_t>(frame.stride) + 1) / 2;
            const size_t uv_w = (static_cast<size_t>(frame.width) + 1) / 2;
            const bool has_planes = (frame.host_planes[0] != nullptr &&
                                     frame.host_planes[1] != nullptr &&
                                     frame.host_planes[2] != nullptr);
            if (!has_planes && frame.host_ptr == nullptr) {
                return fail_publish(slot_idx, target_clients);
            }
            if (has_planes) {
                const size_t src_y_stride = frame.plane_strides[0] > 0 ? frame.plane_strides[0] : frame.stride;
                const size_t src_u_stride = frame.plane_strides[1] > 0 ? frame.plane_strides[1] : u_stride;
                const size_t src_v_stride = frame.plane_strides[2] > 0 ? frame.plane_strides[2] : u_stride;
                if (src_y_stride < frame.width || src_u_stride < uv_w || src_v_stride < uv_w) {
                    return fail_publish(slot_idx, target_clients);
                }
            }
        } else {
            if (static_cast<size_t>(frame.stride) < static_cast<size_t>(frame.width) * 3 ||
                frame.host_ptr == nullptr) {
                return fail_publish(slot_idx, target_clients);
            }
        }
    }

    FrameDispatchMsg msg{};
    if (windowed) {
        const ViewRect actual = window_to_view_rect(
            view, static_cast<int>(frame.width), static_cast<int>(frame.height));
        msg.input_view[0] = actual.x1;
        msg.input_view[1] = actual.y1;
        msg.input_view[2] = actual.x2;
        msg.input_view[3] = actual.y2;
    }
    msg.stream_id = stream_id_;
    msg.frame_id = frame_seq_.fetch_add(1);
    msg.pts = frame.pts;
    msg.capture_time_ms = capture_time_ms > 0
                              ? capture_time_ms
                              : std::chrono::duration_cast<std::chrono::milliseconds>(
                                    std::chrono::system_clock::now().time_since_epoch())
                                    .count();
    msg.slot_index = static_cast<uint32_t>(slot_idx);
    msg.generation = generation;
    msg.width = static_cast<int32_t>(windowed ? view.w : frame.width);
    msg.height = static_cast<int32_t>(windowed ? view.h : frame.height);
    msg.strides[0] = static_cast<int32_t>(windowed ? layout.stride : frame.stride);
    // ⚠️ 恒为 -1：fd 是进程本地句柄，跨进程传值无意义且危险（见 ipc_protocol.hpp 注释）。
    //    真实的 DMA-BUF 句柄仅通过 SCM_RIGHTS 传递，接收端必须只认本地副本 fd。
    msg.reserved_fd = -1;

    msg.has_motion = motion.has_motion;
    msg.motion_box_count = std::min(static_cast<size_t>(motion.motion_box_count), MAX_IPC_MOTION_BOXES);
    for (size_t i = 0; i < msg.motion_box_count; ++i) {
        msg.motion_boxes[i] = motion.motion_boxes[i];
    }
    if (is_zero_copy_frame || (windowed && dma_fd_to_send >= 0)) {
        msg.flags |= FRAME_DISPATCH_FLAG_DMA_FD;
    }

    if (windowed) {
        msg.format = frame.format == FRAME_FORMAT_NV12 ? ZHULONG_PIXEL_FORMAT_NV12
                                                       : ZHULONG_PIXEL_FORMAT_YUV420P;
        for (int i = 0; i < 4; ++i) {
            msg.strides[i] = layout.strides[i];
            msg.offsets[i] = layout.offsets[i];
        }

        uint8_t* dst = dma_slot_data != nullptr ? dma_slot_data : slot_data;
        const int dst_dma_fd = dma_slot_data != nullptr ? dma_fd_to_send : -1;
        if (dst == nullptr ||
            !crop(frame, view, dst, layout.bytes, static_cast<int>(layout.stride), dst_dma_fd)) {
            // 物化失败：绝不发送半截帧。
            return fail_publish(slot_idx, target_clients);
        }
    } else if (frame.format == FRAME_FORMAT_NV12) {
        msg.format = ZHULONG_PIXEL_FORMAT_NV12;
        msg.strides[1] = static_cast<int32_t>(frame.stride);
        msg.offsets[0] = 0;
        const uint32_t eff_vstride = (frame.vstride > 0) ? std::max(frame.vstride, frame.height) : frame.height;
        const size_t y_plane_bytes = static_cast<size_t>(frame.stride) * eff_vstride;
        if (y_plane_bytes > std::numeric_limits<uint32_t>::max()) {
            return fail_publish(slot_idx, target_clients);
        }
        msg.offsets[1] = static_cast<uint32_t>(y_plane_bytes);

        if (!is_zero_copy_frame) {
            if (frame.host_planes[0] && frame.host_planes[1]) {
                // 安全分平面逐行独立拷贝（杜绝把 data[0] 当作单一连续缓冲引发越界）
                // 容量与跨度已由前置门禁保证，此处无需再判。
                const size_t uv_lines = (static_cast<size_t>(frame.height) + 1) / 2;
                const uint32_t src_y_stride = frame.plane_strides[0] > 0 ? frame.plane_strides[0] : frame.stride;
                const uint32_t src_uv_stride = frame.plane_strides[1] > 0 ? frame.plane_strides[1] : frame.stride;

                if (src_y_stride == frame.stride) {
                    std::memcpy(slot_data, frame.host_planes[0],
                                static_cast<size_t>(frame.stride) * frame.height);
                } else {
                    for (size_t r = 0; r < frame.height; ++r) {
                        std::memcpy(slot_data + r * frame.stride, frame.host_planes[0] + r * src_y_stride, frame.width);
                    }
                }

                uint8_t* uv_dst = slot_data + msg.offsets[1];
                if (src_uv_stride == frame.stride) {
                    std::memcpy(uv_dst, frame.host_planes[1],
                                static_cast<size_t>(frame.stride) * uv_lines);
                } else {
                    for (size_t r = 0; r < uv_lines; ++r) {
                        std::memcpy(uv_dst + r * frame.stride, frame.host_planes[1] + r * src_uv_stride, frame.width);
                    }
                }
            } else if (frame.host_ptr) {
                std::memcpy(slot_data, frame.host_ptr, payload_bytes);
            }
        }
    } else if (frame.format == FRAME_FORMAT_YUV420P) {
        msg.format = ZHULONG_PIXEL_FORMAT_YUV420P;
        const size_t y_size = static_cast<size_t>(frame.stride) * frame.height;
        const size_t u_stride = (static_cast<size_t>(frame.stride) + 1) / 2;
        const size_t uv_h = (static_cast<size_t>(frame.height) + 1) / 2;
        const size_t u_size = u_stride * uv_h;
        if (y_size > std::numeric_limits<uint32_t>::max() ||
            u_size > std::numeric_limits<uint32_t>::max() - y_size) {
            return fail_publish(slot_idx, target_clients);
        }

        msg.strides[1] = static_cast<int32_t>(u_stride);
        msg.strides[2] = static_cast<int32_t>(u_stride);
        msg.offsets[0] = 0;
        msg.offsets[1] = static_cast<uint32_t>(y_size);
        msg.offsets[2] = static_cast<uint32_t>(y_size + u_size);

        if (!is_zero_copy_frame) {
            if (frame.host_planes[0] && frame.host_planes[1] && frame.host_planes[2]) {
                // 安全分平面独立拷贝 (杜绝假定 data[0] 物理连续造成的读越界)
                // 容量与跨度已由前置门禁保证，此处无需再判。
                const uint32_t src_y_stride = frame.plane_strides[0] > 0 ? frame.plane_strides[0] : frame.stride;
                const uint32_t src_u_stride = frame.plane_strides[1] > 0 ? frame.plane_strides[1] : static_cast<uint32_t>(u_stride);
                const uint32_t src_v_stride = frame.plane_strides[2] > 0 ? frame.plane_strides[2] : static_cast<uint32_t>(u_stride);

                const size_t uv_w = (static_cast<size_t>(frame.width) + 1) / 2;

                if (src_y_stride == frame.stride) {
                    std::memcpy(slot_data, frame.host_planes[0], y_size);
                } else {
                    for (size_t r = 0; r < frame.height; ++r) {
                        std::memcpy(slot_data + r * frame.stride, frame.host_planes[0] + r * src_y_stride, frame.width);
                    }
                }

                uint8_t* u_dst = slot_data + msg.offsets[1];
                if (src_u_stride == u_stride) {
                    std::memcpy(u_dst, frame.host_planes[1], u_size);
                } else {
                    for (size_t r = 0; r < uv_h; ++r) {
                        std::memcpy(u_dst + r * u_stride, frame.host_planes[1] + r * src_u_stride, uv_w);
                    }
                }

                uint8_t* v_dst = slot_data + msg.offsets[2];
                if (src_v_stride == u_stride) {
                    std::memcpy(v_dst, frame.host_planes[2], u_size);
                } else {
                    for (size_t r = 0; r < uv_h; ++r) {
                        std::memcpy(v_dst + r * u_stride, frame.host_planes[2] + r * src_v_stride, uv_w);
                    }
                }
            } else if (frame.host_ptr) {
                std::memcpy(slot_data, frame.host_ptr, payload_bytes);
            }
        }
    } else {
        msg.format = ZHULONG_PIXEL_FORMAT_BGR24;
        msg.offsets[0] = 0;
        if (!is_zero_copy_frame && frame.host_ptr) {
            // 容量与源指针已由前置门禁保证，此处无需再判。
            std::memcpy(slot_data, frame.host_ptr, payload_bytes);
        }
    }

    // 若非零拷贝直通且使用 DMA 槽位，同步拷入 DMA-BUF 并校验 Cache 刷出结果
    if (!is_zero_copy_frame && !windowed && dma_slot_data) {
        // ⚠️ 耦合前提：DMA 槽位与 SHM 槽位由 start() 用**同一个** options_.slot_size
        //    分配（dma_slots_[i] = allocate(slot_size)；shm_pool_.init(..., slot_size)），
        //    因此「通过上面的 SHM 前置门禁」⟹「DMA 槽位也够」。仍显式核对 size() 并校验
        //    sync_start/sync_end 结果；若同步失败则撤销 dma_fd_to_send，安全降级到 SHM 槽位。
        if (dma_slots_[slot_idx]->size() >= payload_bytes &&
            dma_slots_[slot_idx]->sync_start(zhulong::platform::DMA_SYNC_WRITE)) {
            std::memcpy(dma_slot_data, slot_data, payload_bytes);
            if (!dma_slots_[slot_idx]->sync_end(zhulong::platform::DMA_SYNC_WRITE)) {
                dma_fd_to_send = -1;
            }
        } else {
            dma_fd_to_send = -1;
        }
    }

    // 切换槽位状态为 LEASED（若所有目标客户端在写入期间均已断连，则直接回 FREE）
    {
        std::lock_guard<std::mutex> lock(mutex_);
        slots_[slot_idx].state =
            slots_[slot_idx].active_leases.empty() ? SlotState::FREE : SlotState::LEASED;
    }

    // 向各合格客户端下发帧通知 (若发送失败立即清算租约)
    size_t sent_count = 0;
    for (uint32_t cid : target_clients) {
        if (server_.send_msg_with_fd(cid, MSG_FRAME_DISPATCH, &msg, sizeof(msg), dma_fd_to_send)) {
            ++sent_count;
        } else {
            std::lock_guard<std::mutex> lock(mutex_);
            auto it = clients_.find(cid);
            if (it != clients_.end() && it->second.in_flight_count > 0) {
                it->second.in_flight_count--;
                it->second.delivered_frames--;
                it->second.dropped_frames++;
            }
            if (slots_[slot_idx].generation == generation) {
                slots_[slot_idx].active_leases.erase(cid);
                if (slots_[slot_idx].active_leases.empty()) {
                    slots_[slot_idx].state = SlotState::FREE;
                }
            }
        }
    }

    if (sent_count == 0) {
        total_dropped_.fetch_add(1);
        return false;
    }

    total_published_.fetch_add(1);
    return true;
}

void FrameBroker::handle_client_message(uint32_t client_id, const IpcHeader& header, const uint8_t* payload) {
    if (header.msg_type == MSG_HANDSHAKE_REQ && payload && header.payload_size >= sizeof(HandshakeReq)) {
        HandshakeReq req{};
        std::memcpy(&req, payload, sizeof(req));
        handle_handshake(client_id, req);
    } else if (header.msg_type == MSG_FRAME_ACK && payload && header.payload_size >= sizeof(FrameAckMsg)) {
        FrameAckMsg ack{};
        std::memcpy(&ack, payload, sizeof(ack));
        handle_ack(client_id, ack);
    } else if (header.msg_type == MSG_RESULT && payload && header.payload_size > 0) {
        ResultCallback cb;
        {
            std::lock_guard<std::mutex> lock(mutex_);
            cb = on_result_;
        }
        if (cb) {
            cb(stream_id_, client_id, payload, header.payload_size);
        }
    }
}

void FrameBroker::handle_handshake(uint32_t client_id, const HandshakeReq& req) {
    {
        std::lock_guard<std::mutex> lock(mutex_);
        // 限制字符串最大拷贝长度，防止客户端发送未以 \0 结尾的畸形名字导致越界读
        const size_t name_len = ::strnlen(req.client_name, sizeof(req.client_name));
        auto it = clients_.find(client_id);
        if (it != clients_.end()) {
            it->second.name.assign(req.client_name, name_len);
        } else {
            ClientInfo info{};
            info.client_id = client_id;
            info.name.assign(req.client_name, name_len);
            info.in_flight_count = 0;
            info.dropped_frames = 0;
            info.delivered_frames = 0;
            clients_[client_id] = std::move(info);
        }
    }

    HandshakeResp resp{};
    resp.client_id = client_id;
    resp.status = 0;
    std::snprintf(resp.shm_name, sizeof(resp.shm_name), "%s", shm_name_.c_str());
    resp.slot_count = static_cast<uint32_t>(options_.slot_count);
    resp.slot_size = static_cast<uint32_t>(options_.slot_size);

    server_.send_msg(client_id, MSG_HANDSHAKE_RESP, &resp, sizeof(resp));

    // 重放最近一次任务级配置：晚启动 / 崩溃重启的包进程必须能重新拿到它，否则会静默
    // 回落到算法默认值（表现为「跑几天后阈值自己变回去了」）。拷贝一份再发，避免持锁做 IO。
    decltype(task_config_payloads_) replay;
    {
        std::lock_guard<std::mutex> lock(mutex_);
        replay = task_config_payloads_;
    }
    for (const auto& entry : replay) {
        const auto& message = entry.second;
        server_.send_msg(client_id, message.first, message.second.data(), message.second.size());
    }
}

bool FrameBroker::fail_publish(int slot_idx, const std::vector<uint32_t>& target_clients) {
    std::lock_guard<std::mutex> lock(mutex_);
    for (uint32_t cid : target_clients) {
        auto it = clients_.find(cid);
        if (it != clients_.end() && it->second.in_flight_count > 0) {
            it->second.in_flight_count--;
            it->second.delivered_frames--;
            it->second.dropped_frames++;
        }
    }
    if (slot_idx >= 0 && static_cast<size_t>(slot_idx) < slots_.size()) {
        slots_[slot_idx].state = SlotState::FREE;
        slots_[slot_idx].active_leases.clear();
    }
    total_dropped_.fetch_add(1);
    return false;
}

void FrameBroker::handle_ack(uint32_t client_id, const FrameAckMsg& ack) {
    std::lock_guard<std::mutex> lock(mutex_);

    // ⚠️ 校验必须先于任何状态修改：畸形 ACK（slot_index 越界）若先扣配额，
    //    会「只扣不还」，使该客户端配额被永久蚕食并静默停止收帧。
    if (ack.slot_index >= slots_.size()) {
        return;
    }

    auto& slot = slots_[ack.slot_index];
    // 只有处于 LEASED 状态且 generation 完全匹配的租约才被接受；
    // 杜绝过期/驱逐后的迟到 ACK 破坏槽位状态与配额，并防止对处于 WRITING 状态的槽位提前释放
    if (slot.state != SlotState::LEASED || slot.generation != ack.generation) {
        return;
    }

    // 只有该客户端确实持有该槽位的租约，才扣减在途计数并释放租约
    if (slot.active_leases.erase(client_id) == 0) {
        return;
    }

    auto cit = clients_.find(client_id);
    if (cit != clients_.end() && cit->second.in_flight_count > 0) {
        cit->second.in_flight_count--;
    }

    if (slot.active_leases.empty()) {
        slot.state = SlotState::FREE;
    }
}

void FrameBroker::handle_client_disconnect(uint32_t client_id) {
    std::lock_guard<std::mutex> lock(mutex_);

    // 回收该客户端在所有槽位中的租约（子进程崩溃自愈，防止槽位悬挂泄漏）
    for (auto& slot : slots_) {
        slot.active_leases.erase(client_id);
        if (slot.active_leases.empty() && slot.state == SlotState::LEASED) {
            slot.state = SlotState::FREE;
        }
    }

    clients_.erase(client_id);
}

size_t FrameBroker::client_count() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return clients_.size();
}

bool FrameBroker::get_client_info(uint32_t client_id, ClientInfo& out_info) const {
    std::lock_guard<std::mutex> lock(mutex_);
    auto it = clients_.find(client_id);
    if (it != clients_.end()) {
        out_info = it->second;
        return true;
    }
    return false;
}

std::vector<ClientInfo> FrameBroker::clients_snapshot() const {
    std::lock_guard<std::mutex> lock(mutex_);
    std::vector<ClientInfo> snapshot;
    snapshot.reserve(clients_.size());
    for (const auto& entry : clients_) {
        snapshot.push_back(entry.second);
    }
    // 输出顺序确定：clients_ 是 unordered_map，不排序会让同一份状态在两次
    // 查询中顺序不同，按索引对比或做差分的调用方会得到假的变化。
    std::sort(snapshot.begin(), snapshot.end(),
              [](const ClientInfo& lhs, const ClientInfo& rhs) { return lhs.client_id < rhs.client_id; });
    return snapshot;
}

size_t FrameBroker::broadcast_message(uint16_t msg_type, const void* payload, size_t size) {
    if (!running_.load(std::memory_order_acquire)) {
        return 0;
    }

    // 任务级配置必须**先缓存再广播**：缓存是「晚连上 / 重连」的包进程唯一能拿到配置的途径，
    // 而广播只覆盖此刻已连接的客户端。缓存在 running_ 之后做，停止中的 Broker 不会存半条。
    if ((msg_type == MSG_TASK_CONFIG || msg_type == MSG_TASK_REGIONS) &&
        payload != nullptr && size >= sizeof(TaskConfigMsg) && size <= MAX_IPC_PAYLOAD_SIZE) {
        const auto* bytes = static_cast<const uint8_t*>(payload);
        TaskConfigMsg header{};
        std::memcpy(&header, payload, sizeof(header));
        const size_t id_length = ::strnlen(header.algorithm_id, sizeof(header.algorithm_id));
        if (id_length == 0 || id_length == sizeof(header.algorithm_id) ||
            header.config_json_len != size - sizeof(header)) return 0;
        const std::string key = std::to_string(msg_type) + ":" + std::string(header.algorithm_id, id_length);
        std::lock_guard<std::mutex> lock(mutex_);
        if (task_config_payloads_.find(key) == task_config_payloads_.end() && task_config_payloads_.size() >= 256) {
            ZL_LOGW(0, "ipc.broker", "task config cache capacity exceeded");
            return 0;
        }
        task_config_payloads_[key] = {msg_type, std::vector<uint8_t>(bytes, bytes + size)};
    }

    return server_.broadcast(msg_type, payload, size);
}

} // namespace zhulong::ipc

