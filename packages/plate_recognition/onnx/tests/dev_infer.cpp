/**
 * @file dev_infer.cpp
 * @brief 开发期自测：包根 `sample.jpg` → 本 target 产品代码 → 画框 → `result.jpg` + 终端明细表
 *
 * 这个可执行文件**只服务开发期**，不是交付物：
 *   - `add_executable(... EXCLUDE_FROM_ALL)`，不进安装规则，物理上进不了算法包归档；
 *   - 依赖 stb（header-only），产品代码零依赖。
 *
 * 与宿主黑盒验包（`<manifest.id> --verify`）的分工：
 *   - 验包证明「ABI 生命周期通、能跑出结果」，但它不含图像库，只能吃 PPM 或合成帧；
 *   - 本工具给开发者一张**画好的图**，用眼睛判断框和号码对不对，跨 target 不比数值
 *     （量化不同，结果本就不同）。
 *
 * 纯逻辑断言（采样坐标 / 号牌结构 / 帧视图守卫）与仓库 native 金标测试共用
 * `tests/plate_unit_checks.hpp`，不在这里复制第二份。
 *
 * 用法（在包目录内）：
 *     make test
 *     ./build/plate_recognition_devtest --sample sample.jpg --out build/out/result.jpg
 *
 * 输出：`result.jpg`（vehicle/plate 框）+ stdout 明细表；纯逻辑断言有任何一条失败时
 *       退出码非 0（**不再依赖 assert**：RelWithDebInfo 的 -DNDEBUG 会把断言整块编译掉）。
 */

#include <zhulong/algo.hpp>

#include "pipeline/plate_recognition.hpp"
#include "plate_unit_checks.hpp"

#define STB_IMAGE_IMPLEMENTATION
#include "stb_image.h"
#define STB_IMAGE_WRITE_IMPLEMENTATION
#include "stb_image_write.h"

#include <chrono>
#include <cstddef>
#include <cstdio>
#include <cstring>
#include <utility>
#include <string>
#include <vector>

namespace {

/** @brief 终端明细表的一行 */
struct DetailRow {
    std::string kind;
    std::string label;
    float score{0.0f};
    float box[4]{0.0f, 0.0f, 0.0f, 0.0f};
    std::string text;
};

/**
 * @brief 在 RGB24 缓冲上画一个矩形框（归一化坐标 → 像素，越界裁剪）
 *
 * 不引字体：label 打不出来就打到终端表里。图的作用是「框对不对」，
 * 字号/字体/渲染顺序一旦进断言就会天天假红。
 */
void draw_box(uint8_t* canvas, int width, int height, const float box[4], uint8_t r, uint8_t g, uint8_t b,
              int thickness = 2) {
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
        uint8_t* pixel = canvas + (static_cast<std::size_t>(y) * static_cast<std::size_t>(width) +
                                   static_cast<std::size_t>(x)) * 3;
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

void print_table(const std::string& sample, const char* result_type_name, std::size_t count,
                 double cost_ms, const std::vector<DetailRow>& rows) {
    std::printf("\n=== dev infer ===\n");
    std::printf("sample      : %s\n", sample.c_str());
    std::printf("result_type : %s\n", result_type_name);
    std::printf("count       : %zu\n", count);
    std::printf("cost_ms     : %.2f\n", cost_ms);
    if (rows.empty()) {
        std::printf("(no rows)\n");
        return;
    }
    std::printf("%-8s %-16s %6s  %-34s %s\n", "kind", "label", "score", "box[x1,y1,x2,y2]", "text");
    for (const auto& row : rows) {
        std::printf("%-8s %-16s %6.3f  [%.3f,%.3f,%.3f,%.3f] %s\n", row.kind.c_str(), row.label.c_str(), row.score,
                    row.box[0], row.box[1], row.box[2], row.box[3], row.text.c_str());
    }
}

} // namespace

int main(int argc, char** argv) {
    // 纯逻辑自查先跑：坐标映射、号牌结构、帧视图守卫（不加载模型、不建会话）
    const int pure_failures = zhulong::plate::test::run_pure_unit_checks();
    if (pure_failures == 0) {
        std::printf("[✓] plate algorithm pure checks passed\n");
    } else {
        std::fprintf(stderr, "[✗] plate algorithm pure checks failed: %d\n", pure_failures);
    }

    std::string sample_path = "sample.jpg";
    std::string output_path = "build/out/result.jpg";
    std::string config_json = "{}";
    for (int i = 1; i < argc; ++i) {
        const std::string arg = argv[i];
        if (arg == "--sample" && i + 1 < argc) {
            sample_path = argv[++i];
        } else if (arg == "--out" && i + 1 < argc) {
            output_path = argv[++i];
        } else if (arg == "--config" && i + 1 < argc) {
            config_json = argv[++i];
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
    const std::size_t pixel_bytes =
        static_cast<std::size_t>(width) * static_cast<std::size_t>(height) * 3;
    std::vector<uint8_t> canvas(pixels, pixels + pixel_bytes);
    stbi_image_free(pixels);

    // 直接调用本 target 的产品类（不走 C ABI 表）：dev 工具就该像算法作者那样用它。
    zhulong::plate::PlateRecognitionPlugin plugin;
    zhulong_status_t rc = plugin.init(config_json);
    if (rc != ZHULONG_PLUGIN_OK) {
        std::fprintf(stderr, "init failed: %d (models/ 是否与可执行文件同级？)\n", static_cast<int>(rc));
        return 1;
    }
    zhulong::Session session = plugin.create_session(0);
    if (!session.valid()) {
        std::fprintf(stderr, "create_session failed\n");
        return 1;
    }

    zhulong_frame_view frame;
    std::memset(&frame, 0, sizeof(frame));
    frame.frame_id = 1;
    frame.pts = 0;
    frame.width = width;
    frame.height = height;
    frame.format = ZHULONG_PIXEL_FORMAT_RGB24;
    frame.planes[0] = canvas.data();
    frame.strides[0] = width * 3;
    frame.dma_buf_fd = -1;

    // 与产品路径一致：算法方法需要 TaskContext（dev 环境只填 task_id，params 用默认值）
    zhulong::TaskContext task_context;
    task_context.task_id = 1;

    const auto started = std::chrono::steady_clock::now();
    const auto vehicles = plugin.detect_vehicles(session, zhulong::Image(frame), task_context);
    const double cost_ms =
        std::chrono::duration<double, std::milli>(std::chrono::steady_clock::now() - started).count();

    std::vector<DetailRow> rows;
    for (const auto& vehicle : vehicles) {
        DetailRow row_vehicle;
        row_vehicle.kind = "vehicle";
        row_vehicle.label = vehicle.vehicle_type;
        row_vehicle.score = vehicle.vehicle_score;
        std::memcpy(row_vehicle.box, vehicle.vehicle_box, sizeof(row_vehicle.box));
        draw_box(canvas.data(), width, height, row_vehicle.box, 0, 255, 0);
        rows.push_back(row_vehicle);

        for (const auto& plate : vehicle.plates) {
            DetailRow row_plate;
            row_plate.kind = "plate";
            row_plate.label = plate.plate_color;
            row_plate.score = plate.score;
            std::memcpy(row_plate.box, plate.box, sizeof(row_plate.box));
            row_plate.text = plate.plate_number;
            draw_box(canvas.data(), width, height, row_plate.box, 0, 128, 255);
            rows.push_back(row_plate);
        }
    }

    const char* result_type_name = "vehicle_structure";
    print_table(sample_path, result_type_name, vehicles.size(), cost_ms, rows);
    std::printf("result written: %s\n", output_path.c_str());

    if (stbi_write_jpg(output_path.c_str(), width, height, 3, canvas.data(), 92) == 0) {
        std::fprintf(stderr, "failed to write %s\n", output_path.c_str());
        return 1;
    }
    return pure_failures == 0 ? 0 : 1;
}
