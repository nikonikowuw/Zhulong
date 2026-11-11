/**
 * @file result_serializer.hpp
 * @brief 算法结果 JSON 序列化器与 IPC 64KB 预算安全裁剪
 */

#pragma once

#include <zhulong/plugin.h>
#include "internal/json.hpp"
#include "algorithm/ipc/ipc_protocol.hpp"

#include <algorithm>
#include <cmath>
#include <cstddef>
#include <cstdint>
#include <cstring>
#include <sstream>
#include <string>
#include <vector>

namespace zhulong {
namespace sdk {
namespace detail {

inline std::string base64_encode(const uint8_t* data, size_t len)
{
    static constexpr char kTable[] =
        "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    std::string out;
    out.reserve(((len + 2) / 3) * 4);

    size_t i = 0;
    while (i + 3 <= len)
    {
        uint32_t n = (static_cast<uint32_t>(data[i]) << 16) |
                     (static_cast<uint32_t>(data[i + 1]) << 8) |
                     static_cast<uint32_t>(data[i + 2]);
        out.push_back(kTable[(n >> 18) & 0x3F]);
        out.push_back(kTable[(n >> 12) & 0x3F]);
        out.push_back(kTable[(n >> 6) & 0x3F]);
        out.push_back(kTable[n & 0x3F]);
        i += 3;
    }
    if (i < len)
    {
        if (len - i == 1)
        {
            uint32_t n = static_cast<uint32_t>(data[i]) << 16;
            out.push_back(kTable[(n >> 18) & 0x3F]);
            out.push_back(kTable[(n >> 12) & 0x3F]);
            out.push_back('=');
            out.push_back('=');
        }
        else // len - i == 2
        {
            uint32_t n = (static_cast<uint32_t>(data[i]) << 16) |
                         (static_cast<uint32_t>(data[i + 1]) << 8);
            out.push_back(kTable[(n >> 18) & 0x3F]);
            out.push_back(kTable[(n >> 12) & 0x3F]);
            out.push_back(kTable[(n >> 6) & 0x3F]);
            out.push_back('=');
        }
    }
    return out;
}

inline std::string encode_features_b64(const float* features, uint32_t count)
{
    if (!features || count == 0 || count > ZHULONG_FACE_FEATURE_MAX_DIM)
    {
        return {};
    }
    std::vector<uint8_t> bytes(count * sizeof(float));
    for (uint32_t i = 0; i < count; ++i)
    {
        float val = features[i];
        if (!std::isfinite(val))
        {
            return {};
        }
        uint32_t bits = 0;
        std::memcpy(&bits, &val, sizeof(float));
        bytes[i * 4 + 0] = static_cast<uint8_t>(bits & 0xFF);
        bytes[i * 4 + 1] = static_cast<uint8_t>((bits >> 8) & 0xFF);
        bytes[i * 4 + 2] = static_cast<uint8_t>((bits >> 16) & 0xFF);
        bytes[i * 4 + 3] = static_cast<uint8_t>((bits >> 24) & 0xFF);
    }
    return base64_encode(bytes.data(), bytes.size());
}

/**
 * @brief 将插件返回的推理结果序列化为单行 JSON 字符串
 */
inline std::string serialize_inference_result(uint64_t stream_id,
                                              int64_t pts,
                                              int64_t capture_time_ms,
                                              int64_t cost_ms,
                                              const zhulong_inference_result* result,
                                              const std::string& algorithm_id = "")
{
    std::ostringstream ss;
    ss << "{\"stream_id\":" << stream_id
       << ",\"pts\":" << pts
       << ",\"capture_time_ms\":" << capture_time_ms
       << ",\"cost_ms\":" << cost_ms
       << ",\"algorithm_id\":" << runner::json_quote(algorithm_id);

    if (!result || result->count == 0)
    {
        ss << ",\"task_type\":\"\""
           << ",\"detections\":[]"
           << ",\"classifications\":[]"
           << ",\"persons\":[]"
           << ",\"faces\":[]"
           << ",\"vehicles\":[]}";
        return ss.str();
    }

    if (result->result_type == ZHULONG_RESULT_TYPE_DETECTION)
    {
        ss << ",\"task_type\":\"detection\""
           << ",\"detections\":[";
        const auto* boxes = static_cast<const zhulong_bbox*>(result->data);
        for (uint32_t i = 0; i < result->count; ++i)
        {
            if (i > 0) ss << ",";
            ss << "{\"box\":[" << boxes[i].box[0] << "," << boxes[i].box[1] << ","
               << boxes[i].box[2] << "," << boxes[i].box[3] << "]"
               << ",\"label\":" << runner::json_quote(boxes[i].label)
               << ",\"score\":" << boxes[i].confidence << "}";
        }
        ss << "],\"classifications\":[],\"persons\":[],\"faces\":[],\"vehicles\":[]}";
    }
    else if (result->result_type == ZHULONG_RESULT_TYPE_CLASSIFICATION)
    {
        ss << ",\"task_type\":\"classification\""
           << ",\"detections\":[],\"classifications\":[";
        const auto* items = static_cast<const zhulong_classification_item*>(result->data);
        for (uint32_t i = 0; i < result->count; ++i)
        {
            if (i > 0) ss << ",";
            ss << "{\"label\":" << runner::json_quote(items[i].label)
               << ",\"score\":" << items[i].confidence << "}";
        }
        ss << "],\"persons\":[],\"faces\":[],\"vehicles\":[]}";
    }
    else if (result->result_type == ZHULONG_RESULT_TYPE_PERSON_STRUCTURE)
    {
        ss << ",\"task_type\":\"person_structure\""
           << ",\"detections\":[],\"classifications\":[],\"persons\":[";
        const auto* persons = static_cast<const zhulong_person_item*>(result->data);
        for (uint32_t i = 0; i < result->count; ++i)
        {
            if (i > 0) ss << ",";
            const auto& p = persons[i];
            ss << "{\"track_id\":" << p.track_id
               << ",\"body_box\":[" << p.body_box[0] << "," << p.body_box[1] << ","
               << p.body_box[2] << "," << p.body_box[3] << "]"
               << ",\"body_score\":" << p.body_score
               << ",\"faces\":[";
            for (uint32_t j = 0; j < p.face_count; ++j)
            {
                if (j > 0) ss << ",";
                const auto& f = p.faces[j];
                ss << "{\"box\":[" << f.box[0] << "," << f.box[1] << ","
                   << f.box[2] << "," << f.box[3] << "]"
                   << ",\"quality\":" << f.quality
                   << ",\"landmarks\":[";
                for (size_t k = 0; k < ZHULONG_FACE_LANDMARK_COUNT; ++k)
                {
                    if (k > 0) ss << ",";
                    ss << "[" << f.landmarks[k][0] << "," << f.landmarks[k][1] << "]";
                }
                ss << "],\"matched\":" << (f.matched ? "true" : "false")
                   << ",\"similarity\":" << f.similarity
                   << ",\"person_id\":" << runner::json_quote(f.person_id)
                   << ",\"person_name\":" << runner::json_quote(f.person_name)
                   << ",\"matched_face_id\":" << runner::json_quote(f.matched_face_id)
                   << ",\"candidates\":[";
                for (uint32_t k = 0; k < f.candidate_count; ++k)
                {
                    if (k > 0) ss << ",";
                    const auto& c = f.candidates[k];
                    ss << "{\"person_id\":" << runner::json_quote(c.person_id)
                       << ",\"person_name\":" << runner::json_quote(c.person_name)
                       << ",\"face_id\":" << runner::json_quote(c.face_id)
                       << ",\"similarity\":" << c.similarity << "}";
                }
                ss << "]}";
            }
            ss << "]}";
        }
        ss << "],\"faces\":[],\"vehicles\":[]}";
    }
    else if (result->result_type == ZHULONG_RESULT_TYPE_VEHICLE_STRUCTURE)
    {
        ss << ",\"task_type\":\"vehicle_structure\""
           << ",\"detections\":[],\"classifications\":[],\"persons\":[],\"faces\":[]"
           << ",\"vehicles\":[";
        const auto* vehicles = static_cast<const zhulong_vehicle_item*>(result->data);
        for (uint32_t i = 0; i < result->count; ++i)
        {
            if (i > 0) ss << ",";
            const auto& v = vehicles[i];
            ss << "{\"track_id\":" << v.track_id
               << ",\"vehicle_box\":[" << v.vehicle_box[0] << "," << v.vehicle_box[1] << ","
               << v.vehicle_box[2] << "," << v.vehicle_box[3] << "]"
               << ",\"vehicle_score\":" << v.vehicle_score
               << ",\"vehicle_type\":" << runner::json_quote(v.vehicle_type)
               << ",\"vehicle_color\":" << runner::json_quote(v.vehicle_color)
               << ",\"plates\":[";
            for (uint32_t j = 0; j < v.plate_count; ++j)
            {
                if (j > 0) ss << ",";
                const auto& pl = v.plates[j];
                ss << "{\"box\":[" << pl.box[0] << "," << pl.box[1] << ","
                   << pl.box[2] << "," << pl.box[3] << "]"
                   << ",\"score\":" << pl.score
                   << ",\"plate_number\":" << runner::json_quote(pl.plate_number)
                   << ",\"plate_color\":" << runner::json_quote(pl.plate_color)
                   << ",\"plate_type\":" << pl.plate_type << "}";
            }
            ss << "]}";
        }
        ss << "]}";
    }
    else if (result->result_type == ZHULONG_RESULT_TYPE_SCENE_STRUCTURE)
    {
        ss << ",\"task_type\":\"scene_structure\""
           << ",\"detections\":[],\"classifications\":[],\"vehicles\":[]"
           << ",\"persons\":[";
        const auto* scene = static_cast<const zhulong_scene_structure*>(result->data);
        if (scene && scene->persons && scene->person_count > 0)
        {
            for (uint32_t i = 0; i < scene->person_count; ++i)
            {
                if (i > 0) ss << ",";
                const auto& p = scene->persons[i];
                ss << "{\"track_id\":" << p.track_id
                   << ",\"body_box\":[" << p.body_box[0] << "," << p.body_box[1] << ","
                   << p.body_box[2] << "," << p.body_box[3] << "]"
                   << ",\"body_score\":" << p.body_score
                   << ",\"faces\":[]}";
            }
        }
        ss << "],\"faces\":[";
        if (scene && scene->faces && scene->face_count > 0)
        {
            const uint32_t n_faces = scene->face_count;
            std::vector<std::string> face_base(n_faces);
            std::vector<std::string> face_b64(n_faces);
            size_t total_feat_bytes = 0;

            for (uint32_t i = 0; i < n_faces; ++i)
            {
                const auto& f = scene->faces[i];
                std::ostringstream fs;
                fs << "{\"box\":[" << f.box[0] << "," << f.box[1] << ","
                   << f.box[2] << "," << f.box[3] << "]"
                   << ",\"quality\":" << f.quality
                   << ",\"landmarks\":[";
                for (size_t k = 0; k < ZHULONG_FACE_LANDMARK_COUNT; ++k)
                {
                    if (k > 0) fs << ",";
                    fs << "[" << f.landmarks[k][0] << "," << f.landmarks[k][1] << "]";
                }
                fs << "],\"matched\":" << (f.matched ? "true" : "false")
                   << ",\"similarity\":" << f.similarity
                   << ",\"person_index\":" << f.person_index
                   << ",\"person_id\":" << runner::json_quote(f.person_id)
                   << ",\"person_name\":" << runner::json_quote(f.person_name)
                   << ",\"matched_face_id\":" << runner::json_quote(f.matched_face_id)
                   << ",\"candidates\":[";
                for (uint32_t k = 0; k < f.candidate_count && f.candidates; ++k)
                {
                    if (k > 0) fs << ",";
                    const auto& c = f.candidates[k];
                    fs << "{\"person_id\":" << runner::json_quote(c.person_id)
                       << ",\"person_name\":" << runner::json_quote(c.person_name)
                       << ",\"face_id\":" << runner::json_quote(c.face_id)
                       << ",\"similarity\":" << c.similarity << "}";
                }
                fs << "]";
                face_base[i] = fs.str();

                if (scene->face_features && f.feature_dim > 0 && f.feature_dim <= ZHULONG_FACE_FEATURE_MAX_DIM)
                {
                    face_b64[i] = encode_features_b64(scene->face_features + f.feature_offset, f.feature_dim);
                    if (!face_b64[i].empty())
                    {
                        total_feat_bytes += (17 + face_b64[i].size());
                    }
                }
            }

            // 计算不含特征的基础 JSON 总长度
            size_t base_faces_len = (n_faces > 0 ? (n_faces - 1) : 0) + 2;
            for (uint32_t i = 0; i < n_faces; ++i)
            {
                base_faces_len += face_base[i].size() + 1; // +1 for '}'
            }
            size_t base_total = ss.str().size() + base_faces_len;

            // 预算分配：若 base_total + total_feat_bytes <= MAX_IPC_PAYLOAD_SIZE，全量附带
            // 否则在剩余预算内按 (quality desc, index asc) 择优附加
            std::vector<bool> include_feat(n_faces, false);
            if (base_total + total_feat_bytes <= ipc::MAX_IPC_PAYLOAD_SIZE)
            {
                for (uint32_t i = 0; i < n_faces; ++i)
                {
                    if (!face_b64[i].empty())
                    {
                        include_feat[i] = true;
                    }
                }
            }
            else if (base_total < ipc::MAX_IPC_PAYLOAD_SIZE)
            {
                size_t budget = ipc::MAX_IPC_PAYLOAD_SIZE - base_total;
                std::vector<uint32_t> sorted_indices(n_faces);
                for (uint32_t i = 0; i < n_faces; ++i)
                {
                    sorted_indices[i] = i;
                }
                std::sort(sorted_indices.begin(), sorted_indices.end(), [&](uint32_t a, uint32_t b) {
                    if (scene->faces[a].quality != scene->faces[b].quality)
                    {
                        return scene->faces[a].quality > scene->faces[b].quality;
                    }
                    return a < b;
                });
                for (uint32_t idx : sorted_indices)
                {
                    if (!face_b64[idx].empty())
                    {
                        size_t cost = 17 + face_b64[idx].size();
                        if (cost <= budget)
                        {
                            include_feat[idx] = true;
                            budget -= cost;
                        }
                    }
                }
            }

            for (uint32_t i = 0; i < n_faces; ++i)
            {
                if (i > 0) ss << ",";
                ss << face_base[i];
                if (include_feat[i])
                {
                    ss << ",\"feature_b64\":\"" << face_b64[i] << "\"";
                }
                ss << "}";
            }
        }
        ss << "]}";
    }
    else
    {
        ss << ",\"task_type\":\"custom\",\"detections\":[],\"classifications\":[],\"persons\":[],\"faces\":[],\"vehicles\":[]}";
    }

    return ss.str();
}

} // namespace detail
} // namespace sdk
} // namespace zhulong
