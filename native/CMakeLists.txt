cmake_minimum_required(VERSION 3.20)

project(ZhulongEngine LANGUAGES C CXX)

include(CTest)

add_library(ZhulongEngine STATIC src/engine.cpp)
target_compile_features(ZhulongEngine PUBLIC cxx_std_17)
target_include_directories(ZhulongEngine PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/include)
set_target_properties(ZhulongEngine PROPERTIES POSITION_INDEPENDENT_CODE ON)

target_compile_options(ZhulongEngine PRIVATE
    $<$<CXX_COMPILER_ID:AppleClang,Clang,GNU>:-Wall;-Wextra;-Wpedantic>
)

if(BUILD_TESTING)
    add_executable(ZhulongEngineTests tests/engine_test.cpp)
    target_link_libraries(ZhulongEngineTests PRIVATE ZhulongEngine)
    add_test(NAME ZhulongEngineLifecycle COMMAND ZhulongEngineTests)

    add_executable(ZhulongCAbiTests tests/engine_abi_test.c)
    target_compile_features(ZhulongCAbiTests PRIVATE c_std_11)
    target_link_libraries(ZhulongCAbiTests PRIVATE ZhulongEngine)
    set_target_properties(ZhulongCAbiTests PROPERTIES LINKER_LANGUAGE CXX)
    add_test(NAME ZhulongCAbiLifecycle COMMAND ZhulongCAbiTests)
endif()
