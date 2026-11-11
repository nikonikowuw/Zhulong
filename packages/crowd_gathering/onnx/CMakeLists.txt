# 人员非法聚集检测算法包（onnx 档位）—— 开发期构建
#
# 产出 devtest（读包根 sample.jpg → 调本 target 产品代码 → 写 result.jpg）与
# 包根可执行文件 `crowd_gathering`（名字必须等于 manifest.id）。
# devtest EXCLUDE_FROM_ALL、无安装规则，物理上进不了归档。
#
# ⚠️ 依赖只有 SDK 前缀：`find_package(ZhulongAlgoSDK)`。本文件不得出现任何指向本仓库的
#    相对路径（自查门禁：native/scripts/sdk_clean_check.sh 会 grep 它）。
cmake_minimum_required(VERSION 3.20)
project(crowd_gathering_onnx_dev LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

find_package(ZhulongAlgoSDK 1.0 REQUIRED)

find_package(Threads REQUIRED)

# 产品代码（算法类本体）：包可执行文件与 devtest 共用，避免两份实现。
add_library(crowd_gathering_core OBJECT
    src/detection/person_detector.cpp
    src/analysis/spatial_clustering.cpp
    src/analysis/temporal_tracker.cpp
    src/pipeline/crowd_gathering.cpp
)
target_compile_definitions(crowd_gathering_core PRIVATE ZHULONG_PLUGIN_BUILD)
target_include_directories(crowd_gathering_core PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/src
)
target_link_libraries(crowd_gathering_core PRIVATE Zhulong::AlgoRuntime)

# 交付物：包根可执行文件名必须等于 manifest.id（宿主按 id 找入口）
add_executable(crowd_gathering
    src/main.cpp
    $<TARGET_OBJECTS:crowd_gathering_core>
)
target_include_directories(crowd_gathering PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/src
)
# 自有符号不外泄：静态归档里的 zhulong 符号不进 .dynsym
# --exclude-libs 是 GNU ld 专属（Apple ld 报 unknown options）；macOS 开发机跳过即可。
if(NOT APPLE)
    target_link_options(crowd_gathering PRIVATE "LINKER:--exclude-libs,ALL")
endif()
target_link_libraries(crowd_gathering PRIVATE Zhulong::AlgoSDK Zhulong::AlgoRuntime)

# 包内自测：纯逻辑（配置解析 / 聚类几何 / 时序防抖），不加载模型、不需要会话。
# 只依赖 SDK 前缀 + 包自身源码 → 第三方与 sdk-clean-check 的干净容器都能构建运行。
# ⚠️ 判定不得用 assert()：RelWithDebInfo 定义 NDEBUG 会把断言整段编译掉（见 §5.5）。
add_executable(crowd_gathering_unittest EXCLUDE_FROM_ALL
    tests/unit_test.cpp
    src/analysis/spatial_clustering.cpp
    src/analysis/temporal_tracker.cpp
)
target_include_directories(crowd_gathering_unittest PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/src
)
target_link_libraries(crowd_gathering_unittest PRIVATE Zhulong::AlgoRuntime)

# 开发期自测（EXCLUDE_FROM_ALL、无安装规则 → 物理上进不了归档）
add_executable(crowd_gathering_devtest EXCLUDE_FROM_ALL
    tests/dev_infer.cpp
    $<TARGET_OBJECTS:crowd_gathering_core>
)
target_compile_definitions(crowd_gathering_devtest PRIVATE ZHULONG_PLUGIN_BUILD)
target_include_directories(crowd_gathering_devtest PRIVATE
    ${ZhulongAlgoSDK_DEV_DIR}/stb
    ${CMAKE_CURRENT_SOURCE_DIR}/src
)
# 链接顺序由 Zhulong::AlgoRuntime 的 INTERFACE_LINK_LIBRARIES 保证（第三方运行时排在后面）
target_link_libraries(crowd_gathering_devtest PRIVATE Zhulong::AlgoRuntime)
