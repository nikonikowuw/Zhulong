/**
 * @file plate_recognition.hpp
 * @brief 车牌检测 + 字符/颜色识别插件类（本 target 唯一实现）
 *
 * 资源模型：检测模型会话由 Worker 直接持有（`create_session` 的返回值）；识别模型会话
 * 按 worker_id 保存，供同一 Worker 在执行 `detect_vehicles` 时取用。
 */

#pragma once

#include "plate_detector.hpp"
#include "plate_recognizer.hpp"

#include <zhulong/algo.hpp>

#include <mutex>
#include <string>
#include <unordered_map>
#include <vector>

namespace zhulong::plate {

class PlateRecognitionPlugin {
public:
    PlateRecognitionPlugin() = default;

    zhulong_status_t init(const std::string& config_json);
    zhulong::Session create_session(uint32_t worker_id);
    std::vector<zhulong::VehicleItem> detect_vehicles(zhulong::Session& session, const zhulong::Image& image);

private:
    PlateDetector detector_;
    PlateRecognizer recognizer_;

    zhulong::Model det_model_;
    zhulong::Model rec_model_;
    std::mutex session_mutex_;
    std::unordered_map<uint32_t, zhulong::Session> rec_sessions_;
};

} // namespace zhulong::plate
