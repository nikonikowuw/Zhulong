#pragma once

#include "algorithm/ipc/ipc_protocol.hpp"
#include <zhulong/regions.hpp>
#include <cstring>
#include <string>
#include <string_view>

namespace zhulong::sdk {

/** Worker-owned snapshot, updated only in the worker's socket receive loop. */
class TaskRegionState {
public:
    bool receive(const void* payload, size_t size, std::string_view algorithm_id) {
        if (!payload || size < sizeof(ipc::TaskConfigMsg) || size > ipc::MAX_IPC_PAYLOAD_SIZE) return false;
        ipc::TaskConfigMsg header{};
        std::memcpy(&header, payload, sizeof(header));
        const size_t length = ::strnlen(header.algorithm_id, sizeof(header.algorithm_id));
        if (algorithm_id.empty() || length == sizeof(header.algorithm_id) ||
            std::string_view(header.algorithm_id, length) != algorithm_id ||
            header.config_json_len != size - sizeof(header)) return false;
        const std::string_view next(static_cast<const char*>(payload) + sizeof(header), header.config_json_len);
        if (!received_ || next != json_) {
            json_.assign(next);
            ++revision_;
            received_ = true;
        }
        return true;
    }
    TaskRegions view() const noexcept { return {json_, revision_}; }
private:
    std::string json_;
    uint64_t revision_{0};
    bool received_{false};
};

} // namespace zhulong::sdk
