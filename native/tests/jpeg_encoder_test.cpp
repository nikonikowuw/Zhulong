/**
 * @file jpeg_encoder_test.cpp
 * @brief JPEG 编码后端（软件 / VEPU 硬件）单元测试
 *
 * 覆盖的契约：
 * 1. 后端选择的语义：`ZHULONG_JPEG_BACKEND` 关闭硬件时恒为 software；
 * 2. 两条后端共享同一套几何规则：产物 SOF 尺寸 = 请求矩形尺寸（含非 16 对齐尺寸）；
 * 3. 裁剪矩形与全幅矩形走同一条路径，越界/退化矩形被拒绝而非产出坏图；
 * 4. **亮度敏感性**：仅亮度不同、色度相同的两帧必须产出不同字节 —— 这条直接
 *    拦「编码成功但恒为纯灰图」的静默错误（某些 BSP 上真实出现过）；
 * 5. 硬件路径存在时：尺寸矩阵全部可编，且会话池在尺寸churn 下不崩、不退化。
 *
 * ⚠️ CMake 注册的用例强制 `ZHULONG_JPEG_BACKEND=cpu`（宿主机没有 /dev/mpp_service）。
 *    板端验证硬件路径：`ZHULONG_JPEG_BACKEND=mpp ./ZhulongJpegEncoderTests`。
 */

#include "Zhulong/frame.hpp"
#include "nodes/snapshot/jpeg_encoder.hpp"
#include "nodes/snapshot/jpeg_encoder_backend.hpp"
#include "nodes/snapshot/mpp_jpeg_encoder.hpp"

#include <array>
#include <cstdlib>
#if defined(__linux__)
#include <fcntl.h>
#include <sys/ioctl.h>
#include <sys/mman.h>
#include <unistd.h>
#if __has_include(<linux/dma-heap.h>)
#include <linux/dma-heap.h>
#else
#define DMA_HEAP_IOC_MAGIC 'H'
struct dma_heap_allocation_data {
    uint64_t len;
    uint32_t fd_flags;
    uint32_t heap_flags;
};
#define DMA_HEAP_IOCTL_ALLOC _IOWR(DMA_HEAP_IOC_MAGIC, 0x0, struct dma_heap_allocation_data)
#endif
#endif
#include <cstring>
#include <iostream>
#include <stdexcept>
#include <string>
#include <vector>

#define CHECK(condition) \
    do { \
        if (!(condition)) { \
            throw std::runtime_error("Check failed at line " + std::to_string(__LINE__) + \
                                     ": " #condition); \
        } \
    } while (false)

namespace {

using zhulong::CpuFrameView;
using zhulong::JpegBackend;
using zhulong::FRAME_FORMAT_NV12;
using zhulong::JpegBackendKind;
using zhulong::MppJpegEncoder;

// ---------------------------------------------------------------------------
//                               测试夹具
// ---------------------------------------------------------------------------

/** @brief 合成 NV12 源：stride 故意大于宽度，验证非紧凑布局 */
struct Nv12Image {
    uint32_t width{0};
    uint32_t height{0};
    uint32_t stride{0};
    std::vector<uint8_t> storage;

    Nv12Image(uint32_t w, uint32_t h, uint8_t y_value, uint8_t uv_value, uint32_t pad = 16)
        : width(w), height(h), stride(w + pad) {
        const size_t y_size = static_cast<size_t>(stride) * height;
        storage.assign(y_size + y_size / 2, 0);
        std::memset(storage.data(), y_value, y_size);
        std::memset(storage.data() + y_size, uv_value, y_size / 2);
    }

    CpuFrameView view() {
        CpuFrameView v{};
        v.width = width;
        v.height = height;
        v.format = FRAME_FORMAT_NV12;
        v.planes[0] = storage.data();
        v.planes[1] = storage.data() + static_cast<size_t>(stride) * height;
        v.strides[0] = stride;
        return v;
    }
};

/**
 * @brief 一块 `/dev/dma_heap/system` 分配的大页缓冲（测试用，替代解码器帧）
 *
 * 用它模拟解码器产出的 DMA-BUF：解码器帧在软件侧同样只暴露「fd + stride + vstride」。
 */
struct DmaHeapBuffer {
    int fd{-1};
    void *va{nullptr};
    size_t size{0};

    ~DmaHeapBuffer() {
        if (va != nullptr) {
            munmap(va, size);
        }
        if (fd >= 0) {
            close(fd);
        }
    }

    bool alloc(size_t len) {
#if defined(__linux__)
        const int hfd = open("/dev/dma_heap/system", O_RDWR | O_CLOEXEC);
        if (hfd < 0) {
            return false;
        }
        struct dma_heap_allocation_data data;
        std::memset(&data, 0, sizeof(data));
        data.len = len;
        data.fd_flags = O_RDWR | O_CLOEXEC;
        if (ioctl(hfd, DMA_HEAP_IOCTL_ALLOC, &data) < 0) {
            close(hfd);
            return false;
        }
        close(hfd);
        void *mapped = mmap(nullptr, len, PROT_READ | PROT_WRITE, MAP_SHARED, data.fd, 0);
        if (mapped == MAP_FAILED) {
            close(data.fd);
            return false;
        }
        fd = data.fd;
        va = mapped;
        size = len;
        return true;
#else
        (void)len;
        return false;
#endif
    }
};

/** @brief 从 JPEG 的 SOF 段读出编码尺寸（不链接解码器） */
bool read_jpeg_sof_size(const std::vector<uint8_t> &jpeg, int &out_w, int &out_h) {
    size_t pos = 2;
    while (pos + 4 <= jpeg.size()) {
        if (jpeg[pos] != 0xFF) {
            return false;
        }
        const uint8_t marker = jpeg[pos + 1];
        if (marker == 0xD8 || marker == 0xD9 || (marker >= 0xD0 && marker <= 0xD7) ||
            marker == 0x01) {
            pos += 2;
            continue;
        }
        if (pos + 4 > jpeg.size()) {
            return false;
        }
        const size_t seg_len = (static_cast<size_t>(jpeg[pos + 2]) << 8) | jpeg[pos + 3];
        if (seg_len < 2 || pos + 2 + seg_len > jpeg.size()) {
            return false;
        }
        const bool is_sof =
            marker >= 0xC0 && marker <= 0xCF && marker != 0xC4 && marker != 0xC8 && marker != 0xCC;
        if (is_sof) {
            if (seg_len < 8) {
                return false;
            }
            out_h = (static_cast<int>(jpeg[pos + 5]) << 8) | jpeg[pos + 6];
            out_w = (static_cast<int>(jpeg[pos + 7]) << 8) | jpeg[pos + 8];
            return out_w > 0 && out_h > 0;
        }
        pos += 2 + seg_len;
    }
    return false;
}

void check_jpeg(const std::vector<uint8_t> &jpeg, int expect_w, int expect_h) {
    CHECK(jpeg.size() > 4);
    CHECK(jpeg[0] == 0xFF && jpeg[1] == 0xD8);
    CHECK(jpeg[jpeg.size() - 2] == 0xFF && jpeg[jpeg.size() - 1] == 0xD9);
    int w = 0;
    int h = 0;
    CHECK(read_jpeg_sof_size(jpeg, w, h));
    CHECK(w == expect_w);
    CHECK(h == expect_h);
}

// ---------------------------------------------------------------------------
//                                用例
// ---------------------------------------------------------------------------

void test_backend_selection() {
    JpegBackend &backend = JpegBackend::instance();
    CHECK(backend.available());

    const char *raw = std::getenv("ZHULONG_JPEG_BACKEND");
    const bool explicit_cpu = raw != nullptr && std::strcmp(raw, "cpu") == 0;
    if (explicit_cpu) {
        // 显式关闭硬件时，硬件绝不能被静默打开（CI / 宿主机的确定性保障）
        CHECK(backend.kind() == JpegBackendKind::kSoftware);
        CHECK(!backend.hardware());
    }
    std::cout << "  backend = " << JpegBackend::kind_name(backend.kind())
              << (backend.hardware() ? " (hardware enabled)" : " (hardware unavailable)") << "\n";
}

void test_fullframe_geometry() {
    Nv12Image image(320, 240, 128, 128);
    CpuFrameView view = image.view();
    JpegBackend &backend = JpegBackend::instance();

    std::vector<uint8_t> jpeg;
    CHECK(backend.encode(view, 0, 0, static_cast<int>(image.width),
                         static_cast<int>(image.height), 85, jpeg));
    check_jpeg(jpeg, 320, 240);
}

void test_crop_geometry() {
    Nv12Image image(320, 240, 100, 128);
    CpuFrameView view = image.view();
    JpegBackend &backend = JpegBackend::instance();

    // 非 16 对齐的裁剪尺寸：硬件后端不得改变产物几何
    for (const auto &rect : {std::array<int, 4>{30, 20, 100, 80}, std::array<int, 4>{0, 0, 66, 66},
                             std::array<int, 4>{128, 96, 60, 48}}) {
        std::vector<uint8_t> jpeg;
        CHECK(backend.encode(view, rect[0], rect[1], rect[2], rect[3], 85, jpeg));
        check_jpeg(jpeg, rect[2], rect[3]);
    }
}

void test_degenerate_rect_rejected() {
    Nv12Image image(160, 120, 128, 128);
    CpuFrameView view = image.view();
    JpegBackend &backend = JpegBackend::instance();
    std::vector<uint8_t> jpeg;

    CHECK(!backend.encode(view, 0, 0, 0, 40, 85, jpeg));  // 宽度为 0
    CHECK(!backend.encode(view, 0, 0, 40, 0, 85, jpeg));  // 高度为 0
    CHECK(jpeg.empty());
    // 完全在画面外的矩形：对齐/clamp 后退化，应拒绝而不是产出空图
    CHECK(!backend.encode(view, 400, 400, 40, 40, 85, jpeg));
    CHECK(jpeg.empty());
}

/**
 * @brief 亮度敏感性：拦「编码成功但恒为纯灰」的静默错误
 *
 * 两帧仅 Y 平面不同（16 vs 235），色度同为 128。任何真正使用亮度平面的编码器
 * 必然产出不同字节；忽略 Y 平面的实现会产出完全相同的字节。
 */
void test_luma_sensitivity() {
    Nv12Image dark(160, 120, 16, 128);
    Nv12Image bright(160, 120, 235, 128);
    CpuFrameView dark_view = dark.view();
    CpuFrameView bright_view = bright.view();
    JpegBackend &backend = JpegBackend::instance();

    std::vector<uint8_t> dark_jpeg;
    std::vector<uint8_t> bright_jpeg;
    CHECK(backend.encode(dark_view, 0, 0, 160, 120, 85, dark_jpeg));
    CHECK(backend.encode(bright_view, 0, 0, 160, 120, 85, bright_jpeg));
    check_jpeg(dark_jpeg, 160, 120);
    check_jpeg(bright_jpeg, 160, 120);
    CHECK(dark_jpeg != bright_jpeg);
}

/** @brief 硬编分流阀值：小图留在软件侧（实测小图硬编 wall 更差、CPU 持平） */
void test_hardware_routing() {
    JpegBackend &backend = JpegBackend::instance();
    if (!backend.hardware()) {
        // 软件后端下绝不允许有任何图被路由到硬件
        CHECK(!backend.prefer_hardware(1920, 1080));
        CHECK(!backend.prefer_hardware(320, 240));
        std::cout << "  routing: software only\n";
        return;
    }
    CHECK(backend.prefer_hardware(1920, 1080));
    CHECK(backend.prefer_hardware(320, 240));
    CHECK(backend.prefer_hardware(160, 160));
    CHECK(!backend.prefer_hardware(128, 128));  // 阀值本身：实测硬件不劣但无收益
    CHECK(!backend.prefer_hardware(66, 66));    // 人脸脸框量级
    CHECK(!backend.prefer_hardware(0, 100));
    CHECK(!backend.prefer_hardware(100, -1));
    std::cout << "  routing: hw>& " << JpegBackend::kMinPixelsForHardware << " px\n";
}

/**
 * @brief 直通 vs 上传：同一帧必须产出**字节级相同**的 JPEG
 *
 * 这是「DMA-BUF 直通没读错像素」的唯一低成本证明：两条路径喂给编码器的是同一批
 * 像素，只有来源不同（一个来自 import 的 fd，一个来自 CPU 上传的副本）。编码器
 * 是确定性的，因此产物必然逐字节相等；任何 stride / 偏移 / 一致性错误都会让
 * 字节流立刻分叉。
 */
void test_dma_passthrough_matches_upload(MppJpegEncoder &encoder) {
    constexpr uint32_t kW = 320;
    constexpr uint32_t kH = 240;

    DmaHeapBuffer buffer;
    if (!buffer.alloc(static_cast<size_t>(kW) * kH * 3 / 2)) {
        std::cout << "  dma-heap unavailable: skipped passthrough case\n";
        return;
    }

    // 四象限亮度 + 中性色度：空间错误（行宽/偏移/上下翻转）会立刻改变象限均值
    auto *y_plane = static_cast<uint8_t *>(buffer.va);
    for (uint32_t r = 0; r < kH; ++r) {
        for (uint32_t c = 0; c < kW; ++c) {
            const uint8_t v = (r < kH / 2) ? (c < kW / 2 ? 32 : 96) : (c < kW / 2 ? 160 : 224);
            y_plane[static_cast<size_t>(r) * kW + c] = v;
        }
    }
    std::memset(y_plane + static_cast<size_t>(kW) * kH, 128, static_cast<size_t>(kW) * kH / 2);

    CpuFrameView view{};
    view.width = kW;
    view.height = kH;
    view.format = FRAME_FORMAT_NV12;
    view.planes[0] = y_plane;
    view.planes[1] = y_plane + static_cast<size_t>(kW) * kH;
    view.strides[0] = kW;

    std::vector<uint8_t> uploaded;
    CHECK(encoder.encode_region(view, 0, 0, static_cast<int>(kW), static_cast<int>(kH), 90,
                                uploaded));
    check_jpeg(uploaded, static_cast<int>(kW), static_cast<int>(kH));

    zhulong::HardwareFrame frame;
    frame.width = kW;
    frame.height = kH;
    frame.stride = kW;
    frame.vstride = kH;
    frame.format = FRAME_FORMAT_NV12;
    frame.dma_fd = buffer.fd;

    std::vector<uint8_t> passthrough;
    CHECK(encoder.encode_dma_frame(frame, 90, passthrough));
    check_jpeg(passthrough, static_cast<int>(kW), static_cast<int>(kH));

    // 逐字节相等 ⇒ 直通路径读到的像素与上传路径完全一致
    if (uploaded != passthrough) {
        std::printf("  [diag] upload=%zu B passthrough=%zu B\n", uploaded.size(),
                    passthrough.size());
    }
    CHECK(uploaded == passthrough);
    std::cout << "  passthrough == upload (" << passthrough.size() << " B)\n";
}

/**
 * @brief 生产路径：`JpegBackend::encode_dma` 的首次自证与结果稳定性
 *
 * 覆盖首次调用触发「直通 vs 上传」逐字节自证、以及自证通过后结果保持一致的语义。
 */
void test_backend_passthrough() {
    JpegBackend &backend = JpegBackend::instance();
    if (!backend.hardware()) {
        std::cout << "  backend passthrough: skipped (software)\n";
        return;
    }
    constexpr uint32_t kW = 320;
    constexpr uint32_t kH = 240;

    DmaHeapBuffer buffer;
    if (!buffer.alloc(static_cast<size_t>(kW) * kH * 3 / 2)) {
        std::cout << "  backend passthrough: dma-heap unavailable, skipped\n";
        return;
    }
    auto *y_plane = static_cast<uint8_t *>(buffer.va);
    for (uint32_t r = 0; r < kH; ++r) {
        for (uint32_t c = 0; c < kW; ++c) {
            y_plane[static_cast<size_t>(r) * kW + c] = static_cast<uint8_t>((c * 255) / (kW - 1));
        }
    }
    std::memset(y_plane + static_cast<size_t>(kW) * kH, 128, static_cast<size_t>(kW) * kH / 2);

    CpuFrameView view{};
    view.width = kW;
    view.height = kH;
    view.format = FRAME_FORMAT_NV12;
    view.planes[0] = y_plane;
    view.planes[1] = y_plane + static_cast<size_t>(kW) * kH;
    view.strides[0] = kW;

    zhulong::HardwareFrame frame;
    frame.width = kW;
    frame.height = kH;
    frame.stride = kW;
    frame.vstride = kH;
    frame.format = FRAME_FORMAT_NV12;
    frame.dma_fd = buffer.fd;

    std::vector<uint8_t> first;
    CHECK(backend.encode_dma(frame, view, static_cast<int>(kW), static_cast<int>(kH), 90, first));
    check_jpeg(first, static_cast<int>(kW), static_cast<int>(kH));

    std::vector<uint8_t> second;
    CHECK(backend.encode_dma(frame, view, static_cast<int>(kW), static_cast<int>(kH), 90, second));
    CHECK(first == second); // 同一帧两次直通编码必须稳定
    std::cout << "  backend passthrough: self-validated, stable\n";
}

/** @brief 硬件专用：尺寸矩阵 + 纯色/纹理都不得崩溃，几何必须精确 */
void test_hardware_matrix(MppJpegEncoder &encoder) {
    struct Case {
        int w;
        int h;
    };
    const Case cases[] = {{16, 16}, {32, 32}, {48, 48}, {60, 60}, {66, 66}, {100, 100},
                          {200, 400}, {320, 240}, {1920, 1080}};
    for (const Case &c : cases) {
        Nv12Image image(static_cast<uint32_t>(c.w), static_cast<uint32_t>(c.h), 96, 128, 0);
        CpuFrameView view = image.view();
        std::vector<uint8_t> jpeg;
        CHECK(encoder.encode_region(view, 0, 0, c.w, c.h, 85, jpeg));
        check_jpeg(jpeg, c.w, c.h);
    }
    std::cout << "  hardware matrix: " << (sizeof(cases) / sizeof(cases[0])) << " sizes ok\n";
}

/**
 * @brief 硬件专用：会话池 churn
 *
 * 连续请求远超池容量（8）的不同尺寸，强制触发 LRU 淘汰与 MPP 会话销毁。
 * ⚠️ 这条用例是「MPP 1.5.0 的 enc 会话销毁是否安全」的现场判据：若板端崩溃，
 *    说明必须改为只复用不销毁（见 hardware-backends.md 的 JPEG 小节）。
 */
void test_hardware_pool_churn(MppJpegEncoder &encoder) {
    constexpr int kCount = 24;
    for (int i = 0; i < kCount; ++i) {
        const int w = 32 + (i % 6) * 14;
        const int h = 32 + (i / 6) * 18;
        Nv12Image image(static_cast<uint32_t>(w), static_cast<uint32_t>(h), 80, 128, 0);
        CpuFrameView view = image.view();
        std::vector<uint8_t> jpeg;
        CHECK(encoder.encode_region(view, 0, 0, w, h, 85, jpeg));
        check_jpeg(jpeg, w, h);
    }
    std::cout << "  hardware pool churn: " << kCount << " distinct sizes ok\n";
}

} // namespace

int main() {
    try {
        std::cout << "[jpeg_encoder] backend selection\n";
        test_backend_selection();

        std::cout << "[jpeg_encoder] geometry\n";
        test_fullframe_geometry();
        test_crop_geometry();
        test_degenerate_rect_rejected();

        std::cout << "[jpeg_encoder] luma sensitivity\n";
        test_luma_sensitivity();

        std::cout << "[jpeg_encoder] hardware routing\n";
        test_hardware_routing();

        // 硬件专用用例：直连 MppJpegEncoder，绕过后端选择，便于板端单独验证
        auto hardware = MppJpegEncoder::create();
        if (hardware == nullptr) {
            std::cout << "[jpeg_encoder] hardware encoder unavailable: skipped hw cases\n";
        } else {
            std::cout << "[jpeg_encoder] hardware self test\n";
            CHECK(hardware->self_test());
            std::cout << "[jpeg_encoder] dma-buf passthrough\n";
            test_backend_passthrough();
            test_dma_passthrough_matches_upload(*hardware);
            std::cout << "[jpeg_encoder] hardware matrix\n";
            test_hardware_matrix(*hardware);
            std::cout << "[jpeg_encoder] hardware pool churn\n";
            test_hardware_pool_churn(*hardware);
        }

        std::cout << "ZhulongJpegEncoderTests: PASS\n";
        return 0;
    } catch (const std::exception &ex) {
        std::cerr << "ZhulongJpegEncoderTests: FAIL - " << ex.what() << "\n";
        return 1;
    }
}
