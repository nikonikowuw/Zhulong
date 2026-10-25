/**
 * @file plate_recognition_plugin_test.cpp
 * @brief 车牌识别插件测试：C ABI 生命周期 + 双层切块坐标映射单测 + 真实样本金标回归
 *
 * 三层断言的分工（缺一层就会出现「零检出却绿灯」的假绿）：
 *   1. ABI 生命周期：dlopen → model_create → worker_create → task_open → process → release；
 *   2. 坐标映射单测：双层车牌的上下半重拼是全包最绕的一段算术，直接钉住采样坐标；
 *   3. 真实样本金标：把 packages/plate_recognition/sample.jpg 转成的 PPM 喂进全链路，
 *      钉住车牌号、颜色与检出数 —— 通道顺序、字符表、CTC 解码任何一处漂移都会失败。
 */

#include <zhulong/algo.hpp>
#include <zhulong/plugin.h>

#include "plate_recognizer.hpp"

#include <cassert>
#include <cmath>
#include <cstring>
#include <dlfcn.h>
#include <fstream>
#include <iostream>
#include <string>
#include <vector>

namespace {

/** @brief 测试样本图（P6 PPM，统一 RGB24） */
struct PpmImage {
    int width{0};
    int height{0};
    std::vector<uint8_t> rgb;
};

/** @brief 在多种构建/运行目录下定位测试样本 */
std::string resolve_test_file(const std::string& rel) {
    const std::vector<std::string> prefixes = {
        "",
        "native/",
        "../native/",
        "../../native/",
        "../../../native/",
        "../",
        "../../",
        "../../../",
    };
    for (const auto& prefix : prefixes) {
        std::string candidate = prefix + rel;
        if (std::ifstream(candidate).good()) {
            return candidate;
        }
    }
    return rel;
}

bool load_ppm(const std::string& path, PpmImage& out) {
    const std::string resolved = resolve_test_file(path);
    std::ifstream input(resolved, std::ios::binary);
    if (!input.is_open()) {
        return false;
    }
    std::string magic;
    int max_value = 0;
    if (!(input >> magic) || magic != "P6") {
        return false;
    }
    if (!(input >> out.width >> out.height >> max_value) || out.width <= 0 || out.height <= 0 || max_value <= 0) {
        return false;
    }
    input.get(); // 头部与像素数据之间的单个空白符
    out.rgb.resize(static_cast<size_t>(out.width) * static_cast<size_t>(out.height) * 3);
    input.read(reinterpret_cast<char*>(out.rgb.data()), static_cast<std::streamsize>(out.rgb.size()));
    return input.gcount() == static_cast<std::streamsize>(out.rgb.size());
}

/** @brief 当前构建是否为测试夹具（test_stub）——编译期事实，不做运行期探测 */
constexpr bool kTestStubBuild =
#if defined(ZHULONG_INFERENCE_TEST_STUB)
    true;
#else
    false;
#endif

zhulong_status_t load_plugin(const char* so_path, void** out_handle, zhulong_plugin_api_v1* out_api) {
    void* handle = dlopen(so_path, RTLD_NOW | RTLD_LOCAL);
    if (!handle) {
        std::cerr << "dlopen failed: " << dlerror() << std::endl;
        return ZHULONG_PLUGIN_ERR_NOT_FOUND;
    }
    *out_handle = handle;

    auto get_api = reinterpret_cast<zhulong_status_t (*)(uint32_t, void*, size_t)>(
        dlsym(handle, ZHULONG_PLUGIN_ENTRY_POINT)
    );
    if (!get_api) {
        dlclose(handle);
        *out_handle = nullptr;
        return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
    }

    std::memset(out_api, 0, sizeof(*out_api));
    const zhulong_status_t rc = get_api(1, out_api, sizeof(*out_api));
    if (rc != ZHULONG_PLUGIN_OK) {
        dlclose(handle);
        *out_handle = nullptr;
    }
    return rc;
}

/** @brief 用给定帧执行一次完整推理；失败返回 nullptr */
zhulong_inference_result* run_once(const zhulong_plugin_api_v1& api, zhulong_task_ctx_h task,
                                   const PpmImage& image) {
    zhulong_frame_view frame;
    std::memset(&frame, 0, sizeof(frame));
    frame.frame_id = 7;
    frame.pts = 1000;
    frame.width = image.width;
    frame.height = image.height;
    frame.format = ZHULONG_PIXEL_FORMAT_RGB24;
    frame.planes[0] = image.rgb.data();
    frame.strides[0] = image.width * 3;
    frame.dma_buf_fd = -1;

    zhulong_inference_result* result = nullptr;
    const zhulong_status_t rc = api.process(task, &frame, &result);
    if (rc != ZHULONG_PLUGIN_OK) {
        return nullptr;
    }
    return result;
}

/**
 * @brief 双层车牌切块重拼的坐标映射单测
 *
 * 用 1:1 的裁剪框（168×48 原图区域 → 168×48 模型输入），单层映射必须严格是恒等，
 * 双层映射必须满足「左半 84 列取自上半 5/12、右半 84 列取自下半 2/3」。
 */
void test_sample_point_mapping() {
    constexpr int kTargetW = zhulong::plate::kRecognitionWidth;
    constexpr int kTargetH = zhulong::plate::kRecognitionHeight;
    const float crop_x = 100.0f;
    const float crop_y = 200.0f;
    const float crop_w = static_cast<float>(kTargetW);
    const float crop_h = static_cast<float>(kTargetH);

    const auto single = [&](int x, int y) {
        return zhulong::plate::plate_sample_point(x, y, kTargetW, kTargetH, crop_x, crop_y,
                                                  crop_w, crop_h,
                                                  static_cast<int32_t>(zhulong::plate::PlateLineLayout::SingleLine));
    };
    const auto double_line = [&](int x, int y) {
        return zhulong::plate::plate_sample_point(x, y, kTargetW, kTargetH, crop_x, crop_y,
                                                  crop_w, crop_h,
                                                  static_cast<int32_t>(zhulong::plate::PlateLineLayout::DoubleLine));
    };
    const auto new_energy = [&](int x, int y) {
        return zhulong::plate::plate_sample_point(x, y, kTargetW, kTargetH, crop_x, crop_y,
                                                  crop_w, crop_h,
                                                  static_cast<int32_t>(zhulong::plate::PlateLineLayout::NewEnergy));
    };

    // 单层 + 1:1 裁剪 ⇒ 恒等映射
    for (int y = 0; y < kTargetH; y += 7) {
        for (int x = 0; x < kTargetW; x += 13) {
            const auto point = single(x, y);
            assert(std::fabs(point.fx - (crop_x + static_cast<float>(x))) < 1e-4f);
            assert(std::fabs(point.fy - (crop_y + static_cast<float>(y))) < 1e-4f);
        }
    }

    // 新能源按单层采样（字符位更多，但重排规则相同）
    const auto energy_point = new_energy(10, 10);
    const auto single_point = single(10, 10);
    assert(std::fabs(energy_point.fx - single_point.fx) < 1e-4f);
    assert(std::fabs(energy_point.fy - single_point.fy) < 1e-4f);

    // 双层：左半覆盖整宽 × 上半 5/12；右半覆盖整宽 × 下半 2/3
    // 采样坐标是**像素中心**语义，因此边界允许半像素偏移
    constexpr float kPixelCenter = 0.5f;
    const float upper_band_end = crop_y + crop_h * 5.0f / 12.0f;
    const float lower_band_start = crop_y + crop_h / 3.0f;

    for (int y = 0; y < kTargetH; y += 5) {
        // 左半（x < 84）全部取自上半带
        assert(double_line(0, y).fy < upper_band_end);
        assert(double_line(kTargetW / 2 - 1, y).fy < upper_band_end);
        // 右半（x >= 84）全部取自下半带，且不得超出车牌框下边界
        assert(double_line(kTargetW / 2, y).fy >= lower_band_start - kPixelCenter);
        assert(double_line(kTargetW - 1, y).fy >= lower_band_start - kPixelCenter);
        assert(double_line(kTargetW - 1, y).fy < crop_y + crop_h);
    }

    // 左右两半各自横向铺满整个车牌框
    const float left_first = double_line(0, 0).fx;
    const float left_last = double_line(kTargetW / 2 - 1, 0).fx;
    const float right_first = double_line(kTargetW / 2, 0).fx;
    const float right_last = double_line(kTargetW - 1, 0).fx;
    assert(left_first < left_last);
    assert(right_first < right_last);
    assert(std::fabs(left_first - right_first) < 1e-4f);
    assert(std::fabs(left_last - right_last) < 1e-4f);
    assert(right_first < crop_x + crop_w * 0.1f);
    assert(right_last > crop_x + crop_w * 0.9f);

    std::cout << "[✓] plate_sample_point mapping asserts passed" << std::endl;
}

/**
 * @brief ABI / 生命周期冒烟：合成灰帧不得产生任何检出
 */
void test_abi_lifecycle(const zhulong_plugin_api_v1& api, const std::string& plugin_so) {
    assert(api.api_version == 1);
    assert(std::strcmp(api.plugin_name, "PlateRecognition") == 0);
    assert(std::strcmp(api.plugin_version, "1.0.0") == 0);

    zhulong_model_group_h model = nullptr;
    zhulong_status_t rc = api.model_create("{}", &model);
    if (kTestStubBuild) {
        // 夹具构建没有真实推理能力：真实模型必须被硬拒绝——这是正确行为，不是缺陷。
        // NOT_SUPPORTED = 模型找到但桩不接受；NOT_FOUND = 该工作目录找不到模型。
        assert((rc == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED || rc == ZHULONG_PLUGIN_ERR_NOT_FOUND) &&
               model == nullptr);
        std::cout << "[OK] " << plugin_so << " lifecycle: real model rejected by test_stub build" << std::endl;
        return;
    }
    assert(rc == ZHULONG_PLUGIN_OK && model != nullptr);

    zhulong_worker_ctx_h worker = nullptr;
    rc = api.worker_create(model, 0, &worker);
    assert(rc == ZHULONG_PLUGIN_OK && worker != nullptr);

    zhulong_task_ctx_h task = nullptr;
    rc = api.task_open(worker, 1, &task);
    assert(rc == ZHULONG_PLUGIN_OK && task != nullptr);

    PpmImage gray;
    gray.width = 640;
    gray.height = 480;
    gray.rgb.assign(static_cast<size_t>(gray.width) * static_cast<size_t>(gray.height) * 3, 128);

    zhulong_inference_result* result = run_once(api, task, gray);
    assert(result != nullptr);
    assert(result->result_type == ZHULONG_RESULT_TYPE_VEHICLE_STRUCTURE);
    assert(result->count == 0);
    api.result_release(result);

    api.task_close(task);
    api.worker_destroy(worker);
    api.model_destroy(model);
    std::cout << "[✓] plate plugin ABI/lifecycle passed" << std::endl;
}

/**
 * @brief 真实样本金标回归：钉住检出数、车牌号与车牌颜色
 *
 * ⚠️ 车牌号是**回归基准值**（由与 scripts/test_infer.py 一致的预处理产出），不是人工
 * 标注真值；它保证通道顺序（BGR）、字符表与 CTC 解码不会悄悄漂移。颜色则由样本
 * 像素本身佐证：两块车牌区域的 B 通道显著高于 R 通道，必须是「蓝色」。
 */
#if !defined(ZHULONG_INFERENCE_TEST_STUB)
void test_sample_image_golden(const zhulong_plugin_api_v1& api, const std::string& plugin_so) {
    PpmImage sample;
    if (!load_ppm("native/tests/fixtures/onnx/plate_sample.ppm", sample)) {
        std::cerr << "missing fixture: native/tests/fixtures/onnx/plate_sample.ppm" << std::endl;
        assert(false);
        return;
    }

    zhulong_model_group_h model = nullptr;
    zhulong_status_t rc = api.model_create("{}", &model);
    assert(rc == ZHULONG_PLUGIN_OK && model != nullptr);

    zhulong_worker_ctx_h worker = nullptr;
    rc = api.worker_create(model, 0, &worker);
    assert(rc == ZHULONG_PLUGIN_OK && worker != nullptr);

    zhulong_task_ctx_h task = nullptr;
    rc = api.task_open(worker, 1, &task);
    assert(rc == ZHULONG_PLUGIN_OK && task != nullptr);

    zhulong_inference_result* result = run_once(api, task, sample);
    assert(result != nullptr);
    assert(result->result_type == ZHULONG_RESULT_TYPE_VEHICLE_STRUCTURE);
    assert(result->count == 2);

    const auto* vehicles = static_cast<const zhulong_vehicle_item*>(result->data);
    bool found_first = false;
    bool found_second = false;
    for (uint32_t i = 0; i < result->count; ++i) {
        const auto& vehicle = vehicles[i];
        assert(vehicle.plate_count == 1);
        const auto& plate = vehicle.plates[0];

        // 退化语义自检：车身框==车牌框、vehicle_score 为检测置信度
        for (int k = 0; k < 4; ++k) {
            assert(std::fabs(vehicle.vehicle_box[k] - plate.box[k]) < 1e-6f);
        }
        assert(vehicle.track_id == 0);
        assert(std::strcmp(vehicle.vehicle_type, "unknown") == 0);
        assert(vehicle.vehicle_score > 0.5f);

        // 颜色头对通道顺序极敏感：喂 RGB 会系统性误判成「黑色」
        assert(std::strcmp(plate.plate_color, "蓝色") == 0);
        assert(plate.plate_type == 0);
        assert(plate.score > 0.5f);

        if (std::strcmp(plate.plate_number, "豫B2V9L7") == 0) found_first = true;
        if (std::strcmp(plate.plate_number, "辽DU4356") == 0) found_second = true;
    }
    assert(found_first && found_second);
    api.result_release(result);

    // 配置必须真的生效：置信度阈值拉到 0.99 后同一帧不得再出框
    // （历史缺陷：init 直接 (void)config_json，schema 声明的阈值永远是默认值）
    api.task_close(task);
    api.worker_destroy(worker);
    api.model_destroy(model);

    zhulong_model_group_h strict_model = nullptr;
    rc = api.model_create("{\"confidence_threshold\":0.99,\"nms_threshold\":0.45}", &strict_model);
    assert(rc == ZHULONG_PLUGIN_OK && strict_model != nullptr);
    zhulong_worker_ctx_h strict_worker = nullptr;
    rc = api.worker_create(strict_model, 0, &strict_worker);
    assert(rc == ZHULONG_PLUGIN_OK && strict_worker != nullptr);
    zhulong_task_ctx_h strict_task = nullptr;
    rc = api.task_open(strict_worker, 2, &strict_task);
    assert(rc == ZHULONG_PLUGIN_OK && strict_task != nullptr);

    zhulong_inference_result* strict_result = run_once(api, strict_task, sample);
    assert(strict_result != nullptr);
    assert(strict_result->count == 0);
    api.result_release(strict_result);

    api.task_close(strict_task);
    api.worker_destroy(strict_worker);
    api.model_destroy(strict_model);

    std::cout << "[✓] plate sample golden regression passed" << std::endl;
}
#endif  // !ZHULONG_INFERENCE_TEST_STUB

} // namespace

int main(int argc, char* argv[]) {
    // 1. 纯函数单测：不依赖插件、不依赖模型
    test_sample_point_mapping();

    // 2. 插件全链路
    const char* plugin_so = (argc > 1) ? argv[1] : "./libPlateRecognitionPlugin.so";
    void* handle = nullptr;
    zhulong_plugin_api_v1 api;
    const zhulong_status_t rc = load_plugin(plugin_so, &handle, &api);
    if (rc != ZHULONG_PLUGIN_OK || handle == nullptr) {
        std::cerr << "Failed to load plugin: " << plugin_so << std::endl;
        return 1;
    }

    test_abi_lifecycle(api, plugin_so);
#if !defined(ZHULONG_INFERENCE_TEST_STUB)
    test_sample_image_golden(api, plugin_so);
#endif

    dlclose(handle);
    std::cout << "[✓] PlateRecognitionPlugin tests passed successfully!" << std::endl;
    return 0;
}
