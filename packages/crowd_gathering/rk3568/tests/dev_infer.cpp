/**
 * @file dev_infer.cpp
 * @brief 开发期自测：读 sample.jpg -> 执行聚集检测 -> 画框 -> result.jpg + stdout 明细表
 *
 * 纯逻辑单测在 `tests/unit_test.cpp`（`make unit-test`）。两者刻意分开：本文件要加载
 * 真实权重与会话，而单测必须能在没有模型、没有运行时的环境里跑，且**不得**用
 * `assert()` 判定（RelWithDebInfo 的 NDEBUG 会把它整段编译掉）。
 */

#include <zhulong/algo.hpp>

#include "analysis/spatial_clustering.hpp"
#include "analysis/temporal_tracker.hpp"
#include "common/crowd_config.hpp"
#include "pipeline/crowd_gathering.hpp"

#define STB_IMAGE_IMPLEMENTATION
#include "stb_image.h"
#define STB_IMAGE_WRITE_IMPLEMENTATION
#include "stb_image_write.h"

#include <chrono>
#include <cstdio>
#include <cstring>
#include <string>
#include <thread>
#include <vector>

namespace {

struct DetailRow {
    std::string label;
    float score{0.0f};
    float box[4]{0.0f, 0.0f, 0.0f, 0.0f};
};

void draw_box(uint8_t* canvas, int width, int height, const float box[4], uint8_t r, uint8_t g, uint8_t b,
              int thickness = 3) {
    int x1 = static_cast<int>(box[0] * static_cast<float>(width));
    int y1 = static_cast<int>(box[1] * static_cast<float>(height));
    int x2 = static_cast<int>(box[2] * static_cast<float>(width));
    int y2 = static_cast<int>(box[3] * static_cast<float>(height));
    if (x1 > x2) std::swap(x1, x2);
    if (y1 > y2) std::swap(y1, y2);
    if (x1 < 0) x1 = 0;
    if (y1 < 0) y1 = 0;
    if (x2 >= width) x2 = width - 1;
    if (y2 >= height) y2 = height - 1;

    const auto put = [&](int x, int y) {
        if (x < 0 || y < 0 || x >= width || y >= height) return;
        uint8_t* pixel = canvas + (static_cast<size_t>(y) * width + x) * 3;
        pixel[0] = r;
        pixel[1] = g;
        pixel[2] = b;
    };
    for (int t = 0; t < thickness; ++t) {
        for (int x = x1; x <= x2; ++x) {
            put(x, y1 + t);
            put(x, y2 - t);
        }
        for (int y = y1; y <= y2; ++y) {
            put(x1 + t, y);
            put(x2 - t, y);
        }
    }
}

void print_table(const std::string& sample, uint32_t count, double cost_ms, const std::vector<DetailRow>& rows) {
    std::printf("\n=== dev infer ===\n");
    std::printf("sample      : %s\n", sample.c_str());
    std::printf("result_type : detection\n");
    std::printf("count       : %u\n", count);
    std::printf("cost_ms     : %.2f\n", cost_ms);
    if (rows.empty()) {
        std::printf("(no detections)\n");
        return;
    }
    std::printf("%-24s %6s  %-34s\n", "label", "score", "box[x1,y1,x2,y2]");
    for (const auto& r : rows) {
        std::printf("%-24s %6.3f  [%.3f,%.3f,%.3f,%.3f]\n",
                    r.label.c_str(), r.score, r.box[0], r.box[1], r.box[2], r.box[3]);
    }
}

} // namespace

int main(int argc, char** argv) {
    std::string sample_path = "sample.jpg";
    std::string output_path = "build/out/result.jpg";
    std::string config_json = "{}";
    std::string task_config_json = R"({"gather_duration_seconds": 0.0, "report_person_boxes": true})";

    for (int i = 1; i < argc; ++i) {
        const std::string arg = argv[i];
        if (arg == "--sample" && i + 1 < argc) {
            sample_path = argv[++i];
        } else if (arg == "--out" && i + 1 < argc) {
            output_path = argv[++i];
        } else if (arg == "--config" && i + 1 < argc) {
            config_json = argv[++i];
        } else if (arg == "--task-config" && i + 1 < argc) {
            task_config_json = argv[++i];
        } else {
            std::fprintf(stderr, "unknown argument: %s\n", arg.c_str());
            return 2;
        }
    }

    int width = 0;
    int height = 0;
    int channels = 0;
    uint8_t* pixels = stbi_load(sample_path.c_str(), &width, &height, &channels, 3);
    if (pixels == nullptr) {
        std::fprintf(stderr, "failed to decode sample %s: %s\n", sample_path.c_str(), stbi_failure_reason());
        return 1;
    }
    const size_t pixel_bytes = static_cast<size_t>(width) * static_cast<size_t>(height) * 3;
    std::vector<uint8_t> canvas(pixels, pixels + pixel_bytes);
    stbi_image_free(pixels);

    zhulong::crowd::CrowdGatheringPlugin plugin;
    zhulong_status_t rc = plugin.init(config_json);
    if (rc != ZHULONG_PLUGIN_OK) {
        std::fprintf(stderr, "init failed: %d\n", static_cast<int>(rc));
        return 1;
    }
    zhulong::Session session = plugin.create_session(0);
    if (!session.valid()) {
        std::fprintf(stderr, "create_session failed\n");
        return 1;
    }

    zhulong_frame_view frame{};
    frame.frame_id = 1;
    frame.pts = 0;
    frame.width = width;
    frame.height = height;
    frame.format = ZHULONG_PIXEL_FORMAT_RGB24;
    frame.planes[0] = canvas.data();
    frame.strides[0] = width * 3;
    frame.dma_buf_fd = -1;

    zhulong::TaskContext task_context;
    task_context.task_id = 1;
    task_context.params.json = std::string_view(task_config_json);

    zhulong::Image image(frame);

    const auto t0 = std::chrono::steady_clock::now();
    const auto results = plugin.detect(session, image, task_context);
    const auto t1 = std::chrono::steady_clock::now();
    const double cost_ms = std::chrono::duration<double, std::milli>(t1 - t0).count();

    std::vector<DetailRow> rows;
    for (const auto& det : results) {
        DetailRow row;
        row.label = det.label;
        row.score = det.confidence;
        std::memcpy(row.box, det.box, sizeof(row.box));
        rows.push_back(row);

        if (det.label == "crowd_gathering_alarm") {
            // 聚集告警：粗红框（高亮告警区域）
            draw_box(canvas.data(), width, height, det.box, 255, 0, 0, 5);
        } else {
            // 普通行人：绿框
            draw_box(canvas.data(), width, height, det.box, 0, 255, 0, 2);
        }
    }

    print_table(sample_path, static_cast<uint32_t>(results.size()), cost_ms, rows);

    if (stbi_write_jpg(output_path.c_str(), width, height, 3, canvas.data(), 90) == 0) {
        std::fprintf(stderr, "failed to write output: %s\n", output_path.c_str());
        return 1;
    }
    std::printf("[✓] result written: %s\n", output_path.c_str());
    return 0;
}
