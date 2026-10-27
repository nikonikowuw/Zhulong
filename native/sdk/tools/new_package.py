#!/usr/bin/env python3
"""生成一个算法包骨架（`<id>/<target>/`），只需 SDK 前缀。

    new_package.py demo --target onnx --sdk-prefix /opt/zhulong-sdk
    cd demo/onnx && make SDK_PREFIX=/opt/zhulong-sdk && make test

生成物是**能跑通的最小真包**：空检测实现（`count` 恒 0）、包根可执行文件、开发期自测
（sample.jpg → result.jpg）、验包命令。它不是「示例代码」——照着把 `detect()` 换成真模型
推理就是产品包。

模板内嵌在本文件里（不另放 templates/ 目录）：模板和生成器分家必然漂移，而第三方拿到
两处不一致的 CMakeLists 时无从判断该信哪份。
"""

import argparse
import json
import os
import shutil
import sys
import textwrap

TARGETS = {
    # 目录名 → manifest.platform.target 的取值（两者必须一致，宿主按目录/字段双校验）
    "onnx": {"arch": "x86_64", "backend": "onnx", "input_ext": ".onnx"},
    "rk3568": {"arch": "aarch64", "backend": "rknn", "input_ext": ".rknn"},
}

CMAKE_LISTS = """\
# {title} 算法包 —— 只依赖已安装的 SDK 前缀
#
# `find_package(ZhulongAlgoSDK)` 之后有两个目标：
#   {id}         包根可执行文件（名字必须等于 manifest.json 的 id，宿主按这个约定找入口）
#   {id}_devtest 开发期自测：sample.jpg 进 → build/out/result.jpg 出（EXCLUDE_FROM_ALL，不进归档）
#
# ⚠️ 本文件不得出现任何指向某个仓库的相对路径：这个包必须能被单独拷走、单独构建。
cmake_minimum_required(VERSION 3.20)
project({id}_{target} LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
if(NOT CMAKE_BUILD_TYPE)
    set(CMAKE_BUILD_TYPE RelWithDebInfo)
endif()

find_package(ZhulongAlgoSDK 1.0 REQUIRED)

# 算法类编译成 OBJECT 库：可执行文件与 devtest 共用同一份目标文件，
# 保证「看图」和「交付」跑的是同一段代码。
add_library({id}_core OBJECT src/{id}.cpp)
target_include_directories({id}_core PUBLIC src)
target_link_libraries({id}_core PUBLIC Zhulong::AlgoRuntime)

add_executable({id} src/main.cpp)
target_link_libraries({id} PRIVATE {id}_core Zhulong::AlgoSDK Zhulong::AlgoRuntime)

add_executable({id}_devtest EXCLUDE_FROM_ALL tests/dev_infer.cpp)
target_link_libraries({id}_devtest PRIVATE {id}_core Zhulong::AlgoRuntime)
# dev 资产（vendored stb）由 SDK 提供，不从任何仓库里找
if(DEFINED ZhulongAlgoSDK_DEV_DIR AND EXISTS "${{ZhulongAlgoSDK_DEV_DIR}}/stb")
    target_include_directories({id}_devtest PRIVATE "${{ZhulongAlgoSDK_DEV_DIR}}/stb")
endif()

# 交付物不导出任何 zhulong 符号：静态库里的符号全部收成内部
if(UNIX AND NOT APPLE)
    target_link_options({id} PRIVATE "LINKER:--exclude-libs,ALL")
endif()
"""

MAKEFILE = """\
# {title} 算法包的开发期命令（只依赖 SDK 前缀，不引用任何仓库路径）
#
#   make SDK_PREFIX=<prefix>         构建包根可执行文件 + devtest
#   make test SDK_PREFIX=<prefix>    sample.jpg → build/out/result.jpg
#   make verify SDK_PREFIX=<prefix>  一次性验包（宿主安装时跑的同一条命令）
SDK_PREFIX ?= /opt/zhulong-sdk

BUILD_DIR ?= $(CURDIR)/build
OUT_DIR := $(BUILD_DIR)/out
EXE := $(BUILD_DIR)/{id}
DEVTEST := $(BUILD_DIR)/{id}_devtest
RESULT := $(OUT_DIR)/result-{id}-{target}.jpg
# ⚠️ 骨架不链接图像解码器：--sample 只吃 netpbm（P6/P5）。
#    给 jpg 会如实报 sample_decoded=false（只证明 ABI 生命周期）。
VERIFY_SAMPLE ?= $(CURDIR)/sample.ppm

.PHONY: all test verify clean

all: $(EXE) $(DEVTEST)

$(BUILD_DIR)/CMakeCache.txt: CMakeLists.txt
\tcmake -S $(CURDIR) -B $(BUILD_DIR) -DCMAKE_PREFIX_PATH=$(SDK_PREFIX)

$(EXE): $(BUILD_DIR)/CMakeCache.txt $(wildcard src/*.cpp src/*.hpp)
\tcmake --build $(BUILD_DIR) --target {id}

$(DEVTEST): $(BUILD_DIR)/CMakeCache.txt $(wildcard tests/*.cpp src/*.cpp src/*.hpp)
\tcmake --build $(BUILD_DIR) --target {id}_devtest

verify: $(EXE)
\t@cd $(CURDIR) && $(EXE) --verify --sample $(VERIFY_SAMPLE)

test: $(DEVTEST)
\t@mkdir -p $(OUT_DIR)
\t@cd $(CURDIR) && $(DEVTEST) --sample sample.jpg --out $(RESULT)

clean:
\trm -rf $(BUILD_DIR)
"""

MANIFEST = """\
{{
  "id": "{id}",
  "name": "{title}",
  "version": "0.1.0",
  "description": "由 new_package.py 生成的算法包骨架",
  "platform": {{
    "arch": "{arch}",
    "target": "{target}"
  }},
  "task_type": "{task_type}",
  "category": "general",
  "resources": {{
    "compute_cost": 10,
    "memory_mb": 64
  }}
}}
"""

CONFIG_SCHEMA = """\
{{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "properties": {{
    "score_threshold": {{
      "type": "number",
      "minimum": 0.0,
      "maximum": 1.0,
      "default": 0.5,
      "description": "输出框的最低置信度"
    }}
  }}
}}
"""

MAIN_CPP = """\
/**
 * @file main.cpp
 * @brief 包入口：只有一行宏。宿主不装载包，只 exec 它。
 */

#include <zhulong/algo_main.hpp>

#include "{id}.hpp"

ZHULONG_ALGO_MAIN(zhulong::{ns}::{class_name}, zhulong::sdk::DetectionResultAdapter, "{name}", "0.1.0")
"""

PLUGIN_HPP = """\
/**
 * @file {id}.hpp
 * @brief {title}的算法类（骨架：空实现）
 */

#pragma once

#include <memory>

#include <zhulong/algo.hpp>

namespace zhulong::{ns} {{

class {class_name} {{
public:
    {class_name}() = default;

    /**
     * @brief 加载模型。
     *
     * 骨架直接返回 OK；换真实模型时：`model_.load("models/xxx{input_ext}")`，
     * 路径相对**包根**（宿主以包目录为 CWD 启动包），失败返回
     * `ZHULONG_PLUGIN_ERR_NOT_SUPPORTED`（宿主会报 MODEL_UNAVAILABLE，
     * 而不是把包缺陷伪装成「图里没目标」）。
     */
    zhulong_status_t init(const std::string& config_json);

    /** @brief 每个 Worker 线程各自持有一个会话（不得跨线程共享） */
    zhulong::Session create_session(uint32_t worker_id);

    /** @brief 单帧推理。坐标必须归一化到 [0,1]；无目标返回空 vector，不得返回"空指针"。 */
    std::vector<zhulong::Detection> detect(zhulong::Session& session, const zhulong::Image& image,
                                           zhulong::TaskContext& task_context);

private:
    float score_threshold_{{0.5f}};
}};

}} // namespace zhulong::{ns}
"""

PLUGIN_CPP = """\
/**
 * @file {id}.cpp
 */

#include "{id}.hpp"

#include <cstring>

namespace zhulong::{ns} {{

namespace {{

/** @brief 从 config_json 里抠一个数字字段（骨架级实现：真包请用自带的 JSON 解析） */
float read_float(const std::string& config_json, const char* key, float fallback) {{
    const std::string needle = std::string("\\\"") + key + "\\\"";
    const auto key_pos = config_json.find(needle);
    if (key_pos == std::string::npos) {{
        return fallback;
    }}
    const auto colon = config_json.find(':', key_pos + needle.size());
    if (colon == std::string::npos) {{
        return fallback;
    }}
    try {{
        return std::stof(config_json.substr(colon + 1));
    }} catch (const std::exception&) {{
        return fallback;
    }}
}}

}} // namespace

zhulong_status_t {class_name}::init(const std::string& config_json) {{
    try {{
        score_threshold_ = read_float(config_json, "score_threshold", 0.5f);
        // 骨架不加载模型。换成真模型时务必显式判错，不要静默降级：
        //   if (!model_.load("models/model{input_ext}")) return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
        return ZHULONG_PLUGIN_OK;
    }} catch (...) {{
        return ZHULONG_PLUGIN_ERR_INTERNAL;
    }}
}}

zhulong::Session {class_name}::create_session(uint32_t worker_id) {{
    (void)worker_id;
    // 骨架没有模型，返回空会话（宿主只检查非空句柄，见 plugin.h 约定）
    return zhulong::Session{{}};
}}

std::vector<zhulong::Detection> {class_name}::detect(zhulong::Session& session, const zhulong::Image& image,
                                                     zhulong::TaskContext& task_context) {{
    (void)session;
    (void)image;
    (void)task_context;
    // 骨架：恒无检出。真实现放在这里（预处理 → session.forward → 后处理 → 归一化坐标）。
    return {{}};
}}

}} // namespace zhulong::{ns}
"""

DEV_INFER = """\
/**
 * @file dev_infer.cpp
 * @brief 开发期自测：sample.jpg → 本包算法类 → 画框 → build/out/result.jpg + 终端明细表
 *
 * 只服务开发期（EXCLUDE_FROM_ALL，不进归档）。读图用 SDK 提供的 stb，
 * 产品代码与交付物零图像依赖。
 */

#include <zhulong/algo.hpp>

#include "{id}.hpp"

#define STB_IMAGE_IMPLEMENTATION
#include "stb_image.h"
#define STB_IMAGE_WRITE_IMPLEMENTATION
#include "stb_image_write.h"

#include <chrono>
#include <cstdio>
#include <cstring>
#include <string>
#include <vector>

int main(int argc, char** argv) {{
    std::string sample = "sample.jpg";
    std::string out = "build/out/result.jpg";
    for (int i = 1; i + 1 < argc; i += 2) {{
        if (std::strcmp(argv[i], "--sample") == 0) sample = argv[i + 1];
        if (std::strcmp(argv[i], "--out") == 0) out = argv[i + 1];
    }}

    int width = 0, height = 0, channels = 0;
    unsigned char* pixels = stbi_load(sample.c_str(), &width, &height, &channels, 3);
    if (pixels == nullptr) {{
        std::fprintf(stderr, "failed to read %s: %s\\n", sample.c_str(), stbi_failure_reason());
        return 1;
    }}

    zhulong::{ns}::{class_name} plugin;
    if (plugin.init("{{}}") != ZHULONG_PLUGIN_OK) {{
        std::fprintf(stderr, "init failed\\n");
        stbi_image_free(pixels);
        return 1;
    }}
    auto session = plugin.create_session(0);

    // zhulong::Image 只是帧视图的轻包装：显式构造 frame_view，别指望它有像素字段。
    zhulong_frame_view frame;
    std::memset(&frame, 0, sizeof(frame));
    frame.frame_id = 1;
    frame.pts = 0;
    frame.width = width;
    frame.height = height;
    frame.format = ZHULONG_PIXEL_FORMAT_RGB24;
    frame.planes[0] = pixels;
    frame.strides[0] = width * 3;
    frame.dma_buf_fd = -1;
    zhulong::Image image(frame);

    zhulong::TaskContext context{{}};
    context.task_id = 1;

    const auto started = std::chrono::steady_clock::now();
    const auto detections = plugin.detect(session, image, context);
    const auto cost_ms = std::chrono::duration<double, std::milli>(
                             std::chrono::steady_clock::now() - started).count();

    std::printf("%-10s %-14s %8s %s\\n", "kind", "label", "score", "box[x1,y1,x2,y2]");
    for (const auto& detection : detections) {{
        std::printf("%-10s %-14s %8.3f [%.3f,%.3f,%.3f,%.3f]\\n", "object", detection.label.c_str(),
                    detection.confidence, detection.box[0], detection.box[1], detection.box[2],
                    detection.box[3]);
        const int x1 = static_cast<int>(detection.box[0] * width);
        const int y1 = static_cast<int>(detection.box[1] * height);
        const int x2 = static_cast<int>(detection.box[2] * width);
        const int y2 = static_cast<int>(detection.box[3] * height);
        for (int x = x1; x < x2; ++x) {{
            for (int t = 0; t < 2; ++t) {{
                if (y1 + t >= 0 && y1 + t < height && x >= 0 && x < width) {{
                    unsigned char* pixel = pixels + ((y1 + t) * width + x) * 3;
                    pixel[0] = 0; pixel[1] = 255; pixel[2] = 0;
                }}
                if (y2 - t >= 0 && y2 - t < height && x >= 0 && x < width) {{
                    unsigned char* pixel = pixels + ((y2 - t) * width + x) * 3;
                    pixel[0] = 0; pixel[1] = 255; pixel[2] = 0;
                }}
            }}
        }}
    }}
    std::printf("cost_ms     : %.2f\\n", cost_ms);
    std::printf("count       : %zu\\n", detections.size());

    if (!stbi_write_jpg(out.c_str(), width, height, 3, pixels, 92)) {{
        std::fprintf(stderr, "failed to write %s\\n", out.c_str());
        stbi_image_free(pixels);
        return 1;
    }}
    std::printf("result written: %s\\n", out.c_str());
    stbi_image_free(pixels);
    // 骨架恒无检出，这里不把 count==0 当失败：换真实模型后再加金标断言。
    return 0;
}}
"""

TASKS = {
    "detect": ("DetectionResultAdapter", "detect"),
    "classify": ("ClassificationResultAdapter", "classify"),
}


def identifier(text):
    """把 id 变成合法的 C++ 标识符片段（demo-face → demo_face）。"""
    cleaned = "".join(ch if ch.isalnum() else "_" for ch in text)
    if not cleaned or cleaned[0].isdigit():
        cleaned = "pkg_" + cleaned
    return cleaned


def write(path, content):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as handle:
        handle.write(content)


def sample_ppm(path, width=640, height=480):
    """生成一张灰度渐变 PPM（P6）：没有目标，用来证明协议通而不是证明精度。"""
    header = f"P6\n{width} {height}\n255\n".encode()
    pixels = bytearray()
    for y in range(height):
        for x in range(width):
            value = (x * 255 // max(1, width - 1) + y * 255 // max(1, height - 1)) // 2
            pixels += bytes((value, value, value))
    with open(path, "wb") as handle:
        handle.write(header + pixels)


def main(argv):
    parser = argparse.ArgumentParser(description="生成算法包骨架")
    parser.add_argument("id", help="算法包 id（同时是包根可执行文件名，小写字母/数字/下划线）")
    parser.add_argument("--target", default="onnx", choices=sorted(TARGETS))
    parser.add_argument("--name", help="显示名（默认取 id）")
    parser.add_argument("--task-type", default="detect", choices=["detect", "person_structure", "vehicle_structure"])
    parser.add_argument("--dir", help="生成到哪个父目录（默认当前目录）")
    parser.add_argument("--sdk-prefix", help="仅用于提示：生成后 make 时要用的前缀路径")
    args = parser.parse_args(argv)

    if not args.id.replace("_", "").replace("-", "").isalnum():
        parser.error("id 只能包含字母、数字、下划线、连字符")
    pkg_dir = os.path.join(args.dir or ".", args.id, args.target)
    if os.path.exists(pkg_dir):
        parser.error(f"{pkg_dir} 已存在；先删掉或换一个 id")

    spec = TARGETS[args.target]
    title = args.name or args.id
    ns = identifier(args.id)
    class_name = "".join(part.capitalize() for part in ns.split("_")) + "Plugin"
    fields = {
        "id": args.id,
        "title": title,
        "target": args.target,
        "arch": spec["arch"],
        "input_ext": spec["input_ext"],
        "ns": ns,
        "class_name": class_name,
        "name": title,
        "task_type": args.task_type,
    }

    write(os.path.join(pkg_dir, "CMakeLists.txt"), CMAKE_LISTS.format(**fields))
    write(os.path.join(pkg_dir, "Makefile"), MAKEFILE.format(**fields))
    manifest = json.loads(MANIFEST.format(**fields))
    write(os.path.join(pkg_dir, "manifest.json"), json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
    write(os.path.join(pkg_dir, "config.schema.json"), CONFIG_SCHEMA.format(**fields))
    write(os.path.join(pkg_dir, "src", "main.cpp"), MAIN_CPP.format(**fields))
    write(os.path.join(pkg_dir, "src", f"{args.id}.hpp"), PLUGIN_HPP.format(**fields))
    write(os.path.join(pkg_dir, "src", f"{args.id}.cpp"), PLUGIN_CPP.format(**fields))
    write(os.path.join(pkg_dir, "tests", "dev_infer.cpp"), DEV_INFER.format(**fields))
    os.makedirs(os.path.join(pkg_dir, "models"), exist_ok=True)
    sample_ppm(os.path.join(pkg_dir, "sample.ppm"))
    # manifest 要求 sample.jpg 存在（平台准入会检查）；开发期用同一张图转 jpg 即可，
    # 这里不内嵌二进制资源，只把转换命令写进 README。
    write(os.path.join(pkg_dir, "README.md"), textwrap.dedent(f"""\
        # {title}（{args.target}）

        由 `new_package.py` 生成的最小可用算法包骨架。

        ```bash
        make SDK_PREFIX={args.sdk_prefix or '/opt/zhulong-sdk'}         # 构建
        make test SDK_PREFIX={args.sdk_prefix or '/opt/zhulong-sdk'}    # sample.jpg → build/out/result.jpg
        make verify SDK_PREFIX={args.sdk_prefix or '/opt/zhulong-sdk'}  # 一次性验包（用 sample.ppm）
        ```

        `sample.jpg` 目前缺失，先补一张待检测的图：

        ```bash
        ffmpeg -i sample.ppm sample.jpg
        ```

        然后把 `src/{args.id}.cpp` 里的 `detect()` 换成真实推理：
        加载 `models/` 下的模型（路径相对包根）→ 预处理 → `session.forward` → 后处理，
        输出**归一化** `[x1,y1,x2,y2] ∈ [0,1]`。初始化失败要显式返回
        `ZHULONG_PLUGIN_ERR_NOT_SUPPORTED`，不要静默返回空结果。
        """))
    write(os.path.join(pkg_dir, "models", ".gitkeep"), "")

    print(f"[✓] 已生成 {pkg_dir}")
    print("    下一步：")
    prefix = args.sdk_prefix or "/opt/zhulong-sdk"
    print(f"      cd {pkg_dir}")
    print(f"      ffmpeg -i sample.ppm sample.jpg   # manifest 要求 sample.jpg")
    print(f"      make SDK_PREFIX={prefix} && make test SDK_PREFIX={prefix}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
