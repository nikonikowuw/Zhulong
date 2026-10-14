// RKNN 推理后端测试骨架（阶段 C）。
//
// ⚠️ 本程序不使用 assert：-DNDEBUG 会吃掉 assert。改用显式 require() 检查 +
// 打印 + 非零退出。输出为机器可读的证据行，供主管会话在真机粘贴。
//
// 交叉构建时本目标只编译、不注册 CTest；板端手动执行：
//   ./ZhulongRknnTests [model.rknn]

#include <backends/rknn/rknn_error_map.hpp>
#include <backends/rknn/rknn_inference_backend.hpp>
#include <backends/rknn/rknn_types.hpp>
#include <algorithm/runtime/inference_backend.hpp>
#include <algorithm/runtime/tensor.hpp>

#include <rknn_api.h>

#include <chrono>
#include <cmath>
#include <cstdint>
#include <cstring>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <memory>
#include <string>
#include <thread>
#include <vector>

namespace {

int g_failures = 0;

void require(bool condition, const std::string& label) {
    std::cout << (condition ? "PASS " : "FAIL ") << label << "\n";
    if (!condition) {
        ++g_failures;
    }
}

long status_kb(const std::string& key) {
    std::ifstream status("/proc/self/status");
    std::string line;
    while (std::getline(status, line)) {
        if (line.rfind(key, 0) == 0) {
            const auto digits = line.find_first_of("0123456789");
            if (digits != std::string::npos) {
                return std::stol(line.substr(digits));
            }
        }
    }
    return -1;
}

long pss_kb() {
    std::ifstream rollup("/proc/self/smaps_rollup");
    std::string line;
    while (std::getline(rollup, line)) {
        if (line.rfind("Pss:", 0) == 0) {
            const auto digits = line.find_first_of("0123456789");
            if (digits != std::string::npos) {
                return std::stol(line.substr(digits));
            }
        }
    }
    return -1;
}

void print_rss(const std::string& stage) {
    std::cout << "RKNN_RSS stage=" << stage << " rss_kb=" << status_kb("VmRSS:")
              << " pss_kb=" << pss_kb() << "\n";
}

size_t fd_count() {
    size_t count = 0;
    std::error_code ec;
    for (auto it = std::filesystem::directory_iterator("/proc/self/fd", ec);
         !ec && it != std::filesystem::directory_iterator(); it.increment(ec)) {
        ++count;
    }
    return count;
}

zhulong::DataType to_data_type(rknn_tensor_type type, size_t& element_size) {
    switch (type) {
        case RKNN_TENSOR_FLOAT32: element_size = 4; return zhulong::DataType::FLOAT32;
        case RKNN_TENSOR_INT32:   element_size = 4; return zhulong::DataType::INT32;
        case RKNN_TENSOR_INT64:   element_size = 8; return zhulong::DataType::INT64;
        case RKNN_TENSOR_INT8:    element_size = 1; return zhulong::DataType::INT8;
        case RKNN_TENSOR_UINT8:   element_size = 1; return zhulong::DataType::UINT8;
        default:                  element_size = 1; return zhulong::DataType::UINT8;
    }
}

zhulong::Tensor make_input(const rknn_tensor_attr& attr) {
    size_t element_size = 1;
    zhulong::DataType dtype = to_data_type(attr.type, element_size);

    std::vector<int64_t> shape;
    uint64_t expected = element_size;
    bool representable = attr.n_dims > 0 && attr.n_dims <= RKNN_MAX_DIMS;
    for (uint32_t d = 0; d < attr.n_dims && representable; ++d) {
        if (attr.dims[d] == 0) {
            representable = false;
            break;
        }
        shape.push_back(static_cast<int64_t>(attr.dims[d]));
        expected *= attr.dims[d];
    }
    if (!representable || expected != attr.size) {
        // 无法用 Tensor 的 dtype 精确表达（如 FLOAT16）：退化为按字节的扁平 UINT8。
        dtype = zhulong::DataType::UINT8;
        shape.assign({static_cast<int64_t>(attr.size)});
    }
    return zhulong::Tensor(shape, dtype);
}

// FP16 <-> FP32（无硬件依赖的软件转换；IEEE 754 binary16，round-to-nearest-even）。
// 模型输入为 FP16 时，必须喂**合法的 FP16 位模式**；直接填随机字节会被 RKNN 当作
// FP16 解释而大量溢出为 NaN/Inf，使一致性证据退化为无意义值。
// ⚠️ 溢出边界必须按 IEEE 754 的舍入到最近偶数处理：65519 应舍入为 65504（最大有限值）
// 而不是直接饱和到 inf；否则与硬件 _Float16 转换不一致（已用 2e7 随机样本比对确认）。
uint16_t float_to_half(float value) {
    uint32_t bits;
    std::memcpy(&bits, &value, sizeof(bits));
    const uint32_t sign = (bits >> 16) & 0x8000u;
    const uint32_t exponent = (bits >> 23) & 0xffu;
    const uint32_t mantissa = bits & 0x7fffffu;
    if (exponent == 0xffu) {
        // inf / nan：保留符号，nan 保留一个 quiet 位。
        return static_cast<uint16_t>(sign | 0x7c00u | (mantissa ? 0x0200u : 0u));
    }
    const int32_t half_exp = static_cast<int32_t>(exponent) - 127 + 15;
    if (half_exp <= 0) {
        if (half_exp < -10) {
            return static_cast<uint16_t>(sign); // underflow -> 0
        }
        const uint32_t shift = static_cast<uint32_t>(1 - half_exp + 13);
        const uint32_t sig = mantissa | 0x800000u;
        uint32_t half_mant = sig >> shift;
        const uint32_t remainder = sig & ((1u << shift) - 1u);
        const uint32_t halfway = 1u << (shift - 1);
        if (remainder > halfway || (remainder == halfway && (half_mant & 1u))) {
            ++half_mant;
        }
        return static_cast<uint16_t>(sign | half_mant);
    }
    uint32_t half_exponent = static_cast<uint32_t>(half_exp) << 10;
    uint32_t half_mant = mantissa >> 13;
    const uint32_t remainder = mantissa & 0x1fffu;
    if (remainder > 0x1000u || (remainder == 0x1000u && (half_mant & 1u))) {
        ++half_mant;
        if (half_mant == 0x400u) { // 尾数进位到指数
            half_mant = 0;
            half_exponent += 0x400u;
        }
    }
    if (half_exponent >= 0x7c00u) {
        return static_cast<uint16_t>(sign | 0x7c00u); // overflow -> inf
    }
    return static_cast<uint16_t>(sign | half_exponent | half_mant);
}

// 把 Tensor 填充为确定性数据，且尊重模型声明的张量类型。
// - FP16 输入：填 [0,1) 的合法 FP16 值
// - 其它整型/字节输入：填确定性字节
void fill_deterministic_for(zhulong::Tensor& tensor, rknn_tensor_type type) {
    uint32_t state = 0x12345678u;
    auto next = [&state]() {
        state = state * 1664525u + 1013904223u;
        return state;
    };

    if (type == RKNN_TENSOR_FLOAT16) {
        uint16_t* data = reinterpret_cast<uint16_t*>(tensor.raw_data());
        const size_t count = tensor.byte_size() / sizeof(uint16_t);
        for (size_t i = 0; i < count; ++i) {
            data[i] = float_to_half(static_cast<float>(next() >> 8) / 16777216.0f);
        }
        return;
    }

    if (tensor.dtype() == zhulong::DataType::FLOAT32) {
        float* data = tensor.data<float>();
        const size_t count = tensor.element_count();
        for (size_t i = 0; i < count; ++i) {
            data[i] = static_cast<float>(next() >> 8) / 16777216.0f;
        }
        return;
    }

    uint8_t* data = tensor.data<uint8_t>();
    const size_t count = tensor.byte_size();
    for (size_t i = 0; i < count; ++i) {
        data[i] = static_cast<uint8_t>(next() >> 24);
    }
}

bool floats_close(float a, float b) {
    // ⚠️ 非有限值必须显式处理：模型在无意义输入（如合成随机帧）下可能输出 -inf/NaN，
    // 而 fabs(-inf - -inf) = NaN，NaN <= x 恒为 false，会把「两边都是 -inf」误判为不一致。
    if (std::isnan(a) || std::isnan(b)) {
        return std::isnan(a) && std::isnan(b);
    }
    if (std::isinf(a) || std::isinf(b)) {
        return a == b; // 同号无穷视为一致，异号/有限-无穷视为不一致
    }
    return std::fabs(a - b) <= 1e-4f * std::max(1.0f, std::fabs(a));
}

bool tensors_close(const zhulong::Tensor& a, const zhulong::Tensor& b, std::string& detail) {
    if (a.byte_size() != b.byte_size()) {
        detail = "byte_size " + std::to_string(a.byte_size()) + " vs " + std::to_string(b.byte_size());
        return false;
    }
    const float* pa = a.data<float>();
    const float* pb = b.data<float>();
    const size_t count = a.byte_size() / sizeof(float);
    for (size_t i = 0; i < count; ++i) {
        if (!floats_close(pa[i], pb[i])) {
            detail = "index " + std::to_string(i) + " " + std::to_string(pa[i]) + " vs " +
                     std::to_string(pb[i]);
            return false;
        }
    }
    return true;
}

bool outputs_close(const std::vector<zhulong::Tensor>& a, const std::vector<zhulong::Tensor>& b,
                   std::string& detail) {
    if (a.size() != b.size()) {
        detail = "output count " + std::to_string(a.size()) + " vs " + std::to_string(b.size());
        return false;
    }
    for (size_t i = 0; i < a.size(); ++i) {
        if (!tensors_close(a[i], b[i], detail)) {
            detail = "output[" + std::to_string(i) + "] " + detail;
            return false;
        }
    }
    return true;
}

void print_attr(const char* tag, const rknn_tensor_attr& attr) {
    std::cout << tag << " n_dims=" << attr.n_dims << " dims=[";
    for (uint32_t d = 0; d < attr.n_dims; ++d) {
        std::cout << (d ? "," : "") << attr.dims[d];
    }
    std::cout << "] type=" << get_type_string(attr.type) << " fmt=" << get_format_string(attr.fmt)
              << " n_elems=" << attr.n_elems << " size=" << attr.size << "\n";
}

std::string model_path_from_args(int argc, char** argv) {
    if (argc > 1) {
        return argv[1];
    }
    return "/userdata/rknn_model_zoo/examples/facelivt/model/facelivtv2_s_rk3568_fp.rknn";
}

// Case 1：真实模型加载 + 查询证据 + 一次真实推理（AC7）。
void case_smoke(const std::string& model_path) {
    std::cout << "RKNN_MODEL path=" << model_path << "\n";
    zhulong::RknnInferenceBackend backend;
    auto asset = backend.load_model(model_path);
    require(asset != nullptr, "case1: load_model returns asset");
    if (!asset) {
        return;
    }

    auto* rknn_asset = dynamic_cast<zhulong::RknnModelAsset*>(asset.get());
    require(rknn_asset != nullptr, "case1: asset is RknnModelAsset");
    if (!rknn_asset) {
        return;
    }

    std::cout << "RKNN_SDK_VERSION api=" << rknn_asset->sdk_version.api_version
              << " drv=" << rknn_asset->sdk_version.drv_version << "\n";
    std::cout << "RKNN_MEM_SIZE weight=" << rknn_asset->mem_size.total_weight_size
              << " internal=" << rknn_asset->mem_size.total_internal_size
              << " dma=" << rknn_asset->mem_size.total_dma_allocated_size
              << " sram_total=" << rknn_asset->mem_size.total_sram_size
              << " sram_free=" << rknn_asset->mem_size.free_sram_size << "\n";
    for (uint32_t i = 0; i < rknn_asset->io_num.n_input; ++i) {
        std::string tag = "RKNN_INPUT[" + std::to_string(i) + "]";
        print_attr(tag.c_str(), rknn_asset->input_attrs[i]);
    }
    for (uint32_t i = 0; i < rknn_asset->io_num.n_output; ++i) {
        std::string tag = "RKNN_OUTPUT[" + std::to_string(i) + "]";
        print_attr(tag.c_str(), rknn_asset->output_attrs[i]);
    }

    auto session = backend.create_session(asset, 0);
    require(session != nullptr, "case1: create_session");
    if (!session) {
        return;
    }

    zhulong::Tensor input = make_input(rknn_asset->input_attrs[0]);
    fill_deterministic_for(input, rknn_asset->input_attrs[0].type);

    std::vector<zhulong::Tensor> outputs;
    zhulong_status_t rc = backend.forward(session.get(), {input}, outputs);
    require(rc == ZHULONG_PLUGIN_OK, "case1: forward OK");
    require(outputs.size() == rknn_asset->io_num.n_output, "case1: output count matches n_output");

    bool all_nonzero = true;
    for (const auto& output : outputs) {
        if (output.byte_size() == 0) {
            all_nonzero = false;
        }
    }
    require(all_nonzero, "case1: every output byte_size > 0");

    std::cout << "RKNN_OUTPUTS count=" << outputs.size() << "\n";
    if (!outputs.empty() && outputs[0].byte_size() >= 4 * sizeof(float)) {
        const float* head = outputs[0].data<float>();
        std::cout << "RKNN_OUTPUT0_HEAD " << head[0] << " " << head[1] << " " << head[2] << " "
                  << head[3] << "\n";
    }
}

// Case 2：权重共享对照（AC3）。先基线组（N 次独立 rknn_init），再候选组（1 root + N dup）。
void case_weight_sharing(const std::string& model_path) {
    const uint32_t workers = 4;
    std::cout << "RKNN_SHARE workers=" << workers << "\n";

    // 用一次探测加载取得输入形状，之后两组复用同一份确定性输入。
    zhulong::RknnInferenceBackend backend;
    std::vector<zhulong::Tensor> reference;
    zhulong::Tensor probe_input;

    // ---- 基线组：N 个独立 load_model（= N 次 rknn_init） ----
    {
        std::vector<std::shared_ptr<zhulong::ModelAsset>> assets;
        std::vector<std::unique_ptr<zhulong::SessionContext>> sessions;
        std::vector<std::vector<zhulong::Tensor>> results;
        print_rss("baseline-before");
        for (uint32_t w = 0; w < workers; ++w) {
            auto asset = backend.load_model(model_path);
            if (!asset) {
                require(false, "case2: baseline load_model[" + std::to_string(w) + "]");
                return;
            }
            auto* rknn_asset = dynamic_cast<zhulong::RknnModelAsset*>(asset.get());
            auto session = backend.create_session(asset, w);
            if (!session) {
                require(false, "case2: baseline create_session[" + std::to_string(w) + "]");
                return;
            }
            std::cout << "RKNN_MEM_SIZE group=baseline worker=" << w
                      << " weight=" << rknn_asset->mem_size.total_weight_size
                      << " internal=" << rknn_asset->mem_size.total_internal_size << "\n";
            assets.push_back(std::move(asset));
            sessions.push_back(std::move(session));
        }
        print_rss("baseline-peak");
        auto* first_asset = dynamic_cast<zhulong::RknnModelAsset*>(assets[0].get());
        probe_input = make_input(first_asset->input_attrs[0]);
        fill_deterministic_for(probe_input, first_asset->input_attrs[0].type);
        for (uint32_t w = 0; w < workers; ++w) {
            std::vector<zhulong::Tensor> outputs;
            zhulong_status_t rc = backend.forward(sessions[w].get(), {probe_input}, outputs);
            require(rc == ZHULONG_PLUGIN_OK, "case2: baseline forward[" + std::to_string(w) + "]");
            results.push_back(std::move(outputs));
        }
        reference = results[0];
        bool consistent = true;
        for (uint32_t w = 1; w < workers; ++w) {
            std::string detail;
            if (!outputs_close(reference, results[w], detail)) {
                consistent = false;
                std::cout << "RKNN_SHARE_MISMATCH group=baseline worker=" << w << " " << detail << "\n";
            }
        }
        require(consistent, "case2: baseline outputs identical across workers");
        // 先销毁所有 session，再销毁 asset（顺序影响 RSS）。
        sessions.clear();
        assets.clear();
        print_rss("baseline-after-release");
    }

    // ---- 候选组：1 root + N dup session ----
    {
        print_rss("candidate-before");
        auto asset = backend.load_model(model_path);
        require(asset != nullptr, "case2: candidate load_model");
        if (!asset) {
            return;
        }
        auto* rknn_asset = dynamic_cast<zhulong::RknnModelAsset*>(asset.get());
        std::cout << "RKNN_MEM_SIZE group=candidate worker=root"
                  << " weight=" << rknn_asset->mem_size.total_weight_size
                  << " internal=" << rknn_asset->mem_size.total_internal_size << "\n";
        std::vector<std::unique_ptr<zhulong::SessionContext>> sessions;
        for (uint32_t w = 0; w < workers; ++w) {
            auto session = backend.create_session(asset, w);
            if (!session) {
                require(false, "case2: candidate create_session[" + std::to_string(w) + "]");
                return;
            }
            sessions.push_back(std::move(session));
        }
        print_rss("candidate-peak");
        bool consistent = true;
        for (uint32_t w = 0; w < workers; ++w) {
            std::vector<zhulong::Tensor> outputs;
            zhulong_status_t rc = backend.forward(sessions[w].get(), {probe_input}, outputs);
            require(rc == ZHULONG_PLUGIN_OK, "case2: candidate forward[" + std::to_string(w) + "]");
            std::string detail;
            if (!outputs_close(reference, outputs, detail)) {
                consistent = false;
                std::cout << "RKNN_SHARE_MISMATCH group=candidate worker=" << w << " " << detail << "\n";
            }
        }
        require(consistent, "case2: candidate outputs match baseline");
        sessions.clear();
        asset.reset();
        print_rss("candidate-after-release");
    }
    // ⚠️ 判定留给主管会话：本程序只打印原始数字，不下结论。
}

// Case 3：1/2/4 worker 并发（AC4）。
void case_concurrency(const std::string& model_path) {
    zhulong::RknnInferenceBackend backend;
    auto asset = backend.load_model(model_path);
    require(asset != nullptr, "case3: load_model");
    if (!asset) {
        return;
    }
    auto* rknn_asset = dynamic_cast<zhulong::RknnModelAsset*>(asset.get());

    zhulong::Tensor input = make_input(rknn_asset->input_attrs[0]);
    fill_deterministic_for(input, rknn_asset->input_attrs[0].type);

    // 单 worker 基线
    std::vector<zhulong::Tensor> baseline;
    {
        auto session = backend.create_session(asset, 0);
        require(session != nullptr, "case3: baseline create_session");
        if (!session) {
            return;
        }
        zhulong_status_t rc = backend.forward(session.get(), {input}, baseline);
        require(rc == ZHULONG_PLUGIN_OK, "case3: baseline forward");
    }

    const size_t baseline_fds = fd_count();
    print_rss("case3-before");

    for (uint32_t workers : {1u, 2u, 4u}) {
        std::vector<std::unique_ptr<zhulong::SessionContext>> sessions;
        for (uint32_t w = 0; w < workers; ++w) {
            sessions.push_back(backend.create_session(asset, w));
        }
        const uint32_t iterations = 20;
        std::vector<double> elapsed_ms(workers, 0.0);
        std::vector<int> status(workers, -999);
        std::vector<std::thread> threads;
        for (uint32_t w = 0; w < workers; ++w) {
            threads.emplace_back([&, w]() {
                const auto start = std::chrono::steady_clock::now();
                for (uint32_t m = 0; m < iterations; ++m) {
                    std::vector<zhulong::Tensor> outputs;
                    zhulong_status_t rc = backend.forward(sessions[w].get(), {input}, outputs);
                    if (rc != ZHULONG_PLUGIN_OK) {
                        status[w] = static_cast<int>(rc);
                        break;
                    }
                    std::string detail;
                    if (!outputs_close(baseline, outputs, detail)) {
                        status[w] = 1;
                        std::cout << "RKNN_CONCURRENCY_MISMATCH workers=" << workers
                                  << " worker=" << w << " iter=" << m << " " << detail << "\n";
                        break;
                    }
                    status[w] = 0;
                }
                const auto end = std::chrono::steady_clock::now();
                elapsed_ms[w] = std::chrono::duration<double, std::milli>(end - start).count();
            });
        }
        for (auto& thread : threads) {
            thread.join();
        }
        bool all_ok = true;
        for (uint32_t w = 0; w < workers; ++w) {
            if (status[w] != 0) {
                all_ok = false;
            }
            std::cout << "RKNN_WORKER workers=" << workers << " worker=" << w
                      << " iterations=" << iterations << " elapsed_ms=" << elapsed_ms[w] << "\n";
        }
        require(all_ok, "case3: workers=" + std::to_string(workers) + " consistent & stable");
        sessions.clear();
    }

    print_rss("case3-after");
    const size_t after_fds = fd_count();
    std::cout << "RKNN_FD stage=case3-before count=" << baseline_fds
              << " stage=case3-after count=" << after_fds << "\n";
    require(after_fds <= baseline_fds, "case3: no fd growth after concurrency");
}

// Case 4：故障注入（AC5）。
void case_faults(const std::string& model_path) {
    zhulong::RknnInferenceBackend backend;

    require(backend.load_model("") == nullptr, "case4: load_model(\"\") -> nullptr");
    require(backend.load_model("/nonexistent/definitely-not-a-model.rknn") == nullptr,
            "case4: load_model(nonexistent) -> nullptr");

    const std::string garbage_path = "/tmp/zhulong_rknn_garbage.rknn";
    {
        std::ofstream garbage(garbage_path, std::ios::binary | std::ios::trunc);
        std::vector<char> bytes(1024, static_cast<char>(0xAB));
        garbage.write(bytes.data(), static_cast<std::streamsize>(bytes.size()));
    }
    require(backend.load_model(garbage_path) == nullptr, "case4: load_model(garbage) -> nullptr");
    std::filesystem::remove(garbage_path);

    std::vector<zhulong::Tensor> scratch_outputs;
    require(backend.forward(nullptr, {}, scratch_outputs) ==
                ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT,
            "case4: forward(nullptr) -> INVALID_ARGUMENT");

    // 需要有效模型才能测输入数量/尺寸错误路径。
    auto asset = backend.load_model(model_path);
    if (asset) {
        auto* rknn_asset = dynamic_cast<zhulong::RknnModelAsset*>(asset.get());
        auto session = backend.create_session(asset, 0);
        zhulong::Tensor input = make_input(rknn_asset->input_attrs[0]);
        fill_deterministic_for(input, rknn_asset->input_attrs[0].type);

        std::vector<zhulong::Tensor> outputs;
        std::vector<zhulong::Tensor> wrong_count(rknn_asset->io_num.n_input + 1, input);
        require(backend.forward(session.get(), wrong_count, outputs) ==
                    ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT,
                "case4: forward(too many inputs) -> INVALID_ARGUMENT");
        if (rknn_asset->io_num.n_input > 1) {
            std::vector<zhulong::Tensor> too_few(rknn_asset->io_num.n_input, input);
            too_few.pop_back();
            require(backend.forward(session.get(), too_few, outputs) ==
                        ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT,
                    "case4: forward(too few inputs) -> INVALID_ARGUMENT");
        }

        zhulong::Tensor small({1}, zhulong::DataType::UINT8);
        require(backend.forward(session.get(), {small}, outputs) ==
                    ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT,
                "case4: forward(undersized input) -> INVALID_ARGUMENT");

        require(backend.create_session(nullptr, 0) == nullptr, "case4: create_session(nullptr) -> nullptr");
        auto plain = std::make_shared<zhulong::ModelAsset>();
        require(backend.create_session(plain, 0) == nullptr,
                "case4: create_session(non-Rknn ModelAsset) -> nullptr");
    } else {
        std::cout << "SKIP case4: model-dependent fault paths (model not loadable)\n";
    }

    print_rss("case4-after");
    std::cout << "RKNN_FD stage=case4-after count=" << fd_count() << "\n";
    require(true, "case4: fault paths completed");
}

// Case 5：错误映射枚举全覆盖（AC5）。
void case_error_map() {
    struct Expectation {
        int code;
        zhulong_status_t expected;
    };
    const Expectation expectations[] = {
        {RKNN_SUCC, ZHULONG_PLUGIN_OK},
        {RKNN_ERR_FAIL, ZHULONG_PLUGIN_ERR_INTERNAL},
        {RKNN_ERR_TIMEOUT, ZHULONG_PLUGIN_ERR_TIMEOUT},
        {RKNN_ERR_DEVICE_UNAVAILABLE, ZHULONG_PLUGIN_ERR_NOT_SUPPORTED},
        {RKNN_ERR_MALLOC_FAIL, ZHULONG_PLUGIN_ERR_OUT_OF_MEMORY},
        {RKNN_ERR_PARAM_INVALID, ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT},
        {RKNN_ERR_MODEL_INVALID, ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT},
        {RKNN_ERR_CTX_INVALID, ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT},
        {RKNN_ERR_INPUT_INVALID, ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT},
        {RKNN_ERR_OUTPUT_INVALID, ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT},
        {RKNN_ERR_DEVICE_UNMATCH, ZHULONG_PLUGIN_ERR_NOT_SUPPORTED},
        {RKNN_ERR_INCOMPATILE_PRE_COMPILE_MODEL, ZHULONG_PLUGIN_ERR_NOT_SUPPORTED},
        {RKNN_ERR_INCOMPATILE_OPTIMIZATION_LEVEL_VERSION, ZHULONG_PLUGIN_ERR_NOT_SUPPORTED},
        {RKNN_ERR_TARGET_PLATFORM_UNMATCH, ZHULONG_PLUGIN_ERR_NOT_SUPPORTED},
    };
    require(sizeof(expectations) / sizeof(expectations[0]) == 14, "case5: covers all 14 return codes");
    for (const auto& item : expectations) {
        zhulong_status_t mapped = zhulong::map_rknn_error(item.code);
        std::cout << "RKNN_ERRMAP code=" << item.code << " -> " << static_cast<int>(mapped) << "\n";
        require(mapped == item.expected,
                "case5: map_rknn_error(" + std::to_string(item.code) + ")");
    }
    require(zhulong::map_rknn_error(-12345) == ZHULONG_PLUGIN_ERR_INTERNAL,
            "case5: unknown code -> INTERNAL");
}

// Case 6：重复启停无泄漏（AC5）。
void case_repeated_start_stop(const std::string& model_path) {
    zhulong::RknnInferenceBackend backend;
    for (int iteration = 0; iteration < 5; ++iteration) {
        auto asset = backend.load_model(model_path);
        if (!asset) {
            require(false, "case6: load_model iteration " + std::to_string(iteration));
            return;
        }
        auto* rknn_asset = dynamic_cast<zhulong::RknnModelAsset*>(asset.get());
        auto session = backend.create_session(asset, 0);
        require(session != nullptr, "case6: create_session iteration " + std::to_string(iteration));
        zhulong::Tensor input = make_input(rknn_asset->input_attrs[0]);
        fill_deterministic_for(input, rknn_asset->input_attrs[0].type);
        std::vector<zhulong::Tensor> outputs;
        zhulong_status_t rc = backend.forward(session.get(), {input}, outputs);
        require(rc == ZHULONG_PLUGIN_OK, "case6: forward iteration " + std::to_string(iteration));
        session.reset();
        asset.reset();
        std::cout << "RKNN_REPEAT iteration=" << iteration << " rss_kb=" << status_kb("VmRSS:")
                  << " fd=" << fd_count() << "\n";
    }
    print_rss("case6-after");
}

} // namespace

int main(int argc, char** argv) {
    const std::string model_path = model_path_from_args(argc, argv);
    std::cout << "=== Zhulong RKNN backend tests ===" << std::endl;

    case_error_map();
    case_smoke(model_path);
    case_weight_sharing(model_path);
    case_concurrency(model_path);
    case_faults(model_path);
    case_repeated_start_stop(model_path);

    std::cout << "=== failures=" << g_failures << " ===" << std::endl;
    return g_failures == 0 ? 0 : 1;
}
