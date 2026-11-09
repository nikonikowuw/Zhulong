/**
 * @file plate_recognition_onnx_test.cpp
 * @brief 车牌包 ONNX 金标回归：真实权重 + 真实样本图 → 号码 / 颜色必须逐字对上
 *
 * 这是**唯一**会跑车牌的自动门禁。包内 `make test`（devtest）是给人看的：它画的框和
 * 打的明细表要肉眼判断；本用例把它变成机器结论 —— 号码读错、颜色读错、双层切分坐标
 * 漂移、级联会话配错 worker 都会让 `make check` 变红。
 *
 * 样本：`native/tests/fixtures/onnx/plate_sample.ppm`（与包根 sample.jpg 同一张图，
 * netpbm 版可由本用例直接解码，不引入图像库）。
 *
 * ⚠️ 复合断言顺序有意义：NMS 按置信度降序保留（0.770 > 0.763），因此结果顺序是稳定的；
 *    如果哪天顺序变了，多半是检测/NMS 语义改了，而不是「测试太脆」。
 */

#include <zhulong/algo.hpp>
#include <zhulong/plugin.h>

#include "pipeline/plate_recognition.hpp"
#include "plate_unit_checks.hpp"

#include <cassert>
#include <cstddef>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <fstream>
#include <string>
#include <vector>

#ifndef ZHULONG_PLATE_PKG_DIR
#error "本用例需要 -DZHULONG_PLATE_PKG_DIR=<包目录>：模型按该目录下的 models/ 解析"
#endif

namespace {

struct PpmImage {
    int width{0};
    int height{0};
    std::vector<uint8_t> rgb;
};

/** @brief 相对路径候选（ctest 的 CWD 是构建目录，手工跑通常在仓库根） */
std::string resolve_sample(const std::string& rel) {
    const std::vector<std::string> prefixes = {"", "native/", "../native/", "../../native/",
                                               "../../../native/", "../", "../../", "../../../"};
    for (const auto& prefix : prefixes) {
        std::string candidate = prefix + rel;
        if (std::ifstream(candidate).good()) {
            return candidate;
        }
    }
    return rel;
}

bool load_ppm(const std::string& path, PpmImage& out) {
    std::ifstream file(resolve_sample(path), std::ios::binary);
    if (!file.is_open()) {
        return false;
    }
    std::string magic;
    int max_value = 0;
    if (!(file >> magic) || magic != "P6") {
        return false;
    }
    if (!(file >> out.width >> out.height >> max_value) || out.width <= 0 || out.height <= 0 || max_value <= 0) {
        return false;
    }
    file.get(); // 跳过分隔符
    out.rgb.resize(static_cast<std::size_t>(out.width) * static_cast<std::size_t>(out.height) * 3);
    file.read(reinterpret_cast<char*>(out.rgb.data()), static_cast<std::streamsize>(out.rgb.size()));
    return file.gcount() == static_cast<std::streamsize>(out.rgb.size());
}

zhulong_frame_view make_frame(PpmImage& image) {
    zhulong_frame_view frame;
    std::memset(&frame, 0, sizeof(frame));
    frame.frame_id = 1;
    frame.width = image.width;
    frame.height = image.height;
    frame.format = ZHULONG_PIXEL_FORMAT_RGB24;
    frame.planes[0] = image.rgb.data();
    frame.strides[0] = image.width * 3;
    frame.dma_buf_fd = -1;
    return frame;
}

} // namespace

int main(int argc, char** argv) {
    // 1. 纯逻辑先过（不加载模型）：采样坐标 / 号牌结构 / 帧视图守卫
    const int pure_failures = zhulong::plate::test::run_pure_unit_checks();
    if (pure_failures != 0) {
        std::fprintf(stderr, "[✗] pure checks failed: %d\n", pure_failures);
        return 1;
    }

    // 2. 模型目录：包内 models/。显式环境变量优先（开发者可能想换成另一套权重），
    //    因此 setenv 不覆盖已有值。
    (void)::setenv("ZHULONG_PLATE_MODEL_DIR", ZHULONG_PLATE_PKG_DIR, /*overwrite=*/0);

    const std::string sample = argc > 1 ? argv[1] : "tests/fixtures/onnx/plate_sample.ppm";
    PpmImage image;
    if (!load_ppm(sample, image)) {
        std::fprintf(stderr, "无法读取样本 %s（P6 netpbm）\n", resolve_sample(sample).c_str());
        return 1;
    }

    zhulong::plate::PlateRecognitionPlugin plugin;
    const zhulong_status_t rc = plugin.init("{}");
    if (rc != ZHULONG_PLUGIN_OK) {
        std::fprintf(stderr, "init failed: %d（models/ 是否在 %s 下？）\n", static_cast<int>(rc),
                     ZHULONG_PLATE_PKG_DIR);
        return 1;
    }

    zhulong::Session session = plugin.create_session(0);
    assert(session.valid());

    zhulong_frame_view frame = make_frame(image);
    zhulong::TaskContext task_context;
    task_context.task_id = 1;

    // 3. 金标：样本图里两块蓝牌，号码逐字断言
    const auto vehicles = plugin.detect_vehicles(session, zhulong::Image(frame), task_context);
    assert(vehicles.size() == 2);

    const std::vector<std::string> expected_numbers = {"豫B2V9L7", "辽DU4356"};
    for (std::size_t i = 0; i < vehicles.size() && i < expected_numbers.size(); ++i) {
        const auto& vehicle = vehicles[i];
        // 退化模式语义：车 == 车牌框，无跟踪 ID，车型未知
        assert(vehicle.track_id == 0);
        assert(vehicle.plates.size() == 1);
        assert(vehicle.vehicle_type == "unknown");
        assert(vehicle.vehicle_score > 0.0f && vehicle.vehicle_score <= 1.0f);

        const auto& plate = vehicle.plates[0];
        if (plate.plate_number != expected_numbers[i]) {
            std::fprintf(stderr, "第 %zu 块车牌号码不符：期望 %s，实际 %s\n", i, expected_numbers[i].c_str(),
                         plate.plate_number.c_str());
        }
        assert(plate.plate_number == expected_numbers[i]);
        assert(plate.plate_color == "蓝色");
        assert(plate.plate_type == static_cast<int32_t>(zhulong::plate::PlateKind::SingleLine));
        // plate.score 是字符识别置信度，vehicle_score 是检测置信度：两者语义不同、不可互换
        assert(plate.score > 0.0f && plate.score <= 1.0f);

        // 归一化框：与车身框逐位一致（退化语义），且在 [0,1] 内且非退化
        for (std::size_t k = 0; k < 4; ++k) {
            assert(plate.box[k] == vehicle.vehicle_box[k]);
            assert(plate.box[k] >= 0.0f && plate.box[k] <= 1.0f);
        }
        assert(plate.box[2] > plate.box[0]);
        assert(plate.box[3] > plate.box[1]);
    }

    // 4. 级联会话按 worker_id 配对：换一个 worker 也必须能拿到识别会话，
    //    否则「检测出框、识别恒空」这种半死状态不会被任何现有用例发现。
    zhulong::Session other_worker = plugin.create_session(7);
    assert(other_worker.valid());
    const auto other_vehicles = plugin.detect_vehicles(other_worker, zhulong::Image(frame), task_context);
    assert(other_vehicles.size() == vehicles.size());
    assert(other_vehicles[0].plates[0].plate_number == expected_numbers[0]);

    // 5. 阈值通道：0.99 远高于样本的检测置信度（0.77），必须一块都不报。
    //    这同时证明「config_json 真的被应用了」，而不是恒用默认 0.35。
    assert(plugin.init(R"({"confidence_threshold": 0.99})") == ZHULONG_PLUGIN_OK);
    const auto suppressed = plugin.detect_vehicles(session, zhulong::Image(frame), task_context);
    assert(suppressed.empty());

    std::printf("[✓] plate onnx gold: %s / %s\n", expected_numbers[0].c_str(), expected_numbers[1].c_str());
    return 0;
}
