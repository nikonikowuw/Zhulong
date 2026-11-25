# 安全帽穿戴检测算法包（onnx 档位）
#
# 产出包根可执行文件 `ppe_helmet`（名字必须等于 manifest.id）与两个开发期目标：
# `ppe_helmet_unittest`（纯逻辑，不加载模型）和 `ppe_helmet_devtest`（读 sample.jpg
# 跑完整推理并写 result.jpg）。两者都 EXCLUDE_FROM_ALL 且无安装规则，物理上进不了归档。
#
# ⚠️ 依赖只有 SDK 前缀：`find_package(ZhulongAlgoSDK)`。本文件不得出现任何指向本仓库的
#    相对路径（自查门禁：native/scripts/sdk_clean_check.sh 会 grep 它），否则这个包
#    就不再是可独立开发的工程了。
cmake_minimum_required(VERSION 3.20)
project(ppe_helmet_onnx_dev LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

if(NOT CMAKE_BUILD_TYPE)
    set(CMAKE_BUILD_TYPE RelWithDebInfo CACHE STRING "Build type" FORCE)
endif()

find_package(ZhulongAlgoSDK 1.0 REQUIRED)
find_package(Threads REQUIRED)

# 包版本只认 manifest.json —— 唯一事实源。
# `main.cpp` 通过 `ZHULONG_PPE_PLUGIN_VERSION` 把包版本回显给 `--verify` 与宿主握手，
# 避免清单升版后二进制仍报旧版本触发 `ErrVersionMismatch`。
file(READ "${CMAKE_CURRENT_SOURCE_DIR}/manifest.json" ZHULONG_PPE_MANIFEST_JSON)
string(REGEX MATCH "\"version\"[ \t\r\n]*:[ \t\r\n]*\"([^\"]*)\"" _version_match "${ZHULONG_PPE_MANIFEST_JSON}")
set(ZHULONG_PPE_PLUGIN_VERSION "${CMAKE_MATCH_1}")
if(NOT ZHULONG_PPE_PLUGIN_VERSION)
    message(WARNING "manifest.json 中未解析出 version：包版本回退为 0.0.0")
    set(ZHULONG_PPE_PLUGIN_VERSION "0.0.0")
endif()
message(STATUS "ppe_helmet 包版本（manifest.json）: ${ZHULONG_PPE_PLUGIN_VERSION}")

# 警告是**产品代码**的门禁：仓库 native 侧统一 -Wall -Wextra -Wpedantic，包独立构建时
# 不能因为「没有宿主 CMakeLists 替我加」就默默变成零警告检查。
# （不做 -Werror：第三方 SDK 头的警告不该阻断包作者，盯 warnings 数量即可。）
set(ZHULONG_PPE_WARNINGS
    $<$<CXX_COMPILER_ID:GNU,Clang,AppleClang>:-Wall;-Wextra;-Wpedantic>
)

add_library(ppe_helmet_core OBJECT
    src/detection/yolov8_ppe_decode.cpp
    src/detection/yolov8_ppe_detector.cpp
    src/analysis/helmet_association.cpp
    src/analysis/violation_tracker.cpp
    src/pipeline/ppe_helmet.cpp
)
target_compile_definitions(ppe_helmet_core PRIVATE ZHULONG_PLUGIN_BUILD)
target_compile_options(ppe_helmet_core PRIVATE ${ZHULONG_PPE_WARNINGS})
target_include_directories(ppe_helmet_core PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/src)
target_link_libraries(ppe_helmet_core PUBLIC Zhulong::AlgoRuntime Threads::Threads)

# 交付物：包根可执行文件名必须等于 manifest.id（宿主按 id 找入口）
add_executable(ppe_helmet
    src/main.cpp
    $<TARGET_OBJECTS:ppe_helmet_core>
)
target_compile_options(ppe_helmet PRIVATE ${ZHULONG_PPE_WARNINGS})
target_compile_definitions(ppe_helmet PRIVATE
    ZHULONG_PPE_PLUGIN_VERSION="${ZHULONG_PPE_PLUGIN_VERSION}")
target_include_directories(ppe_helmet PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
# 自有符号不外泄：静态归档里的 zhulong 符号不进 .dynsym
# --exclude-libs 是 GNU ld 专属（Apple ld 报 unknown options）；macOS 开发机跳过即可。
if(NOT APPLE)
    target_link_options(ppe_helmet PRIVATE "LINKER:--exclude-libs,ALL")
else()
    # Mach-O ld 不会自动把未定义的 __attribute__((weak)) 引用解析为 NULL；
    # 非人脸包不提供 zhulong_face_extract_feature，需在 macOS 开发机显式允许该弱符号留空。
    target_link_options(ppe_helmet PRIVATE "LINKER:-U,_zhulong_face_extract_feature")
endif()
target_link_libraries(ppe_helmet PRIVATE Zhulong::AlgoSDK Zhulong::AlgoRuntime)

add_executable(ppe_helmet_unittest EXCLUDE_FROM_ALL
    tests/unit_test.cpp
    $<TARGET_OBJECTS:ppe_helmet_core>
)
target_compile_options(ppe_helmet_unittest PRIVATE ${ZHULONG_PPE_WARNINGS})
target_include_directories(ppe_helmet_unittest PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
target_link_libraries(ppe_helmet_unittest PRIVATE Zhulong::AlgoRuntime Threads::Threads)

add_executable(ppe_helmet_devtest EXCLUDE_FROM_ALL
    tests/dev_infer.cpp
    $<TARGET_OBJECTS:ppe_helmet_core>
)
# ⚠️ -UNDEBUG：RelWithDebInfo 的 NDEBUG 会把后来者顺手写的 assert() 整块编译掉。
#    本包断言走 tests/unit_test.cpp 的 check()（不依赖 NDEBUG），-UNDEBUG 是兜底。
target_compile_options(ppe_helmet_devtest PRIVATE
    ${ZHULONG_PPE_WARNINGS}
    $<$<CXX_COMPILER_ID:GNU,Clang,AppleClang>:-UNDEBUG>
)
target_include_directories(ppe_helmet_devtest PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
# stb 以 SYSTEM 引入：它的告警不是本包的，不该淹没产品代码的 -Wall 输出
target_include_directories(ppe_helmet_devtest SYSTEM PRIVATE ${ZhulongAlgoSDK_DEV_DIR}/stb)
target_link_libraries(ppe_helmet_devtest PRIVATE Zhulong::AlgoRuntime Threads::Threads)
