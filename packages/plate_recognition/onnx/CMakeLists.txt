# 车牌识别算法包（onnx 档位）—— 开发期构建
#
# 产出 devtest（读包根 sample.jpg → 调本 target 产品代码 → 写 result.jpg）与
# 包根可执行文件 `plate_recognition`（名字必须等于 manifest.id）。
# devtest EXCLUDE_FROM_ALL、无安装规则，物理上进不了归档。
#
# ⚠️ 依赖只有 SDK 前缀：`find_package(ZhulongAlgoSDK)`。本文件不得出现任何指向本仓库的
#    相对路径（自查门禁：native/scripts/sdk_clean_check.sh 会 grep 它），否则这个包
#    就不再是可独立开发的工程了。
cmake_minimum_required(VERSION 3.20)
project(plate_recognition_onnx_dev LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

find_package(ZhulongAlgoSDK 1.0 REQUIRED)

# 警告是**产品代码**的门禁：仓库 native 侧统一 -Wall -Wextra -Wpedantic，包独立构建时
# 不能因为「没有宿主 CMakeLists 替我加」就默默变成零警告检查。
# （不做 -Werror：第三方 SDK 头的警告不该阻断包作者，盯 warnings 数量即可。）
set(ZHULONG_PLATE_WARNINGS
    $<$<CXX_COMPILER_ID:GNU,Clang,AppleClang>:-Wall;-Wextra;-Wpedantic>
)

# 产品代码（算法类本体）：包可执行文件与 devtest 共用，避免两份实现。
add_library(plate_recognition_core OBJECT
    src/detection/plate_detector.cpp
    src/recognition/plate_recognizer.cpp
    src/format/plate_format.cpp
    src/pipeline/plate_recognition.cpp
)
target_compile_definitions(plate_recognition_core PRIVATE ZHULONG_PLUGIN_BUILD)
target_compile_options(plate_recognition_core PRIVATE ${ZHULONG_PLATE_WARNINGS})
target_include_directories(plate_recognition_core PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/src
)
target_link_libraries(plate_recognition_core PRIVATE Zhulong::AlgoRuntime)

# 交付物：包根可执行文件名必须等于 manifest.id（宿主按 id 找入口）
add_executable(plate_recognition
    src/main.cpp
    $<TARGET_OBJECTS:plate_recognition_core>
)
target_compile_options(plate_recognition PRIVATE ${ZHULONG_PLATE_WARNINGS})
target_include_directories(plate_recognition PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/src
)
# 自有符号不外泄：静态归档里的 zhulong 符号不进 .dynsym
target_link_options(plate_recognition PRIVATE "LINKER:--exclude-libs,ALL")
target_link_libraries(plate_recognition PRIVATE Zhulong::AlgoSDK Zhulong::AlgoRuntime)

# 开发期自测（EXCLUDE_FROM_ALL、无安装规则 → 物理上进不了归档）
add_executable(plate_recognition_devtest EXCLUDE_FROM_ALL
    tests/dev_infer.cpp
    $<TARGET_OBJECTS:plate_recognition_core>
)
target_compile_definitions(plate_recognition_devtest PRIVATE ZHULONG_PLUGIN_BUILD)
# ⚠️ -UNDEBUG：devtest 里还有 assert()（第三方 stb 与将来新增的断言），RelWithDebInfo 的
#    NDEBUG 会把它们整块编译掉。仓库 native 侧为此专门有一条 ASSERT_TEST_TARGETS 纪律；
#    本包自己的断言走 tests/plate_unit_checks.hpp 的 PLATE_CHECK（不依赖 NDEBUG），
#    -UNDEBUG 是给「后来者顺手写的 assert」兜底。
target_compile_options(plate_recognition_devtest PRIVATE
    ${ZHULONG_PLATE_WARNINGS}
    $<$<CXX_COMPILER_ID:GNU,Clang,AppleClang>:-UNDEBUG>
)
target_include_directories(plate_recognition_devtest PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/src
    ${CMAKE_CURRENT_SOURCE_DIR}/tests
)
# stb 以 SYSTEM 引入：它的告警不是本包的，不该淹没产品代码的 -Wall 输出
# （SYSTEM 头里的告警被抑制，我们自己的 TU 仍然按 -Wall -Wextra -Wpedantic 检查）。
target_include_directories(plate_recognition_devtest SYSTEM PRIVATE
    ${ZhulongAlgoSDK_DEV_DIR}/stb
)
# 链接顺序由 Zhulong::AlgoRuntime 的 INTERFACE_LINK_LIBRARIES 保证（第三方运行时排在后面）
target_link_libraries(plate_recognition_devtest PRIVATE Zhulong::AlgoRuntime)
