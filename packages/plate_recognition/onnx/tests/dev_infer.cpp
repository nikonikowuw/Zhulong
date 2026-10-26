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
#include <zhulong/plugin.h>

#define STB_IMAGE_IMPLEMENTATION
#include "stb_image.h"
#define STB_IMAGE_WRITE_IMPLEMENTATION
#include "stb_image_write.h"

#include <chrono>
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

/** @brief 取得本包导出的 C ABI 函数表（与产品路径完全一致的那张表） */
zhulong_status_t load_api(zhulong_plugin_api_v1& api) {
    std::memset(&api, 0, sizeof(api));
    return zhulong_plugin_get_api(1, &api, sizeof(api));
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

} // namespace

int main(int argc, char** argv) {
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

    zhulong_plugin_api_v1 api;
    zhulong_status_t rc = load_api(api);
    if (rc != ZHULONG_PLUGIN_OK) {
        std::fprintf(stderr, "zhulong_plugin_get_api failed: %d\n", static_cast<int>(rc));
        return 1;
    }

    zhulong_model_group_h model = nullptr;
    if ((rc = api.model_create(config_json.c_str(), &model)) != ZHULONG_PLUGIN_OK) {
        std::fprintf(stderr, "model_create failed: %d (models/ 是否与可执行文件同级？)\n", static_cast<int>(rc));
        return 1;
    }
    zhulong_worker_ctx_h worker = nullptr;
    if ((rc = api.worker_create(model, 0, &worker)) != ZHULONG_PLUGIN_OK) {
        std::fprintf(stderr, "worker_create failed: %d\n", static_cast<int>(rc));
        api.model_destroy(model);
        return 1;
    }
    zhulong_task_ctx_h task = nullptr;
    if ((rc = api.task_open(worker, 1, &task)) != ZHULONG_PLUGIN_OK) {
        std::fprintf(stderr, "task_open failed: %d\n", static_cast<int>(rc));
        api.worker_destroy(worker);
        api.model_destroy(model);
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

    zhulong_inference_result* result = nullptr;
    const auto started = std::chrono::steady_clock::now();
    rc = api.process(task, &frame, &result);
    const double cost_ms =
        std::chrono::duration<double, std::milli>(std::chrono::steady_clock::now() - started).count();

    std::vector<DetailRow> rows;
    const char* result_type_name = "unknown";
    uint32_t count = 0;
    if (rc == ZHULONG_PLUGIN_OK && result != nullptr) {
        count = result->count;
        switch (result->result_type) {
            case ZHULONG_RESULT_TYPE_DETECTION: {
                result_type_name = "detection";
                const auto* items = static_cast<const zhulong_bbox*>(result->data);
                for (uint32_t i = 0; i < result->count; ++i) {
                    DetailRow row;
                    row.kind = "box";
                    row.label = items[i].label;
                    row.score = items[i].confidence;
                    std::memcpy(row.box, items[i].box, sizeof(row.box));
                    draw_box(canvas.data(), width, height, row.box, 0, 255, 0);
                    rows.push_back(row);
                }
                break;
            }
            case ZHULONG_RESULT_TYPE_VEHICLE_STRUCTURE: {
                result_type_name = "vehicle_structure";
                const auto* vehicles = static_cast<const zhulong_vehicle_item*>(result->data);
                for (uint32_t i = 0; i < result->count; ++i) {
                    DetailRow vehicle;
                    vehicle.kind = "vehicle";
                    vehicle.label = vehicles[i].vehicle_type;
                    vehicle.score = vehicles[i].vehicle_score;
                    std::memcpy(vehicle.box, vehicles[i].vehicle_box, sizeof(vehicle.box));
                    draw_box(canvas.data(), width, height, vehicle.box, 0, 255, 0);
                    rows.push_back(vehicle);

                    for (uint32_t p = 0; p < vehicles[i].plate_count; ++p) {
                        const zhulong_plate_item& plate = vehicles[i].plates[p];
                        DetailRow row;
                        row.kind = "plate";
                        row.label = plate.plate_color;
                        row.score = plate.score;
                        std::memcpy(row.box, plate.box, sizeof(row.box));
                        row.text = plate.plate_number;
                        draw_box(canvas.data(), width, height, row.box, 0, 128, 255);
                        rows.push_back(row);
                    }
                }
                break;
            }
            case ZHULONG_RESULT_TYPE_PERSON_STRUCTURE: {
                result_type_name = "person_structure";
                const auto* persons = static_cast<const zhulong_person_item*>(result->data);
                for (uint32_t i = 0; i < result->count; ++i) {
                    DetailRow person;
                    person.kind = "person";
                    person.label = "body";
                    person.score = persons[i].body_score;
                    std::memcpy(person.box, persons[i].body_box, sizeof(person.box));
                    draw_box(canvas.data(), width, height, person.box, 0, 255, 0);
                    rows.push_back(person);

                    for (uint32_t f = 0; f < persons[i].face_count; ++f) {
                        const zhulong_face_item& face = persons[i].faces[f];
                        DetailRow row;
                        row.kind = "face";
                        row.label = face.matched ? "matched" : "unmatched";
                        row.score = face.quality;
                        std::memcpy(row.box, face.box, sizeof(row.box));
                        if (face.person_name[0] != '\0') {
                            row.text = face.person_name;
                        }
                        draw_box(canvas.data(), width, height, row.box, 255, 128, 0);
                        rows.push_back(row);
                    }
                }
                break;
            }
            case ZHULONG_RESULT_TYPE_CLASSIFICATION: {
                result_type_name = "classification";
                const auto* items = static_cast<const zhulong_classification_item*>(result->data);
                for (uint32_t i = 0; i < result->count; ++i) {
                    DetailRow row;
                    row.kind = "class";
                    row.label = items[i].label;
                    row.score = items[i].confidence;
                    rows.push_back(row);
                }
                break;
            }
            default:
                break;
        }
    } else {
        std::fprintf(stderr, "process failed: %d\n", static_cast<int>(rc));
    }

    print_table(sample_path, result_type_name, count, cost_ms, rows);
    std::printf("result written: %s\n", output_path.c_str());

    if (stbi_write_jpg(output_path.c_str(), width, height, 3, canvas.data(), 92) == 0) {
        std::fprintf(stderr, "failed to write %s\n", output_path.c_str());
        if (result != nullptr) api.result_release(result);
        api.task_close(task);
        api.worker_destroy(worker);
        api.model_destroy(model);
        return 1;
    }

    if (result != nullptr) api.result_release(result);
    api.task_close(task);
    api.worker_destroy(worker);
    api.model_destroy(model);
    return rc == ZHULONG_PLUGIN_OK ? 0 : 1;
}
