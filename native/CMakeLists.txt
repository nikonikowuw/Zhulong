# Zhulong Native 引擎 CMake 构建配置
cmake_minimum_required(VERSION 3.20)
project(ZhulongEngine LANGUAGES C CXX)

include(CTest)
include(CheckCXXSourceCompiles)

# 依赖标准线程库
find_package(Threads REQUIRED)

# 强制要求 C++17 标准且关闭编译器扩展
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# ==============================================================================
#                      1. 静态 FFmpeg 依赖与工具链一致性强校验
# ==============================================================================
# 仅接受由 native/scripts/build.py 明确准备好的静态依赖安装前缀；
# configure 阶段严禁发起网络下载或查询宿主机系统环境的 FFmpeg。
set(FFMPEG_ROOT "" CACHE PATH "Pinned FFmpeg installation from native/scripts/build.py")
if(NOT EXISTS "${FFMPEG_ROOT}/zhulong-ffmpeg.json")
    message(FATAL_ERROR "Missing verified static FFmpeg prefix. Run make native-deps, then make native-build (offline).")
endif()

file(READ "${FFMPEG_ROOT}/zhulong-ffmpeg.json" FFMPEG_MANIFEST)

# 校验当前 CMake 使用的 C/C++ 编译器指纹必须与编译 FFmpeg 时完全一致（防编译器热替换/混用）
foreach(LANG cc cxx)
    if(LANG STREQUAL "cc")
        set(COMPILER "${CMAKE_C_COMPILER}")
    else()
        set(COMPILER "${CMAKE_CXX_COMPILER}")
    endif()
    file(REAL_PATH "${COMPILER}" COMPILER_REAL)
    file(SHA256 "${COMPILER_REAL}" COMPILER_SHA256)
    string(JSON EXPECTED_COMPILER GET "${FFMPEG_MANIFEST}" profile ${LANG})
    string(JSON EXPECTED_SHA256 GET "${FFMPEG_MANIFEST}" profile tools ${LANG} sha256)
    if(NOT COMPILER_REAL STREQUAL EXPECTED_COMPILER OR NOT COMPILER_SHA256 STREQUAL EXPECTED_SHA256)
        message(FATAL_ERROR "FFmpeg and Engine toolchain identities differ; refusing mixed target libraries")
    endif()
endforeach()

# 校验交叉编译与 Host 模式环境隔离
string(JSON FFMPEG_MODE GET "${FFMPEG_MANIFEST}" profile mode)
if(CMAKE_CROSSCOMPILING)
    if(FFMPEG_MODE STREQUAL "host")
        message(FATAL_ERROR "Cross Engine cannot use host FFmpeg")
    endif()
    string(JSON FFMPEG_SYSROOT GET "${FFMPEG_MANIFEST}" profile sysroot)
    if(NOT CMAKE_SYSROOT STREQUAL FFMPEG_SYSROOT)
        message(FATAL_ERROR "FFmpeg and Engine sysroots differ")
    endif()
elseif(NOT FFMPEG_MODE STREQUAL "host")
    message(FATAL_ERROR "Host Engine cannot use cross FFmpeg")
endif()

# 校验所需的静态库文件（libavformat.a, libavcodec.a, libavutil.a）必须存在
set(FFMPEG_LIBRARIES "")
foreach(LIB avformat avcodec avutil)
    set(ARCHIVE "${FFMPEG_ROOT}/lib/lib${LIB}.a")
    if(NOT EXISTS "${ARCHIVE}")
        message(FATAL_ERROR "Required static archive missing: ${ARCHIVE}")
    endif()
    list(APPEND FFMPEG_LIBRARIES "${ARCHIVE}")
endforeach()

# 校验系统依赖库白名单（仅允许 -pthread, -lm, -latomic, -ldl）
set(FFMPEG_SYSTEM_LIBRARIES "" CACHE STRING "System dependencies from this target's static .pc files")
if(NOT FFMPEG_SYSTEM_LIBRARIES)
    message(FATAL_ERROR "Pass target static system libraries via native/scripts/build.py; no implicit host defaults")
endif()
foreach(LIB IN LISTS FFMPEG_SYSTEM_LIBRARIES)
    if(APPLE AND LIB MATCHES "^-framework (CoreFoundation|CoreVideo|CoreMedia|AudioToolbox|VideoToolbox|Security)$")
        continue()
    endif()
    if(NOT LIB MATCHES "^-(pthread|lm|latomic|ldl)$")
        message(FATAL_ERROR "Unreviewed FFmpeg system dependency: ${LIB}")
    endif()
endforeach()

# ==============================================================================
#                      2. C++17 与静态依赖端到端编译链接校验
# ==============================================================================
set(CMAKE_REQUIRED_INCLUDES "${FFMPEG_ROOT}/include")
set(CMAKE_REQUIRED_LIBRARIES ${FFMPEG_LIBRARIES} Threads::Threads ${FFMPEG_SYSTEM_LIBRARIES})

# 动态测试当前编译器与静态库的链接健全性（不依赖假定的编译器版本底线）
unset(ZHULONG_LINK_WORKS CACHE)
check_cxx_source_compiles("
#include <thread>
#include <mutex>
#include <optional>
extern \"C\" {
#include <libavformat/avformat.h>
#include <libavcodec/packet.h>
}
int main() {
  std::optional<int> value(1); std::mutex mutex;
  std::thread worker([&] { std::lock_guard<std::mutex> lock(mutex); ++*value; });
  worker.join();
  auto *packet = av_packet_alloc(); av_packet_free(&packet);
  auto *context = avformat_alloc_context(); avformat_free_context(context);
  return avformat_version() == 0;
}" ZHULONG_LINK_WORKS)

if(NOT ZHULONG_LINK_WORKS)
    message(FATAL_ERROR "C++17/thread/static FFmpeg target link check failed")
endif()
unset(CMAKE_REQUIRED_INCLUDES)
unset(CMAKE_REQUIRED_LIBRARIES)

# ==============================================================================
#                      3. Sanitizer 内存与线程安全检测支持
# ==============================================================================
set(ZHULONG_SANITIZER "" CACHE STRING "address (ASan+UBSan), thread, or empty")
if(ZHULONG_SANITIZER STREQUAL "address")
    # UBSan 默认遇到未定义行为仅警告并继续，设置 -fno-sanitize-recover 确保遇错立崩立查
    add_compile_options(-fsanitize=address,undefined -fno-sanitize-recover=undefined -fno-omit-frame-pointer)
    add_link_options(-fsanitize=address,undefined)
elseif(ZHULONG_SANITIZER STREQUAL "thread")
    add_compile_options(-fsanitize=thread -fno-omit-frame-pointer)
    add_link_options(-fsanitize=thread)
elseif(NOT ZHULONG_SANITIZER STREQUAL "")
    message(FATAL_ERROR "Unknown sanitizer")
endif()

# ==============================================================================
#                      4. 构建 ZhulongEngine 静态库目标
# ==============================================================================
add_library(ZhulongEngine STATIC
    src/abi/engine.cpp
    src/abi/snapshot.cpp
    src/pipeline/engine.cpp
    src/pipeline/stream.cpp
    src/pipeline/preview_normalizer.cpp
    src/pipeline/subscription.cpp
    src/pipeline/frame_publisher.cpp
    src/pipeline/motion_detector.cpp
    src/nodes/capture/rtsp_input.cpp
    src/nodes/decode/decoder_factory.cpp
    src/nodes/decode/ffmpeg_decoder.cpp
    src/nodes/decode/mpp_loader.cpp
    src/nodes/decode/mpp_decoder.cpp
    src/nodes/snapshot/jpeg_encoder.cpp
    src/nodes/snapshot/jpeg_encoder_backend.cpp
    src/nodes/snapshot/mpp_jpeg_encoder.cpp
)
target_compile_features(ZhulongEngine PUBLIC cxx_std_17)
target_include_directories(ZhulongEngine
    PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/include
    PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src ${FFMPEG_ROOT}/include
)
target_link_libraries(ZhulongEngine PUBLIC ZhulongAlgoIpc ${FFMPEG_LIBRARIES} Threads::Threads ${CMAKE_DL_LIBS} ${FFMPEG_SYSTEM_LIBRARIES})
set_target_properties(ZhulongEngine PROPERTIES POSITION_INDEPENDENT_CODE ON)
target_compile_options(ZhulongEngine PRIVATE
    $<$<CXX_COMPILER_ID:AppleClang,Clang,GNU>:-Wall;-Wextra;-Wpedantic>
)

# ==============================================================================
#          4.0 后端选择：推理 / 视觉前处理（编译期 profile 隔离）
# ==============================================================================
# 推理后端与视觉前处理后端是两个正交开关：可「RKNN 推理 + CPU 前处理」，也可
# 「ONNX 推理 + RGA 前处理」。视觉后端的 CPU 实现是**产品实现**（默认 cpu 合理）；
# 推理后端没有默认值，必须显式指定（见下方校验）。
# 视觉后端 rga 由兄弟任务 10-13-algo-rga-vision-backend 实现，本任务只声明变量。
# ⚠️ 没有默认推理后端：调用方（native/scripts/build.py）必须显式传入。
#    `test_stub` 只允许用于测试夹具构建（broker/runner 回归），其实现位于 tests/fixtures/。
set(ZHULONG_INFERENCE_BACKEND "" CACHE STRING "算法运行库推理后端: onnx | rknn | test_stub（无默认值，必须显式指定）")
set_property(CACHE ZHULONG_INFERENCE_BACKEND PROPERTY STRINGS onnx rknn test_stub)
set(ZHULONG_VISION_BACKEND "cpu" CACHE STRING "算法运行库视觉前处理后端: cpu | rga")
set_property(CACHE ZHULONG_VISION_BACKEND PROPERTY STRINGS cpu rga)
set(ZHULONG_RKNN_ROOT "" CACHE PATH "RKNN 前缀（include/rknn_api.h + lib/librknnrt.so）；仅 ZHULONG_INFERENCE_BACKEND=rknn 需要")
set(ZHULONG_ONNX_ROOT "" CACHE PATH "ONNX Runtime 前缀（include/onnxruntime_cxx_api.h + lib/libonnxruntime.so）；仅 ZHULONG_INFERENCE_BACKEND=onnx 需要")
set(ZHULONG_RGA_ROOT "" CACHE PATH "RGA 前缀（include/rga/im2d.h + lib/librga.so*）；仅 ZHULONG_VISION_BACKEND=rga 需要")

if(ZHULONG_INFERENCE_BACKEND STREQUAL "")
    message(FATAL_ERROR
        "未选择推理后端：请显式传入 -DZHULONG_INFERENCE_BACKEND=onnx|rknn"
        "（测试夹具构建用 test_stub）。没有默认后端——静默降级到桩后端会产出"
        "「编译成功但不可推理」的产物，这是明确禁止的。")
endif()
if(NOT ZHULONG_INFERENCE_BACKEND MATCHES "^(rknn|onnx|test_stub)$")
    message(FATAL_ERROR "ZHULONG_INFERENCE_BACKEND 取值非法: ${ZHULONG_INFERENCE_BACKEND}（期望 rknn、onnx 或 test_stub）")
endif()
if(NOT ZHULONG_VISION_BACKEND MATCHES "^(cpu|rga)$")
    message(FATAL_ERROR "ZHULONG_VISION_BACKEND 取值非法: ${ZHULONG_VISION_BACKEND}（期望 cpu 或 rga）")
endif()

# RGA 视觉前处理后端：与 RKNN 同理，include 目录 PRIVATE、库以绝对文件路径链接。
# ⚠️ librga.so 的 SONAME 是 librga.so.2（不是 librga.so），NEEDED 会显示 librga.so.2。
if(ZHULONG_VISION_BACKEND STREQUAL "rga")
    if(NOT ZHULONG_RGA_ROOT)
        message(FATAL_ERROR "ZHULONG_VISION_BACKEND=rga 需要 -DZHULONG_RGA_ROOT=<rga 前缀>")
    endif()
    if(NOT EXISTS "${ZHULONG_RGA_ROOT}/include/rga/im2d.h")
        message(FATAL_ERROR "RGA 前缀缺少 include/rga/im2d.h: ${ZHULONG_RGA_ROOT}")
    endif()
    if(NOT EXISTS "${ZHULONG_RGA_ROOT}/lib/librga.so.2")
        message(FATAL_ERROR "RGA 前缀缺少 lib/librga.so.2: ${ZHULONG_RGA_ROOT}")
    endif()
    # 与 RKNN 同理：下游目标链接 ZhulongAlgoRuntime 时需解析其传递依赖 librga.so.2 的符号。
    add_link_options(-Wl,-rpath-link,${ZHULONG_RGA_ROOT}/lib)
endif()

# RKNN 推理后端：include 目录 PRIVATE（AC1），库以绝对文件路径链接。
# ⚠️ -rpath-link 是必须的：下游可执行/插件链接 ZhulongAlgoRuntime 时，GNU ld 需要
# 解析其传递依赖 librknnrt.so 的符号，而 ld **不**使用 -L 目录做 rpath-link 搜索。
# 它只影响链接期解析，不会把 librknnrt.so 加进下游的 NEEDED（AC2）。
if(ZHULONG_INFERENCE_BACKEND STREQUAL "rknn")
    if(NOT ZHULONG_RKNN_ROOT)
        message(FATAL_ERROR "ZHULONG_INFERENCE_BACKEND=rknn 需要 -DZHULONG_RKNN_ROOT=<rknpu2 前缀>")
    endif()
    if(NOT EXISTS "${ZHULONG_RKNN_ROOT}/include/rknn_api.h")
        message(FATAL_ERROR "RKNN 前缀缺少 include/rknn_api.h: ${ZHULONG_RKNN_ROOT}")
    endif()
    if(NOT EXISTS "${ZHULONG_RKNN_ROOT}/lib/librknnrt.so")
        message(FATAL_ERROR "RKNN 前缀缺少 lib/librknnrt.so: ${ZHULONG_RKNN_ROOT}")
    endif()
    # ⚠️ -rpath-link 是必须的：下游目标（ZhulongRknnTests / 插件）链接 ZhulongAlgoRuntime
    # 时，GNU ld 必须解析其传递依赖 librknnrt.so 的符号，而 ld **不**用 -L 目录做
    # rpath-link 搜索。实测：去掉本行后链接报 `undefined reference to rknn_init` 等 8 个符号。
    # 它只影响链接期符号解析，不会把 librknnrt.so 写进下游的 NEEDED（AC2）。
    add_link_options(-Wl,-rpath-link,${ZHULONG_RKNN_ROOT}/lib)
endif()

# ONNX Runtime 推理后端：include 目录 PRIVATE，库以绝对文件路径链接。
if(ZHULONG_INFERENCE_BACKEND STREQUAL "onnx")
    if(NOT ZHULONG_ONNX_ROOT)
        message(FATAL_ERROR "ZHULONG_INFERENCE_BACKEND=onnx 需要 -DZHULONG_ONNX_ROOT=<onnxruntime 前缀>")
    endif()
    if(NOT EXISTS "${ZHULONG_ONNX_ROOT}/include/onnxruntime_cxx_api.h")
        message(FATAL_ERROR "ONNX Runtime 前缀缺少 include/onnxruntime_cxx_api.h: ${ZHULONG_ONNX_ROOT}")
    endif()
    if(NOT EXISTS "${ZHULONG_ONNX_ROOT}/lib/libonnxruntime.so")
        message(FATAL_ERROR "ONNX Runtime 前缀缺少 lib/libonnxruntime.so: ${ZHULONG_ONNX_ROOT}")
    endif()
    add_link_options(-Wl,-rpath-link,${ZHULONG_ONNX_ROOT}/lib)
endif()

# ==============================================================================
#  4.1 公共算法运行库：OBJECT 单一归属 → SHARED（迁移基线）+ STATIC（交付形态）
# ==============================================================================
# 源文件只在 OBJECT 库声明一次，SHARED 与 STATIC 都从同一批对象构建。若两个目标各自
# 编译同一翻译单元，链接结果取决于归档扫描顺序，最坏会出现两份 DmaBufAllocator /
# GalleryRegistry 单例（design.md §4.1.1 第 2 条）。
#
# ⚠️ **不要**给 OBJECT 库设 CXX_VISIBILITY_PRESET hidden（design.md §4.1.1 第 3 条）：
#    ZhulongAlgoRuntime（SHARED）仍从这批对象构建，FaceRecognitionPlugin /
#    PlateRecognitionPlugin 还 target_link_libraries 它；hidden 会让 .so 不再导出
#    DmaBufAllocator / GalleryRegistry 等符号 → 插件链接期 undefined reference。
#    算法包可执行文件不外泄 zhulong 符号由两件事满足：可执行文件默认不导出未被动态
#    对象引用的符号（不加 -rdynamic）+ 链接侧 -Wl,--exclude-libs,ALL。
add_library(ZhulongPlatformObjects OBJECT
    src/platform/dmabuf/dmabuf_allocator.cpp
)
add_library(ZhulongAlgoRuntimeObjects OBJECT
    src/algorithm/runtime/cpu_vision_backend.cpp
    src/algorithm/runtime/detection_postprocess.cpp
    src/algorithm/runtime/gallery_registry.cpp
    src/algorithm/runtime/letterbox_geometry.cpp
    src/algorithm/runtime/runtime_factory.cpp
)
# ⚠️ OBJECT 库不会自动开 PIC（SHARED 才会）。漏掉这行，链接 PIE 时报
#    `R_X86_64_32 ... can not be used when making a PIE object`。
set_target_properties(ZhulongPlatformObjects ZhulongAlgoRuntimeObjects PROPERTIES
    POSITION_INDEPENDENT_CODE ON
)

# 两个最终产物共享同一批对象，静态产物与 .so 同名（libZhulongAlgoRuntime.a / .so）。
set(ZHULONG_ALGO_RUNTIME_OBJECTS
    $<TARGET_OBJECTS:ZhulongAlgoRuntimeObjects>
    $<TARGET_OBJECTS:ZhulongPlatformObjects>
)
add_library(ZhulongAlgoRuntime SHARED ${ZHULONG_ALGO_RUNTIME_OBJECTS})
add_library(ZhulongAlgoRuntimeStatic STATIC ${ZHULONG_ALGO_RUNTIME_OBJECTS})
set_target_properties(ZhulongAlgoRuntimeStatic PROPERTIES OUTPUT_NAME ZhulongAlgoRuntime)

# 后端差异只累积到这两个变量，最后统一施加到四个目标上，避免同一份设置写四遍：
#   ZHULONG_ALGO_RUNTIME_PRIVATE_LIBS → 后端动态库（SHARED 私有；STATIC 以 PUBLIC 传递）
#   ZHULONG_ALGO_RUNTIME_PUBLIC_DEFS  → 后端编译定义（OBJECT 编译 + SHARED/STATIC 使用要求）
set(ZHULONG_ALGO_RUNTIME_PRIVATE_LIBS "")
set(ZHULONG_ALGO_RUNTIME_PUBLIC_DEFS "")

# RKNN 推理后端：只进运行库，宿主 ZhulongEngine 绝不链接。
# 绝对路径 include（PRIVATE）与绝对文件路径链接，避免污染 sysroot 与 CMAKE_FIND_ROOT_PATH。
if(ZHULONG_INFERENCE_BACKEND STREQUAL "rknn")
    target_sources(ZhulongAlgoRuntimeObjects PRIVATE src/backends/rknn/rknn_inference_backend.cpp)
    target_include_directories(ZhulongAlgoRuntimeObjects PRIVATE ${ZHULONG_RKNN_ROOT}/include)
    list(APPEND ZHULONG_ALGO_RUNTIME_PRIVATE_LIBS ${ZHULONG_RKNN_ROOT}/lib/librknnrt.so Threads::Threads)
    list(APPEND ZHULONG_ALGO_RUNTIME_PUBLIC_DEFS ZHULONG_INFERENCE_RKNN=1)
    # ⚠️ 产物卫生（AC6）：CMake 会为「构建树之外的链接库」自动加 BUILD_RPATH，把开发机
    # 绝对路径烧进 .so（实测为 /home/niko/dev/Go/Zhulong/native/vendor/rknpu2/lib）。
    # 该 .so 要 scp 到 RK3568，不应携带宿主机路径。清空 BUILD_RPATH；
    # 下游目标的符号解析由上面的 add_link_options(-Wl,-rpath-link,…) 保证。
    set_target_properties(ZhulongAlgoRuntime PROPERTIES SKIP_BUILD_RPATH TRUE BUILD_RPATH "")
endif()

# ONNX Runtime 推理后端：只进运行库，宿主 ZhulongEngine 绝不链接。
if(ZHULONG_INFERENCE_BACKEND STREQUAL "onnx")
    target_sources(ZhulongAlgoRuntimeObjects PRIVATE src/backends/onnx/onnx_inference_backend.cpp)
    target_include_directories(ZhulongAlgoRuntimeObjects SYSTEM PRIVATE ${ZHULONG_ONNX_ROOT}/include)
    list(APPEND ZHULONG_ALGO_RUNTIME_PRIVATE_LIBS ${ZHULONG_ONNX_ROOT}/lib/libonnxruntime.so Threads::Threads)
    list(APPEND ZHULONG_ALGO_RUNTIME_PUBLIC_DEFS ZHULONG_INFERENCE_ONNX=1)
    set_target_properties(ZhulongAlgoRuntime PROPERTIES SKIP_BUILD_RPATH TRUE BUILD_RPATH "")
endif()

# 测试夹具桩后端：实现位于 tests/fixtures/，**只允许**由 test_stub 构建编译。
# 产品构建（onnx / rknn）从不包含该文件，SDK 分发物也不含它。
if(ZHULONG_INFERENCE_BACKEND STREQUAL "test_stub")
    target_sources(ZhulongAlgoRuntimeObjects PRIVATE tests/fixtures/stub_inference_backend.cpp)
    target_include_directories(ZhulongAlgoRuntimeObjects PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/tests/fixtures)
    list(APPEND ZHULONG_ALGO_RUNTIME_PUBLIC_DEFS ZHULONG_INFERENCE_TEST_STUB=1)
endif()

# RGA 视觉后端：同样只进运行库，宿主绝不链接。
# ⚠️ letterbox_geometry.cpp 是 CPU/RGA **共用**的几何计算，已在上面基础目标中，不在此重复。
if(ZHULONG_VISION_BACKEND STREQUAL "rga")
    target_sources(ZhulongAlgoRuntimeObjects PRIVATE
        src/backends/rga/rga_vision_backend.cpp
    )
    target_include_directories(ZhulongAlgoRuntimeObjects PRIVATE ${ZHULONG_RGA_ROOT}/include)
    list(APPEND ZHULONG_ALGO_RUNTIME_PRIVATE_LIBS ${ZHULONG_RGA_ROOT}/lib/librga.so.2 Threads::Threads)
    target_compile_definitions(ZhulongAlgoRuntimeObjects PRIVATE ZHULONG_VISION_RGA=1)
    # 产物卫生：清空 BUILD_RPATH，理由同 RKNN 分支。
    set_target_properties(ZhulongAlgoRuntime PROPERTIES SKIP_BUILD_RPATH TRUE BUILD_RPATH "")
endif()

# 编译期公共设置：OBJECT 库真正编译，SHARED / STATIC 把同一份设置作为使用要求传给下游
# （sdk/include + src 的头文件搜索路径、cxx_std_17、后端编译定义三处必须一致）。
foreach(target ZhulongPlatformObjects ZhulongAlgoRuntimeObjects)
    target_compile_features(${target} PRIVATE cxx_std_17)
    target_include_directories(${target} PRIVATE
        ${CMAKE_CURRENT_SOURCE_DIR}/sdk/include ${CMAKE_CURRENT_SOURCE_DIR}/src
    )
    target_compile_definitions(${target} PRIVATE ${ZHULONG_ALGO_RUNTIME_PUBLIC_DEFS})
endforeach()
foreach(target ZhulongAlgoRuntime ZhulongAlgoRuntimeStatic)
    target_compile_features(${target} PUBLIC cxx_std_17)
    # ⚠️ 对外只暴露 sdk/include（阶段 2.3）：算法包 target 与第三方构建只允许这套头。
    #    `src` 是仓库内部实现目录，任何消费方想要它都应由自己显式声明（白盒测试才会要）。
    target_include_directories(${target} PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/sdk/include)
    target_include_directories(${target} PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
    target_compile_definitions(${target} PUBLIC ${ZHULONG_ALGO_RUNTIME_PUBLIC_DEFS})
endforeach()
foreach(target ZhulongPlatformObjects ZhulongAlgoRuntimeObjects)
    target_compile_options(${target} PRIVATE
        $<$<CXX_COMPILER_ID:AppleClang,Clang,GNU>:-Wall;-Wextra;-Wpedantic>
    )
endforeach()
# 静态库不能「链接」依赖，只能把最终链接需求交给消费者；故 STATIC 用 PUBLIC，
# SHARED 维持 PRIVATE（下游靠 .so 的 NEEDED 解析，不继承 ORT / RKNN 链接项）。
target_link_libraries(ZhulongAlgoRuntime PRIVATE ${ZHULONG_ALGO_RUNTIME_PRIVATE_LIBS})
target_link_libraries(ZhulongAlgoRuntimeStatic PUBLIC ${ZHULONG_ALGO_RUNTIME_PRIVATE_LIBS})

# ==============================================================================
#  4.0.9 迁移期：人脸包按 target 选源码目录（阶段 3.9 删掉这段桥接）
# ==============================================================================
# 包源码已按 target 分家：`packages/face_recognition/{onnx,rk3568}/src`。
# 宿主构建按推理后端选择一份——rknn 构建用板端实现，其余（onnx / test_stub 夹具）
# 用主机实现。包真正独立构建后（SDK 前缀），这个变量应当消失。
if(ZHULONG_INFERENCE_BACKEND STREQUAL "rknn")
    set(ZHULONG_FACE_IMPL_DIR ${CMAKE_CURRENT_SOURCE_DIR}/../packages/face_recognition/rk3568)
else()
    set(ZHULONG_FACE_IMPL_DIR ${CMAKE_CURRENT_SOURCE_DIR}/../packages/face_recognition/onnx)
endif()

# ==============================================================================
#  4.1.1 算法包 SDK 安装（make sdk-install → build/sdk/prefix）
# ==============================================================================
# 交给第三方的是「已编译好的公共头 + 静态归档 + CMake 包配置」，不含 native/src。
# ⚠️ 刻意**不用** install(EXPORT)：导出的目标不能携带构建机绝对路径，前缀一旦搬迁就失效，
#    所以手写 ZhulongAlgoSDKConfig.cmake（cmake/ZhulongAlgoSDKConfig.cmake.in）。
# ⚠️ test_stub 夹具构建不产生任何 SDK 安装规则：夹具产物不可交付（见 hardware-backends）。
if(ZHULONG_INFERENCE_BACKEND STREQUAL "test_stub")
    message(STATUS "test_stub 夹具构建：跳过 SDK 安装规则（夹具产物不可交付）")
else()
    include(CMakePackageConfigHelpers)

    # 版本与协议号的唯一来源：native/sdk/abi.json（3.2b）。改它等于要求已发布包重建。
    file(READ "${CMAKE_CURRENT_SOURCE_DIR}/sdk/abi.json" ZHULONG_SDK_ABI)
    string(JSON ZHULONG_SDK_VERSION GET "${ZHULONG_SDK_ABI}" sdk_version)
    string(JSON ZHULONG_SDK_API_VERSION GET "${ZHULONG_SDK_ABI}" api_version)
    string(JSON ZHULONG_SDK_PROTOCOL GET "${ZHULONG_SDK_ABI}" protocol)

    if(ZHULONG_INFERENCE_BACKEND STREQUAL "rknn")
        set(ZHULONG_SDK_BACKEND rknn)
        set(ZHULONG_SDK_BACKEND_DEFINE ZHULONG_INFERENCE_RKNN=1)
    else()
        set(ZHULONG_SDK_BACKEND onnx)
        set(ZHULONG_SDK_BACKEND_DEFINE ZHULONG_INFERENCE_ONNX=1)
    endif()

    configure_file(cmake/ZhulongAlgoSDKConfig.cmake.in
                   "${CMAKE_CURRENT_BINARY_DIR}/ZhulongAlgoSDKConfig.cmake" @ONLY)
    write_basic_package_version_file("${CMAKE_CURRENT_BINARY_DIR}/ZhulongAlgoSDKConfigVersion.cmake"
        VERSION ${ZHULONG_SDK_VERSION}
        COMPATIBILITY SameMajorVersion)
    file(WRITE "${CMAKE_CURRENT_BINARY_DIR}/VERSION"
         "sdk_version=${ZHULONG_SDK_VERSION}\napi_version=${ZHULONG_SDK_API_VERSION}\nprotocol=${ZHULONG_SDK_PROTOCOL}\nbackend=${ZHULONG_SDK_BACKEND}\n")

    install(TARGETS ZhulongAlgoRuntimeStatic ARCHIVE DESTINATION lib)
    install(DIRECTORY sdk/include/ DESTINATION include)
    # 包作者自查工具与说明：协议属于 SDK 公开面，工具必须随 SDK 走，
    # 不能只放在仓库的 native/tests 里（那样第三方拿不到，仓库又多一处协议知识）。
    install(PROGRAMS sdk/tools/algo_probe.py sdk/tools/new_package.py DESTINATION tools)
    install(FILES sdk/README.md DESTINATION .)
    # dev/ = 开发期资产（自制自测用的 stb），不是产品依赖
    install(DIRECTORY sdk/tests/third_party/stb/ DESTINATION dev/stb)
    install(FILES
        "${CMAKE_CURRENT_BINARY_DIR}/ZhulongAlgoSDKConfig.cmake"
        "${CMAKE_CURRENT_BINARY_DIR}/ZhulongAlgoSDKConfigVersion.cmake"
        DESTINATION lib/cmake/ZhulongAlgoSDK)
    install(FILES "${CMAKE_CURRENT_BINARY_DIR}/VERSION" sdk/abi.json DESTINATION .)
endif()
# ==============================================================================
# ⚠️ 源文件只在 OBJECT 库里编译一次：宿主归档（ZhulongLog）与 SDK 归档
#    （libzhulong_algo_sdk.a）都从同一批对象构建，避免同一 TU 两份定义。
add_library(ZhulongLogObjects OBJECT src/common/log.cpp)
target_compile_features(ZhulongLogObjects PRIVATE cxx_std_17)
target_include_directories(ZhulongLogObjects PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
set_target_properties(ZhulongLogObjects PROPERTIES POSITION_INDEPENDENT_CODE ON)

add_library(ZhulongLog STATIC $<TARGET_OBJECTS:ZhulongLogObjects>)
target_compile_features(ZhulongLog PUBLIC cxx_std_17)
target_include_directories(ZhulongLog PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/src)
target_link_libraries(ZhulongLog PUBLIC Threads::Threads)
set_target_properties(ZhulongLog PROPERTIES POSITION_INDEPENDENT_CODE ON)

# ⚠️ IPC 源同样先编进 OBJECT 库：宿主归档（ZhulongAlgoIpc）与 SDK 归档
#    （libzhulong_algo_sdk.a）共享同一批对象，一个 TU 只编译一次。
add_library(ZhulongAlgoIpcObjects OBJECT
    src/algorithm/ipc/shm_pool.cpp
    src/algorithm/ipc/unix_socket.cpp
    src/algorithm/ipc/broker_resource_lock.cpp
    src/algorithm/ipc/frame_broker.cpp
)
target_compile_features(ZhulongAlgoIpcObjects PRIVATE cxx_std_17)
target_include_directories(ZhulongAlgoIpcObjects PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/sdk/include ${CMAKE_CURRENT_SOURCE_DIR}/include ${CMAKE_CURRENT_SOURCE_DIR}/src
)
target_compile_definitions(ZhulongAlgoIpcObjects PRIVATE ZHULONG_IPC_LOGGING=1)
set_target_properties(ZhulongAlgoIpcObjects PROPERTIES POSITION_INDEPENDENT_CODE ON)

# ⚠️ dmabuf_allocator.cpp 的唯一归属是 ZhulongPlatformObjects（见 4.1）。这里用
#    $<TARGET_OBJECTS:…> 把同一批对象**归档**进 libZhulongAlgoIpc.a，而不是再编一遍：
#    · 宿主 CGO 链接行是写死的 engine.a + ipc.a + log.a（native/scripts/build.py），
#      DmaBufAllocator 符号必须真的落在某个归档里，不能靠 OBJECT 库的传递依赖；
#    · 同一构建树里该翻译单元只编译一次，DmaBufAllocator::instance() 只有一个定义。
add_library(ZhulongAlgoIpc STATIC
    $<TARGET_OBJECTS:ZhulongPlatformObjects>
    $<TARGET_OBJECTS:ZhulongAlgoIpcObjects>
)
target_compile_features(ZhulongAlgoIpc PUBLIC cxx_std_17)
target_include_directories(ZhulongAlgoIpc
    PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/sdk/include ${CMAKE_CURRENT_SOURCE_DIR}/include ${CMAKE_CURRENT_SOURCE_DIR}/src
)
target_link_libraries(ZhulongAlgoIpc PUBLIC ZhulongLog Threads::Threads)
target_compile_definitions(ZhulongAlgoIpc PRIVATE ZHULONG_IPC_LOGGING=1)
if(UNIX AND NOT APPLE)
    target_link_libraries(ZhulongAlgoIpc PUBLIC rt)
endif()
set_target_properties(ZhulongAlgoIpc PROPERTIES POSITION_INDEPENDENT_CODE ON)

# ==============================================================================
#  4.2 Android/iOS 无关：算法包 SDK 骨架（libzhulong_algo_sdk.a）
# ==============================================================================
# 算法包可执行文件 = 包自己的算法对象 + 本归档。源集合（design.md §4.4）：
#   algo_main.cpp（broker 循环 / 验包 / 结果序列化）
#   + IPC 对象（unix_socket / shm_pool）与 log 对象
# ⚠️ 这些对象与宿主归档（ZhulongAlgoIpc / ZhulongLog）是**同一批 .o**，不是再编一份：
#    同一构建树一个 TU 只编译一次，也不会有两份静态状态。
# ⚠️ 同一个最终二进制里不要同时链接 libzhulong_algo_sdk.a 与 libZhulongAlgoIpc.a：
#    两者都含这批对象，`--whole-archive` 会变成重复定义。宿主侧用 AlgoIpc，
#    包侧用 AlgoSDK。
add_library(ZhulongAlgoSdkObjects OBJECT
    sdk/src/algo_main.cpp
)
target_compile_features(ZhulongAlgoSdkObjects PRIVATE cxx_std_17)
target_include_directories(ZhulongAlgoSdkObjects PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/sdk/include
    ${CMAKE_CURRENT_SOURCE_DIR}/sdk/src
    ${CMAKE_CURRENT_SOURCE_DIR}/src
)
target_compile_definitions(ZhulongAlgoSdkObjects PRIVATE ${ZHULONG_ALGO_RUNTIME_PUBLIC_DEFS})
target_compile_options(ZhulongAlgoSdkObjects PRIVATE
    $<$<CXX_COMPILER_ID:AppleClang,Clang,GNU>:-Wall;-Wextra;-Wpedantic>
)
set_target_properties(ZhulongAlgoSdkObjects PROPERTIES POSITION_INDEPENDENT_CODE ON)

add_library(ZhulongAlgoSdkStatic STATIC
    $<TARGET_OBJECTS:ZhulongAlgoSdkObjects>
    $<TARGET_OBJECTS:ZhulongAlgoIpcObjects>
    $<TARGET_OBJECTS:ZhulongLogObjects>
)
set_target_properties(ZhulongAlgoSdkStatic PROPERTIES OUTPUT_NAME zhulong_algo_sdk)
target_compile_features(ZhulongAlgoSdkStatic PUBLIC cxx_std_17)
target_include_directories(ZhulongAlgoSdkStatic PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/sdk/include)
# 骨架实现（socket / shm / 线程）需要的系统库，与 ZhulongAlgoIpc 一致：
# 少了它们，交叉链接会报 `libpthread.so.0: DSO missing from command line`。
target_link_libraries(ZhulongAlgoSdkStatic PUBLIC Threads::Threads)
if(UNIX AND NOT APPLE)
    target_link_libraries(ZhulongAlgoSdkStatic PUBLIC rt)
endif()
# ⚠️ 不在这里链接运行库：骨架对运行库的引用由**消费端**解析（包可执行文件链接
#    Zhulong::AlgoSDK + Zhulong::AlgoRuntime，顺序由 SDK 配置保证）。
#    原因：宿主 runner 在迁移期仍要 dlopen 插件 .so，那个 .so 的 NEEDED 是
#    libZhulongAlgoRuntime.so；如果 runner 自己链了静态运行库、又不再加载 .so，
#    dlopen 会以「libZhulongAlgoRuntime.so: cannot open shared object」失败。

# 安装规则必须写在本目标定义之后（CMake 要求 install(TARGETS) 的目标已存在）；
# test_stub 夹具构建不安装任何 SDK 产物（夹具产物不可交付）。
if(NOT ZHULONG_INFERENCE_BACKEND STREQUAL "test_stub")
    install(TARGETS ZhulongAlgoSdkStatic ARCHIVE DESTINATION lib)
endif()

# ==============================================================================
#  4.4 包可执行文件由包自己的工程构建（不再有 .so 插件目标）
# ==============================================================================
# 交付形态是「包 = 自带算法类的可执行文件」，由 `packages/<id>/<target>/CMakeLists.txt`
# 经 SDK 前缀独立构建（`make -C packages/... ` 或 build_package.py）。
# 宿主侧只保留一个**测试用**的包形态夹具：`zhulong_test_algo_consumer`，
# 它让跨进程 FrameBroker 回归仍然验证生产形态而不是 dlopen。
# 曾经在这里的 MockDetector / MockClassifier / MockPersonStructure / 两个算法插件
# SHARED 目标已随 dlopen 通道一起删除：没有装载者，就没有「导出表白名单」这回事。

# ==============================================================================
#                      5. 单元测试与集成测试构建（BUILD_TESTING）
# ==============================================================================
if(BUILD_TESTING)
    # Bounded native logging core is shared by the host engine and standalone runner,
    # but intentionally not linked into ZhulongAlgoRuntime.
    add_executable(ZhulongNativeLogTests tests/native_log_test.cpp)
    target_compile_features(ZhulongNativeLogTests PRIVATE cxx_std_17)
    target_include_directories(ZhulongNativeLogTests PRIVATE src)
    target_link_libraries(ZhulongNativeLogTests PRIVATE ZhulongLog Threads::Threads)
    add_test(NAME ZhulongNativeLog COMMAND ZhulongNativeLogTests)
    set_tests_properties(ZhulongNativeLog PROPERTIES TIMEOUT 10)

    # C++ 引擎生命周期基础测试
    add_executable(ZhulongEngineTests tests/engine_test.cpp)
    target_link_libraries(ZhulongEngineTests PRIVATE ZhulongEngine)
    add_test(NAME ZhulongEngineLifecycle COMMAND ZhulongEngineTests)

    # 纯 C11 兼容性与 ABI 防御边界测试
    add_executable(ZhulongCAbiTests tests/engine_abi_test.c)
    target_compile_features(ZhulongCAbiTests PRIVATE c_std_11)
    target_link_libraries(ZhulongCAbiTests PRIVATE ZhulongEngine)
    set_target_properties(ZhulongCAbiTests PROPERTIES LINKER_LANGUAGE CXX)
    add_test(NAME ZhulongCAbiLifecycle COMMAND ZhulongCAbiTests)

    # 插件纯 C ABI 契约测试
    add_executable(ZhulongPluginCAbiTests tests/plugin_c_abi_test.c)
    target_compile_features(ZhulongPluginCAbiTests PRIVATE c_std_11)
    target_include_directories(ZhulongPluginCAbiTests PRIVATE sdk/include)
    add_test(NAME ZhulongPluginCAbi COMMAND ZhulongPluginCAbiTests)
    set_tests_properties(ZhulongPluginCAbi PROPERTIES TIMEOUT 10)

    # DMA-BUF 物理分配器单元测试
    add_executable(ZhulongDmaBufAllocatorTests tests/dmabuf_allocator_test.cpp)
    target_compile_features(ZhulongDmaBufAllocatorTests PRIVATE cxx_std_17)
    target_include_directories(ZhulongDmaBufAllocatorTests PRIVATE src)
    target_link_libraries(ZhulongDmaBufAllocatorTests PRIVATE ZhulongAlgoIpc)
    add_test(NAME ZhulongDmaBufAllocator COMMAND ZhulongDmaBufAllocatorTests)
    set_tests_properties(ZhulongDmaBufAllocator PROPERTIES TIMEOUT 10)

    # IPC 套接字 SCM_RIGHTS 描述符透传单元测试
    add_executable(ZhulongIpcSocketFdTests tests/ipc_socket_fd_test.cpp)
    target_compile_features(ZhulongIpcSocketFdTests PRIVATE cxx_std_17)
    target_include_directories(ZhulongIpcSocketFdTests PRIVATE sdk/include include src)
    target_link_libraries(ZhulongIpcSocketFdTests PRIVATE ZhulongAlgoIpc)
    add_test(NAME ZhulongIpcSocketFd COMMAND ZhulongIpcSocketFdTests)
    set_tests_properties(ZhulongIpcSocketFd PROPERTIES TIMEOUT 10)

    # 算法公共运行库与 HAL 单元测试
    add_executable(ZhulongAlgoRuntimeTests tests/algo_runtime_test.cpp)
    target_link_libraries(ZhulongAlgoRuntimeTests PRIVATE ZhulongAlgoRuntime)
    add_test(NAME ZhulongAlgoRuntime COMMAND ZhulongAlgoRuntimeTests)
    set_tests_properties(ZhulongAlgoRuntime PROPERTIES TIMEOUT 10)

    # CPU 视觉前处理后端回归测试。
    # ⚠️ 需显式把 src 加入搜索路径（直接实例化 CpuVisionBackend，属白盒测试）。
    #    补上 rga_vision_test 之外的空缺：那个 target 只在 ZHULONG_VISION_BACKEND=rga 时构建。
    add_executable(ZhulongCpuVisionTests tests/cpu_vision_backend_test.cpp)
    target_compile_features(ZhulongCpuVisionTests PRIVATE cxx_std_17)
    target_include_directories(ZhulongCpuVisionTests PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
    target_link_libraries(ZhulongCpuVisionTests PRIVATE ZhulongAlgoRuntime)
    add_test(NAME ZhulongCpuVision COMMAND ZhulongCpuVisionTests)
    set_tests_properties(ZhulongCpuVision PROPERTIES TIMEOUT 10)

    # FrameBroker 跨进程共享内存与租约集成测试
    add_executable(ZhulongFrameBrokerTests tests/frame_broker_test.cpp)
    target_compile_features(ZhulongFrameBrokerTests PRIVATE cxx_std_17)
    target_include_directories(ZhulongFrameBrokerTests PRIVATE sdk/include include src)
    target_link_libraries(ZhulongFrameBrokerTests PRIVATE ZhulongAlgoIpc)
    # 子进程用「包形态」的桩算法可执行文件（自带算法类），不再 dlopen 插件
    add_executable(zhulong_test_algo_consumer tests/fixtures/stub_algo_consumer.cpp)
    target_compile_features(zhulong_test_algo_consumer PRIVATE cxx_std_17)
    target_link_libraries(zhulong_test_algo_consumer PRIVATE ZhulongAlgoSdkStatic ZhulongAlgoRuntime ${CMAKE_DL_LIBS})
    add_test(NAME ZhulongFrameBroker COMMAND ZhulongFrameBrokerTests $<TARGET_FILE:zhulong_test_algo_consumer>)
    set_tests_properties(ZhulongFrameBroker PROPERTIES TIMEOUT 45)

    add_executable(ZhulongAlgoJsonTests tests/algo_json_test.cpp)
    target_compile_features(ZhulongAlgoJsonTests PRIVATE cxx_std_17)
    target_include_directories(ZhulongAlgoJsonTests PRIVATE sdk/src/internal)

    add_executable(ZhulongGallerySyncTests tests/gallery_sync_test.cpp)
    target_compile_features(ZhulongGallerySyncTests PRIVATE cxx_std_17)
    target_include_directories(ZhulongGallerySyncTests PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/sdk/include ${CMAKE_CURRENT_SOURCE_DIR}/sdk/src)
    target_link_libraries(ZhulongGallerySyncTests PRIVATE ZhulongAlgoRuntime Threads::Threads)
    add_test(NAME ZhulongGallerySync COMMAND ZhulongGallerySyncTests)
    set_tests_properties(ZhulongGallerySync PROPERTIES TIMEOUT 10)

    # 夹具（test_stub）构建没有真实推理能力：这里只注册不依赖真实模型的断言，
    # 模型相关断言在源码里用 ZHULONG_INFERENCE_TEST_STUB 编译期排除（不再运行期 SKIP）。
    if(ZHULONG_INFERENCE_BACKEND STREQUAL "test_stub" OR ZHULONG_INFERENCE_BACKEND STREQUAL "onnx")
        add_executable(ZhulongFaceRecognitionPluginTests
            tests/face_recognition_plugin_test.cpp
            ${ZHULONG_FACE_IMPL_DIR}/src/detection/detector.cpp
            ${ZHULONG_FACE_IMPL_DIR}/src/alignment/aligner.cpp
            ${ZHULONG_FACE_IMPL_DIR}/src/feature/extractor.cpp
            ${ZHULONG_FACE_IMPL_DIR}/src/matching/matcher.cpp
            ${ZHULONG_FACE_IMPL_DIR}/src/pipeline/face_recognition.cpp
        )
        target_compile_features(ZhulongFaceRecognitionPluginTests PRIVATE cxx_std_17)
        target_include_directories(ZhulongFaceRecognitionPluginTests PRIVATE
            ${CMAKE_CURRENT_SOURCE_DIR}/sdk/include
            ${CMAKE_CURRENT_SOURCE_DIR}/src
            ${ZHULONG_FACE_IMPL_DIR}/src
        )
        target_link_libraries(ZhulongFaceRecognitionPluginTests PRIVATE ZhulongAlgoRuntime ${CMAKE_DL_LIBS} Threads::Threads)
        add_test(NAME ZhulongFaceRecognitionPlugin COMMAND ZhulongFaceRecognitionPluginTests)
        set_tests_properties(ZhulongFaceRecognitionPlugin PROPERTIES TIMEOUT 10)
    endif()



    # 人脸几何对齐 + 1:N 比对自测。
    #
    # ⚠️ 源文件在**包内**（packages/face_recognition/<target>/tests/），这里只把它编进
    #    ctest 以便 `make check` 覆盖：包内 `make unit-test` 与这里编译的是同一份文件，
    #    不复制副本。归属理由见 .trellis/spec/native/algo-plugin-contract.md §5.5。
    #    该测试不加载模型、不需要会话，因此两种推理后端档位都注册。
    add_executable(ZhulongFaceGeometryTests
        ${ZHULONG_FACE_IMPL_DIR}/tests/aligner_matcher_test.cpp
        ${ZHULONG_FACE_IMPL_DIR}/src/alignment/aligner.cpp
        ${ZHULONG_FACE_IMPL_DIR}/src/matching/matcher.cpp
    )
    target_compile_features(ZhulongFaceGeometryTests PRIVATE cxx_std_17)
    target_include_directories(ZhulongFaceGeometryTests PRIVATE
        ${CMAKE_CURRENT_SOURCE_DIR}/sdk/include
        ${ZHULONG_FACE_IMPL_DIR}/src
    )
    target_link_libraries(ZhulongFaceGeometryTests PRIVATE ZhulongAlgoRuntime Threads::Threads)
    add_test(NAME ZhulongFaceGeometry COMMAND ZhulongFaceGeometryTests)
    set_tests_properties(ZhulongFaceGeometry PROPERTIES TIMEOUT 10)

    if(ZHULONG_INFERENCE_BACKEND STREQUAL "onnx")
        add_executable(ZhulongFaceRecognitionOnnxTests
            tests/face_recognition_onnx_test.cpp
            ${ZHULONG_FACE_IMPL_DIR}/src/detection/detector.cpp
            ${ZHULONG_FACE_IMPL_DIR}/src/alignment/aligner.cpp
            ${ZHULONG_FACE_IMPL_DIR}/src/feature/extractor.cpp
            ${ZHULONG_FACE_IMPL_DIR}/src/matching/matcher.cpp
            ${ZHULONG_FACE_IMPL_DIR}/src/pipeline/face_recognition.cpp
        )
        target_compile_features(ZhulongFaceRecognitionOnnxTests PRIVATE cxx_std_17)
        target_include_directories(ZhulongFaceRecognitionOnnxTests PRIVATE
            ${CMAKE_CURRENT_SOURCE_DIR}/sdk/include
            ${CMAKE_CURRENT_SOURCE_DIR}/src
            ${ZHULONG_FACE_IMPL_DIR}/src
        )
        target_link_libraries(ZhulongFaceRecognitionOnnxTests PRIVATE ZhulongAlgoRuntime ${CMAKE_DL_LIBS} Threads::Threads)
        add_test(NAME ZhulongFaceRecognitionOnnx COMMAND ZhulongFaceRecognitionOnnxTests)
        set_tests_properties(ZhulongFaceRecognitionOnnx PROPERTIES TIMEOUT 20)
    endif()

    # 运动区域检测算法单元测试（纯合成帧与宏块聚类）
    add_executable(ZhulongMotionDetectorTests tests/motion_detector_test.cpp)
    target_compile_features(ZhulongMotionDetectorTests PRIVATE cxx_std_17)
    target_include_directories(ZhulongMotionDetectorTests PRIVATE src include)
    target_link_libraries(ZhulongMotionDetectorTests PRIVATE ZhulongEngine)
    add_test(NAME ZhulongMotionDetector COMMAND ZhulongMotionDetectorTests)

    # Runner 骨架帧运动门禁单元测试（报文前缀兼容 + 短路判定，纯头文件无需链接）
    add_executable(ZhulongMotionGateTests tests/motion_gate_test.cpp)
    target_compile_features(ZhulongMotionGateTests PRIVATE cxx_std_17)
    target_include_directories(ZhulongMotionGateTests PRIVATE sdk/include sdk/src src)
    add_test(NAME ZhulongMotionGate COMMAND ZhulongMotionGateTests)

    # Runner 骨架单路 Worker 无法失败边界（异常 → kExitWorkerCrashed，纯头文件无需链接）
    add_executable(ZhulongWorkerGuardTests tests/worker_guard_test.cpp)
    target_compile_features(ZhulongWorkerGuardTests PRIVATE cxx_std_17)
    target_include_directories(ZhulongWorkerGuardTests PRIVATE sdk/include sdk/src src)
    add_test(NAME ZhulongWorkerGuard COMMAND ZhulongWorkerGuardTests)

    # Runner 骨架无进展看门狗判定契约（计时窗口 + 阈值解析，纯头文件无需链接）
    add_executable(ZhulongStallWatchTests tests/stall_watch_test.cpp)
    target_compile_features(ZhulongStallWatchTests PRIVATE cxx_std_17)
    target_include_directories(ZhulongStallWatchTests PRIVATE sdk/include sdk/src src)
    add_test(NAME ZhulongStallWatch COMMAND ZhulongStallWatchTests)

    # ⚠️ 这些测试完全依赖 assert()，必须显式 -UNDEBUG：否则 RelWithDebInfo 构建下
    #    NDEBUG 会把全部断言编译掉，测试会「假绿」——曾经 ZhulongIpcSocketFdTests
    #    就不在本列表中，其断言从未真正执行过。
    set(ASSERT_TEST_TARGETS
        ZhulongAlgoRuntimeTests
        ZhulongCpuVisionTests
        ZhulongAlgoJsonTests
        ZhulongFrameBrokerTests
        ZhulongGallerySyncTests
        ZhulongIpcSocketFdTests
        ZhulongDmaBufAllocatorTests
        ZhulongMotionDetectorTests
        ZhulongMotionGateTests
        ZhulongWorkerGuardTests
        ZhulongStallWatchTests
    )
    if(TARGET ZhulongFaceRecognitionPluginTests)
        list(APPEND ASSERT_TEST_TARGETS ZhulongFaceRecognitionPluginTests)
    endif()
    if(TARGET ZhulongPlateRecognitionPluginTests)
        list(APPEND ASSERT_TEST_TARGETS ZhulongPlateRecognitionPluginTests)
    endif()
    if(TARGET ZhulongFaceRecognitionOnnxTests)
        list(APPEND ASSERT_TEST_TARGETS ZhulongFaceRecognitionOnnxTests)
    endif()
    foreach(TEST_TARGET ${ASSERT_TEST_TARGETS})
        target_compile_options(${TEST_TARGET} PRIVATE $<$<CXX_COMPILER_ID:GNU,Clang,AppleClang>:-UNDEBUG>)
    endforeach()

    # 预览契约整形器单元测试（avcC/hvcC、B 帧时间戳与断流重置）
    add_executable(ZhulongPreviewNormalizerTests
        tests/preview_normalizer_test.cpp
        src/pipeline/preview_normalizer.cpp
    )
    target_compile_features(ZhulongPreviewNormalizerTests PRIVATE cxx_std_17)
    target_include_directories(ZhulongPreviewNormalizerTests PRIVATE src include)
    target_link_libraries(ZhulongPreviewNormalizerTests PRIVATE Threads::Threads)
    add_test(NAME ZhulongPreviewNormalizer COMMAND ZhulongPreviewNormalizerTests)
    set_tests_properties(ZhulongPreviewNormalizer PROPERTIES TIMEOUT 10)

    # RTSP 采集节点单元测试（URL 规范化、选项、排空）
    add_executable(ZhulongCaptureUnitTests tests/capture_unit_test.cpp)
    target_include_directories(ZhulongCaptureUnitTests PRIVATE src)
    target_link_libraries(ZhulongCaptureUnitTests PRIVATE ZhulongEngine)
    add_test(NAME ZhulongCaptureUnit COMMAND ZhulongCaptureUnitTests)

    # RTSP 真实网络回环集成测试（基于 Python RTSP 测试桩）
    add_executable(ZhulongRtspTests tests/rtsp_integration_test.cpp)
    target_link_libraries(ZhulongRtspTests PRIVATE ZhulongEngine)

    # 硬件帧对象与多算法零拷贝生命周期单元测试
    add_executable(ZhulongFrameTests tests/frame_test.cpp)
    target_link_libraries(ZhulongFrameTests PRIVATE ZhulongEngine)
    add_test(NAME ZhulongFrameLifecycle COMMAND ZhulongFrameTests)

    # 解码节点与有界队列单元测试
    add_executable(ZhulongDecoderTests tests/decoder_test.cpp)
    target_include_directories(ZhulongDecoderTests PRIVATE src)
    target_link_libraries(ZhulongDecoderTests PRIVATE ZhulongEngine)
    add_test(NAME ZhulongDecoder COMMAND ZhulongDecoderTests)

    # 抓拍 JPEG 编码链路单元测试（离线合成 NV12 / YUV420P，不需要真实摄像机）
    add_executable(ZhulongSnapshotTests tests/snapshot_test.cpp)
    target_include_directories(ZhulongSnapshotTests PRIVATE src)
    target_link_libraries(ZhulongSnapshotTests PRIVATE ZhulongEngine)
    add_test(NAME ZhulongSnapshot COMMAND ZhulongSnapshotTests)

    # JPEG 编码后端选择与硬件（VEPU）编码自检。
    # ⚠️ 默认强制 software 后端：本机（x86）没有 /dev/mpp_service，若允许 auto
    #    会让测试结果依赖构建机环境；硬件路径由板端用 ZHULONG_JPEG_BACKEND=mpp 覆盖。
    add_executable(ZhulongJpegEncoderTests tests/jpeg_encoder_test.cpp)
    target_include_directories(ZhulongJpegEncoderTests PRIVATE src)
    target_link_libraries(ZhulongJpegEncoderTests PRIVATE ZhulongEngine)
    add_test(NAME ZhulongJpegEncoder COMMAND ZhulongJpegEncoderTests)
    set_tests_properties(ZhulongJpegEncoder PROPERTIES
        ENVIRONMENT "ZHULONG_JPEG_BACKEND=cpu" TIMEOUT 120)

    # 底库登记用的特征提取改由包可执行文件的 --extract-feature 通道完成
    # （Go 侧 internal/faceextract）。宿主不再 dlopen 任何算法 .so，因此原
    # FacePluginLoader 的 TTL/惰性卸载黑盒测试随实现一起下线；通道行为由
    # packages/face_recognition 的 devtest 与 Go 侧单测覆盖。

    if(NOT CMAKE_CROSSCOMPILING)
        find_package(Python3 REQUIRED COMPONENTS Interpreter)
        add_test(NAME ZhulongAlgoJsonRoundTrip COMMAND ${Python3_EXECUTABLE}
            ${CMAKE_CURRENT_SOURCE_DIR}/tests/algo_json_roundtrip_test.py $<TARGET_FILE:ZhulongAlgoJsonTests>)
        add_test(NAME ZhulongRtspIntegration COMMAND ${Python3_EXECUTABLE}
            ${CMAKE_CURRENT_SOURCE_DIR}/tests/rtsp_fixture.py $<TARGET_FILE:ZhulongRtspTests>)
        set_tests_properties(ZhulongRtspIntegration PROPERTIES TIMEOUT 90)

        # 构建契约测试
        add_test(NAME ZhulongBuildContract COMMAND ${Python3_EXECUTABLE}
            ${CMAKE_CURRENT_SOURCE_DIR}/tests/build_contract_test.py ${FFMPEG_ROOT})
        set_tests_properties(ZhulongBuildContract PROPERTIES TIMEOUT 90)
    endif()

    set_tests_properties(ZhulongEngineLifecycle ZhulongCAbiLifecycle ZhulongCaptureUnit ZhulongFrameLifecycle ZhulongDecoder PROPERTIES TIMEOUT 20)

    # RKNN 后端测试：仅在推理后端为 rknn 时构建。
    # 交叉时只编译不注册 CTest（板端手动执行）；host/rknn 时注册。
    if(ZHULONG_INFERENCE_BACKEND STREQUAL "rknn")
        add_executable(ZhulongRknnTests tests/rknn_backend_test.cpp)
        target_compile_features(ZhulongRknnTests PRIVATE cxx_std_17)
        target_include_directories(ZhulongRknnTests PRIVATE src ${ZHULONG_RKNN_ROOT}/include)
        target_link_libraries(ZhulongRknnTests PRIVATE ZhulongAlgoRuntime Threads::Threads)
        if(NOT CMAKE_CROSSCOMPILING)
            add_test(NAME ZhulongRknnBackend COMMAND ZhulongRknnTests)
            set_tests_properties(ZhulongRknnBackend PROPERTIES TIMEOUT 120)
        endif()
    endif()

    # ONNX Runtime 后端测试：仅在推理后端为 onnx 时构建。
    if(ZHULONG_INFERENCE_BACKEND STREQUAL "onnx")
        add_executable(ZhulongOnnxTests tests/onnx_backend_test.cpp)
        target_compile_features(ZhulongOnnxTests PRIVATE cxx_std_17)
        target_include_directories(ZhulongOnnxTests SYSTEM PRIVATE ${ZHULONG_ONNX_ROOT}/include)
        target_include_directories(ZhulongOnnxTests PRIVATE src)
        target_compile_definitions(ZhulongOnnxTests PRIVATE
            ZHULONG_ONNX_TEST_MODEL_PATH="${CMAKE_CURRENT_SOURCE_DIR}/tests/fixtures/onnx/add_const.onnx"
        )
        target_compile_options(ZhulongOnnxTests PRIVATE $<$<CXX_COMPILER_ID:GNU,Clang,AppleClang>:-UNDEBUG>)
        target_link_libraries(ZhulongOnnxTests PRIVATE ZhulongAlgoRuntime ${ZHULONG_ONNX_ROOT}/lib/libonnxruntime.so Threads::Threads)
        if(NOT CMAKE_CROSSCOMPILING)
            add_test(NAME ZhulongOnnxBackend COMMAND ZhulongOnnxTests)
            set_tests_properties(ZhulongOnnxBackend PROPERTIES
                TIMEOUT 60
                ENVIRONMENT "LD_LIBRARY_PATH=${ZHULONG_ONNX_ROOT}/lib:$ENV{LD_LIBRARY_PATH}"
            )
        endif()
    endif()

    # RGA 视觉后端测试：仅在视觉后端为 rga 时构建（同 RKNN：交叉只编译不注册）。
    if(ZHULONG_VISION_BACKEND STREQUAL "rga")
        add_executable(ZhulongRgaTests tests/rga_vision_test.cpp)
        target_compile_features(ZhulongRgaTests PRIVATE cxx_std_17)
        target_include_directories(ZhulongRgaTests PRIVATE src ${ZHULONG_RGA_ROOT}/include)
        target_link_libraries(ZhulongRgaTests PRIVATE ZhulongAlgoRuntime Threads::Threads)
        if(NOT CMAKE_CROSSCOMPILING)
            add_test(NAME ZhulongRgaBackend COMMAND ZhulongRgaTests)
            set_tests_properties(ZhulongRgaBackend PROPERTIES TIMEOUT 120)
        endif()
    endif()
endif()
