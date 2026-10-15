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
    src/pipeline/engine.cpp
    src/pipeline/stream.cpp
    src/pipeline/subscription.cpp
    src/pipeline/frame_publisher.cpp
    src/nodes/capture/rtsp_input.cpp
    src/nodes/decode/ffmpeg_decoder.cpp
)
target_compile_features(ZhulongEngine PUBLIC cxx_std_17)
target_include_directories(ZhulongEngine
    PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/include
    PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src ${FFMPEG_ROOT}/include
)
target_link_libraries(ZhulongEngine PUBLIC ZhulongAlgoIpc ${FFMPEG_LIBRARIES} Threads::Threads ${FFMPEG_SYSTEM_LIBRARIES})
set_target_properties(ZhulongEngine PROPERTIES POSITION_INDEPENDENT_CODE ON)
target_compile_options(ZhulongEngine PRIVATE
    $<$<CXX_COMPILER_ID:AppleClang,Clang,GNU>:-Wall;-Wextra;-Wpedantic>
)

# ==============================================================================
#          4.0 后端选择：推理 / 视觉前处理（编译期 profile 隔离）
# ==============================================================================
# 推理后端与视觉前处理后端是两个正交开关：可「RKNN 推理 + CPU 前处理」，也可
# 「CPU 推理 + RGA 前处理」。默认均为 cpu，保持既有 CPU 路径零回归。
# 视觉后端 rga 由兄弟任务 10-13-algo-rga-vision-backend 实现，本任务只声明变量。
set(ZHULONG_INFERENCE_BACKEND "cpu" CACHE STRING "算法运行库推理后端: cpu | rknn")
set_property(CACHE ZHULONG_INFERENCE_BACKEND PROPERTY STRINGS cpu rknn)
set(ZHULONG_VISION_BACKEND "cpu" CACHE STRING "算法运行库视觉前处理后端: cpu | rga")
set_property(CACHE ZHULONG_VISION_BACKEND PROPERTY STRINGS cpu rga)
set(ZHULONG_RKNN_ROOT "" CACHE PATH "RKNN 前缀（include/rknn_api.h + lib/librknnrt.so）；仅 ZHULONG_INFERENCE_BACKEND=rknn 需要")
set(ZHULONG_RGA_ROOT "" CACHE PATH "RGA 前缀（include/rga/im2d.h + lib/librga.so*）；仅 ZHULONG_VISION_BACKEND=rga 需要")

if(NOT ZHULONG_INFERENCE_BACKEND MATCHES "^(cpu|rknn)$")
    message(FATAL_ERROR "ZHULONG_INFERENCE_BACKEND 取值非法: ${ZHULONG_INFERENCE_BACKEND}（期望 cpu 或 rknn）")
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

# ==============================================================================
#                      4.1 构建公共算法运行库 (libzhulong_algo_runtime.so)
# ==============================================================================
add_library(ZhulongAlgoRuntime SHARED
    src/algorithm/runtime/cpu_inference_backend.cpp
    src/algorithm/runtime/cpu_vision_backend.cpp
    src/algorithm/runtime/gallery_registry.cpp
    src/algorithm/runtime/letterbox_geometry.cpp
    src/algorithm/runtime/runtime_factory.cpp
)
target_compile_features(ZhulongAlgoRuntime PUBLIC cxx_std_17)
target_include_directories(ZhulongAlgoRuntime
    PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/sdk/include ${CMAKE_CURRENT_SOURCE_DIR}/src
)
target_compile_options(ZhulongAlgoRuntime PRIVATE
    $<$<CXX_COMPILER_ID:AppleClang,Clang,GNU>:-Wall;-Wextra;-Wpedantic>
)

# RKNN 推理后端：仅链接进 ZhulongAlgoRuntime（.so），宿主 ZhulongEngine 绝不链接。
# 绝对路径 include（PRIVATE）与绝对文件路径链接，避免污染 sysroot 与 CMAKE_FIND_ROOT_PATH。
if(ZHULONG_INFERENCE_BACKEND STREQUAL "rknn")
    target_sources(ZhulongAlgoRuntime PRIVATE src/backends/rknn/rknn_inference_backend.cpp)
    target_include_directories(ZhulongAlgoRuntime PRIVATE ${ZHULONG_RKNN_ROOT}/include)
    target_link_libraries(ZhulongAlgoRuntime PRIVATE ${ZHULONG_RKNN_ROOT}/lib/librknnrt.so Threads::Threads)
    target_compile_definitions(ZhulongAlgoRuntime PRIVATE ZHULONG_INFERENCE_RKNN=1)
    # ⚠️ 产物卫生（AC6）：CMake 会为「构建树之外的链接库」自动加 BUILD_RPATH，把开发机
    # 绝对路径烧进 .so（实测为 /home/niko/dev/Go/Zhulong/native/vendor/rknpu2/lib）。
    # 该 .so 要 scp 到 RK3568，不应携带宿主机路径。清空 BUILD_RPATH；
    # 下游目标的符号解析由上面的 add_link_options(-Wl,-rpath-link,…) 保证。
    set_target_properties(ZhulongAlgoRuntime PROPERTIES SKIP_BUILD_RPATH TRUE BUILD_RPATH "")
endif()

# RGA 视觉后端：同样只链接进 ZhulongAlgoRuntime（.so），宿主绝不链接。
# ⚠️ letterbox_geometry.cpp 是 CPU/RGA **共用**的几何计算，已在上面基础目标中，不在此重复。
if(ZHULONG_VISION_BACKEND STREQUAL "rga")
    target_sources(ZhulongAlgoRuntime PRIVATE
        src/backends/rga/rga_vision_backend.cpp
    )
    target_include_directories(ZhulongAlgoRuntime PRIVATE ${ZHULONG_RGA_ROOT}/include)
    target_link_libraries(ZhulongAlgoRuntime PRIVATE ${ZHULONG_RGA_ROOT}/lib/librga.so.2 Threads::Threads)
    target_compile_definitions(ZhulongAlgoRuntime PRIVATE ZHULONG_VISION_RGA=1)
    # 产物卫生：清空 BUILD_RPATH，理由同 RKNN 分支。
    set_target_properties(ZhulongAlgoRuntime PROPERTIES SKIP_BUILD_RPATH TRUE BUILD_RPATH "")
endif()
# ==============================================================================
add_library(ZhulongAlgoIpc STATIC
    src/algorithm/ipc/shm_pool.cpp
    src/algorithm/ipc/unix_socket.cpp
    src/algorithm/ipc/frame_broker.cpp
)
target_compile_features(ZhulongAlgoIpc PUBLIC cxx_std_17)
target_include_directories(ZhulongAlgoIpc
    PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/sdk/include ${CMAKE_CURRENT_SOURCE_DIR}/include ${CMAKE_CURRENT_SOURCE_DIR}/src
)
target_link_libraries(ZhulongAlgoIpc PUBLIC Threads::Threads)
if(UNIX AND NOT APPLE)
    target_link_libraries(ZhulongAlgoIpc PUBLIC rt)
endif()
set_target_properties(ZhulongAlgoIpc PROPERTIES POSITION_INDEPENDENT_CODE ON)

# ==============================================================================
#                      4.3 构建通用算法 Runner 进程 (zhulong_algo_runner)
# ==============================================================================
add_executable(zhulong_algo_runner
    src/algorithm/runner/runner.cpp
)
target_compile_features(zhulong_algo_runner PUBLIC cxx_std_17)
target_include_directories(zhulong_algo_runner
    PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/sdk/include ${CMAKE_CURRENT_SOURCE_DIR}/src
)
target_link_libraries(zhulong_algo_runner PRIVATE ZhulongAlgoIpc ZhulongAlgoRuntime ${CMAKE_DL_LIBS})

# ==============================================================================
#                      4.4 构建示例脱机插件 (.so)
# ==============================================================================
add_library(MockDetector SHARED tests/plugins/mock_detector/mock_detector.cpp)
target_compile_features(MockDetector PUBLIC cxx_std_17)
target_compile_definitions(MockDetector PRIVATE ZHULONG_PLUGIN_BUILD)
target_include_directories(MockDetector PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/sdk/include ${CMAKE_CURRENT_SOURCE_DIR}/src)
target_link_libraries(MockDetector PRIVATE ZhulongAlgoRuntime)
set_target_properties(MockDetector PROPERTIES CXX_VISIBILITY_PRESET hidden VISIBILITY_INLINES_HIDDEN YES)

add_library(MockClassifier SHARED tests/plugins/mock_classifier/mock_classifier.cpp)
target_compile_features(MockClassifier PUBLIC cxx_std_17)
target_compile_definitions(MockClassifier PRIVATE ZHULONG_PLUGIN_BUILD)
target_include_directories(MockClassifier PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/sdk/include ${CMAKE_CURRENT_SOURCE_DIR}/src)
target_link_libraries(MockClassifier PRIVATE ZhulongAlgoRuntime)
set_target_properties(MockClassifier PROPERTIES CXX_VISIBILITY_PRESET hidden VISIBILITY_INLINES_HIDDEN YES)

add_library(MockPersonStructure SHARED tests/plugins/mock_person_structure/mock_person_structure.cpp)
target_compile_features(MockPersonStructure PUBLIC cxx_std_17)
target_compile_definitions(MockPersonStructure PRIVATE ZHULONG_PLUGIN_BUILD)
target_include_directories(MockPersonStructure PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/sdk/include ${CMAKE_CURRENT_SOURCE_DIR}/src)
target_link_libraries(MockPersonStructure PRIVATE ZhulongAlgoRuntime)
set_target_properties(MockPersonStructure PROPERTIES CXX_VISIBILITY_PRESET hidden VISIBILITY_INLINES_HIDDEN YES)

# ==============================================================================
#                      4.5 构建人脸识别算法插件 (FaceRecognitionPlugin)
# ==============================================================================
add_library(FaceRecognitionPlugin SHARED
    ${CMAKE_CURRENT_SOURCE_DIR}/../packages/face_recognition/src/face_recognition.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../packages/face_recognition/src/matcher.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../packages/face_recognition/src/aligner.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../packages/face_recognition/src/detector.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../packages/face_recognition/src/extractor.cpp
)
target_compile_features(FaceRecognitionPlugin PUBLIC cxx_std_17)
target_compile_definitions(FaceRecognitionPlugin PRIVATE ZHULONG_PLUGIN_BUILD)
target_include_directories(FaceRecognitionPlugin PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/sdk/include
    ${CMAKE_CURRENT_SOURCE_DIR}/src
    ${CMAKE_CURRENT_SOURCE_DIR}/../packages/face_recognition/src
)
target_link_libraries(FaceRecognitionPlugin PRIVATE ZhulongAlgoRuntime)
set_target_properties(FaceRecognitionPlugin PROPERTIES CXX_VISIBILITY_PRESET hidden VISIBILITY_INLINES_HIDDEN YES)

if(CMAKE_SYSTEM_NAME STREQUAL "Linux")
    set(PLUGIN_EXPORT_MAP "${CMAKE_CURRENT_SOURCE_DIR}/cmake/plugin-exports.map")
    target_link_options(MockDetector PRIVATE "LINKER:--version-script,${PLUGIN_EXPORT_MAP}")
    target_link_options(MockClassifier PRIVATE "LINKER:--version-script,${PLUGIN_EXPORT_MAP}")
    target_link_options(MockPersonStructure PRIVATE "LINKER:--version-script,${PLUGIN_EXPORT_MAP}")
    target_link_options(FaceRecognitionPlugin PRIVATE "LINKER:--version-script,${PLUGIN_EXPORT_MAP}")
endif()

# ==============================================================================
#                      5. 单元测试与集成测试构建（BUILD_TESTING）
# ==============================================================================
if(BUILD_TESTING)
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

    # 算法公共运行库与 HAL 单元测试
    add_executable(ZhulongAlgoRuntimeTests tests/algo_runtime_test.cpp)
    target_link_libraries(ZhulongAlgoRuntimeTests PRIVATE ZhulongAlgoRuntime)
    add_test(NAME ZhulongAlgoRuntime COMMAND ZhulongAlgoRuntimeTests)
    set_tests_properties(ZhulongAlgoRuntime PROPERTIES TIMEOUT 10)

    # 算法 Runner 与示例插件集成测试
    add_executable(ZhulongAlgoRunnerTests tests/algo_runner_test.cpp)
    target_compile_features(ZhulongAlgoRunnerTests PRIVATE cxx_std_17)
    target_include_directories(ZhulongAlgoRunnerTests PRIVATE sdk/include)
    target_link_libraries(ZhulongAlgoRunnerTests PRIVATE ${CMAKE_DL_LIBS} Threads::Threads)
    add_test(NAME ZhulongAlgoRunner COMMAND ZhulongAlgoRunnerTests $<TARGET_FILE:MockDetector> $<TARGET_FILE:MockClassifier> $<TARGET_FILE:MockPersonStructure>)
    set_tests_properties(ZhulongAlgoRunner PROPERTIES TIMEOUT 10)

    # FrameBroker 跨进程共享内存与租约集成测试
    add_executable(ZhulongFrameBrokerTests tests/frame_broker_test.cpp)
    target_compile_features(ZhulongFrameBrokerTests PRIVATE cxx_std_17)
    target_include_directories(ZhulongFrameBrokerTests PRIVATE sdk/include include src)
    target_link_libraries(ZhulongFrameBrokerTests PRIVATE ZhulongAlgoIpc)
    add_test(NAME ZhulongFrameBroker COMMAND ZhulongFrameBrokerTests $<TARGET_FILE:zhulong_algo_runner> $<TARGET_FILE:MockDetector>)
    set_tests_properties(ZhulongFrameBroker PROPERTIES TIMEOUT 15)

    add_executable(ZhulongAlgoJsonTests tests/algo_json_test.cpp)
    target_compile_features(ZhulongAlgoJsonTests PRIVATE cxx_std_17)
    target_include_directories(ZhulongAlgoJsonTests PRIVATE src/algorithm/runner)

    add_executable(ZhulongAlgoRunnerGalleryTests tests/algo_runner_gallery_test.cpp)
    target_compile_features(ZhulongAlgoRunnerGalleryTests PRIVATE cxx_std_17)
    target_include_directories(ZhulongAlgoRunnerGalleryTests PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/sdk/include ${CMAKE_CURRENT_SOURCE_DIR}/src ${CMAKE_CURRENT_SOURCE_DIR}/src/algorithm/runner)
    target_link_libraries(ZhulongAlgoRunnerGalleryTests PRIVATE ZhulongAlgoRuntime Threads::Threads)
    add_test(NAME ZhulongAlgoRunnerGallery COMMAND ZhulongAlgoRunnerGalleryTests)
    set_tests_properties(ZhulongAlgoRunnerGallery PROPERTIES TIMEOUT 10)

    # 人脸识别脱机插件单元测试
    add_executable(ZhulongFaceRecognitionPluginTests
        tests/face_recognition_plugin_test.cpp
        ${CMAKE_CURRENT_SOURCE_DIR}/../packages/face_recognition/src/detector.cpp
        ${CMAKE_CURRENT_SOURCE_DIR}/../packages/face_recognition/src/aligner.cpp
        ${CMAKE_CURRENT_SOURCE_DIR}/../packages/face_recognition/src/extractor.cpp
    )
    target_compile_features(ZhulongFaceRecognitionPluginTests PRIVATE cxx_std_17)
    target_include_directories(ZhulongFaceRecognitionPluginTests PRIVATE
        ${CMAKE_CURRENT_SOURCE_DIR}/sdk/include
        ${CMAKE_CURRENT_SOURCE_DIR}/src
        ${CMAKE_CURRENT_SOURCE_DIR}/../packages/face_recognition/src
    )
    target_link_libraries(ZhulongFaceRecognitionPluginTests PRIVATE ZhulongAlgoRuntime ${CMAKE_DL_LIBS} Threads::Threads)
    add_test(NAME ZhulongFaceRecognitionPlugin COMMAND ZhulongFaceRecognitionPluginTests $<TARGET_FILE:FaceRecognitionPlugin>)
    set_tests_properties(ZhulongFaceRecognitionPlugin PROPERTIES TIMEOUT 10)

    foreach(TEST_TARGET ZhulongAlgoRuntimeTests ZhulongAlgoRunnerTests ZhulongAlgoJsonTests ZhulongFrameBrokerTests ZhulongAlgoRunnerGalleryTests ZhulongFaceRecognitionPluginTests)
        target_compile_options(${TEST_TARGET} PRIVATE $<$<CXX_COMPILER_ID:GNU,Clang,AppleClang>:-UNDEBUG>)
    endforeach()

    # 独立黑盒验包 CLI 工具测试
    add_test(NAME ZhulongAlgoVerifyCli COMMAND zhulong_algo_runner --verify-plugin $<TARGET_FILE:MockDetector> --sample mock_sample.jpg)
    set_tests_properties(ZhulongAlgoVerifyCli PROPERTIES TIMEOUT 10)

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

    # 人脸特征提取单次 C ABI 单元测试
    add_executable(ZhulongFaceFeatureExtractTests tests/face_feature_extract_test.cpp)
    target_link_libraries(ZhulongFaceFeatureExtractTests PRIVATE ZhulongEngine)
    add_test(NAME ZhulongFaceFeatureExtract COMMAND ZhulongFaceFeatureExtractTests)
    set_tests_properties(ZhulongFaceFeatureExtract PROPERTIES TIMEOUT 10)

    if(NOT CMAKE_CROSSCOMPILING)
        find_package(Python3 REQUIRED COMPONENTS Interpreter)
        add_test(NAME ZhulongAlgoJsonRoundTrip COMMAND ${Python3_EXECUTABLE}
            ${CMAKE_CURRENT_SOURCE_DIR}/tests/algo_json_roundtrip_test.py $<TARGET_FILE:ZhulongAlgoJsonTests>)
        add_test(NAME ZhulongAlgoRunnerJson COMMAND ${Python3_EXECUTABLE}
            ${CMAKE_CURRENT_SOURCE_DIR}/tests/algo_runner_json_test.py
            $<TARGET_FILE:zhulong_algo_runner> $<TARGET_FILE:MockDetector>)
        if(CMAKE_SYSTEM_NAME STREQUAL "Linux")
            add_test(NAME ZhulongPluginSymbolVisibility COMMAND ${Python3_EXECUTABLE}
                ${CMAKE_CURRENT_SOURCE_DIR}/tests/check_plugin_symbols.py ${CMAKE_NM}
                $<TARGET_FILE:MockDetector> $<TARGET_FILE:MockClassifier> $<TARGET_FILE:MockPersonStructure>)
        endif()
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
