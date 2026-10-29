/**
 * @file dev_infer.cpp
 * @brief 开发期自测：包根 `sample.jpg` → 本 target 产品代码 → 画框 → `result.jpg` + 终端明细表
 *
 * 这个可执行文件**只服务开发期**，不是交付物：
 *   - `add_executable(... EXCLUDE_FROM_ALL)`，不进安装规则，物理上进不了算法包归档；
 *   - 依赖 stb（header-only），产品代码零依赖。
 *
 * 与宿主黑盒验包（`zhulong_algo_runner --verify-plugin`）的分工：
 *   - 验包证明「ABI 生命周期通、能跑出结果」，但它不含图像库，只能吃 PPM 或合成帧；
 *   - 本工具给开发者一张**画好的图**，用眼睛判断框和号码对不对，跨 target 不比数值
 *     （量化不同，结果本就不同）。
 *
 * 用法（在包目录内）：
 *     make test
 *     ./build/<id>_devtest --sample sample.jpg --out build/out/result.jpg
 *
 * 输出：`result.jpg`（vehicle/plate/face 框与标签）+ stdout 明细表。
 */

#include <zhulong/algo.hpp>

#include "plate_format.hpp"
#include "plate_recognition.hpp"
#include "plate_recognizer.hpp"

#define STB_IMAGE_IMPLEMENTATION
#include "stb_image.h"
#define STB_IMAGE_WRITE_IMPLEMENTATION
#include "stb_image_write.h"

#include <chrono>
#include <cmath>
#include <cassert>
#include <cstdio>
#include <cstring>
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

void print_table(const std::string& sample, const char* result_type_name, uint32_t count,
                 double cost_ms, const std::vector<DetailRow>& rows) {
    std::printf("\n=== dev infer ===\n");
    std::printf("sample      : %s\n", sample.c_str());
    std::printf("result_type : %s\n", result_type_name);
    std::printf("count       : %u\n", count);
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

void run_internal_unit_tests() {
    using namespace zhulong::plate;

    // 1. 双层车牌切块重拼的坐标映射单测
    constexpr int kTargetW = kRecognitionWidth;
    constexpr int kTargetH = kRecognitionHeight;
    const float crop_x = 100.0f;
    const float crop_y = 200.0f;
    const float crop_w = static_cast<float>(kTargetW);
    const float crop_h = static_cast<float>(kTargetH);

    const auto single = [&](int x, int y) {
        return plate_sample_point(x, y, kTargetW, kTargetH, crop_x, crop_y,
                                  crop_w, crop_h,
                                  static_cast<int32_t>(PlateLineLayout::SingleLine));
    };
    const auto double_line = [&](int x, int y) {
        return plate_sample_point(x, y, kTargetW, kTargetH, crop_x, crop_y,
                                  crop_w, crop_h,
                                  static_cast<int32_t>(PlateLineLayout::DoubleLine));
    };
    const auto new_energy = [&](int x, int y) {
        return plate_sample_point(x, y, kTargetW, kTargetH, crop_x, crop_y,
                                  crop_w, crop_h,
                                  static_cast<int32_t>(PlateLineLayout::NewEnergy));
    };

    // 单层恒等映射
    for (int y = 0; y < kTargetH; y += 7) {
        for (int x = 0; x < kTargetW; x += 13) {
            const auto point = single(x, y);
            assert(std::fabs(point.fx - (crop_x + static_cast<float>(x))) < 1e-4f);
            assert(std::fabs(point.fy - (crop_y + static_cast<float>(y))) < 1e-4f);
        }
    }
    const auto energy_point = new_energy(10, 10);
    const auto single_point = single(10, 10);
    assert(std::fabs(energy_point.fx - single_point.fx) < 1e-4f);
    assert(std::fabs(energy_point.fy - single_point.fy) < 1e-4f);

    constexpr float kPixelCenter = 0.5f;
    const float upper_band_end = crop_y + crop_h * 5.0f / 12.0f;
    const float lower_band_start = crop_y + crop_h / 3.0f;
    for (int y = 0; y < kTargetH; y += 5) {
        assert(double_line(0, y).fy < upper_band_end);
        assert(double_line(kTargetW / 2 - 1, y).fy < upper_band_end);
        assert(double_line(kTargetW / 2, y).fy >= lower_band_start - kPixelCenter);
        assert(double_line(kTargetW - 1, y).fy >= lower_band_start - kPixelCenter);
        assert(double_line(kTargetW - 1, y).fy < crop_y + crop_h);
    }

    // 2. 号码结构规则校验
    assert(validate_plate_number("豫B2V9L7").valid);
    assert(validate_plate_number("辽DU4356").valid);
    assert(validate_plate_number("京A12345").valid);
    assert(validate_plate_number("粤AD12345").valid);
    assert(validate_plate_number("粤AF12345").valid);
    assert(validate_plate_number("京A12345D").valid);
    assert(validate_plate_number("京A12345F").valid);
    assert(validate_plate_number("京A1234警").valid);
    assert(validate_plate_number("粤B1234学").valid);
    assert(validate_plate_number("冀B1234挂").valid);
    assert(validate_plate_number("使123456").valid);
    assert(validate_plate_number("领123456").valid);

    assert(validate_plate_number("").problem == PlateFormatProblem::BadLength);
    assert(validate_plate_number("京A12").problem == PlateFormatProblem::BadLength);
    assert(validate_plate_number("京A12345678").problem == PlateFormatProblem::BadLength);
    assert(validate_plate_number("美A12345").problem == PlateFormatProblem::BadProvince);
    assert(validate_plate_number("京I12345").problem == PlateFormatProblem::BadAuthority);
    assert(validate_plate_number("京O12345").problem == PlateFormatProblem::BadAuthority);
    assert(validate_plate_number("京A1学345").problem == PlateFormatProblem::BadCharacter);
    assert(validate_plate_number("京A123456").problem == PlateFormatProblem::BadNewEnergyLayout);
    assert(validate_plate_number("\xE4\xBA").problem == PlateFormatProblem::InvalidUtf8);

    // 3. 负 stride 显式拒绝
    PlateRecognizer recognizer;
    zhulong::Session session;
    zhulong_frame_view bad_stride_frame;
    std::memset(&bad_stride_frame, 0, sizeof(bad_stride_frame));
    uint8_t dummy[100] = {0};
    bad_stride_frame.planes[0] = dummy;
    bad_stride_frame.width = 100;
    bad_stride_frame.height = 100;
    bad_stride_frame.format = ZHULONG_PIXEL_FORMAT_RGB24;
    bad_stride_frame.strides[0] = -300;
    const auto bad_result = recognizer.recognize(session, bad_stride_frame, {0.1f, 0.1f, 0.5f, 0.5f}, 0);
    assert(bad_result.plate_number.empty());

    std::printf("[✓] plate algorithm internal unit asserts passed\n");
}

} // namespace

int main(int argc, char** argv) {
    // 运行算法内部纯函数单测（坐标映射、号牌结构、负 stride 防护）
    run_internal_unit_tests();

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
    const size_t pixel_bytes = static_cast<size_t>(width) * static_cast<size_t>(height) * 3;
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
    const uint32_t count = static_cast<uint32_t>(vehicles.size());
    print_table(sample_path, result_type_name, count, cost_ms, rows);
    std::printf("result written: %s\n", output_path.c_str());

    if (stbi_write_jpg(output_path.c_str(), width, height, 3, canvas.data(), 92) == 0) {
        std::fprintf(stderr, "failed to write %s\n", output_path.c_str());
        return 1;
    }
    return 0;
}
