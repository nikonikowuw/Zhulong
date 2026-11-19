cmake_minimum_required(VERSION 3.20)
project(ppe_helmet LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

if(NOT CMAKE_BUILD_TYPE)
    set(CMAKE_BUILD_TYPE RelWithDebInfo CACHE STRING "Build type" FORCE)
endif()

find_package(ZhulongAlgoSDK 1.0 REQUIRED)
find_package(Threads REQUIRED)

if(NOT "${ZhulongAlgoSDK_BACKEND}" STREQUAL "rknn")
    message(FATAL_ERROR "ppe_helmet/rk3568 必须使用 rknn 档 SDK 前缀构建（当前: ${ZhulongAlgoSDK_BACKEND}）")
endif()

add_library(ppe_helmet_core OBJECT
    src/detection/yolov8_ppe_decode.cpp
    src/detection/yolov8_ppe_detector.cpp
    src/analysis/helmet_association.cpp
    src/analysis/violation_tracker.cpp
    src/pipeline/ppe_helmet.cpp
)
target_compile_definitions(ppe_helmet_core PRIVATE ZHULONG_PLUGIN_BUILD)
target_include_directories(ppe_helmet_core PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/src)
target_link_libraries(ppe_helmet_core PUBLIC Zhulong::AlgoRuntime Threads::Threads)

add_executable(ppe_helmet
    src/main.cpp
    $<TARGET_OBJECTS:ppe_helmet_core>
)
target_include_directories(ppe_helmet PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
if(NOT APPLE)
    target_link_options(ppe_helmet PRIVATE "LINKER:--exclude-libs,ALL")
endif()
target_link_libraries(ppe_helmet PRIVATE Zhulong::AlgoSDK Zhulong::AlgoRuntime)

add_executable(ppe_helmet_unittest EXCLUDE_FROM_ALL
    tests/unit_test.cpp
    $<TARGET_OBJECTS:ppe_helmet_core>
)
target_include_directories(ppe_helmet_unittest PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
target_link_libraries(ppe_helmet_unittest PRIVATE Zhulong::AlgoRuntime Threads::Threads)

add_executable(ppe_helmet_devtest EXCLUDE_FROM_ALL
    tests/dev_infer.cpp
    $<TARGET_OBJECTS:ppe_helmet_core>
)
target_include_directories(ppe_helmet_devtest PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/src
    ${ZhulongAlgoSDK_DEV_DIR}/stb
)
target_link_libraries(ppe_helmet_devtest PRIVATE Zhulong::AlgoRuntime Threads::Threads)
