// 宿主 SDK 契约测试：编译真实 RKNN 后端，API stub 只检查参数、状态与所有权。
// 不加载厂商库，不读取大尺寸测试输入，不据此证明 NPU 精度/布局或驱动安全。

#include <backends/rknn/rknn_inference_backend.hpp>
#include <backends/rknn/rknn_types.hpp>

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <limits>
#include <map>
#include <memory>
#include <new>
#include <utility>
#include <vector>

namespace {

using zhulong::DataType;
using zhulong::Tensor;

void require(bool condition, const char* label) {
    if (!condition) {
        std::fprintf(stderr, "FAIL %s\n", label);
        std::exit(1);
    }
}

rknn_tensor_attr input_attr(uint32_t width = 8, uint32_t height = 8) {
    rknn_tensor_attr attr{};
    attr.n_dims = 4;
    attr.dims[0] = 1;
    attr.dims[1] = height;
    attr.dims[2] = width;
    attr.dims[3] = 3;
    attr.n_elems = width * height * 3;
    attr.size = attr.n_elems;
    attr.size_with_stride = attr.size;
    attr.w_stride = width;
    attr.fmt = RKNN_TENSOR_NHWC;
    attr.type = RKNN_TENSOR_UINT8;
    std::snprintf(attr.name, sizeof(attr.name), "%s", "input");
    return attr;
}

rknn_tensor_attr output_attr(uint32_t elements = 1) {
    rknn_tensor_attr attr{};
    attr.n_dims = 1;
    attr.dims[0] = elements;
    attr.n_elems = elements;
    attr.size = elements * sizeof(float);
    attr.size_with_stride = attr.size;
    attr.type = RKNN_TENSOR_FLOAT32;
    return attr;
}

enum class MalformedAttr { none, zero_rank, excessive_rank, zero_dim, zero_size, wrong_count };

void corrupt_attr(rknn_tensor_attr& attr, MalformedAttr fault) {
    switch (fault) {
        case MalformedAttr::none: break;
        case MalformedAttr::zero_rank: attr.n_dims = 0; break;
        case MalformedAttr::excessive_rank: attr.n_dims = RKNN_MAX_DIMS + 1; break;
        case MalformedAttr::zero_dim: attr.dims[0] = 0; break;
        case MalformedAttr::zero_size: attr.size = 0; break;
        case MalformedAttr::wrong_count: ++attr.n_elems; break;
    }
}

struct FakeContext {
    rknn_tensor_attr input;
    rknn_tensor_attr output;
};

struct FakeRuntime {
    rknn_tensor_attr model_input{input_attr()};
    rknn_tensor_attr model_output{output_attr()};
    bool dynamic{false};
    bool mismatch_output_size{false};
    MalformedAttr input_fault{MalformedAttr::none};
    MalformedAttr output_fault{MalformedAttr::none};
    int shape_status{RKNN_SUCC};
    int input_query_status{RKNN_SUCC};
    int output_query_status{RKNN_SUCC};
    int bind_status{RKNN_SUCC};
    int run_status{RKNN_SUCC};
    int get_status{RKNN_SUCC};
    uint32_t shape_calls{0};
    uint32_t input_queries{0};
    uint32_t output_queries{0};
    uint32_t import_calls{0};
    uint32_t bind_calls{0};
    uint32_t host_calls{0};
    uint32_t run_calls{0};
    uint32_t release_calls{0};
    uint32_t destroyed_mems{0};
    uint32_t imported_size{0};
    rknn_tensor_attr last_binding{};
    rknn_input last_host_input{};
    rknn_context next_context{1};
    std::map<rknn_context, FakeContext> contexts;
    std::map<rknn_tensor_mem*, rknn_context> memories;
};

// 仅此独立单线程测试进程的 SDK 注入点，不进入任何产品目标。
FakeRuntime* runtime = nullptr;

class RuntimeScope {
public:
    explicit RuntimeScope(FakeRuntime& fake) {
        require(runtime == nullptr, "one fake runtime at a time");
        runtime = &fake;
    }
    ~RuntimeScope() {
        require(runtime->memories.empty(), "all imported memory released");
        require(runtime->contexts.empty(), "all root/probe/session contexts released");
        runtime = nullptr;
    }
    RuntimeScope(const RuntimeScope&) = delete;
    RuntimeScope& operator=(const RuntimeScope&) = delete;
    RuntimeScope(RuntimeScope&&) = delete;
    RuntimeScope& operator=(RuntimeScope&&) = delete;
};

struct BackendFixture {
    FakeRuntime fake;
    RuntimeScope scope{fake};
    zhulong::RknnInferenceBackend backend;
    std::shared_ptr<zhulong::RknnModelAsset> model;
    std::unique_ptr<zhulong::SessionContext> session;
    std::vector<Tensor> outputs;

    explicit BackendFixture(rknn_tensor_attr input = input_attr(), bool dynamic = false,
                            rknn_tensor_attr output = output_attr()) {
        fake.model_input = input;
        fake.model_output = output;
        fake.dynamic = dynamic;
        model = std::dynamic_pointer_cast<zhulong::RknnModelAsset>(backend.load_model("contract.rknn"));
        require(model != nullptr, "fake model loaded");
        session = backend.create_session(model, 0);
        require(session != nullptr, "fake worker session created");
    }

    zhulong::RknnSessionContext& state() {
        return *static_cast<zhulong::RknnSessionContext*>(session.get());
    }

    zhulong_status_t forward(Tensor input) {
        std::vector<Tensor> inputs;
        inputs.push_back(std::move(input));
        return backend.forward(session.get(), inputs, outputs);
    }
};

void test_packed_dma_gate() {
    const auto check_rejected = [](rknn_tensor_attr attr, zhulong_status_t expected) {
        BackendFixture fixture(attr);
        require(fixture.forward(Tensor::from_dma_buf(42, {1, 8, 8, 3}, DataType::UINT8)) == expected,
                "unproven DMA layout/capacity rejected");
        require(fixture.fake.import_calls == 0 && fixture.fake.bind_calls == 0 && fixture.fake.run_calls == 0,
                "DMA rejection precedes all memory/hardware calls");
        require(fixture.outputs.empty(), "rejected DMA has no outputs");
    };
    auto padded = input_attr();
    padded.w_stride = 16;
    padded.size_with_stride = padded.size * 2;
    check_rejected(padded, ZHULONG_PLUGIN_ERR_NOT_SUPPORTED);
    padded = input_attr();
    padded.w_stride = 16; // 即使 size 字段未体现 padding，也不能相信紧密布局。
    check_rejected(padded, ZHULONG_PLUGIN_ERR_NOT_SUPPORTED);
    padded = input_attr();
    padded.h_stride = 16;
    check_rejected(padded, ZHULONG_PLUGIN_ERR_NOT_SUPPORTED);
    padded = input_attr();
    padded.size_with_stride = 0;
    check_rejected(padded, ZHULONG_PLUGIN_ERR_NOT_SUPPORTED);
    padded = input_attr();
    padded.fmt = RKNN_TENSOR_NC1HWC2;
    check_rejected(padded, ZHULONG_PLUGIN_ERR_NOT_SUPPORTED);
    padded = input_attr();
    padded.type = RKNN_TENSOR_FLOAT16;
    padded.size *= 2;
    padded.size_with_stride = padded.size;
    check_rejected(padded, ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT);

    BackendFixture fixture;
    require(fixture.forward(Tensor::from_dma_buf(42, {1, 8, 8, 3}, DataType::UINT8)) == ZHULONG_PLUGIN_OK,
            "packed DMA accepted by stub");
    require(fixture.fake.last_binding.pass_through == 0, "dtype equality must not bypass Runtime conversion");
    require(fixture.fake.imported_size == 192, "exact packed import capacity submitted");
    require(fixture.forward(Tensor::from_dma_buf(42, {1, 4, 16, 3}, DataType::UINT8)) ==
                ZHULONG_PLUGIN_ERR_NOT_SUPPORTED, "equal element count does not prove DMA shape/layout");
    require(fixture.fake.import_calls == 1 && fixture.fake.run_calls == 1, "bad shape leaves old binding intact");
}

void test_host_conversion_and_mode_switch() {
    auto padded = input_attr();
    padded.w_stride = 16;
    padded.size_with_stride = padded.size * 2;
    {
        BackendFixture fixture(padded);
        require(fixture.forward(Tensor({1, 8, 8, 3}, DataType::UINT8)) == ZHULONG_PLUGIN_OK,
                "standard Host path still accepts packed input to padded model");
        require(fixture.fake.last_host_input.pass_through == 0 && fixture.fake.host_calls == 1,
                "Host conversion retained");
    }

    BackendFixture fixture;
    // 验证 DMA -> Host 的注册释放，不混用单次输入。
    require(fixture.forward(Tensor::from_dma_buf(42, {1, 8, 8, 3}, DataType::UINT8)) == ZHULONG_PLUGIN_OK,
            "switch to DMA");
    require(fixture.forward(Tensor({1, 8, 8, 3}, DataType::UINT8)) == ZHULONG_PLUGIN_OK, "switch back to Host");
    require(fixture.state().cached_input_mems.empty() && fixture.fake.memories.empty(),
            "Host switch releases DMA registrations");
}

void test_dtype_rebind_and_rollback() {
    BackendFixture fixture;
    uint8_t pixels[192]{};
    const auto input = [&](DataType dtype, int fd = 42) {
        return Tensor::from_dma_buf(fd, {1, 8, 8, 3}, dtype, pixels);
    };
    require(fixture.forward(input(DataType::UINT8)) == ZHULONG_PLUGIN_OK, "initial DMA binding");
    require(fixture.forward(input(DataType::UINT8)) == ZHULONG_PLUGIN_OK, "same identity/type cache hit");
    require(fixture.fake.import_calls == 1 && fixture.fake.bind_calls == 1, "hit skips import and binding");
    fixture.fake.bind_status = RKNN_ERR_INPUT_INVALID;
    require(fixture.forward(input(DataType::INT8)) == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT, "failed dtype rebind");
    require(fixture.state().cached_input_mems[0].dtype == DataType::UINT8, "failed rebind does not commit dtype");
    fixture.fake.bind_status = RKNN_SUCC;
    require(fixture.forward(input(DataType::INT8)) == ZHULONG_PLUGIN_OK, "retry dtype rebind");
    require(fixture.fake.import_calls == 1 && fixture.fake.bind_calls == 3, "dtype change rebinds without reimport");
    require(fixture.fake.last_binding.type == RKNN_TENSOR_INT8 && fixture.fake.last_binding.pass_through == 0,
            "requested dtype and conversion flag submitted");

    fixture.fake.bind_status = RKNN_ERR_FAIL;
    require(fixture.forward(input(DataType::INT8, 43)) == ZHULONG_PLUGIN_ERR_INTERNAL, "failed new registration");
    require(fixture.state().cached_input_mems[0].fd == 42 && fixture.fake.memories.size() == 1,
            "new-memory rollback retains old registration, releases candidate");
    fixture.fake.bind_status = RKNN_SUCC;
    require(fixture.forward(input(DataType::INT8, 43)) == ZHULONG_PLUGIN_OK, "retry new-memory binding");
    require(fixture.state().cached_input_mems[0].fd == 43 && fixture.fake.memories.size() == 1,
            "commit replaces old registration exactly once");
}

void test_dynamic_shapes_and_worker_isolation() {
    BackendFixture fixture(input_attr(), true);
    auto second_worker = fixture.backend.create_session(fixture.model, 1);
    require(second_worker != nullptr, "second worker created");
    for (const int64_t width : {16, 4, 16}) {
        require(fixture.forward(Tensor::from_dma_buf(42, {1, width, width, 3}, DataType::UINT8)) ==
                    ZHULONG_PLUGIN_OK, "dynamic large -> small -> large accepted");
        require(fixture.fake.last_binding.dims[1] == width && fixture.fake.last_binding.dims[2] == width,
                "binding uses current dimensions, not load-time asset");
        require(fixture.fake.last_binding.size == width * width * 3 && fixture.fake.imported_size == width * width * 3,
                "current capacity submitted");
        require(fixture.outputs[0].shape() == std::vector<int64_t>{width}, "current output dimensions used");
    }
    require(fixture.fake.shape_calls == 3 && fixture.fake.input_queries == 3 && fixture.fake.output_queries == 3,
            "every shape switch refreshes both input and output attributes");
    require(fixture.model->input_attrs[0].dims[1] == 8 && fixture.model->output_attrs[0].dims[0] == 1,
            "shared load-time asset remains immutable");
    std::vector<Tensor> inputs;
    inputs.push_back(Tensor::from_dma_buf(43, {1, 8, 8, 3}, DataType::UINT8));
    require(fixture.backend.forward(second_worker.get(), inputs, fixture.outputs) == ZHULONG_PLUGIN_OK,
            "second worker uses its own shape");
    require(fixture.state().current_input_attrs[0].dims[1] == 16, "second worker cannot change first worker metadata");
}

void test_dynamic_unbatched_3d_input_expanded_to_4d() {
    BackendFixture fixture(input_attr(), true);
    // 3D unbatched input {8, 8, 3} on a dynamic 4D model [1, H, W, 3]
    Tensor input_3d({8, 8, 3}, DataType::UINT8);
    require(fixture.forward(input_3d) == ZHULONG_PLUGIN_OK, "unbatched 3D input accepted by dynamic 4D model");
    require(fixture.state().current_input_attrs[0].n_dims == 4, "dynamic shape reconfiguration normalizes to 4D");
    require(fixture.state().current_input_attrs[0].dims[0] == 1 &&
            fixture.state().current_input_attrs[0].dims[1] == 8 &&
            fixture.state().current_input_attrs[0].dims[2] == 8 &&
            fixture.state().current_input_attrs[0].dims[3] == 3,
            "normalized shape matches batch-1 4D layout");
    require(fixture.fake.last_host_input.fmt == RKNN_TENSOR_NHWC, "3D HWC input mapped to NHWC");
    require(fixture.outputs[0].shape() == std::vector<int64_t>{8}, "outputs properly generated");

    // Dynamic shape switch with 3D input
    Tensor input_3d_large({16, 16, 3}, DataType::UINT8);
    require(fixture.forward(input_3d_large) == ZHULONG_PLUGIN_OK, "dynamic shape switch with 3D input accepted");
    require(fixture.state().current_input_attrs[0].dims[1] == 16 &&
            fixture.state().current_input_attrs[0].dims[2] == 16,
            "reconfigured to new dimensions");

    // DMA buffer with 3D shape is also accepted
    require(fixture.forward(Tensor::from_dma_buf(42, {16, 16, 3}, DataType::UINT8)) == ZHULONG_PLUGIN_OK,
            "DMA buffer with 3D shape accepted");
    require(fixture.fake.last_binding.n_dims == 4 &&
            fixture.fake.last_binding.dims[0] == 1 &&
            fixture.fake.last_binding.dims[1] == 16 &&
            fixture.fake.last_binding.dims[2] == 16 &&
            fixture.fake.last_binding.dims[3] == 3,
            "DMA binding receives normalized 4D dims");
}

void test_invalid_current_metadata_and_retry() {
    const MalformedAttr faults[]{MalformedAttr::zero_rank, MalformedAttr::excessive_rank,
                                MalformedAttr::zero_dim, MalformedAttr::zero_size, MalformedAttr::wrong_count};
    for (const bool corrupt_input : {false, true}) {
        for (const auto fault : faults) {
            BackendFixture fixture(input_attr(), true);
            require(fixture.forward(Tensor::from_dma_buf(42, {1, 8, 8, 3}, DataType::UINT8)) == ZHULONG_PLUGIN_OK,
                    "establish valid dynamic binding");
            if (corrupt_input) {
                fixture.fake.input_fault = fault;
            } else {
                fixture.fake.output_fault = fault;
            }
            require(fixture.forward(Tensor::from_dma_buf(42, {1, 16, 16, 3}, DataType::UINT8)) ==
                        ZHULONG_PLUGIN_ERR_INTERNAL, "query success with invalid attributes is INTERNAL, not OK");
            require(fixture.outputs.empty() && !fixture.state().shapes_set, "failed query clears outputs and shape validity");
            require(fixture.fake.run_calls == 1 && fixture.fake.memories.empty(), "failed switch cannot run with old bindings");
            fixture.fake.input_fault = MalformedAttr::none;
            fixture.fake.output_fault = MalformedAttr::none;
            // 请求旧 shape 也必须重新配置：硬件已切到 16，缓存不能继续声称 8 生效。
            require(fixture.forward(Tensor::from_dma_buf(42, {1, 8, 8, 3}, DataType::UINT8)) == ZHULONG_PLUGIN_OK,
                    "retry old shape after failed refresh");
            require(fixture.fake.shape_calls == 3 && fixture.fake.last_binding.dims[1] == 8,
                    "retry restores hardware shape and binding");
        }
    }
    BackendFixture fixture(input_attr(), true);
    fixture.fake.output_query_status = RKNN_ERR_TIMEOUT;
    require(fixture.forward(Tensor({1, 8, 8, 3}, DataType::UINT8)) == ZHULONG_PLUGIN_ERR_TIMEOUT,
            "real query errors retain SDK mapping");
    require(!fixture.state().shapes_set && fixture.outputs.empty(), "query error does not commit shape");
}

void test_sdk_uint32_boundaries() {
    constexpr uint32_t limit = std::numeric_limits<uint32_t>::max();
    auto attr = input_attr();
    attr.n_dims = 1;
    attr.dims[0] = limit;
    attr.n_elems = limit;
    attr.size = limit;
    attr.size_with_stride = limit;
    attr.w_stride = 0;
    BackendFixture fixture(attr);
    uint8_t placeholder = 0;
    // Stub 不读取输入，只记录参数：不分配 4 GiB、不把伪容量交给真实 Runtime。
    require(fixture.forward(Tensor::from_dma_buf(-1, {limit}, DataType::UINT8, &placeholder)) == ZHULONG_PLUGIN_OK,
            "UINT32_MAX Host byte count representable");
    require(fixture.fake.last_host_input.size == limit, "Host byte count is not truncated");
    require(fixture.forward(Tensor::from_dma_buf(42, {limit}, DataType::UINT8)) == ZHULONG_PLUGIN_OK,
            "UINT32_MAX DMA byte count representable");
    require(fixture.fake.imported_size == limit, "DMA byte count is not truncated");

    const uint32_t before_runs = fixture.fake.run_calls;
    require(fixture.forward(Tensor::from_dma_buf(42, {static_cast<int64_t>(limit) + 1}, DataType::UINT8)) ==
                ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT, "dimension above UINT32_MAX rejected");
    require(fixture.forward(Tensor::from_dma_buf(42, {1, 1, 2147483648LL, 2}, DataType::UINT8)) ==
                ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT, "bytes above UINT32_MAX rejected even when all dimensions fit");
    require(fixture.forward(Tensor::from_dma_buf(-1, {1073741824LL}, DataType::FLOAT32, &placeholder)) ==
                ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT, "oversized Host byte count rejected");
    require(fixture.fake.run_calls == before_runs && fixture.fake.import_calls == 1 && fixture.fake.host_calls == 1,
            "oversized input never reaches SDK");

    // dtype 转 float 后的输出字节数也受 uint32 API 限制。
    auto output = output_attr();
    output.dims[0] = 1073741824;
    output.n_elems = output.dims[0];
    output.type = RKNN_TENSOR_UINT8;
    output.size = output.n_elems;
    output.size_with_stride = output.size;
    fixture.state().current_output_attrs[0] = output;
    require(fixture.forward(Tensor::from_dma_buf(42, {limit}, DataType::UINT8)) == ZHULONG_PLUGIN_ERR_INTERNAL,
            "want_float output overflow rejected before allocation/run");
    require(fixture.fake.run_calls == before_runs && fixture.outputs.empty(), "oversized output never runs hardware");
}

void test_matching_shape_oversized_bytes() {
    auto attr = input_attr();
    attr.n_dims = 1;
    attr.dims[0] = 1073741824;
    attr.n_elems = attr.dims[0];
    attr.size = attr.n_elems;
    attr.size_with_stride = attr.size;
    attr.w_stride = 0;
    BackendFixture fixture(attr);
    uint8_t placeholder = 0;
    for (const int fd : {-1, 42}) {
        // 元素数和 shape 都匹配；仅 dtype 的字节倍率越界，不能靠 shape 校验碰巧拦住。
        require(fixture.forward(Tensor::from_dma_buf(fd, {attr.dims[0]}, DataType::FLOAT32, &placeholder)) ==
                    ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT, "matching shape with uint32 byte overflow rejected");
        require(fixture.outputs.empty(), "oversized byte count clears outputs");
    }
    require(fixture.fake.import_calls == 0 && fixture.fake.host_calls == 0 && fixture.fake.run_calls == 0,
            "matching-shape byte overflow never reaches SDK");
}

void test_dynamic_oversized_input_has_no_side_effects() {
    BackendFixture fixture(input_attr(), true);
    require(fixture.forward(Tensor::from_dma_buf(42, {1, 8, 8, 3}, DataType::UINT8)) == ZHULONG_PLUGIN_OK,
            "valid dynamic frame before overflow");
    const auto shapes = fixture.state().current_input_shapes;
    require(fixture.forward(Tensor::from_dma_buf(42, {1, 1, 4294967297LL, 1}, DataType::UINT8)) ==
                ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT, "oversized dynamic dimension rejected before set_input_shapes");
    require(fixture.fake.shape_calls == 1 && fixture.fake.input_queries == 1 && fixture.fake.import_calls == 1,
            "invalid dimensions leave SDK call counts unchanged");
    require(fixture.state().shapes_set && fixture.state().current_input_shapes == shapes &&
                fixture.state().cached_input_mems.size() == 1, "precheck preserves last valid shape/binding");
}

void test_output_release_and_failed_results() {
    BackendFixture fixture;
    fixture.fake.mismatch_output_size = true;
    require(fixture.forward(Tensor({1, 8, 8, 3}, DataType::UINT8)) == ZHULONG_PLUGIN_ERR_INTERNAL,
            "output byte mismatch rejected");
    require(fixture.outputs.empty() && fixture.fake.release_calls == 1, "mismatch clears results and releases output view");
    fixture.fake.mismatch_output_size = false;
    fixture.fake.get_status = RKNN_ERR_TIMEOUT;
    require(fixture.forward(Tensor({1, 8, 8, 3}, DataType::UINT8)) == ZHULONG_PLUGIN_ERR_TIMEOUT, "outputs_get timeout mapped");
    require(fixture.outputs.empty() && fixture.fake.release_calls == 1, "failed get has no armed release");
    fixture.fake.get_status = RKNN_SUCC;
    require(fixture.forward(Tensor({1, 8, 8, 3}, DataType::UINT8)) == ZHULONG_PLUGIN_OK, "success after failure");
    require(fixture.outputs[0].data<float>()[0] == 1.0f && fixture.fake.release_calls == 2,
            "preallocated output survives release");
    require(fixture.backend.forward(nullptr, {}, fixture.outputs) == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT &&
                fixture.outputs.empty(), "invalid session also clears previous outputs");
}

} // namespace

extern "C" {

int rknn_init(rknn_context* ctx, void*, uint32_t, uint32_t, rknn_init_extend*) {
    *ctx = runtime->next_context++;
    runtime->contexts.emplace(*ctx, FakeContext{runtime->model_input, runtime->model_output});
    return RKNN_SUCC;
}

int rknn_destroy(rknn_context ctx) {
    for (const auto& memory : runtime->memories) {
        require(memory.second != ctx, "context destroyed only after its registrations");
    }
    require(runtime->contexts.erase(ctx) == 1, "context destroyed exactly once");
    return RKNN_SUCC;
}

int rknn_dup_context(rknn_context* source, rknn_context* ctx) {
    *ctx = runtime->next_context++;
    runtime->contexts.emplace(*ctx, runtime->contexts.at(*source));
    return RKNN_SUCC;
}

int rknn_query(rknn_context ctx, rknn_query_cmd cmd, void* info, uint32_t size) {
    const auto& context = runtime->contexts.at(ctx);
    switch (cmd) {
        case RKNN_QUERY_IN_OUT_NUM:
            require(size == sizeof(rknn_input_output_num), "I/O query size");
            *static_cast<rknn_input_output_num*>(info) = {1, 1};
            return RKNN_SUCC;
        case RKNN_QUERY_SDK_VERSION:
        case RKNN_QUERY_MEM_SIZE:
            std::memset(info, 0, size);
            return RKNN_SUCC;
        case RKNN_QUERY_INPUT_DYNAMIC_RANGE: {
            auto& range = *static_cast<rknn_input_range*>(info);
            range.shape_number = runtime->dynamic ? 1 : 0;
            return RKNN_SUCC;
        }
        case RKNN_QUERY_CURRENT_INPUT_ATTR:
            ++runtime->input_queries;
            if (runtime->input_query_status != RKNN_SUCC) return runtime->input_query_status;
            break;
        case RKNN_QUERY_CURRENT_OUTPUT_ATTR:
            ++runtime->output_queries;
            if (runtime->output_query_status != RKNN_SUCC) return runtime->output_query_status;
            break;
        case RKNN_QUERY_INPUT_ATTR:
        case RKNN_QUERY_OUTPUT_ATTR:
            break;
        default:
            return RKNN_ERR_PARAM_INVALID;
    }
    require(size == sizeof(rknn_tensor_attr), "tensor query size");
    auto& attr = *static_cast<rknn_tensor_attr*>(info);
    require(attr.index == 0, "query tensor index set before SDK call");
    const bool input = cmd == RKNN_QUERY_INPUT_ATTR || cmd == RKNN_QUERY_CURRENT_INPUT_ATTR;
    attr = input ? context.input : context.output;
    if (cmd == RKNN_QUERY_CURRENT_INPUT_ATTR) corrupt_attr(attr, runtime->input_fault);
    if (cmd == RKNN_QUERY_CURRENT_OUTPUT_ATTR) corrupt_attr(attr, runtime->output_fault);
    return RKNN_SUCC;
}

int rknn_set_input_shapes(rknn_context ctx, uint32_t count, rknn_tensor_attr attrs[]) {
    ++runtime->shape_calls;
    require(count == 1 && attrs[0].n_dims > 0 && attrs[0].n_dims <= RKNN_MAX_DIMS, "shape API count and rank");
    require(std::strcmp(attrs[0].name, "input") == 0, "shape API preserves original tensor name");
    if (runtime->shape_status != RKNN_SUCC) return runtime->shape_status;
    auto& context = runtime->contexts.at(ctx);
    const auto model_type = context.input.type;
    context.input = attrs[0];
    context.input.type = model_type;
    uint64_t elements = 1;
    for (uint32_t d = 0; d < attrs[0].n_dims; ++d) elements *= attrs[0].dims[d];
    require(elements <= std::numeric_limits<uint32_t>::max(), "shape elements fit SDK fields");
    context.input.n_elems = static_cast<uint32_t>(elements);
    context.input.size = context.input.n_elems; // 动态测试模型为 UINT8。
    context.input.size_with_stride = context.input.size;
    context.input.w_stride = attrs[0].n_dims == 4 ? attrs[0].dims[2] : 0;
    context.output = output_attr(attrs[0].n_dims == 4 ? attrs[0].dims[2] : 1);
    return RKNN_SUCC;
}

rknn_tensor_mem* rknn_create_mem_from_fd(rknn_context ctx, int32_t fd, void* va, uint32_t size, int32_t offset) {
    require(offset == 0 && runtime->contexts.count(ctx) == 1, "import offset/context");
    ++runtime->import_calls;
    runtime->imported_size = size;
    auto* mem = new (std::nothrow) rknn_tensor_mem{};
    if (!mem) return nullptr;
    mem->fd = fd;
    mem->virt_addr = va;
    mem->size = size;
    runtime->memories.emplace(mem, ctx);
    return mem;
}

int rknn_destroy_mem(rknn_context ctx, rknn_tensor_mem* mem) {
    require(runtime->contexts.count(ctx) == 1 && runtime->memories.at(mem) == ctx, "memory released in owning context");
    runtime->memories.erase(mem);
    ++runtime->destroyed_mems;
    delete mem;
    return RKNN_SUCC;
}

int rknn_set_io_mem(rknn_context ctx, rknn_tensor_mem* mem, rknn_tensor_attr* attr) {
    require(runtime->memories.at(mem) == ctx, "binding uses registered memory in same context");
    ++runtime->bind_calls;
    if (runtime->bind_status != RKNN_SUCC) return runtime->bind_status;
    runtime->last_binding = *attr;
    return RKNN_SUCC;
}

int rknn_inputs_set(rknn_context, uint32_t count, rknn_input inputs[]) {
    require(count == 1 && inputs[0].buf != nullptr, "Host input count/pointer");
    ++runtime->host_calls;
    runtime->last_host_input = inputs[0];
    return RKNN_SUCC;
}

int rknn_run(rknn_context, rknn_run_extend* extend) {
    require(extend == nullptr, "synchronous Runtime call retained");
    ++runtime->run_calls;
    return runtime->run_status;
}

int rknn_outputs_get(rknn_context, uint32_t count, rknn_output outputs[], rknn_output_extend*) {
    if (runtime->get_status != RKNN_SUCC) return runtime->get_status;
    require(count == 1 && outputs[0].is_prealloc == 1 && outputs[0].want_float == 1 && outputs[0].buf != nullptr,
            "preallocated float output contract");
    // memcpy 写 scalar，避免假设 Tensor 的字节缓冲在所有平台上都具有 float 对齐。
    const float value = 1.0f;
    std::memcpy(outputs[0].buf, &value, sizeof(value));
    if (runtime->mismatch_output_size) --outputs[0].size;
    return RKNN_SUCC;
}

int rknn_outputs_release(rknn_context, uint32_t, rknn_output[]) {
    ++runtime->release_calls;
    return RKNN_SUCC;
}

} // extern "C"

int main() {
    const std::pair<const char*, void (*)()> tests[]{
        {"packed_dma_gate", test_packed_dma_gate},
        {"host_conversion_and_mode_switch", test_host_conversion_and_mode_switch},
        {"dtype_rebind_and_rollback", test_dtype_rebind_and_rollback},
        {"dynamic_shapes_and_worker_isolation", test_dynamic_shapes_and_worker_isolation},
        {"dynamic_unbatched_3d_input_expanded_to_4d", test_dynamic_unbatched_3d_input_expanded_to_4d},
        {"invalid_current_metadata_and_retry", test_invalid_current_metadata_and_retry},
        {"sdk_uint32_boundaries", test_sdk_uint32_boundaries},
        {"matching_shape_oversized_bytes", test_matching_shape_oversized_bytes},
        {"dynamic_oversized_input_has_no_side_effects", test_dynamic_oversized_input_has_no_side_effects},
        {"output_release_and_failed_results", test_output_release_and_failed_results},
    };
    for (const auto& test : tests) {
        test.second();
        std::printf("PASS RKNN_CONTRACT %s\n", test.first);
    }
    return 0;
}
