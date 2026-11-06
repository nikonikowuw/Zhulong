// 宿主 SDK/UAPI 契约回归：真实 RGA 后端 + im2d / DMA ioctl stub。
// 只证明顺序、错误状态、Tensor 提交与 fd 所有权，不证明真实 DMA cache/图像精度。

#include <backends/rga/rga_vision_backend.hpp>
#include <platform/dmabuf/dmabuf_sync.hpp>

#include <cstddef>
#include <rga/im2d.h>

#include <algorithm>
#include <array>
#include <cerrno>
#include <cstdarg>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <fcntl.h>
#include <map>
#include <unistd.h>
#include <utility>
#include <vector>

extern "C" int __real_open(const char*, int, ...);
extern "C" std::FILE* __real_fopen(const char*, const char*);
extern "C" int __real_ioctl(int, unsigned long, ...);

namespace {

using zhulong::DataType;
using zhulong::LetterboxParams;
using zhulong::Tensor;

void require(bool condition, const char* label) {
    if (!condition) {
        std::fprintf(stderr, "FAIL %s\n", label);
        std::exit(1);
    }
}

enum class EventKind { check, start, end, run };

struct Event {
    EventKind kind;
    int fd{-1};
    uint64_t flags{0};
    uint8_t first_byte{0};
};

struct FakeRga {
    bool device_available{true};
    bool hardware_readable{false};
    IM_STATUS header_status{IM_STATUS_NOERROR};
    IM_STATUS check_status{IM_STATUS_NOERROR};
    IM_STATUS run_status{IM_STATUS_SUCCESS};
    int sync_error_fd{-1};
    int start_error{0};
    int end_error{0};
    unsigned start_interrupts{0};
    unsigned end_interrupts{0};
    bool permanent_start_error{false};
    bool permanent_end_error{false};
    std::map<int, std::pair<uint8_t*, size_t>> mappings;
    std::array<Event, 512> events{};
    size_t event_count{0};
    rga_buffer_t last_destination{};
    im_rect last_destination_rect{};

    void record(Event event) {
        require(event_count < events.size(), "bounded fake event log");
        events[event_count++] = event;
    }

    size_t count(EventKind kind) const {
        return static_cast<size_t>(std::count_if(events.begin(), events.begin() + event_count,
                                                [kind](const Event& event) { return event.kind == kind; }));
    }
};

FakeRga* runtime = nullptr;

class RuntimeScope {
public:
    explicit RuntimeScope(FakeRga& fake) {
        require(runtime == nullptr, "one fake RGA at a time");
        runtime = &fake;
    }
    ~RuntimeScope() { runtime = nullptr; }
    RuntimeScope(const RuntimeScope&) = delete;
    RuntimeScope& operator=(const RuntimeScope&) = delete;
    RuntimeScope(RuntimeScope&&) = delete;
    RuntimeScope& operator=(RuntimeScope&&) = delete;
};

struct BackendFixture {
    FakeRga fake;
    RuntimeScope scope{fake};
    zhulong::RgaVisionBackend backend;

    explicit BackendFixture(FakeRga config = {}) : fake(std::move(config)) {
        fake.event_count = 0; // 不把构造期间的偏移探测计入每帧调用顺序。
    }
};

class TestFd {
public:
    TestFd() : fd_(__real_open("/dev/null", O_RDWR | O_CLOEXEC)) { require(fd_ >= 0, "test owner fd opened"); }
    ~TestFd() { ::close(fd_); }
    TestFd(const TestFd&) = delete;
    TestFd& operator=(const TestFd&) = delete;
    TestFd(TestFd&&) = delete;
    TestFd& operator=(TestFd&&) = delete;
    int get() const noexcept { return fd_; }
    bool open() const noexcept { return ::fcntl(fd_, F_GETFD) >= 0; }
private:
    int fd_;
};

zhulong_frame_view packed_view(std::vector<uint8_t>& pixels, int height = 64) {
    zhulong_frame_view view{};
    view.dma_buf_fd = -1;
    view.width = 64;
    view.height = height;
    view.format = ZHULONG_PIXEL_FORMAT_RGB24;
    view.planes[0] = pixels.data();
    view.strides[0] = 192;
    return view;
}

LetterboxParams parameters() {
    LetterboxParams params;
    params.target_width = 32;
    params.target_height = 32;
    params.scale = -7.0f;
    params.pad_left = -7;
    params.pad_top = -7;
    return params;
}

void require_unchanged_geometry(const LetterboxParams& params) {
    require(params.scale == -7.0f && params.pad_left == -7 && params.pad_top == -7,
            "failed letterbox does not publish geometry");
}

void test_probe_failures_are_observable() {
    {
        FakeRga config;
        config.header_status = IM_STATUS_ERROR_VERSION;
        BackendFixture fixture(config);
        require(!fixture.backend.rga_usable() && fixture.backend.last_error() == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED,
                "header/version probe failure recorded");
    }
    {
        FakeRga config;
        config.device_available = false;
        BackendFixture fixture(config);
        require(!fixture.backend.rga_usable() && fixture.backend.last_error() == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED,
                "device access probe failure recorded");
    }
    {
        FakeRga config;
        config.check_status = IM_STATUS_NOT_SUPPORTED;
        BackendFixture fixture(config);
        require(!fixture.backend.rga_usable() && fixture.backend.last_error() == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED,
                "failed offset probe recorded");
    }
    {
        FakeRga config;
        config.hardware_readable = true;
        BackendFixture fixture(config);
        require(fixture.backend.rga_usable() && fixture.backend.last_error() == ZHULONG_PLUGIN_OK,
                "successful capability probe reports OK");
    }
    BackendFixture fixture;
    require(fixture.backend.rga_usable() && fixture.backend.last_error() == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED,
            "debugfs stride fallback remains observable before first successful operation");
}

void test_success_clears_error_for_all_operations() {
    BackendFixture fixture;
    std::vector<uint8_t> source(64 * 64 * 3, 79);
    std::vector<uint8_t> destination(32 * 32 * 3);
    auto view = packed_view(source);
    const zhulong::CropRect roi{0, 0, 32, 32};
    for (const int operation : {0, 1, 2}) {
        Tensor tensor;
        auto params = parameters();
        const auto call = [&]() {
            switch (operation) {
                case 0:
                    return fixture.backend.resize(source.data(), 64, 64, 192, view.format,
                                                   destination.data(), 96, 32, 32, view.format);
                case 1:
                    return fixture.backend.letterbox(view, tensor, params);
                default:
                    return fixture.backend.crop_scale(view, roi, destination.data(), 96, 32, 32, view.format);
            }
        };
        fixture.fake.check_status = IM_STATUS_NOT_SUPPORTED;
        const auto previous_fallbacks = fixture.backend.fallback_count();
        require(call() == ZHULONG_PLUGIN_OK, "failed RGA precheck falls back to CPU successfully");
        require(fixture.backend.last_error() == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED &&
                    fixture.backend.fallback_count() == previous_fallbacks + 1, "fallback reason and count retained");
        fixture.fake.check_status = IM_STATUS_NOERROR;
        require(call() == ZHULONG_PLUGIN_OK, "RGA success after fallback");
        require(fixture.backend.last_error() == ZHULONG_PLUGIN_OK &&
                    fixture.backend.fallback_count() == previous_fallbacks + 1, "success clears stale error without fallback");
    }
}

void test_dma_padding_sync_precedes_hardware() {
    BackendFixture fixture;
    TestFd owner;
    std::vector<uint8_t> source(64 * 32 * 3, 79);
    std::vector<uint8_t> destination(32 * 32 * 3, 55);
    fixture.fake.mappings.emplace(owner.get(), std::make_pair(destination.data(), destination.size()));
    auto view = packed_view(source, 32);
    Tensor tensor = Tensor::from_dma_buf(owner.get(), {32, 32, 3}, DataType::UINT8, destination.data());
    auto params = parameters();
    require(fixture.backend.letterbox(view, tensor, params) == ZHULONG_PLUGIN_OK, "mapped DMA letterbox succeeds");
    require(fixture.fake.event_count == 4, "one precheck, one CPU sync pair, one hardware run");
    const auto& events = fixture.fake.events;
    require(events[0].kind == EventKind::check && events[1].kind == EventKind::start &&
                events[2].kind == EventKind::end && events[3].kind == EventKind::run, "check -> START -> END -> RGA order");
    require(events[1].flags == DMA_BUF_SYNC_WRITE && events[2].flags == (DMA_BUF_SYNC_END | DMA_BUF_SYNC_WRITE),
            "padding uses matching WRITE flags");
    require(events[1].fd == owner.get() && events[2].fd == owner.get() &&
                events[1].first_byte == 55 && events[2].first_byte == 114, "CPU padding occurs inside sync bracket");
    require(destination[0] == 114 && destination[8 * 32 * 3] == 17, "padding survives stub ROI write");
    require(fixture.fake.last_destination.fd == owner.get() && fixture.fake.last_destination.width == 32 &&
                fixture.fake.last_destination.height == 32 && fixture.fake.last_destination_rect.y == 8,
            "RGA describes full target; offset expressed by drect only");
    require(params.scale == 0.5f && params.pad_top == 8 && params.pad_left == 0, "geometry committed on success");
    require(tensor.raw_data() == destination.data() && tensor.dma_buf_fd() == owner.get() && owner.open(),
            "reused DMA target keeps mapping and borrowed fd");
}

void test_dma_sync_failure_stops_without_fallback() {
    for (const bool end_failure : {false, true}) {
        BackendFixture fixture;
        TestFd owner;
        std::vector<uint8_t> source(64 * 32 * 3, 79);
        std::vector<uint8_t> destination(32 * 32 * 3, 55);
        fixture.fake.mappings.emplace(owner.get(), std::make_pair(destination.data(), destination.size()));
        fixture.fake.sync_error_fd = owner.get();
        if (end_failure) {
            fixture.fake.end_error = EIO;
            fixture.fake.permanent_end_error = true;
        } else {
            fixture.fake.start_error = EIO;
            fixture.fake.permanent_start_error = true;
        }
        auto view = packed_view(source, 32);
        // dtype/shape 不同但容量够：失败后连元数据重绑也不能提交。
        Tensor tensor = Tensor::from_dma_buf(owner.get(), {768}, DataType::INT32, destination.data());
        auto params = parameters();
        require(fixture.backend.letterbox(view, tensor, params) == ZHULONG_PLUGIN_ERR_INVALID_STATE,
                "START/END failure fails closed");
        require(fixture.fake.count(EventKind::run) == 0 && fixture.backend.fallback_count() == 0,
                "failed cache sync cannot submit hardware or retry unsafe CPU fallback");
        require(tensor.shape() == std::vector<int64_t>{768} && tensor.dtype() == DataType::INT32 && owner.open(),
                "failed sync leaves target metadata/fd ownership unchanged");
        require(fixture.backend.last_error() == ZHULONG_PLUGIN_ERR_INVALID_STATE, "sync failure reason recorded");
        require_unchanged_geometry(params);
        require(destination[0] == (end_failure ? 114 : 55), "START failure precedes writes; END failure can leave dirty bytes");
        require(fixture.fake.count(EventKind::start) == 1 && fixture.fake.count(EventKind::end) == (end_failure ? 1U : 0U),
                "no spurious or duplicate END call");
    }
}

void test_cpu_fallback_syncs_borrowed_source_and_target() {
    BackendFixture fixture;
    TestFd source_owner;
    TestFd target_owner;
    std::vector<uint8_t> source(64 * 32 * 3, 79);
    std::vector<uint8_t> destination(32 * 32 * 3, 55);
    fixture.fake.mappings.emplace(source_owner.get(), std::make_pair(source.data(), source.size()));
    fixture.fake.mappings.emplace(target_owner.get(), std::make_pair(destination.data(), destination.size()));
    auto view = packed_view(source, 32);
    view.dma_buf_fd = source_owner.get();
    Tensor tensor = Tensor::from_dma_buf(target_owner.get(), {768}, DataType::INT32, destination.data());
    auto params = parameters();
    fixture.fake.check_status = IM_STATUS_NOT_SUPPORTED;
    require(fixture.backend.letterbox(view, tensor, params) == ZHULONG_PLUGIN_OK, "CPU fallback with DMA source/target");
    require(fixture.fake.event_count == 5 && fixture.fake.count(EventKind::run) == 0, "CPU fallback adds two sync pairs only");
    const auto& events = fixture.fake.events;
    require(events[1].kind == EventKind::start && events[1].fd == source_owner.get() && events[1].flags == DMA_BUF_SYNC_READ &&
                events[2].kind == EventKind::start && events[2].fd == target_owner.get() && events[2].flags == DMA_BUF_SYNC_WRITE,
            "fallback begins source READ and target WRITE before CPU access");
    require(events[3].kind == EventKind::end && events[3].fd == target_owner.get() &&
                events[4].kind == EventKind::end && events[4].fd == source_owner.get(), "fallback ends both CPU windows");
    require(destination[0] == 114 && destination[8 * 32 * 3] == 79, "real CPU fallback writes padding and content");
    require(tensor.dtype() == DataType::UINT8 && tensor.shape() == std::vector<int64_t>({32, 32, 3}) &&
                tensor.dma_buf_fd() == target_owner.get(), "successful fallback commits rebind, not CPU reallocation");
    require(source_owner.open() && target_owner.open(), "neither borrowed source nor target fd closed");
}

void test_failed_precheck_does_not_change_destination() {
    for (const bool dma_target : {false, true}) {
        BackendFixture fixture;
        TestFd owner;
        std::vector<uint8_t> source(64 * 64 * 4, 79);
        std::vector<uint8_t> external(32 * 32 * 3, 55);
        auto view = packed_view(source);
        view.format = ZHULONG_PIXEL_FORMAT_RGBA32; // RGA 支持，但 CPU letterbox 不支持。
        view.strides[0] = 256;
        Tensor tensor = dma_target ? Tensor::from_dma_buf(owner.get(), {768}, DataType::INT32, external.data())
                                   : Tensor({1}, DataType::UINT8);
        if (!dma_target) tensor.data<uint8_t>()[0] = 77;
        const auto shape = tensor.shape();
        const auto dtype = tensor.dtype();
        const auto pointer = tensor.raw_data();
        const auto fd = tensor.dma_buf_fd();
        auto params = parameters();
        fixture.fake.check_status = IM_STATUS_NOT_SUPPORTED;
        require(fixture.backend.letterbox(view, tensor, params) == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED,
                "RGA precheck and CPU unsupported format both fail");
        require(tensor.shape() == shape && tensor.dtype() == dtype && tensor.raw_data() == pointer && tensor.dma_buf_fd() == fd,
                "failed attempt preserves shape, type, memory and fd");
        require((dma_target ? external[0] : tensor.data<uint8_t>()[0]) == (dma_target ? 55 : 77),
                "precheck rejection performs no padding write");
        require(fixture.fake.count(EventKind::check) == 1 && fixture.fake.count(EventKind::run) == 0 &&
                    fixture.fake.count(EventKind::start) == 0, "precheck failure never enters CPU sync or hardware");
        require_unchanged_geometry(params);
    }
}

void test_sync_guard_retries_and_balances_end() {
    BackendFixture fixture;
    TestFd owner;
    fixture.fake.sync_error_fd = owner.get();
    fixture.fake.start_error = EINTR;
    fixture.fake.end_error = EAGAIN;
    fixture.fake.start_interrupts = 2;
    fixture.fake.end_interrupts = 1;
    {
        zhulong::platform::ScopedFdSync sync(owner.get(), zhulong::platform::kDmaSyncWrite);
        require(sync.active(), "START retries EINTR");
        require(sync.finish() && !sync.active(), "explicit END retries EAGAIN and disarms destructor");
    }
    require(fixture.fake.count(EventKind::start) == 3 && fixture.fake.count(EventKind::end) == 2,
            "only failed requests retried; destructor does not duplicate END");
    fixture.fake.event_count = 0;
    {
        zhulong::platform::ScopedFdSync sync(owner.get(), zhulong::platform::kDmaSyncRead);
        require(sync.active(), "RAII READ begins");
    }
    require(fixture.fake.count(EventKind::start) == 1 && fixture.fake.count(EventKind::end) == 1,
            "early scope exit balances END");
    fixture.fake.event_count = 0;
    {
        zhulong::platform::ScopedFdSync sync(-1, zhulong::platform::kDmaSyncWrite);
        require(!sync.active() && sync.finish(), "Host/no fd is a successful no-op");
    }
    require(fixture.fake.event_count == 0, "Host buffer has no sync ioctl");
    fixture.fake.start_error = EAGAIN;
    fixture.fake.permanent_start_error = true;
    {
        zhulong::platform::ScopedFdSync sync(owner.get(), zhulong::platform::kDmaSyncWrite);
        require(!sync.active(), "permanent EAGAIN terminates bounded retries");
    }
    require(fixture.fake.count(EventKind::start) == 51 && fixture.fake.count(EventKind::end) == 0,
            "retry limit enforced; failed START never gets END");
    require(owner.open(), "sync guard never closes borrowed fd");
}

} // namespace

extern "C" int __wrap_open(const char* path, int flags, ...) {
    mode_t mode = 0;
    if ((flags & O_CREAT) != 0) {
        va_list args;
        va_start(args, flags);
        mode = static_cast<mode_t>(va_arg(args, int));
        va_end(args);
    }
    if (std::strcmp(path, "/dev/rga") == 0) {
        if (!runtime->device_available) {
            errno = EACCES;
            return -1;
        }
        return __real_open("/dev/null", flags, mode);
    }
    return __real_open(path, flags, mode);
}

extern "C" std::FILE* __wrap_fopen(const char* path, const char* mode) {
    if (std::strcmp(path, "/sys/kernel/debug/rkrga/hardware") == 0) {
        if (!runtime->hardware_readable) {
            errno = ENOENT;
            return nullptr;
        }
        auto* file = std::tmpfile();
        require(file != nullptr, "fake hardware debugfs file");
        std::fputs("byte_stride_align: 4\n", file);
        std::rewind(file);
        return file;
    }
    if (std::strcmp(path, "/sys/kernel/debug/rkrga/version") == 0) {
        errno = ENOENT;
        return nullptr;
    }
    return __real_fopen(path, mode);
}

extern "C" int __wrap_ioctl(int fd, unsigned long request, ...) {
    va_list args;
    va_start(args, request);
    void* info = va_arg(args, void*);
    va_end(args);
    if (request != DMA_BUF_IOCTL_SYNC) return __real_ioctl(fd, request, info);
    const auto flags = static_cast<dma_buf_sync*>(info)->flags;
    const bool ending = (flags & DMA_BUF_SYNC_END) != 0;
    const auto mapping = runtime->mappings.find(fd);
    const uint8_t before = mapping == runtime->mappings.end() ? 0 : mapping->second.first[0];
    runtime->record({ending ? EventKind::end : EventKind::start, fd, flags, before});
    if (fd == runtime->sync_error_fd) {
        auto& remaining = ending ? runtime->end_interrupts : runtime->start_interrupts;
        const bool permanent = ending ? runtime->permanent_end_error : runtime->permanent_start_error;
        if (permanent || remaining > 0) {
            if (remaining > 0) --remaining;
            errno = ending ? runtime->end_error : runtime->start_error;
            return -1;
        }
    }
    return 0;
}

IM_STATUS imcheckHeader(im_api_version_t) { return runtime->header_status; }

extern "C" const char* querystring(int) { return "contract-stub"; }

extern "C" IM_STATUS imcheck_t(rga_buffer_t, rga_buffer_t, rga_buffer_t, im_rect, im_rect, im_rect, int) {
    runtime->record({EventKind::check});
    return runtime->check_status;
}

extern "C" rga_buffer_t wrapbuffer_virtualaddr_t(void* address, int width, int height, int wstride, int hstride, int format) {
    rga_buffer_t buffer{};
    buffer.vir_addr = address;
    buffer.fd = -1;
    buffer.width = width;
    buffer.height = height;
    buffer.wstride = wstride;
    buffer.hstride = hstride;
    buffer.format = format;
    return buffer;
}

extern "C" rga_buffer_t wrapbuffer_fd_t(int fd, int width, int height, int wstride, int hstride, int format) {
    auto buffer = wrapbuffer_virtualaddr_t(nullptr, width, height, wstride, hstride, format);
    buffer.fd = fd;
    return buffer;
}

IM_STATUS improcess(rga_buffer_t, rga_buffer_t destination, rga_buffer_t, im_rect, im_rect drect, im_rect,
                    int, int*, im_opt_t*, int usage) {
    require(usage == IM_SYNC, "synchronous RGA submission retained");
    runtime->record({EventKind::run});
    runtime->last_destination = destination;
    runtime->last_destination_rect = drect;
    if (runtime->run_status != IM_STATUS_SUCCESS && runtime->run_status != IM_STATUS_NOERROR) return runtime->run_status;
    auto* pixels = static_cast<uint8_t*>(destination.vir_addr);
    if (destination.fd >= 0) {
        const auto mapping = runtime->mappings.find(destination.fd);
        require(mapping != runtime->mappings.end(), "fake DMA destination has known mapping");
        require(mapping->second.second >= static_cast<size_t>(destination.wstride) * destination.hstride * 3,
                "full target fits known allocation");
        pixels = mapping->second.first;
    }
    require(pixels != nullptr && drect.x >= 0 && drect.y >= 0 && drect.x + drect.width <= destination.width &&
                drect.y + drect.height <= destination.height, "stub ROI is within full target");
    for (int row = 0; row < drect.height; ++row) {
        const size_t offset = (static_cast<size_t>(drect.y + row) * destination.wstride + drect.x) * 3;
        std::memset(pixels + offset, 17, static_cast<size_t>(drect.width) * 3);
    }
    return runtime->run_status;
}

int main() {
    const std::pair<const char*, void (*)()> tests[]{
        {"probe_failures_are_observable", test_probe_failures_are_observable},
        {"success_clears_error_for_all_operations", test_success_clears_error_for_all_operations},
        {"dma_padding_sync_precedes_hardware", test_dma_padding_sync_precedes_hardware},
        {"dma_sync_failure_stops_without_fallback", test_dma_sync_failure_stops_without_fallback},
        {"cpu_fallback_syncs_borrowed_source_and_target", test_cpu_fallback_syncs_borrowed_source_and_target},
        {"failed_precheck_does_not_change_destination", test_failed_precheck_does_not_change_destination},
        {"sync_guard_retries_and_balances_end", test_sync_guard_retries_and_balances_end},
    };
    for (const auto& test : tests) {
        test.second();
        std::printf("PASS RGA_CONTRACT %s\n", test.first);
    }
    return 0;
}
