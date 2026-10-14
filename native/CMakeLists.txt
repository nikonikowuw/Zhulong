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
#                      4.1 构建公共算法运行库 (libzhulong_algo_runtime.so)
# ==============================================================================
add_library(ZhulongAlgoRuntime SHARED
    src/algorithm/runtime/cpu_inference_backend.cpp
    src/algorithm/runtime/cpu_vision_backend.cpp
    src/algorithm/runtime/runtime_factory.cpp
)
target_compile_features(ZhulongAlgoRuntime PUBLIC cxx_std_17)
target_include_directories(ZhulongAlgoRuntime
    PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/sdk/include ${CMAKE_CURRENT_SOURCE_DIR}/src
)
target_compile_options(ZhulongAlgoRuntime PRIVATE
    $<$<CXX_COMPILER_ID:AppleClang,Clang,GNU>:-Wall;-Wextra;-Wpedantic>
)

# ==============================================================================
#                      4.2 构建跨进程 IPC 与 FrameBroker 库 (ZhulongAlgoIpc)
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
target_link_libraries(zhulong_algo_runner PRIVATE ZhulongAlgoIpc ${CMAKE_DL_LIBS})

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

if(CMAKE_SYSTEM_NAME STREQUAL "Linux")
    set(PLUGIN_EXPORT_MAP "${CMAKE_CURRENT_SOURCE_DIR}/cmake/plugin-exports.map")
    target_link_options(MockDetector PRIVATE "LINKER:--version-script,${PLUGIN_EXPORT_MAP}")
    target_link_options(MockClassifier PRIVATE "LINKER:--version-script,${PLUGIN_EXPORT_MAP}")
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
    add_test(NAME ZhulongAlgoRunner COMMAND ZhulongAlgoRunnerTests $<TARGET_FILE:MockDetector> $<TARGET_FILE:MockClassifier>)
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

    foreach(TEST_TARGET ZhulongAlgoRuntimeTests ZhulongAlgoRunnerTests ZhulongAlgoJsonTests ZhulongFrameBrokerTests)
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
                $<TARGET_FILE:MockDetector> $<TARGET_FILE:MockClassifier>)
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
endif()
