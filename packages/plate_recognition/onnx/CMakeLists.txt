# 车牌识别算法包（onnx 档位）—— 开发期构建
#
# 这里只产出 `tests/dev_infer.cpp` 的自测可执行文件：读包根 sample.jpg → 调本 target
# 产品代码 → 画框 → 写 result.jpg。它 EXCLUDE_FROM_ALL、无安装规则，物理上进不了归档。
# 正式包构建（包根可执行文件）见阶段 3.3。
#
# ⚠️ 依赖只有 SDK 前缀：`find_package(ZhulongAlgoSDK)`。本文件不得出现仓库路径
#    （../native、../../build 之类），否则这个包就不再是可独立开发的工程了。
cmake_minimum_required(VERSION 3.20)
project(plate_recognition_onnx_dev LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

find_package(ZhulongAlgoSDK 1.0 REQUIRED)

find_package(Threads REQUIRED)

add_executable(plate_recognition_devtest EXCLUDE_FROM_ALL
    tests/dev_infer.cpp
    src/plate_recognition.cpp
    src/plate_detector.cpp
    src/plate_recognizer.cpp
)
target_compile_definitions(plate_recognition_devtest PRIVATE ZHULONG_PLUGIN_BUILD)
target_include_directories(plate_recognition_devtest PRIVATE
    ${ZhulongAlgoSDK_DEV_DIR}/stb
    ${CMAKE_CURRENT_SOURCE_DIR}/src
)
# 链接顺序由 Zhulong::AlgoRuntime 的 INTERFACE_LINK_LIBRARIES 保证（第三方运行时排在后面）
target_link_libraries(plate_recognition_devtest PRIVATE Zhulong::AlgoRuntime)
