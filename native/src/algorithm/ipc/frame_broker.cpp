/**
 * @file frame_broker.cpp
 * @brief 共享内存帧分发宿主实现、多平面安全复制与代次防 ABA 租约回收
 */

#include "frame_broker.hpp"
#include <cstring>
#include <iostream>

namespace zhulong::ipc {

FrameBroker::FrameBroker() = default;

FrameBroker::~FrameBroker() {
    stop();
}

bool FrameBroker::start(uint64_t stream_id, const std::string& base_dir,
                        BrokerOptions options, std::string* error_msg) {
    stop();
    stream_id_ = stream_id;
    options_ = options;

    shm_name_ = "/zhulong_shm_" + std::to_string(stream_id_);
    socket_path_ = base_dir + "/zhulong_broker_" + std::to_string(stream_id_) + ".sock";

    // 1. 初始化共享内存池 (主写模式)
    if (!shm_pool_.init(shm_name_, ShmMode::CREATE_READ_WRITE, options_.slot_count, options_.slot_size, error_msg)) {
        return false;
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
        return false;
    }

    running_.store(true);
    return true;
}

void FrameBroker::stop() {
    if (!running_.exchange(false)) {
        return;
    }

    server_.stop();
    shm_pool_.close();

    std::lock_guard<std::mutex> lock(mutex_);
    slots_.clear();
    clients_.clear();
}

int FrameBroker::find_or_evict_free_slot(std::unique_lock<std::mutex>& /*lock*/) {
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

bool FrameBroker::publish_frame(const HardwareFrame& frame) {
    if (!running_.load()) return false;

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

    // 写入数据到共享内存槽位
    uint8_t* slot_data = shm_pool_.slot_write_ptr(slot_idx);
    if (!slot_data) {
        // 共享内存槽位获取失败：原子回退在途配额
        std::lock_guard<std::mutex> lock(mutex_);
        for (uint32_t cid : target_clients) {
            auto it = clients_.find(cid);
            if (it != clients_.end() && it->second.in_flight_count > 0) {
                it->second.in_flight_count--;
                it->second.delivered_frames--;
                it->second.dropped_frames++;
            }
        }
        slots_[slot_idx].state = SlotState::FREE;
        slots_[slot_idx].active_leases.clear();
        total_dropped_.fetch_add(1);
        return false;
    }

    FrameDispatchMsg msg{};
    msg.stream_id = stream_id_;
    msg.frame_id = frame_seq_.fetch_add(1);
    msg.pts = frame.pts;
    msg.slot_index = static_cast<uint32_t>(slot_idx);
    msg.generation = generation;
    msg.width = static_cast<int32_t>(frame.width);
    msg.height = static_cast<int32_t>(frame.height);
    msg.strides[0] = static_cast<int32_t>(frame.stride);
    msg.reserved_fd = frame.dma_fd;

    if (frame.format == FRAME_FORMAT_NV12) {
        msg.format = ZHULONG_PIXEL_FORMAT_NV12;
        msg.strides[1] = static_cast<int32_t>(frame.stride);
        msg.offsets[0] = 0;
        msg.offsets[1] = static_cast<uint32_t>(frame.y_plane_size());

        size_t total_bytes = frame.y_plane_size() + static_cast<size_t>(frame.stride) * ((frame.height + 1) / 2);
        if (total_bytes <= options_.slot_size) {
            if (frame.host_planes[0] && frame.host_planes[1]) {
                // 安全分平面逐行独立拷贝（杜绝把 data[0] 当作单一连续缓冲引发越界）
                size_t uv_lines = (frame.height + 1) / 2;
                uint32_t src_y_stride = frame.plane_strides[0] > 0 ? frame.plane_strides[0] : frame.stride;
                uint32_t src_uv_stride = frame.plane_strides[1] > 0 ? frame.plane_strides[1] : frame.stride;

                if (src_y_stride == frame.stride) {
                    std::memcpy(slot_data, frame.host_planes[0], frame.stride * frame.height);
                } else {
                    for (size_t r = 0; r < frame.height; ++r) {
                        std::memcpy(slot_data + r * frame.stride, frame.host_planes[0] + r * src_y_stride, frame.width);
                    }
                }

                uint8_t* uv_dst = slot_data + msg.offsets[1];
                if (src_uv_stride == frame.stride) {
                    std::memcpy(uv_dst, frame.host_planes[1], frame.stride * uv_lines);
                } else {
                    for (size_t r = 0; r < uv_lines; ++r) {
                        std::memcpy(uv_dst + r * frame.stride, frame.host_planes[1] + r * src_uv_stride, frame.width);
                    }
                }
            } else if (frame.host_ptr) {
                std::memcpy(slot_data, frame.host_ptr, total_bytes);
            }
        }
    } else if (frame.format == FRAME_FORMAT_YUV420P) {
        msg.format = ZHULONG_PIXEL_FORMAT_YUV420P;
        size_t y_size = static_cast<size_t>(frame.stride) * frame.height;
        size_t u_stride = (frame.stride + 1) / 2;
        size_t u_size = u_stride * ((frame.height + 1) / 2);

        msg.strides[1] = static_cast<int32_t>(u_stride);
        msg.strides[2] = static_cast<int32_t>(u_stride);
        msg.offsets[0] = 0;
        msg.offsets[1] = static_cast<uint32_t>(y_size);
        msg.offsets[2] = static_cast<uint32_t>(y_size + u_size);

        size_t total_bytes = y_size + u_size * 2;
        if (total_bytes <= options_.slot_size) {
            if (frame.host_planes[0] && frame.host_planes[1] && frame.host_planes[2]) {
                // 安全分平面独立拷贝 (杜绝假定 data[0] 物理连续造成的读越界)
                uint32_t src_y_stride = frame.plane_strides[0] > 0 ? frame.plane_strides[0] : frame.stride;
                uint32_t src_u_stride = frame.plane_strides[1] > 0 ? frame.plane_strides[1] : static_cast<uint32_t>(u_stride);
                uint32_t src_v_stride = frame.plane_strides[2] > 0 ? frame.plane_strides[2] : static_cast<uint32_t>(u_stride);

                size_t uv_h = (frame.height + 1) / 2;
                size_t uv_w = (frame.width + 1) / 2;

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
                std::memcpy(slot_data, frame.host_ptr, total_bytes);
            }
        }
    } else {
        msg.format = ZHULONG_PIXEL_FORMAT_BGR24;
        msg.offsets[0] = 0;
        size_t total_bytes = static_cast<size_t>(frame.stride) * frame.height;
        if (total_bytes <= options_.slot_size && frame.host_ptr) {
            std::memcpy(slot_data, frame.host_ptr, total_bytes);
        }
    }

    // 切换槽位状态为 LEASED
    {
        std::lock_guard<std::mutex> lock(mutex_);
        slots_[slot_idx].state = SlotState::LEASED;
    }

    // 向各合格客户端下发帧通知 (若发送失败立即清算租约)
    for (uint32_t cid : target_clients) {
        if (!server_.send_msg(cid, MSG_FRAME_DISPATCH, &msg, sizeof(msg))) {
            std::lock_guard<std::mutex> lock(mutex_);
            auto it = clients_.find(cid);
            if (it != clients_.end() && it->second.in_flight_count > 0) {
                it->second.in_flight_count--;
                it->second.delivered_frames--;
                it->second.dropped_frames++;
            }
            slots_[slot_idx].active_leases.erase(cid);
            if (slots_[slot_idx].active_leases.empty()) {
                slots_[slot_idx].state = SlotState::FREE;
            }
        }
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
        ClientInfo info{};
        info.client_id = client_id;
        info.name = req.client_name;
        info.in_flight_count = 0;
        info.dropped_frames = 0;
        info.delivered_frames = 0;
        clients_[client_id] = info;
    }

    HandshakeResp resp{};
    resp.client_id = client_id;
    resp.status = 0;
    std::strncpy(resp.shm_name, shm_name_.c_str(), sizeof(resp.shm_name) - 1);
    resp.slot_count = static_cast<uint32_t>(options_.slot_count);
    resp.slot_size = static_cast<uint32_t>(options_.slot_size);

    server_.send_msg(client_id, MSG_HANDSHAKE_RESP, &resp, sizeof(resp));
}

void FrameBroker::handle_ack(uint32_t client_id, const FrameAckMsg& ack) {
    std::lock_guard<std::mutex> lock(mutex_);

    // 递减客户端在途计数
    auto cit = clients_.find(client_id);
    if (cit != clients_.end() && cit->second.in_flight_count > 0) {
        cit->second.in_flight_count--;
    }

    // 校验槽位与代次
    if (ack.slot_index >= slots_.size()) {
        return;
    }

    auto& slot = slots_[ack.slot_index];
    if (slot.generation != ack.generation) {
        // 过期的历史 ACK，忽略
        return;
    }

    slot.active_leases.erase(client_id);
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

size_t FrameBroker::broadcast_message(uint16_t msg_type, const void* payload, size_t size) {
    if (!running_.load(std::memory_order_relaxed)) {
        return 0;
    }
    return server_.broadcast(msg_type, payload, size);
}

} // namespace zhulong::ipc

