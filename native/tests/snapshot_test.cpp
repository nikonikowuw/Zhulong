/**
 * @file snapshot_test.cpp
 * @brief 抓拍 JPEG 编码链路单元测试（离线合成 NV12，无需真实摄像机）
 *
 * 覆盖的契约：
 * 1. 归一化框 → 像素矩形的对齐与 clamp（含退化框、越界框、NaN 拒绝）；
 * 2. NV12（单平面交错 UV）与 YUV420P（三平面）两种源的提取结果一致；
 * 3. 编码产物是**结构完整**的 JPEG（SOI/EOI 完整，SOF 段声明的尺寸与请求一致）；
 * 4. C ABI 句柄的 count/at/destroy 语义，含空句柄与越界索引；
 * 5. 裁剪图必须是全幅图的一个真子集区域（尺寸与 box 换算一致）。
 */

#include "Zhulong/engine.h"
#include "Zhulong/frame.hpp"
#include "algorithm/ipc/frame_broker.hpp"
#include "algorithm/ipc/unix_socket.hpp"
#include "nodes/snapshot/jpeg_encoder.hpp"
#include "pipeline/frame_publisher.hpp"

#include <array>
#include <atomic>
#include <chrono>
#include <cmath>
#include <cstring>
#include <iostream>
#include <stdexcept>
#include <string>
#include <thread>
#include <vector>

#define CHECK(condition) \
    do { \
        if (!(condition)) { \
            throw std::runtime_error("Check failed at line " + std::to_string(__LINE__) + ": " #condition); \
        } \
    } while (false)

namespace {

constexpr uint32_t kWidth = 64;
constexpr uint32_t kHeight = 48;

/**
 * @brief 构造一块 NV12 合成源（stride 故意大于宽度，验证非紧凑布局）
 *
 * Y 平面用水平渐变，UV 平面填常数，便于肉眼与断言双向校验。
 */
struct Nv12Source {
    std::vector<uint8_t> storage;
    uint32_t stride{0};
    uint32_t vstride{0};

    explicit Nv12Source(uint32_t w, uint32_t h, uint32_t stride_pad = 16)
        : stride(w + stride_pad), vstride(h) {
        const size_t y_size = static_cast<size_t>(stride) * vstride;
        const size_t uv_size = static_cast<size_t>(stride) * (vstride / 2);
        storage.assign(y_size + uv_size, 0);
        for (uint32_t y = 0; y < h; ++y) {
            for (uint32_t x = 0; x < w; ++x) {
                storage[static_cast<size_t>(y) * stride + x] =
                    static_cast<uint8_t>((x * 255) / (w > 1 ? w - 1 : 1));
            }
        }
        uint8_t *uv = storage.data() + y_size;
        for (size_t i = 0; i < uv_size; ++i) {
            uv[i] = (i % 2 == 0) ? 128 : 128; // 中性色度
        }
    }

    zhulong::CpuFrameView view() const {
        zhulong::CpuFrameView v{};
        v.planes[0] = storage.data();
        v.planes[1] = storage.data() + static_cast<size_t>(stride) * vstride;
        v.strides[0] = stride;
        v.strides[1] = stride;
        v.width = kWidth;
        v.height = kHeight;
        v.format = zhulong::FRAME_FORMAT_NV12;
        return v;
    }
};

/**
 * @brief 构造等价的 YUV420P 三平面源（用于与 NV12 路径交叉验证）
 */
struct Yuv420pSource {
    std::vector<uint8_t> y;
    std::vector<uint8_t> u;
    std::vector<uint8_t> v;

    explicit Yuv420pSource(uint32_t w, uint32_t h) {
        y.resize(static_cast<size_t>(w) * h);
        u.assign(static_cast<size_t>(w / 2) * (h / 2), 128);
        v.assign(static_cast<size_t>(w / 2) * (h / 2), 128);
        for (uint32_t row = 0; row < h; ++row) {
            for (uint32_t col = 0; col < w; ++col) {
                y[static_cast<size_t>(row) * w + col] =
                    static_cast<uint8_t>((col * 255) / (w > 1 ? w - 1 : 1));
            }
        }
    }

    zhulong::CpuFrameView view() const {
        zhulong::CpuFrameView frame{};
        frame.planes[0] = y.data();
        frame.planes[1] = u.data();
        frame.planes[2] = v.data();
        frame.strides[0] = kWidth;
        frame.strides[1] = kWidth / 2;
        frame.strides[2] = kWidth / 2;
        frame.width = kWidth;
        frame.height = kHeight;
        frame.format = zhulong::FRAME_FORMAT_YUV420P;
        return frame;
    }
};

/**
 * @brief 从 JPEG 的 SOF0/SOF2 段里读取真实编码尺寸
 *
 * 不链接 FFmpeg：本测试要证明的是「产物是结构完整、尺寸正确的 JPEG」，
 * 而这只需要解析 JPEG 段结构即可，不必引入解码器依赖（也更贴近前端
 * 浏览器解图时真正依赖的那部分结构）。
 *
 * @return true 找到 SOF 段并成功读出宽高
 */
bool read_jpeg_sof_size(const std::vector<uint8_t> &jpeg, int &out_w, int &out_h) {
    size_t pos = 2; // 跳过 SOI
    while (pos + 4 <= jpeg.size()) {
        if (jpeg[pos] != 0xFF) {
            return false; // 段边界丢失
        }
        const uint8_t marker = jpeg[pos + 1];
        // 无负载标记：SOI/EOI/RSTn/TEM
        if (marker == 0xD8 || marker == 0xD9 || (marker >= 0xD0 && marker <= 0xD7) || marker == 0x01) {
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
        // SOF0..SOF15 中除 0xC4(DHT)/0xC8(JPG)/0xCC(DAC) 外都是帧头
        const bool is_sof = marker >= 0xC0 && marker <= 0xCF &&
                            marker != 0xC4 && marker != 0xC8 && marker != 0xCC;
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

/** @brief 断言 JPEG 以 SOI (FFD8) 开头、以 EOI (FFD9) 结尾 */
void check_jpeg_envelope(const std::vector<uint8_t> &jpeg) {
    CHECK(jpeg.size() > 4);
    CHECK(jpeg[0] == 0xFF && jpeg[1] == 0xD8);
    CHECK(jpeg[jpeg.size() - 2] == 0xFF && jpeg[jpeg.size() - 1] == 0xD9);
}

void test_normalized_box_to_pixels() {
    int x = 0, y = 0, w = 0, h = 0;

    // 1. 满幅框 → 整个画面（对齐后）
    CHECK(zhulong::normalized_box_to_pixels(
        std::array<float, 4>{0.0f, 0.0f, 1.0f, 1.0f}.data(), kWidth, kHeight, x, y, w, h));
    CHECK(x == 0 && y == 0 && w == static_cast<int>(kWidth) && h == static_cast<int>(kHeight));

    // 2. 中心框 25%~75% → 32x24，起点 16/12（均为偶数，无需对齐）
    CHECK(zhulong::normalized_box_to_pixels(
        std::array<float, 4>{0.25f, 0.25f, 0.75f, 0.75f}.data(), kWidth, kHeight, x, y, w, h));
    CHECK(x == 16 && y == 12 && w == 32 && h == 24);

    // 3. 越界框被 clamp 进画面，不允许负坐标或超出宽高
    CHECK(zhulong::normalized_box_to_pixels(
        std::array<float, 4>{-0.5f, -0.5f, 1.5f, 1.5f}.data(), kWidth, kHeight, x, y, w, h));
    CHECK(x == 0 && y == 0 && w == static_cast<int>(kWidth) && h == static_cast<int>(kHeight));

    // 4. 退化框（宽或高不足 2 像素）必须被拒绝，而不是返回 0 尺寸继续编码
    CHECK(!zhulong::normalized_box_to_pixels(
        std::array<float, 4>{0.5f, 0.5f, 0.5f, 0.5f}.data(), kWidth, kHeight, x, y, w, h));

    // 5. 含 NaN 的框必须被拒绝（否则 (int)(NaN * w) 是未定义行为）
    CHECK(!zhulong::normalized_box_to_pixels(
        std::array<float, 4>{std::nanf(""), 0.1f, 0.5f, 0.5f}.data(), kWidth, kHeight, x, y, w, h));

    // 6. x1 > x2 的颠倒框应自动交换而不是产生负宽度
    CHECK(zhulong::normalized_box_to_pixels(
        std::array<float, 4>{0.75f, 0.75f, 0.25f, 0.25f}.data(), kWidth, kHeight, x, y, w, h));
    CHECK(x == 16 && y == 12 && w == 32 && h == 24);
}

void test_extract_region_nv12_and_yuv420p() {
    Nv12Source nv12(kWidth, kHeight);
    Yuv420pSource yuv(kWidth, kHeight);

    zhulong::YuvFrame from_nv12{};
    zhulong::YuvFrame from_yuv{};

    // 全幅提取：两条路径的尺寸必须一致
    CHECK(zhulong::extract_yuv_region(nv12.view(), 0, 0, static_cast<int>(kWidth),
                                      static_cast<int>(kHeight), from_nv12));
    CHECK(zhulong::extract_yuv_region(yuv.view(), 0, 0, static_cast<int>(kWidth),
                                      static_cast<int>(kHeight), from_yuv));
    CHECK(from_nv12.width == kWidth && from_nv12.height == kHeight);
    CHECK(from_yuv.width == kWidth && from_yuv.height == kHeight);
    CHECK(from_nv12.y.size() == static_cast<size_t>(kWidth) * kHeight);
    CHECK(from_yuv.y.size() == from_nv12.y.size());
    CHECK(from_nv12.u.size() == static_cast<size_t>(kWidth / 2) * (kHeight / 2));

    // Y 平面必须逐字节相同：NV12 与 YUV420P 的 Y 平面布局本就一致
    CHECK(std::memcmp(from_nv12.y.data(), from_yuv.y.data(), from_nv12.y.size()) == 0);

    // 裁剪提取：16,12 起点 + 32x24 → Y 平面取的是原图对应区域
    zhulong::YuvFrame crop{};
    CHECK(zhulong::extract_yuv_region(nv12.view(), 16, 12, 32, 24, crop));
    CHECK(crop.width == 32 && crop.height == 24);
    for (uint32_t row = 0; row < 24; ++row) {
        const uint8_t *expect = from_nv12.y.data() + static_cast<size_t>(row + 12) * kWidth + 16;
        const uint8_t *actual = crop.y.data() + static_cast<size_t>(row) * 32;
        CHECK(std::memcmp(expect, actual, 32) == 0);
    }

    // 奇数坐标与奇数尺寸必须被对齐到偶数，且不能越界
    zhulong::YuvFrame odd{};
    CHECK(zhulong::extract_yuv_region(nv12.view(), 3, 3, 15, 15, odd));
    CHECK(odd.width % 2 == 0 && odd.height % 2 == 0);
    CHECK(odd.width <= kWidth && odd.height <= kHeight);

    // 不支持的格式必须被拒绝（而不是把垃圾数据编成 JPEG）
    zhulong::CpuFrameView bogus = nv12.view();
    bogus.format = zhulong::FRAME_FORMAT_UNKNOWN;
    CHECK(!zhulong::extract_yuv_region(bogus, 0, 0, 8, 8, odd));

    // 空平面必须被拒绝，不允许解引用空指针
    zhulong::CpuFrameView nullview = nv12.view();
    nullview.planes[0] = nullptr;
    CHECK(!zhulong::extract_yuv_region(nullview, 0, 0, 8, 8, odd));
}

void test_encode_jpeg_roundtrip() {
    CHECK(zhulong::jpeg_encoder_available());

    Nv12Source src(kWidth, kHeight);
    zhulong::YuvFrame frame{};
    CHECK(zhulong::extract_yuv_region(src.view(), 0, 0, static_cast<int>(kWidth),
                                      static_cast<int>(kHeight), frame));

    std::vector<uint8_t> jpeg;
    CHECK(zhulong::encode_jpeg(frame, 85, jpeg));
    check_jpeg_envelope(jpeg);

    int w = 0;
    int h = 0;
    CHECK(read_jpeg_sof_size(jpeg, w, h));
    CHECK(w == static_cast<int>(kWidth));
    CHECK(h == static_cast<int>(kHeight));

    // 质量必须真实生效：低质量产物应显著小于高质量产物
    std::vector<uint8_t> low;
    CHECK(zhulong::encode_jpeg(frame, 20, low));
    CHECK(low.size() < jpeg.size());

    // 尺寸不合规（奇数）必须被拒绝
    zhulong::YuvFrame odd = frame;
    odd.width = 63;
    std::vector<uint8_t> ignored;
    CHECK(!zhulong::encode_jpeg(odd, 85, ignored));

    // 平面长度与声明尺寸不匹配必须被拒绝（防止越界读）
    zhulong::YuvFrame short_frame = frame;
    short_frame.y.resize(10);
    CHECK(!zhulong::encode_jpeg(short_frame, 85, ignored));
}

void test_c_abi_handle_semantics() {
    // 1. 空句柄的只读接口必须安全
    CHECK(Zhulong_snapshot_count(nullptr) == 0);
    CHECK(Zhulong_snapshot_at(nullptr, 0) == nullptr);
    Zhulong_snapshot_destroy(nullptr);

    // 2. 用公开 ABI 构造一个结果（经由 encode_snapshot，不依赖真实流）
    Nv12Source src(kWidth, kHeight);
    zhulong::YuvFrame full{};
    CHECK(zhulong::extract_yuv_region(src.view(), 0, 0, static_cast<int>(kWidth),
                                      static_cast<int>(kHeight), full));

    std::vector<uint8_t> full_jpeg;
    CHECK(zhulong::encode_jpeg(full, 85, full_jpeg));
    check_jpeg_envelope(full_jpeg);

    // 3. 校验 Zhulong_snapshot_request 结构体零初始化语义与 target_pts 字段
    Zhulong_snapshot_request req{};
    CHECK(req.quality == 0);
    CHECK(req.box_count == 0);
    CHECK(req.boxes == nullptr);
    CHECK(req.target_pts == 0);
    CHECK(req.target_capture_time_ms == 0);

    // 4. 抓拍自证访问器对空句柄必须安全，且语义是「身份未知 / 不算精确」
    CHECK(Zhulong_snapshot_source_pts(nullptr) == 0);
    CHECK(Zhulong_snapshot_source_capture_time_ms(nullptr) == 0);
    CHECK(Zhulong_snapshot_source_exact(nullptr) == 0);
}

void test_frame_publisher_find_frame() {
    using zhulong::HardwareFrame;
    using zhulong::FramePublisher;

    FramePublisher publisher(nullptr, nullptr);

    // 1. 空缓存时查询返回空
    CHECK(publisher.find_frame(0) == nullptr);
    CHECK(publisher.find_frame(100) == nullptr);
    CHECK(publisher.latest_frame() == nullptr);

    std::atomic<int> release_count{0};
    auto make_frame = [&](int64_t pts) -> HardwareFrame::ConstPtr {
        auto frame = std::make_shared<HardwareFrame>();
        frame->width = 1920;
        frame->height = 1080;
        frame->stride = 1920;
        frame->has_pts = true;
        frame->pts = pts;
        frame->release_fn = [&release_count](HardwareFrame* /*self*/) {
            release_count.fetch_add(1);
        };
        return frame;
    };

    // 2. 压入第一帧 (pts=1000)
    publisher.push_frame(make_frame(1000));
    CHECK(publisher.find_frame(1000) != nullptr);
    CHECK(publisher.find_frame(1000)->pts == 1000);
    // pts <= 0 回退最新帧
    CHECK(publisher.find_frame(0) != nullptr && publisher.find_frame(0)->pts == 1000);
    CHECK(publisher.find_frame(-1) != nullptr && publisher.find_frame(-1)->pts == 1000);
    // 未匹配的 pts 也回退最新帧
    CHECK(publisher.find_frame(9999) != nullptr && publisher.find_frame(9999)->pts == 1000);
    CHECK(publisher.latest_frame() != nullptr && publisher.latest_frame()->pts == 1000);

    // 3. 压入到容量上限 (容量固定为 4: 1000, 2000, 3000, 4000)
    publisher.push_frame(make_frame(2000));
    publisher.push_frame(make_frame(3000));
    publisher.push_frame(make_frame(4000));
    CHECK(release_count == 0); // 容量为 4 时不淘汰

    // 验证精准匹配各个历史帧
    CHECK(publisher.find_frame(1000)->pts == 1000);
    CHECK(publisher.find_frame(2000)->pts == 2000);
    CHECK(publisher.find_frame(3000)->pts == 3000);
    CHECK(publisher.find_frame(4000)->pts == 4000);
    CHECK(publisher.find_frame(0)->pts == 4000); // 最新帧

    // 4. 压入第 5 帧 (pts=5000)，触发淘汰最旧帧 (pts=1000)
    publisher.push_frame(make_frame(5000));
    CHECK(release_count == 1); // pts=1000 被成功在锁外析构释放

    // 1000 已被淘汰，查询 1000 应安全回退到当前最新帧 5000
    CHECK(publisher.find_frame(1000)->pts == 5000);
    // 其余未被淘汰的帧依然能精确命中
    CHECK(publisher.find_frame(2000)->pts == 2000);
    CHECK(publisher.find_frame(3000)->pts == 3000);
    CHECK(publisher.find_frame(4000)->pts == 4000);
    CHECK(publisher.find_frame(5000)->pts == 5000);

    // 5. stop() 清理所有剩余帧，触发所有帧在锁外析构
    publisher.stop();
    CHECK(release_count == 5);
    CHECK(publisher.find_frame(0) == nullptr);
}


void test_frame_publisher_snapshot_history() {
    using zhulong::FramePublisher;
    using zhulong::HardwareFrame;
    using zhulong::ViewWindow;

    // 与 WindowCrop::copy 同布局的全幅物化：Y/UV 分平面逐行紧打包，dst_stride = align_up(w, 64)。
    auto crop = [](const HardwareFrame& frame, const ViewWindow& win, uint8_t* dst, size_t size,
                   int stride, int /*dst_dma_fd*/) {
        if (win.w <= 0 || win.h <= 0 || frame.host_planes[0] == nullptr ||
            frame.host_planes[1] == nullptr) {
            return false;
        }
        const size_t y_bytes = static_cast<size_t>(stride) * static_cast<size_t>(win.h);
        if (y_bytes * 3 / 2 > size) {
            return false;
        }
        for (int row = 0; row < win.h; ++row) {
            std::memcpy(dst + static_cast<size_t>(row) * static_cast<size_t>(stride),
                        frame.host_planes[0] +
                            static_cast<size_t>(row + win.y) * frame.plane_strides[0],
                        static_cast<size_t>(stride));
        }
        for (int row = 0; row < win.h / 2; ++row) {
            std::memcpy(dst + y_bytes + static_cast<size_t>(row) * static_cast<size_t>(stride),
                        frame.host_planes[1] +
                            static_cast<size_t>(row + win.y / 2) * frame.plane_strides[1],
                        static_cast<size_t>(stride));
        }
        return true;
    };

    FramePublisher publisher(nullptr, nullptr, crop);

    // 每个 pts 填充可辨认的常量，用于验证「取回的确实是那一帧的像素」。
    auto make_frame = [](int64_t pts) -> HardwareFrame::ConstPtr {
        auto frame = std::make_shared<HardwareFrame>();
        frame->width = kWidth;
        frame->height = kHeight;
        frame->stride = kWidth;
        frame->vstride = kHeight;
        frame->format = zhulong::FRAME_FORMAT_NV12;
        auto bytes = std::make_shared<std::vector<uint8_t>>(
            static_cast<size_t>(kWidth) * kHeight * 3 / 2, static_cast<uint8_t>(pts & 0xFF));
        frame->host_ptr = bytes->data();
        frame->host_planes[0] = bytes->data();
        frame->host_planes[1] = bytes->data() + static_cast<size_t>(kWidth) * kHeight;
        frame->plane_strides[0] = kWidth;
        frame->plane_strides[1] = kWidth;
        frame->pts = pts;
        frame->has_pts = true;
        frame->release_fn = [bytes](HardwareFrame* /*self*/) {};
        return frame;
    };

    // 1. 连续发布 18 帧（pts 100..1800）：最新 4 帧留在借用环（零拷贝），
    //    前 14 帧依次从借用环淘汰并惰性物化进副本环；副本环按 12 帧上限淘汰最旧两帧（pts 100、200）。
    //    每帧带一个单调的采集时钟 capture_time_ms = 5000 + i，作为抓拍的首选身份键。
    HardwareFrame::ConstPtr last_frame;
    for (int64_t i = 0; i < 18; ++i) {
        last_frame = make_frame(100 + i * 100);
        publisher.push_frame(last_frame, 5000 + i);
    }
    CHECK(publisher.snapshot_cache_frames() == 12);
    CHECK(publisher.snapshot_materialized_count() == 14);
    CHECK(publisher.snapshot_cache_bytes() > 0);
    CHECK(publisher.snapshot_miss_count() == 0);

    // 2. 精确命中：最新帧在借用环中直接命中，像素内容与 pts 对应
    auto exact = publisher.find_frame(1800);
    CHECK(exact != nullptr);
    CHECK(exact->pts == 1800 && exact->has_pts);
    CHECK(exact->width == kWidth && exact->height == kHeight);
    CHECK(exact->format == zhulong::FRAME_FORMAT_NV12);
    CHECK(exact->dma_fd < 0);
    CHECK(exact->host_planes[0] != nullptr);
    CHECK(exact->host_planes[1] == exact->host_planes[0] + static_cast<size_t>(kWidth) * kHeight);
    CHECK(exact->host_planes[0][0] == static_cast<uint8_t>(1800 & 0xFF));
    CHECK(exact->plane_strides[0] == kWidth);

    // 3. 两个身份键都 <= 0：取借用环尾部，并回报「取到的就是最新帧」
    auto latest = publisher.resolve_frame(0, 0);
    CHECK(latest.frame != nullptr && latest.frame->pts == 1800);
    CHECK(latest.source.exact);
    CHECK(latest.source.has_pts && latest.source.pts == 1800);
    CHECK(latest.source.capture_time_ms == 5017);
    CHECK(publisher.latest_frame() != nullptr && publisher.latest_frame()->pts == 1800);

    // 4. 身份键优先：capture_time_ms 精确命中，即使同时给了指向另一帧的 pts
    auto by_time = publisher.resolve_frame(800, 5017);
    CHECK(by_time.frame != nullptr && by_time.frame->pts == 1800);
    CHECK(by_time.source.exact && by_time.source.capture_time_ms == 5017);

    // 5. 同一帧仍留在借用环里时直接返回借用帧（保留硬件 JPEG 直通），身份不变
    auto by_time_hw = publisher.resolve_frame(0, 5017);
    CHECK(by_time_hw.frame == last_frame);
    CHECK(by_time_hw.source.exact && by_time_hw.source.capture_time_ms == 5017);

    // 6. 超出 4 帧借用环、但仍在副本环内：capture_time_ms 同样精确命中（惰性物化的副本）
    auto older_time = publisher.resolve_frame(0, 5002);
    CHECK(older_time.frame != nullptr && older_time.frame->pts == 300);
    CHECK(older_time.frame->dma_fd < 0);
    CHECK(older_time.frame->host_planes[0][0] == static_cast<uint8_t>(300 & 0xFF));
    CHECK(older_time.source.exact && older_time.source.pts == 300);
    CHECK(older_time.source.capture_time_ms == 5002);
    CHECK(publisher.snapshot_miss_count() == 0);

    // 7. 帧已滑出副本环：按 capture_time_ms 回退最近的副本帧，且必须自报 exact=false
    //    —— 这正是宿主拒绝把证据图与检测结果绑定的信号。
    auto evicted_time = publisher.resolve_frame(0, 5000);
    CHECK(evicted_time.frame != nullptr && evicted_time.frame->pts == 300);
    CHECK(!evicted_time.source.exact);
    CHECK(evicted_time.source.capture_time_ms == 5002);
    CHECK(publisher.snapshot_miss_count() == 1);

    // 8. 只给 pts：精确命中时身份一并回报（副本环自带 capture_time_ms）
    auto by_pts = publisher.resolve_frame(800, 0);
    CHECK(by_pts.frame != nullptr && by_pts.frame->pts == 800);
    CHECK(by_pts.source.exact && by_pts.source.pts == 800);
    CHECK(by_pts.source.capture_time_ms == 5007);

    // 9. 借用环帧自带 capture_time_ms；同时给不匹配的 capture_time_ms 时，
    //    pts 命中不算精确（没能证实是同一帧）。
    publisher.record_published_pts(5017, 1800, true, 0);
    auto borrowed = publisher.resolve_frame(1800, 0);
    CHECK(borrowed.frame == last_frame);
    CHECK(borrowed.source.exact && borrowed.source.capture_time_ms == 5017);
    //    ⚠️ 未证实的身份不计入 miss（那是「淘汰」的计数），但必须自报 exact=false，
    //    由宿主侧告警 —— 见 internal/record/snapshot.go 的 verifySource。
    auto unconfirmed = publisher.resolve_frame(1800, 9999);
    CHECK(unconfirmed.frame == last_frame);
    CHECK(!unconfirmed.source.exact);
    CHECK(publisher.snapshot_miss_count() == 1);

    // 10. 超出 4 帧借用环、但仍在副本环内的帧必须精确命中（慢算法回传的常态）
    auto older = publisher.find_frame(300);
    CHECK(older != nullptr && older->pts == 300);
    CHECK(publisher.snapshot_miss_count() == 1);

    // 11. 已被淘汰的帧回退到 PTS 最近的副本帧（而非最新帧），并计入未命中
    auto evicted = publisher.find_frame(100);
    CHECK(evicted != nullptr && evicted->pts == 300);
    CHECK(publisher.snapshot_miss_count() == 2);

    // 12. 副本字节随返回值存活：停流清空历史与缓冲池后仍可安全读取
    auto kept = publisher.find_frame(800);
    publisher.stop();
    CHECK(publisher.find_frame(0) == nullptr);
    CHECK(kept != nullptr);
    CHECK(kept->host_planes[0][0] == static_cast<uint8_t>(800 & 0xFF));
}

void test_frame_publisher_lazy_eviction_with_broker() {
    using namespace zhulong;
    using namespace zhulong::ipc;

    auto crop = [](const HardwareFrame& frame, const ViewWindow& win, uint8_t* dst, size_t size,
                   int stride, int /*dst_dma_fd*/) {
        if (win.w <= 0 || win.h <= 0 || frame.host_planes[0] == nullptr ||
            frame.host_planes[1] == nullptr) {
            return false;
        }
        const size_t y_bytes = static_cast<size_t>(stride) * static_cast<size_t>(win.h);
        if (y_bytes * 3 / 2 > size) {
            return false;
        }
        for (int row = 0; row < win.h; ++row) {
            std::memcpy(dst + static_cast<size_t>(row) * static_cast<size_t>(stride),
                        frame.host_planes[0] +
                            static_cast<size_t>(row + win.y) * frame.plane_strides[0],
                        static_cast<size_t>(stride));
        }
        for (int row = 0; row < win.h / 2; ++row) {
            std::memcpy(dst + y_bytes + static_cast<size_t>(row) * static_cast<size_t>(stride),
                        frame.host_planes[1] +
                            static_cast<size_t>(row + win.y / 2) * frame.plane_strides[1],
                        static_cast<size_t>(stride));
        }
        return true;
    };

    auto make_frame = [](int64_t pts, uint8_t fill) -> HardwareFrame::ConstPtr {
        auto frame = std::make_shared<HardwareFrame>();
        frame->width = kWidth;
        frame->height = kHeight;
        frame->stride = kWidth;
        frame->vstride = kHeight;
        frame->format = FRAME_FORMAT_NV12;
        auto bytes = std::make_shared<std::vector<uint8_t>>(
            static_cast<size_t>(kWidth) * kHeight * 3 / 2, fill);
        frame->host_ptr = bytes->data();
        frame->host_planes[0] = bytes->data();
        frame->host_planes[1] = bytes->data() + static_cast<size_t>(kWidth) * kHeight;
        frame->plane_strides[0] = kWidth;
        frame->plane_strides[1] = kWidth;
        frame->pts = pts;
        frame->has_pts = true;
        frame->release_fn = [bytes](HardwareFrame* /*self*/) {};
        return frame;
    };

    FrameBroker broker;
    BrokerOptions opt;
    opt.slot_count = 6;
    opt.slot_size = 64 * 1024;
    opt.max_in_flight_per_client = 2;
    opt.lease_timeout = std::chrono::milliseconds(500);
    std::string err;
    CHECK(broker.start(7711, "/tmp", opt, &err));

    auto connect_client = [&](UnixSocketClient& client, const char* name) -> uint32_t {
        CHECK(client.connect(broker.socket_path(), &err));
        HandshakeReq req{};
        req.api_version = 1;
        std::strncpy(req.client_name, name, sizeof(req.client_name) - 1);
        CHECK(client.send_msg(MSG_HANDSHAKE_REQ, &req, sizeof(req)));
        IpcHeader hdr{};
        std::vector<uint8_t> payload;
        CHECK(client.recv_msg(hdr, payload, 1000));
        CHECK(hdr.msg_type == MSG_HANDSHAKE_RESP);
        HandshakeResp resp{};
        std::memcpy(&resp, payload.data(), sizeof(resp));
        return resp.client_id;
    };

    auto recv_and_ack = [&](UnixSocketClient& client, uint32_t cid) -> FrameDispatchMsg {
        IpcHeader hdr{};
        std::vector<uint8_t> payload;
        CHECK(client.recv_msg(hdr, payload, 1000));
        CHECK(hdr.msg_type == MSG_FRAME_DISPATCH);
        FrameDispatchMsg msg{};
        std::memcpy(&msg, payload.data(), sizeof(msg));
        FrameAckMsg ack{};
        ack.stream_id = msg.stream_id;
        ack.frame_id = msg.frame_id;
        ack.slot_index = msg.slot_index;
        ack.generation = msg.generation;
        ack.client_id = cid;
        CHECK(client.send_msg(MSG_FRAME_ACK, &ack, sizeof(ack)));
        return msg;
    };

    UnixSocketClient fast_client;
    const uint32_t fast_cid = connect_client(fast_client, "fast_snap");
    for (int i = 0; i < 50 && broker.client_count() < 1; ++i) {
        std::this_thread::sleep_for(std::chrono::milliseconds(5));
    }
    CHECK(broker.client_count() == 1);

    FramePublisher publisher(nullptr, &broker, crop);

    // 1. 连续投递 7 帧并立即干净 ACK（无 MSG_RESULT）：借用环淘汰的 3 帧都必须零拷贝跳过！
    for (int i = 0; i < 7; ++i) {
        auto frame = make_frame(1000 + i * 100, static_cast<uint8_t>(10 + i));
        PublishReceipt receipt{};
        CHECK(broker.publish_frame(*frame, ViewWindow{}, FrameMotionInfo{}, crop, 9000 + i, &receipt));
        CHECK(receipt.delivered_clients == 1 && receipt.frame_id > 0);
        const auto msg = recv_and_ack(fast_client, fast_cid);
        CHECK(msg.frame_id == receipt.frame_id);
        for (int w = 0; w < 50 && !broker.is_frame_completed_cleanly(receipt.frame_id); ++w) {
            std::this_thread::sleep_for(std::chrono::milliseconds(2));
        }
        CHECK(broker.is_frame_completed_cleanly(receipt.frame_id));
        publisher.push_frame(frame, 9000 + i, receipt.frame_id);
    }
    CHECK(publisher.snapshot_materialized_count() == 0);
    CHECK(publisher.snapshot_cache_frames() == 0);

    // 2. 投递第 8 帧（ct=9100）后调用 mark_result_frame 再 ACK，再推 4 帧将其挤出借用环：
    //    尽管它已 ACK，因为曾产出 MSG_RESULT，淘汰时必须物化进副本环并精确命中。
    {
        auto result_frame = make_frame(2000, 0xAB);
        PublishReceipt receipt{};
        CHECK(broker.publish_frame(*result_frame, ViewWindow{}, FrameMotionInfo{}, crop, 9100, &receipt));
        CHECK(receipt.delivered_clients == 1);
        publisher.push_frame(result_frame, 9100, receipt.frame_id);
        // 模拟 MSG_RESULT 先于 MSG_FRAME_ACK 到达
        publisher.mark_result_frame(true, 9100, true, 2000);
        (void)recv_and_ack(fast_client, fast_cid);
        for (int w = 0; w < 50 && !broker.is_frame_completed_cleanly(receipt.frame_id); ++w) {
            std::this_thread::sleep_for(std::chrono::milliseconds(2));
        }
        CHECK(broker.is_frame_completed_cleanly(receipt.frame_id));
    }
    for (int i = 0; i < 4; ++i) {
        auto filler = make_frame(2100 + i * 100, static_cast<uint8_t>(40 + i));
        PublishReceipt receipt{};
        CHECK(broker.publish_frame(*filler, ViewWindow{}, FrameMotionInfo{}, crop, 9101 + i, &receipt));
        (void)recv_and_ack(fast_client, fast_cid);
        for (int w = 0; w < 50 && !broker.is_frame_completed_cleanly(receipt.frame_id); ++w) {
            std::this_thread::sleep_for(std::chrono::milliseconds(2));
        }
        publisher.push_frame(filler, 9101 + i, receipt.frame_id);
    }
    CHECK(publisher.snapshot_materialized_count() == 1);
    CHECK(publisher.snapshot_cache_frames() == 1);
    auto hit_result = publisher.resolve_frame(0, 9100);
    CHECK(hit_result.frame != nullptr && hit_result.source.exact);
    CHECK(hit_result.source.pts == 2000 && hit_result.source.capture_time_ms == 9100);
    CHECK(hit_result.frame->host_planes[0][0] == 0xAB);

    // 2b. 极端抢占时序：MSG_RESULT 与 MSG_FRAME_ACK 均在 push_frame 落借用环之前到达。
    //     pending_result_marks_ 必须暂存并在 push_frame 时认领，保证淘汰时依然物化不丢失。
    {
        auto early_frame = make_frame(2500, 0xBC);
        PublishReceipt receipt{};
        CHECK(broker.publish_frame(*early_frame, ViewWindow{}, FrameMotionInfo{}, crop, 9150, &receipt));
        CHECK(receipt.delivered_clients == 1);
        publisher.mark_result_frame(true, 9150, true, 2500);
        (void)recv_and_ack(fast_client, fast_cid);
        for (int w = 0; w < 50 && !broker.is_frame_completed_cleanly(receipt.frame_id); ++w) {
            std::this_thread::sleep_for(std::chrono::milliseconds(2));
        }
        CHECK(broker.is_frame_completed_cleanly(receipt.frame_id));
        publisher.push_frame(early_frame, 9150, receipt.frame_id);
    }
    for (int i = 0; i < 4; ++i) {
        auto filler = make_frame(2600 + i * 100, static_cast<uint8_t>(50 + i));
        PublishReceipt receipt{};
        CHECK(broker.publish_frame(*filler, ViewWindow{}, FrameMotionInfo{}, crop, 9151 + i, &receipt));
        (void)recv_and_ack(fast_client, fast_cid);
        for (int w = 0; w < 50 && !broker.is_frame_completed_cleanly(receipt.frame_id); ++w) {
            std::this_thread::sleep_for(std::chrono::milliseconds(2));
        }
        publisher.push_frame(filler, 9151 + i, receipt.frame_id);
    }
    CHECK(publisher.snapshot_materialized_count() == 2);
    auto hit_early = publisher.resolve_frame(0, 9150);
    CHECK(hit_early.frame != nullptr && hit_early.source.exact);
    CHECK(hit_early.source.pts == 2500 && hit_early.source.capture_time_ms == 9150);
    CHECK(hit_early.frame->host_planes[0][0] == 0xBC);

    // 3. 慢客户端不 ACK 的在途帧被快客户端推送的新帧挤出借用环：
    //    is_frame_completed_cleanly == false，淘汰时必须物化进副本环并精确命中。
    UnixSocketClient slow_client;
    (void)connect_client(slow_client, "slow_snap");
    for (int i = 0; i < 50 && broker.client_count() < 2; ++i) {
        std::this_thread::sleep_for(std::chrono::milliseconds(5));
    }
    CHECK(broker.client_count() == 2);
    auto wait_client_idle = [&](uint32_t cid) {
        ClientInfo info{};
        for (int w = 0; w < 100; ++w) {
            if (broker.get_client_info(cid, info) && info.in_flight_count == 0) {
                return;
            }
            std::this_thread::sleep_for(std::chrono::milliseconds(2));
        }
        CHECK(broker.get_client_info(cid, info) && info.in_flight_count == 0);
    };
    {
        auto slow_frame = make_frame(3000, 0xCD);
        PublishReceipt receipt{};
        CHECK(broker.publish_frame(*slow_frame, ViewWindow{}, FrameMotionInfo{}, crop, 9200, &receipt));
        CHECK(receipt.delivered_clients == 2);
        // 仅快客户端 ACK，慢客户端仍在推理不 ACK
        (void)recv_and_ack(fast_client, fast_cid);
        wait_client_idle(fast_cid);
        publisher.push_frame(slow_frame, 9200, receipt.frame_id);
        CHECK(!broker.is_frame_completed_cleanly(receipt.frame_id));
    }
    for (int i = 0; i < 4; ++i) {
        auto filler = make_frame(3100 + i * 100, static_cast<uint8_t>(60 + i));
        PublishReceipt receipt{};
        CHECK(broker.publish_frame(*filler, ViewWindow{}, FrameMotionInfo{}, crop, 9201 + i, &receipt));
        CHECK(receipt.delivered_clients >= 1);
        (void)recv_and_ack(fast_client, fast_cid);
        wait_client_idle(fast_cid);
        publisher.push_frame(filler, 9201 + i, receipt.frame_id);
    }
    auto hit_slow = publisher.resolve_frame(0, 9200);
    CHECK(hit_slow.frame != nullptr && hit_slow.source.exact);
    CHECK(hit_slow.source.pts == 3000 && hit_slow.source.capture_time_ms == 9200);
    CHECK(hit_slow.frame->host_planes[0][0] == 0xCD);

    publisher.stop();
    slow_client.disconnect();
    fast_client.disconnect();
    broker.stop();
}
} // namespace

int main() {
    try {
        test_normalized_box_to_pixels();
        test_extract_region_nv12_and_yuv420p();
        test_encode_jpeg_roundtrip();
        test_c_abi_handle_semantics();
        test_frame_publisher_find_frame();
        test_frame_publisher_snapshot_history();
        test_frame_publisher_lazy_eviction_with_broker();
        std::cout << "snapshot_test: all checks passed" << std::endl;
        return 0;
    } catch (const std::exception &e) {
        std::cerr << "snapshot_test failed: " << e.what() << std::endl;
        return 1;
    }
}
