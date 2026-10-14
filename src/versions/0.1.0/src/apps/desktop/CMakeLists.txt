# =============================================================================
# 桌面客户端构建定义 / Desktop client build definition
#
# 架构：刻意拆成两个目标。
#
#   pcx_core               纯逻辑静态库：JSON、i18n、IPC 客户端。**不依赖 wxWidgets**。
#   proxyclient-desktop    GUI 可执行文件：wxWidgets + pcx_core。
#
# 这么拆的理由：CI 里单元测试只需要 pcx_core，不必安装 GUI 工具链。
# 若把它们编在一起，任何一次「只是想跑个 JSON 解析测试」都要先装好
# wxWidgets 与 GTK，CI 会变慢变脆，测试也就没人愿意跑了。
#
# 依赖策略（见 design/adr/ADR-0002）：
#   - 不做 latest 跟踪；nlohmann/json 固定到具体 tag；
#   - 优先用系统已装的版本（find_package），取不到才下载固定版本；
#   - 版本记录在 deploy/targets/versions.lock.json。
# =============================================================================

cmake_minimum_required(VERSION 3.21)

project(proxyclient-desktop
    VERSION 0.1.0
    DESCRIPTION "Dual-core Xboard proxy client - desktop front end"
    LANGUAGES CXX)

# -----------------------------------------------------------------------------
# 编译选项
# -----------------------------------------------------------------------------
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# 默认构建类型：不指定时 CMake 用空字符串，导致既没有优化也没有调试信息。
if(NOT CMAKE_BUILD_TYPE AND NOT CMAKE_CONFIGURATION_TYPES)
    set(CMAKE_BUILD_TYPE Release CACHE STRING "Build type" FORCE)
endif()

option(PCX_BUILD_GUI    "Build the wxWidgets GUI executable" ON)
option(PCX_BUILD_TESTS  "Build unit tests"                   ON)
option(PCX_WERROR       "Treat warnings as errors"           OFF)
option(PCX_STRICT_CONVERSION "Also treat -Wconversion/-Wsign-conversion as errors" OFF)

# 警告选项：不开 -Werror 作为默认，因为首次编译时把警告当错误
# 会让「能跑起来」这件事变得遥不可及；CI 里用 PCX_WERROR=ON 收紧。
add_library(pcx_warnings INTERFACE)
if(MSVC)
    target_compile_options(pcx_warnings INTERFACE /W4 /permissive-)
    if(PCX_WERROR)
        target_compile_options(pcx_warnings INTERFACE /WX)
    endif()
else()
    target_compile_options(pcx_warnings INTERFACE
        -Wall -Wextra -Wpedantic
        -Wshadow -Wnon-virtual-dtor -Wcast-align
        -Wunused -Woverloaded-virtual)

    # -Wconversion / -Wsign-conversion 单独成一个开关，默认只报告、不致命。
    #
    # 理由：它们**不属于** -Wall -Wextra，正是因为对正常的写法噪音极大
    # （几乎每个 int/size_t 混用处都会报警）。在代码从未编译过的情况下
    # 把它们并入 -Werror，得到的是「改一处、推一次、再报一处」的长循环，
    # 而不是更安全的代码——每轮都要消耗一次 CI 构建。
    #
    # 因此保留告警（能看见问题），但不让它挡住第一次成功编译；
    # 等功能验证过、有真实运行证据之后，再打开 PCX_STRICT_CONVERSION 收口。
    target_compile_options(pcx_warnings INTERFACE -Wconversion -Wsign-conversion)

    if(PCX_WERROR)
        target_compile_options(pcx_warnings INTERFACE -Werror)

        # 注意顺序与位置：-Werror 会把上面**所有**告警都变成错误，
        # 包括刚说的那两个高噪音的。必须在这之后显式把它们排除，
        # 否则前面的豁免完全不起作用（-Wno-error=X 只影响 X 这一项）。
        if(NOT PCX_STRICT_CONVERSION)
            target_compile_options(pcx_warnings INTERFACE
                -Wno-error=conversion -Wno-error=sign-conversion)
        endif()
    endif()
endif()

# -----------------------------------------------------------------------------
# 依赖：JSON 库
#
# 说明：本项目的 IPC 协议是 JSON（设计基线 §7.3），因此桌面端需要一个
# JSON 库。写法上先用系统版本，取不到再拉取固定 tag。
# 不手写解析器的理由：JSON 转义、UTF-8、数字精度这些细节很容易写错，
# 而错误表现往往是「偶发解析失败」，极难排查。
# -----------------------------------------------------------------------------
find_package(nlohmann_json 3.11 CONFIG QUIET)
if(NOT nlohmann_json_FOUND)
    message(STATUS "nlohmann_json not found locally; fetching the pinned release")
    include(FetchContent)
    FetchContent_Declare(nlohmann_json
        GIT_REPOSITORY https://github.com/nlohmann/json.git
        GIT_TAG        v3.11.3
        GIT_SHALLOW    TRUE)
    FetchContent_MakeAvailable(nlohmann_json)
else()
    message(STATUS "using the system nlohmann_json")
endif()

# -----------------------------------------------------------------------------
# pcx_core：纯逻辑，不依赖 GUI
# -----------------------------------------------------------------------------
# -----------------------------------------------------------------------------
# pcx_core：纯逻辑，不依赖 GUI
#
# settings 与 app_context 放在这里而不是 GUI 目标里：它们是纯逻辑
# （读写配置、组织状态），放进 GUI 目标就只能在装了 wxWidgets 的环境里
# 才能编译和测试，而它们的逻辑恰恰最需要单元测试。
# -----------------------------------------------------------------------------
add_library(pcx_core STATIC
    src/core/json_util.cpp
    src/core/i18n.cpp
    src/core/ipc_protocol.cpp
    src/core/ipc_transport.cpp
    src/core/ipc_client.cpp
    src/app/settings.cpp
    src/app/app_context.cpp)
target_include_directories(pcx_core PUBLIC src)
target_link_libraries(pcx_core PUBLIC nlohmann_json::nlohmann_json PRIVATE pcx_warnings)

# 线程支持：IPC 客户端有后台读取线程。
find_package(Threads REQUIRED)
target_link_libraries(pcx_core PUBLIC Threads::Threads)

# Windows 上只用命名管道，不需要 Winsock。

# 让代码能知道当前版本（用于版本握手与「关于」界面）。
target_compile_definitions(pcx_core PUBLIC
    PCX_VERSION="${PROJECT_VERSION}"
    PCX_IPC_PROTOCOL_VERSION=1
    PCX_IPC_SCHEMA_VERSION="1.0.0")

# -----------------------------------------------------------------------------
# GUI 可执行文件
#
# 源码尚未写完时给出明确提示并跳过 GUI 目标，而不是让 cmake 在配置阶段
# 报「找不到源文件」——那种报错看不出真正的原因，也挡住了核心库与测试的构建。
# 源码补齐后这里会自动启用，不需要改配置。
# -----------------------------------------------------------------------------
if(PCX_BUILD_GUI)
    set(PCX_GUI_SOURCES
        src/main.cpp
        src/ui/main_frame.cpp
        src/ui/simple_panel.cpp
        src/ui/pro_panel.cpp)

    # 检查**全部** GUI 源文件，而不是只检查 main.cpp：
    # 只检查一个的话，缺其它文件时 cmake 会在 add_executable 处报
    # 「找不到源文件」，那个错误看不出真正的原因。
    set(PCX_GUI_MISSING "")
    foreach(gui_source IN LISTS PCX_GUI_SOURCES)
        if(NOT EXISTS "${CMAKE_CURRENT_SOURCE_DIR}/${gui_source}")
            list(APPEND PCX_GUI_MISSING ${gui_source})
        endif()
    endforeach()

    if(PCX_GUI_MISSING)
        message(WARNING
            "GUI sources are not implemented yet; skipping the proxyclient-desktop "
            "target. Missing: ${PCX_GUI_MISSING}. "
            "pcx_core and the tests still build.")
    else()
        # wxWidgets 有两种查找方式，两者的链接方式不同：
        #
        #   CONFIG 模式：vcpkg 等包管理器安装的 wx 提供 wxWidgetsConfig.cmake，
        #                链接到 wx::core / wx::base 导入目标。
        #   模块模式：发行版（apt 的 libwxgtk3.2-dev）提供 FindwxWidgets.cmake，
        #                通过 wxWidgets_USE_FILE 设置编译选项，链接 wxWidgets_LIBRARIES。
        #
        # 先试 CONFIG 再回退模块模式：只支持一种的话，
        # Windows（vcpkg）与 Linux（apt）必然有一边在 find_package 处失败。
        find_package(wxWidgets CONFIG QUIET COMPONENTS core base)
        if(wxWidgets_FOUND)
            set(PCX_WX_LIBRARIES wx::core wx::base)
            message(STATUS "using the wxWidgets CMake package (config mode)")
        else()
            find_package(wxWidgets REQUIRED COMPONENTS core base)
            include(${wxWidgets_USE_FILE})
            set(PCX_WX_LIBRARIES ${wxWidgets_LIBRARIES})
            message(STATUS "using FindwxWidgets (module mode)")
        endif()

        # Windows 下用 WIN32 生成子系统为 Windows 的可执行文件（不弹控制台）。
        if(WIN32)
            add_executable(proxyclient-desktop WIN32 ${PCX_GUI_SOURCES})
        else()
            add_executable(proxyclient-desktop ${PCX_GUI_SOURCES})
        endif()

        target_link_libraries(proxyclient-desktop PRIVATE pcx_core ${PCX_WX_LIBRARIES} pcx_warnings)
        if(DEFINED wxWidgets_INCLUDE_DIRS)
            target_include_directories(proxyclient-desktop PRIVATE ${wxWidgets_INCLUDE_DIRS})
        endif()

        install(TARGETS proxyclient-desktop RUNTIME DESTINATION bin)
        install(DIRECTORY ${CMAKE_CURRENT_SOURCE_DIR}/../../../../../../locales/
                DESTINATION share/proxyclient/locales
                FILES_MATCHING PATTERN "*.json")
    endif()
endif()

# -----------------------------------------------------------------------------
# 单元测试
#
# 只测 pcx_core：不依赖 GUI 工具链，因此在任何 runner 上都能跑。
# -----------------------------------------------------------------------------
if(PCX_BUILD_TESTS)
    enable_testing()

    add_executable(pcx_tests
        tests/test_main.cpp
        tests/test_json_util.cpp
        tests/test_i18n.cpp
        tests/test_ipc_protocol.cpp
        tests/test_ipc_transport.cpp
        tests/test_settings.cpp)
    target_link_libraries(pcx_tests PRIVATE pcx_core pcx_warnings)

    add_test(NAME pcx_tests COMMAND pcx_tests)

    # 让测试能找到仓库里的语言资源与出厂配置。
    #
    # 直接加载真实的 client-config.json 是有意的：它是打包时分发的文件，
    # 解析不了的话用户看到的是「软件名变成 default、端口全错」，且没有任何报错。
    # 层数必须数准：从 apps/desktop 到仓库根是 6 层。
    # 写成 4 层会解析到 src/versions/ 下面一个不存在的路径，
    # 表现是测试报「读不到语言资源 / 读不到出厂配置」，
    # 而错误信息里只有一句 error.empty()，看不出是路径问题。
    set_tests_properties(pcx_tests PROPERTIES
        ENVIRONMENT "PCX_LOCALES_DIR=${CMAKE_CURRENT_SOURCE_DIR}/../../../../../../locales;PCX_CONFIG_DIR=${CMAKE_CURRENT_SOURCE_DIR}/../../../../../../design")

    message(STATUS "tests enabled; run them with: ctest --output-on-failure")
endif()
