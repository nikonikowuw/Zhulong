// RGA 视觉前处理后端测试（阶段 D）。
//
// ⚠️ 本程序不使用 assert：-DNDEBUG 会吃掉 assert。改用显式 require() 检查 +
// 打印 + 非零退出。输出为机器可读的证据行，供真机粘贴。
//
// 交叉构建时本目标只编译、不注册 CTest；板端手动执行：
//   ./ZhulongRgaTests

#include <backends/rga/rga_error_map.hpp>
#include <backends/rga/rga_vision_backend.hpp>
#include <algorithm/runtime/cpu_vision_backend.hpp>
#include <algorithm/runtime/letterbox_geometry.hpp>
#include <algorithm/runtime/tensor.hpp>
#include <algorithm/runtime/vision_backend.hpp>

#include <rga/im2d.h>

#include <cmath>
#include <cstdint>
#include <cstring>
#include <iostream>
#include <string>
#include <vector>

namespace {

int g_failures = 0;

void require(bool condition, const std::string& label) {
    std::cout << (condition ? "PASS " : "FAIL ") << label << "\n";
    if (!condition) {
        ++g_failures;
    }
}

// 确定性像素生成（不依赖 rand，保证两次运行可比）。
uint8_t pixel_at(int x, int y, int channel) {
    return static_cast<uint8_t>((x * 7 + y * 13 + channel * 31) & 0xFF);
}

std::vector<uint8_t> make_image(int width, int height, int channels) {
    std::vector<uint8_t> data(static_cast<size_t>(width) * height * channels);
    for (int y = 0; y < height; ++y) {
        for (int x = 0; x < width; ++x) {
            for (int c = 0; c < channels; ++c) {
                data[(static_cast<size_t>(y) * width + x) * channels + c] = pixel_at(x, y, c);
            }
        }
    }
    return data;
}

// 逐像素最大绝对差。RGA 与 CPU 的缩放实现不同，不要求逐位相同（AC3）。
int max_abs_diff(const std::vector<uint8_t>& a, const std::vector<uint8_t>& b) {
    if (a.size() != b.size()) return 1000000;
    int worst = 0;
    for (size_t i = 0; i < a.size(); ++i) {
        const int diff = std::abs(static_cast<int>(a[i]) - static_cast<int>(b[i]));
        if (diff > worst) worst = diff;
    }
    return worst;
}

zhulong_status_t run_letterbox(zhulong::IVisionBackend& backend, const uint8_t* pixels,
                               int width, int height, int stride, zhulong_pixel_format_t format,
                               zhulong_pixel_format_t target_format, int target_width,
                               int target_height, zhulong::Tensor& output,
                               zhulong::LetterboxParams* output_params = nullptr) {
    zhulong_frame_view frame{};
    frame.width = width;
    frame.height = height;
    frame.format = format;
    frame.planes[0] = pixels;
    frame.strides[0] = stride;

    zhulong::LetterboxParams params;
    params.target_width = target_width;
    params.target_height = target_height;
    params.target_format = target_format;
    zhulong_status_t rc = backend.letterbox(frame, output, params);
    if (output_params) *output_params = params;
    return rc;
}

// ── Case 1：探测与版本证据（AC6）─────────────────────────────────────────
void case_probe() {
    zhulong::RgaVisionBackend backend;
    std::cout << "RGA_USABLE " << (backend.rga_usable() ? "1" : "0") << "\n";
    std::cout << "RGA_STRIDE_ALIGN " << backend.stride_align() << "\n";
    std::cout << "RGA_OFFSET_ALIGN " << backend.offset_align() << "\n";
    std::cout << "RGA_API_VERSION " << backend.api_version() << "\n";
    std::cout << "RGA_DRIVER_VERSION " << backend.driver_version() << "\n";
    std::cout << "RGA_FALLBACK_COUNT " << backend.fallback_count() << "\n";
    if (backend.rga_usable()) {
        require(backend.stride_align() > 0, "case1: stride_align probed at runtime (non-zero)");
        require(backend.offset_align() > 0, "case1: offset_align probed at runtime (non-zero)");
        require(std::strlen(backend.api_version()) > 0, "case1: api_version recorded");
    } else {
        std::cout << "NOTE case1: RGA unavailable in this environment; fallback path only\n";
    }
}

// ── Case 2：几何计算与 CPU 一致（AC3 前半）───────────────────────────────
void case_geometry() {
    struct Case { int sw, sh, tw, th; };
    const Case cases[] = {
        {1920, 1080, 640, 640},   // 16:9 → 方形：pad_top 非 0
        {1280, 720, 640, 640},
        {640, 640, 640, 640},     // 已同尺寸：无 padding
        {2, 2, 3, 3},             // 极小图
        {100, 1000, 640, 640},    // 竖长图
    };
    for (const Case& c : cases) {
        const zhulong::LetterboxGeometry g =
            zhulong::compute_letterbox_geometry(c.sw, c.sh, c.tw, c.th);
        const bool ok = g.unpad_w >= 1 && g.unpad_w <= c.tw &&
                        g.unpad_h >= 1 && g.unpad_h <= c.th &&
                        g.pad_left >= 0 && g.pad_top >= 0 &&
                        g.pad_left * 2 + g.unpad_w <= c.tw &&
                        g.pad_top * 2 + g.unpad_h <= c.th;
        require(ok, "case2: geometry in range src=" + std::to_string(c.sw) + "x" +
                        std::to_string(c.sh) + " target=" + std::to_string(c.tw) + "x" +
                        std::to_string(c.th) + " unpad=" + std::to_string(g.unpad_w) + "x" +
                        std::to_string(g.unpad_h) + " pad=" + std::to_string(g.pad_left) + "," +
                        std::to_string(g.pad_top));
    }
}

// ── Case 3：RGA vs CPU 逐像素比对（AC3）──────────────────────────────────
void case_pixel_compare() {
    const int src_w = 1920, src_h = 1080;
    const std::vector<uint8_t> source = make_image(src_w, src_h, 3);

    zhulong::RgaVisionBackend rga;
    zhulong::CpuVisionBackend cpu;

    for (const int target : {640, 608, 320}) {
        zhulong::Tensor rga_out, cpu_out;
        zhulong::LetterboxParams rga_params, cpu_params;
        const zhulong_status_t rga_rc = run_letterbox(rga, source.data(), src_w, src_h, src_w * 3,
                                                      ZHULONG_PIXEL_FORMAT_RGB24,
                                                      ZHULONG_PIXEL_FORMAT_RGB24, target, target,
                                                      rga_out, &rga_params);
        const zhulong_status_t cpu_rc = run_letterbox(cpu, source.data(), src_w, src_h, src_w * 3,
                                                      ZHULONG_PIXEL_FORMAT_RGB24,
                                                      ZHULONG_PIXEL_FORMAT_RGB24, target, target,
                                                      cpu_out, &cpu_params);
        require(rga_rc == ZHULONG_PLUGIN_OK, "case3: rga letterbox target=" + std::to_string(target));
        require(cpu_rc == ZHULONG_PLUGIN_OK, "case3: cpu letterbox target=" + std::to_string(target));
        if (rga_rc != ZHULONG_PLUGIN_OK || cpu_rc != ZHULONG_PLUGIN_OK) continue;

        require(rga_out.shape() == cpu_out.shape(),
                "case3: shape matches cpu target=" + std::to_string(target));
        require(rga_params.scale == cpu_params.scale &&
                    rga_params.pad_left == cpu_params.pad_left &&
                    rga_params.pad_top == cpu_params.pad_top,
                "case3: scale/padding matches cpu target=" + std::to_string(target));

        std::vector<uint8_t> rga_pixels(static_cast<const uint8_t*>(rga_out.raw_data()),
                                        static_cast<const uint8_t*>(rga_out.raw_data()) + rga_out.byte_size());
        std::vector<uint8_t> cpu_pixels(static_cast<const uint8_t*>(cpu_out.raw_data()),
                                        static_cast<const uint8_t*>(cpu_out.raw_data()) + cpu_out.byte_size());
        const int worst = max_abs_diff(rga_pixels, cpu_pixels);
        std::cout << "RGA_PIXEL_DIFF target=" << target << " max_abs_diff=" << worst << "\n";
        require(worst <= 128, "case3: pixel diff within tolerance target=" + std::to_string(target) +
                                  " max=" + std::to_string(worst));

        const uint8_t* rga_buf = rga_out.data<uint8_t>();
        if (rga_params.pad_top > 0) {
            require(rga_buf[0] == 114, "case3: top padding is 114 target=" + std::to_string(target));
        }
    }
}

// ── Case 4：格式能力矩阵（AC4）──────────────────────────────────────────
void case_formats() {
    const int w = 64, h = 64;
    zhulong::RgaVisionBackend rga;
    zhulong::CpuVisionBackend cpu;

    // 1) Packed RGB24 / BGR24
    {
        const std::vector<uint8_t> src = make_image(w, h, 3);
        zhulong::Tensor out;
        require(run_letterbox(rga, src.data(), w, h, w * 3, ZHULONG_PIXEL_FORMAT_RGB24,
                              ZHULONG_PIXEL_FORMAT_RGB24, 32, 32, out) == ZHULONG_PLUGIN_OK,
                "case4: rga accepts RGB24");
        require(run_letterbox(rga, src.data(), w, h, w * 3, ZHULONG_PIXEL_FORMAT_BGR24,
                              ZHULONG_PIXEL_FORMAT_RGB24, 32, 32, out) == ZHULONG_PLUGIN_OK,
                "case4: rga accepts BGR24");
    }

    // 2) RGBA32 (单平面 4 字节/像素)
    {
        const std::vector<uint8_t> src = make_image(w, h, 4);
        zhulong::Tensor out;
        zhulong_status_t rga_rc = run_letterbox(rga, src.data(), w, h, w * 4,
                                                ZHULONG_PIXEL_FORMAT_RGBA32,
                                                ZHULONG_PIXEL_FORMAT_RGB24, 32, 32, out);
        if (rga.rga_usable()) {
            require(rga_rc == ZHULONG_PLUGIN_OK, "case4: rga accepts RGBA32");
        } else {
            require(rga_rc == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED, "case4: fallback rejects RGBA32");
        }
    }

    // 3) NV12 连续平面
    {
        std::vector<uint8_t> nv12(w * h + w * (h / 2), 128);
        zhulong_frame_view frame{};
        frame.width = w;
        frame.height = h;
        frame.format = ZHULONG_PIXEL_FORMAT_NV12;
        frame.planes[0] = nv12.data();
        frame.planes[1] = nv12.data() + w * h;
        frame.strides[0] = w;
        frame.strides[1] = w;

        zhulong::Tensor out;
        zhulong::LetterboxParams params;
        params.target_width = 32;
        params.target_height = 32;
        params.target_format = ZHULONG_PIXEL_FORMAT_RGB24;

        zhulong_status_t rga_rc = rga.letterbox(frame, out, params);
        zhulong_status_t cpu_rc = cpu.letterbox(frame, out, params);
        require(cpu_rc == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED, "case4: cpu rejects NV12");
        if (rga.rga_usable()) {
            require(rga_rc == ZHULONG_PLUGIN_OK, "case4: rga accepts contiguous NV12");
        }

        // 非连续 NV12 (plane1 错位) 应被安全拒绝并回退 CPU (返回 NOT_SUPPORTED)
        frame.planes[1] = nv12.data() + w * h + 1;
        uint64_t prev_fallback = rga.fallback_count();
        rga_rc = rga.letterbox(frame, out, params);
        require(rga_rc == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED, "case4: rga rejects non-contiguous NV12");
        require(rga.fallback_count() > prev_fallback, "case4: non-contiguous NV12 increments fallback_count");

        // NV12 含垂直对齐步长 (如 MPP 硬件解码 1080P height=1080, vstride=1088)
        {
            const int vstride = 80; // 80 > 64，对齐为 16 的倍数
            std::vector<uint8_t> nv12_vpad(static_cast<size_t>(w) * vstride + w * (h / 2), 128);
            zhulong_frame_view frame_vpad{};
            frame_vpad.width = w;
            frame_vpad.height = h;
            frame_vpad.format = ZHULONG_PIXEL_FORMAT_NV12;
            frame_vpad.planes[0] = nv12_vpad.data();
            frame_vpad.planes[1] = nv12_vpad.data() + w * vstride;
            frame_vpad.strides[0] = w;
            frame_vpad.strides[1] = w;

            zhulong::Tensor out_vpad;
            zhulong_status_t rga_rc_vpad = rga.letterbox(frame_vpad, out_vpad, params);
            if (rga.rga_usable()) {
                require(rga_rc_vpad == ZHULONG_PLUGIN_OK, "case4: rga accepts NV12 with vertical stride alignment");
            }
        }
    }

    // 4) YUV420P 连续平面
    {
        std::vector<uint8_t> yuv420p(w * h + 2 * (w / 2) * (h / 2), 128);
        zhulong_frame_view frame{};
        frame.width = w;
        frame.height = h;
        frame.format = ZHULONG_PIXEL_FORMAT_YUV420P;
        frame.planes[0] = yuv420p.data();
        frame.planes[1] = yuv420p.data() + w * h;
        frame.planes[2] = yuv420p.data() + w * h + (w / 2) * (h / 2);
        frame.strides[0] = w;
        frame.strides[1] = w / 2;
        frame.strides[2] = w / 2;

        zhulong::Tensor out;
        zhulong::LetterboxParams params;
        params.target_width = 32;
        params.target_height = 32;
        params.target_format = ZHULONG_PIXEL_FORMAT_RGB24;

        zhulong_status_t rga_rc = rga.letterbox(frame, out, params);
        zhulong_status_t cpu_rc = cpu.letterbox(frame, out, params);
        require(cpu_rc == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED, "case4: cpu rejects YUV420P");
        if (rga.rga_usable()) {
            require(rga_rc == ZHULONG_PLUGIN_OK, "case4: rga accepts contiguous YUV420P");
        }

        // 非连续 YUV420P 应被安全拒绝并回退 CPU
        frame.planes[2] = yuv420p.data() + w * h + (w / 2) * (h / 2) + 1;
        rga_rc = rga.letterbox(frame, out, params);
        require(rga_rc == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED, "case4: rga rejects non-contiguous YUV420P");
    }

    // 5) UNKNOWN
    {
        const std::vector<uint8_t> src(w * h * 3, 0);
        zhulong::Tensor out;
        require(run_letterbox(rga, src.data(), w, h, w * 3, ZHULONG_PIXEL_FORMAT_UNKNOWN,
                              ZHULONG_PIXEL_FORMAT_RGB24, 32, 32, out) == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED,
                "case4: rga rejects UNKNOWN");
        require(run_letterbox(cpu, src.data(), w, h, w * 3, ZHULONG_PIXEL_FORMAT_UNKNOWN,
                              ZHULONG_PIXEL_FORMAT_RGB24, 32, 32, out) == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED,
                "case4: cpu rejects UNKNOWN");
    }
}

// ── Case 5：通道交换（AC3）──────────────────────────────────────────────
void case_channel_swap() {
    const std::vector<uint8_t> bgr = {
        0, 0, 255, 0, 255, 0,       // 行 0：(0,0,255)=红  (0,255,0)=绿
        255, 0, 0, 255, 255, 255    // 行 1：(255,0,0)=蓝  (255,255,255)=白
    };
    zhulong::RgaVisionBackend rga;
    zhulong::CpuVisionBackend cpu;

    zhulong::Tensor rga_out, cpu_out;
    const zhulong_status_t rga_rc = run_letterbox(rga, bgr.data(), 2, 2, 6,
                                                  ZHULONG_PIXEL_FORMAT_BGR24,
                                                  ZHULONG_PIXEL_FORMAT_RGB24, 2, 2, rga_out);
    const zhulong_status_t cpu_rc = run_letterbox(cpu, bgr.data(), 2, 2, 6,
                                                  ZHULONG_PIXEL_FORMAT_BGR24,
                                                  ZHULONG_PIXEL_FORMAT_RGB24, 2, 2, cpu_out);
    require(rga_rc == ZHULONG_PLUGIN_OK, "case5: rga bgr->rgb ok");
    require(cpu_rc == ZHULONG_PLUGIN_OK, "case5: cpu bgr->rgb ok");
    if (rga_rc != ZHULONG_PLUGIN_OK || cpu_rc != ZHULONG_PLUGIN_OK) return;

    const uint8_t* p = rga_out.data<uint8_t>();
    std::cout << "RGA_SWAP_PIXELS " << (int)p[0] << "," << (int)p[1] << "," << (int)p[2] << " | "
              << (int)p[3] << "," << (int)p[4] << "," << (int)p[5] << "\n";
    require(p[0] == 255 && p[1] == 0 && p[2] == 0, "case5: rga red channel first");
    require(p[3] == 0 && p[4] == 255 && p[5] == 0, "case5: rga green channel second");

    std::vector<uint8_t> rga_pixels(rga_out.data<uint8_t>(), rga_out.data<uint8_t>() + rga_out.byte_size());
    std::vector<uint8_t> cpu_pixels(cpu_out.data<uint8_t>(), cpu_out.data<uint8_t>() + cpu_out.byte_size());
    const int worst = max_abs_diff(rga_pixels, cpu_pixels);
    std::cout << "RGA_SWAP_DIFF max_abs_diff=" << worst << "\n";
    require(worst == 0, "case5: 1:1 swap identical to cpu");
}

// ── Case 6：resize 直接调用（AC3）───────────────────────────────────────
void case_resize() {
    const int src_w = 640, src_h = 480;
    const std::vector<uint8_t> source = make_image(src_w, src_h, 3);
    const int dst_w = 320, dst_h = 240;
    std::vector<uint8_t> rga_dst(static_cast<size_t>(dst_w) * dst_h * 3, 0);
    std::vector<uint8_t> cpu_dst(static_cast<size_t>(dst_w) * dst_h * 3, 0);

    zhulong::RgaVisionBackend rga;
    zhulong::CpuVisionBackend cpu;
    const zhulong_status_t rga_rc = rga.resize(source.data(), src_w, src_h, src_w * 3,
                                               ZHULONG_PIXEL_FORMAT_RGB24, rga_dst.data(), dst_w * 3,
                                               dst_w, dst_h, ZHULONG_PIXEL_FORMAT_RGB24);
    const zhulong_status_t cpu_rc = cpu.resize(source.data(), src_w, src_h, src_w * 3,
                                               ZHULONG_PIXEL_FORMAT_RGB24, cpu_dst.data(), dst_w * 3,
                                               dst_w, dst_h, ZHULONG_PIXEL_FORMAT_RGB24);
    require(rga_rc == ZHULONG_PLUGIN_OK, "case6: rga resize ok");
    require(cpu_rc == ZHULONG_PLUGIN_OK, "case6: cpu resize ok");
    if (rga_rc != ZHULONG_PLUGIN_OK || cpu_rc != ZHULONG_PLUGIN_OK) return;

    const int worst = max_abs_diff(rga_dst, cpu_dst);
    std::cout << "RGA_RESIZE_DIFF max_abs_diff=" << worst << "\n";
    require(worst <= 128, "case6: resize within tolerance max=" + std::to_string(worst));

    // stride=0 必须按 packed 处理（与 CPU 语义一致）。
    std::vector<uint8_t> rga_zero(static_cast<size_t>(dst_w) * dst_h * 3, 0);
    const zhulong_status_t zero_rc = rga.resize(source.data(), src_w, src_h, 0,
                                                ZHULONG_PIXEL_FORMAT_RGB24, rga_zero.data(), 0,
                                                dst_w, dst_h, ZHULONG_PIXEL_FORMAT_RGB24);
    require(zero_rc == ZHULONG_PLUGIN_OK, "case6: rga resize accepts stride=0");
    require(max_abs_diff(rga_zero, rga_dst) == 0, "case6: stride=0 same as packed stride");
}

// ── Case 7：故障注入与回退（AC2 / AC7）──────────────────────────────────
void case_faults_and_fallback() {
    zhulong::RgaVisionBackend rga;

    zhulong::Tensor out;
    zhulong_status_t rc = run_letterbox(rga, nullptr, 64, 64, 192,
                                        ZHULONG_PIXEL_FORMAT_RGB24,
                                        ZHULONG_PIXEL_FORMAT_RGB24, 32, 32, out);
    require(rc == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT, "case7: null pixels -> INVALID_ARGUMENT");

    rc = run_letterbox(rga, reinterpret_cast<const uint8_t*>(0x1000), -1, 64, 192,
                       ZHULONG_PIXEL_FORMAT_RGB24, ZHULONG_PIXEL_FORMAT_RGB24, 32, 32, out);
    require(rc == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT, "case7: negative width -> INVALID_ARGUMENT");

    const std::vector<uint8_t> small = make_image(64, 64, 3);
    rc = run_letterbox(rga, small.data(), 64, 64, 100, ZHULONG_PIXEL_FORMAT_RGB24,
                       ZHULONG_PIXEL_FORMAT_RGB24, 32, 32, out);
    require(rc == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT, "case7: undersized stride -> INVALID_ARGUMENT");

    std::vector<uint8_t> dst(64 * 64 * 3, 0);
    rc = rga.resize(nullptr, 64, 64, 192, ZHULONG_PIXEL_FORMAT_RGB24, dst.data(), 192,
                    64, 64, ZHULONG_PIXEL_FORMAT_RGB24);
    require(rc == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT, "case7: resize null src -> INVALID_ARGUMENT");

    require(zhulong::map_rga_status(IM_STATUS_SUCCESS) == ZHULONG_PLUGIN_OK, "case7: map SUCCESS");
    require(zhulong::map_rga_status(IM_STATUS_NOERROR) == ZHULONG_PLUGIN_OK, "case7: map NOERROR");
    require(zhulong::map_rga_status(IM_STATUS_NOT_SUPPORTED) == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED, "case7: map NOT_SUPPORTED");
    require(zhulong::map_rga_status(IM_STATUS_OUT_OF_MEMORY) == ZHULONG_PLUGIN_ERR_OUT_OF_MEMORY, "case7: map OUT_OF_MEMORY");
    require(zhulong::map_rga_status(IM_STATUS_INVALID_PARAM) == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT, "case7: map INVALID_PARAM");
    require(zhulong::map_rga_status(IM_STATUS_ILLEGAL_PARAM) == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT, "case7: map ILLEGAL_PARAM");
    require(zhulong::map_rga_status(IM_STATUS_ERROR_VERSION) == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED, "case7: map ERROR_VERSION");
    require(zhulong::map_rga_status(IM_STATUS_FAILED) == ZHULONG_PLUGIN_ERR_INTERNAL, "case7: map FAILED");
    require(zhulong::map_rga_status(static_cast<IM_STATUS>(-999)) == ZHULONG_PLUGIN_ERR_INTERNAL, "case7: map unknown -> INTERNAL");
}

// ── Case 8：重复调用无泄漏（AC7）────────────────────────────────────────
void case_repeated() {
    const int w = 1920, h = 1080;
    const std::vector<uint8_t> source = make_image(w, h, 3);
    zhulong::RgaVisionBackend rga;

    long rss_first = -1, rss_last = -1;
    auto rss_kb = []() -> long {
        std::FILE* f = std::fopen("/proc/self/status", "r");
        if (!f) return -1;
        char line[256];
        long value = -1;
        while (std::fgets(line, sizeof(line), f)) {
            if (std::strncmp(line, "VmRSS:", 6) == 0) {
                std::sscanf(line + 6, "%ld", &value);
                break;
            }
        }
        std::fclose(f);
        return value;
    };

    const int iterations = 20;
    for (int i = 0; i < iterations; ++i) {
        zhulong::Tensor out;
        const zhulong_status_t rc = run_letterbox(rga, source.data(), w, h, w * 3,
                                                  ZHULONG_PIXEL_FORMAT_RGB24,
                                                  ZHULONG_PIXEL_FORMAT_RGB24, 640, 640, out);
        require(rc == ZHULONG_PLUGIN_OK, "case8: iteration " + std::to_string(i));
        if (i == 1) rss_first = rss_kb();
        rss_last = rss_kb();
    }
    std::cout << "RGA_REPEAT iterations=" << iterations << " rss_first_kb=" << rss_first
              << " rss_last_kb=" << rss_last
              << " fallback_count=" << rga.fallback_count() << "\n";
    require(rss_first > 0 && rss_last > 0, "case8: rss readable");
    require(rss_last - rss_first < 8192, "case8: no rss growth (delta=" +
                                             std::to_string(rss_last - rss_first) + " kb)");
}

// ── Case 9：裁剪与缩放 (crop_scale) 及奇数对齐适配 ────────────────────────
void case_crop_scale() {
    const int src_w = 640, src_h = 480;
    const std::vector<uint8_t> source = make_image(src_w, src_h, 3);
    zhulong_frame_view frame{};
    frame.width = src_w;
    frame.height = src_h;
    frame.format = ZHULONG_PIXEL_FORMAT_RGB24;
    frame.planes[0] = source.data();
    frame.strides[0] = src_w * 3;

    zhulong::RgaVisionBackend rga;
    const int dst_w = 128, dst_h = 128;
    std::vector<uint8_t> dst(static_cast<size_t>(dst_w) * dst_h * 3, 0);

    // 1) 偶数 ROI：完全对齐
    zhulong::CropRect even_roi{100, 100, 200, 200};
    zhulong_status_t rc = rga.crop_scale(frame, even_roi, dst.data(), dst_w * 3, dst_w, dst_h,
                                         ZHULONG_PIXEL_FORMAT_RGB24);
    require(rc == ZHULONG_PLUGIN_OK, "case9: even roi crop_scale ok");

    // 2) 奇数 ROI：触发对齐适配 (rect_adapted_count 增加)
    const uint64_t prev_rect_adapted = rga.rect_adapted_count();
    zhulong::CropRect odd_roi{101, 103, 199, 197};
    rc = rga.crop_scale(frame, odd_roi, dst.data(), dst_w * 3, dst_w, dst_h,
                        ZHULONG_PIXEL_FORMAT_RGB24);
    require(rc == ZHULONG_PLUGIN_OK, "case9: odd roi crop_scale ok");
    if (rga.rga_usable()) {
        require(rga.rect_adapted_count() > prev_rect_adapted,
                "case9: odd roi incremented rect_adapted_count");
    }
}

// ── Case 10：行跨度适配 (stride_adapted_count) ───────────────────────────
void case_stride_adaptation() {
    const int src_w = 100, src_h = 100;
    // ⚠️ RGB24 像素为 3 字节，且硬件对齐为 4 字节，故行字节 stride 须为 lcm(3,4)=12 的倍数。
    // 紧凑行宽为 300 字节，填充到 312 字节（104 像素宽，312 > 300）。
    const int padded_stride = 312;
    const std::vector<uint8_t> source(static_cast<size_t>(padded_stride) * src_h, 100);

    zhulong::RgaVisionBackend rga;
    const uint64_t prev_stride_adapted = rga.stride_adapted_count();

    zhulong::Tensor out;
    const zhulong_status_t rc = run_letterbox(rga, source.data(), src_w, src_h, padded_stride,
                                              ZHULONG_PIXEL_FORMAT_RGB24,
                                              ZHULONG_PIXEL_FORMAT_RGB24, 64, 64, out);
    require(rc == ZHULONG_PLUGIN_OK, "case10: padded stride letterbox ok");
    if (rga.rga_usable()) {
        require(rga.stride_adapted_count() > prev_stride_adapted,
                "case10: padded stride incremented stride_adapted_count");
    }
}

// ── Case 11：热路径 CPU Tensor 原地复用 ────────────────────────────────────
void case_tensor_reuse() {
    zhulong::RgaVisionBackend rga;
    const int w = 64, h = 64;
    const std::vector<uint8_t> src = make_image(w, h, 3);
    zhulong::Tensor out({32, 32, 3}, zhulong::DataType::UINT8);
    const void* initial_ptr = out.raw_data();

    zhulong_status_t rc1 = run_letterbox(rga, src.data(), w, h, w * 3,
                                         ZHULONG_PIXEL_FORMAT_RGB24,
                                         ZHULONG_PIXEL_FORMAT_RGB24, 32, 32, out);
    require(rc1 == ZHULONG_PLUGIN_OK, "case11: first letterbox ok");
    require(out.raw_data() == initial_ptr, "case11: CPU tensor buffer reused on first call");

    const void* second_ptr = out.raw_data();
    zhulong_status_t rc2 = run_letterbox(rga, src.data(), w, h, w * 3,
                                         ZHULONG_PIXEL_FORMAT_RGB24,
                                         ZHULONG_PIXEL_FORMAT_RGB24, 32, 32, out);
    require(rc2 == ZHULONG_PLUGIN_OK, "case11: second letterbox ok");
    require(out.raw_data() == second_ptr, "case11: CPU tensor buffer reused on second call");
}

} // namespace

int main() {
    std::cout << "=== Zhulong RGA vision backend tests ===" << std::endl;

    case_probe();
    case_geometry();
    case_pixel_compare();
    case_formats();
    case_channel_swap();
    case_resize();
    case_faults_and_fallback();
    case_repeated();
    case_crop_scale();
    case_stride_adaptation();
    case_tensor_reuse();

    std::cout << "=== failures=" << g_failures << " ===" << std::endl;
    return g_failures == 0 ? 0 : 1;
}
