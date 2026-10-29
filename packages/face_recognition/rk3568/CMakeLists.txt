# 人脸识别算法包（rk3568 档位）—— 开发期构建
#
# 产出 devtest（读包根 sample.jpg → 调本 target 产品代码 → 写 result.jpg）与
# 包根可执行文件 `face_recognition`（名字必须等于 manifest.id）。
# 依赖只有 SDK 前缀：`find_package(ZhulongAlgoSDK)`，本文件不得出现仓库路径。
cmake_minimum_required(VERSION 3.20)
project(face_recognition_rk3568 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

find_package(ZhulongAlgoSDK 1.0 REQUIRED)
find_package(Threads REQUIRED)

# 产品代码（算法类本体）：可执行文件与 devtest 共用，避免两份实现。
add_library(face_recognition_core OBJECT
    src/detection/detector.cpp
    src/alignment/aligner.cpp
    src/feature/extractor.cpp
    src/matching/matcher.cpp
    src/pipeline/face_recognition.cpp
)
target_compile_definitions(face_recognition_core PRIVATE ZHULONG_PLUGIN_BUILD)
target_include_directories(face_recognition_core PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
target_link_libraries(face_recognition_core PRIVATE Zhulong::AlgoRuntime)

# 交付物：包根可执行文件名必须等于 manifest.id
add_executable(face_recognition
    src/main.cpp
    $<TARGET_OBJECTS:face_recognition_core>
)
target_include_directories(face_recognition PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
target_link_options(face_recognition PRIVATE "LINKER:--exclude-libs,ALL")
target_link_libraries(face_recognition PRIVATE Zhulong::AlgoSDK Zhulong::AlgoRuntime)

# 开发期自测（EXCLUDE_FROM_ALL、无安装规则 → 物理上进不了归档）
add_executable(face_recognition_devtest EXCLUDE_FROM_ALL
    tests/dev_infer.cpp
    $<TARGET_OBJECTS:face_recognition_core>
)
target_compile_definitions(face_recognition_devtest PRIVATE ZHULONG_PLUGIN_BUILD)
target_include_directories(face_recognition_devtest PRIVATE
    ${ZhulongAlgoSDK_DEV_DIR}/stb
    ${CMAKE_CURRENT_SOURCE_DIR}/src
)
target_link_libraries(face_recognition_devtest PRIVATE Zhulong::AlgoRuntime)
