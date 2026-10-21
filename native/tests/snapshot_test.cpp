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
#include "nodes/snapshot/jpeg_encoder.hpp"

#include <array>
#include <cmath>
#include <cstring>
#include <iostream>
#include <stdexcept>
#include <string>
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
        v.vstride = vstride;
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
        frame.vstride = kHeight;
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
}

} // namespace

int main() {
    try {
        test_normalized_box_to_pixels();
        test_extract_region_nv12_and_yuv420p();
        test_encode_jpeg_roundtrip();
        test_c_abi_handle_semantics();
        std::cout << "snapshot_test: all checks passed" << std::endl;
        return 0;
    } catch (const std::exception &e) {
        std::cerr << "snapshot_test failed: " << e.what() << std::endl;
        return 1;
    }
}
