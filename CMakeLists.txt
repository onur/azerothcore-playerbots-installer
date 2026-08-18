cmake_minimum_required(VERSION 3.16)

project(PlayerbotsWorkspace NONE)

include(ExternalProject)

# 1. Install prefix configuration (default: dist/ in repository root)
if(NOT DEFINED CMAKE_INSTALL_PREFIX OR CMAKE_INSTALL_PREFIX_INITIALIZED_TO_DEFAULT)
    set(CMAKE_INSTALL_PREFIX "${CMAKE_CURRENT_SOURCE_DIR}/dist" CACHE PATH "Install directory prefix" FORCE)
endif()

# Find Go compiler
find_program(GO_EXECUTABLE go)

# 2. Discover MySQL, Boost, and OpenSSL paths from CMake flags, environment variables, or local defaults
if(NOT DEFINED MYSQL_ROOT_DIR AND DEFINED ENV{MYSQL_ROOT_DIR})
    set(MYSQL_ROOT_DIR "$ENV{MYSQL_ROOT_DIR}")
endif()
if(NOT DEFINED MYSQL_ROOT_DIR AND EXISTS "${CMAKE_CURRENT_SOURCE_DIR}/deps/mysql-8.0.46-winx64")
    set(MYSQL_ROOT_DIR "${CMAKE_CURRENT_SOURCE_DIR}/deps/mysql-8.0.46-winx64")
endif()

if(NOT DEFINED BOOST_ROOT_DIR AND DEFINED ENV{BOOST_ROOT_DIR})
    set(BOOST_ROOT_DIR "$ENV{BOOST_ROOT_DIR}")
endif()
if(NOT DEFINED BOOST_ROOT_DIR AND DEFINED ENV{BOOST_ROOT})
    set(BOOST_ROOT_DIR "$ENV{BOOST_ROOT}")
endif()
if(NOT DEFINED BOOST_ROOT_DIR AND EXISTS "C:/local/boost_1_84_0")
    set(BOOST_ROOT_DIR "C:/local/boost_1_84_0")
endif()

if(NOT DEFINED OPENSSL_ROOT_DIR AND DEFINED ENV{OPENSSL_ROOT_DIR})
    set(OPENSSL_ROOT_DIR "$ENV{OPENSSL_ROOT_DIR}")
endif()
if(NOT DEFINED OPENSSL_ROOT_DIR AND EXISTS "${CMAKE_CURRENT_SOURCE_DIR}/deps/openssl-3.5.7/x64")
    set(OPENSSL_ROOT_DIR "${CMAKE_CURRENT_SOURCE_DIR}/deps/openssl-3.5.7/x64")
endif()

# Normalize paths to absolute paths with forward slashes
if(MYSQL_ROOT_DIR)
    get_filename_component(MYSQL_ROOT_DIR "${MYSQL_ROOT_DIR}" ABSOLUTE BASE_DIR "${CMAKE_CURRENT_SOURCE_DIR}")
    file(TO_CMAKE_PATH "${MYSQL_ROOT_DIR}" MYSQL_ROOT_DIR)
    set(MYSQL_ROOT_DIR "${MYSQL_ROOT_DIR}" CACHE PATH "MySQL root directory" FORCE)
endif()
if(BOOST_ROOT_DIR)
    get_filename_component(BOOST_ROOT_DIR "${BOOST_ROOT_DIR}" ABSOLUTE BASE_DIR "${CMAKE_CURRENT_SOURCE_DIR}")
    file(TO_CMAKE_PATH "${BOOST_ROOT_DIR}" BOOST_ROOT_DIR)
    set(BOOST_ROOT_DIR "${BOOST_ROOT_DIR}" CACHE PATH "Boost root directory" FORCE)
endif()
if(OPENSSL_ROOT_DIR)
    get_filename_component(OPENSSL_ROOT_DIR "${OPENSSL_ROOT_DIR}" ABSOLUTE BASE_DIR "${CMAKE_CURRENT_SOURCE_DIR}")
    file(TO_CMAKE_PATH "${OPENSSL_ROOT_DIR}" OPENSSL_ROOT_DIR)
    set(OPENSSL_ROOT_DIR "${OPENSSL_ROOT_DIR}" CACHE PATH "OpenSSL root directory" FORCE)
endif()
if(CMAKE_INSTALL_PREFIX)
    get_filename_component(CMAKE_INSTALL_PREFIX "${CMAKE_INSTALL_PREFIX}" ABSOLUTE BASE_DIR "${CMAKE_CURRENT_SOURCE_DIR}")
    file(TO_CMAKE_PATH "${CMAKE_INSTALL_PREFIX}" CMAKE_INSTALL_PREFIX_CLEAN)
    set(CMAKE_INSTALL_PREFIX "${CMAKE_INSTALL_PREFIX_CLEAN}" CACHE PATH "Install directory prefix" FORCE)
endif()

message(STATUS "=== Playerbots Workspace Build Configuration ===")
message(STATUS "  Install Prefix : ${CMAKE_INSTALL_PREFIX_CLEAN}")
message(STATUS "  MySQL Root     : ${MYSQL_ROOT_DIR}")
message(STATUS "  Boost Root     : ${BOOST_ROOT_DIR}")
message(STATUS "  OpenSSL Root   : ${OPENSSL_ROOT_DIR}")
message(STATUS "  Go Compiler    : ${GO_EXECUTABLE}")

# 3. Prepare cmake arguments for azerothcore-wotlk
set(ACORE_CMAKE_ARGS
    "-DCMAKE_POLICY_DEFAULT_CMP0175=OLD"
    "-DCMAKE_POLICY_DEFAULT_CMP0153=OLD"
    "-DCMAKE_POLICY_DEFAULT_CMP0144=NEW"
    "-DCMAKE_POLICY_DEFAULT_CMP0167=OLD"
    "-DCMAKE_INSTALL_PREFIX=${CMAKE_INSTALL_PREFIX_CLEAN}"
    "-DWITH_WARNINGS=1"
    "-DTOOLS_BUILD=all"
    "-DSCRIPTS=static"
    "-DMODULES=static"
)

if(CMAKE_BUILD_TYPE)
    list(APPEND ACORE_CMAKE_ARGS "-DCMAKE_BUILD_TYPE=${CMAKE_BUILD_TYPE}")
endif()

if(MYSQL_ROOT_DIR)
    list(APPEND ACORE_CMAKE_ARGS
        "-DMYSQL_ROOT_DIR=${MYSQL_ROOT_DIR}"
        "-DMYSQL_INCLUDE_DIR=${MYSQL_ROOT_DIR}/include"
        "-DMYSQL_LIBRARY=${MYSQL_ROOT_DIR}/lib/libmysql.lib"
    )
endif()

if(BOOST_ROOT_DIR)
    list(APPEND ACORE_CMAKE_ARGS
        "-DBOOST_ROOT=${BOOST_ROOT_DIR}"
        "-DBoost_ROOT=${BOOST_ROOT_DIR}"
    )
endif()

if(OPENSSL_ROOT_DIR)
    list(APPEND ACORE_CMAKE_ARGS
        "-DOPENSSL_ROOT_DIR=${OPENSSL_ROOT_DIR}"
        "-DOPENSSL_INCLUDE_DIR=${OPENSSL_ROOT_DIR}/include"
        "-DOPENSSL_CRYPTO_LIBRARY=${OPENSSL_ROOT_DIR}/lib/libcrypto.lib"
        "-DOPENSSL_SSL_LIBRARY=${OPENSSL_ROOT_DIR}/lib/libssl.lib"
    )
endif()

# 4. Superbuild AzerothCore + mod-playerbots
ExternalProject_Add(azerothcore
    SOURCE_DIR "${CMAKE_CURRENT_SOURCE_DIR}/azerothcore-wotlk"
    BINARY_DIR "${CMAKE_CURRENT_BINARY_DIR}/azerothcore-build"
    CMAKE_ARGS ${ACORE_CMAKE_ARGS}
    BUILD_ALWAYS 1
    INSTALL_DIR "${CMAKE_INSTALL_PREFIX_CLEAN}"
)

# 5. Windows post-install steps: Copy DLLs, MySQL distribution, and build Go startup tool
if(WIN32)
    # Collect DLLs to copy
    set(DLL_COPY_COMMANDS)
    if(EXISTS "${MYSQL_ROOT_DIR}/lib/libmysql.dll")
        list(APPEND DLL_COPY_COMMANDS
            COMMAND ${CMAKE_COMMAND} -E copy_if_different
                "${MYSQL_ROOT_DIR}/lib/libmysql.dll"
                "${CMAKE_INSTALL_PREFIX_CLEAN}/libmysql.dll"
        )
    endif()

    if(EXISTS "${OPENSSL_ROOT_DIR}/bin/libcrypto-3-x64.dll")
        list(APPEND DLL_COPY_COMMANDS
            COMMAND ${CMAKE_COMMAND} -E copy_if_different
                "${OPENSSL_ROOT_DIR}/bin/libcrypto-3-x64.dll"
                "${CMAKE_INSTALL_PREFIX_CLEAN}/libcrypto-3-x64.dll"
        )
    endif()

    if(EXISTS "${OPENSSL_ROOT_DIR}/bin/libssl-3-x64.dll")
        list(APPEND DLL_COPY_COMMANDS
            COMMAND ${CMAKE_COMMAND} -E copy_if_different
                "${OPENSSL_ROOT_DIR}/bin/libssl-3-x64.dll"
                "${CMAKE_INSTALL_PREFIX_CLEAN}/libssl-3-x64.dll"
        )
    endif()

    if(EXISTS "${OPENSSL_ROOT_DIR}/lib/ossl-modules/legacy.dll")
        list(APPEND DLL_COPY_COMMANDS
            COMMAND ${CMAKE_COMMAND} -E copy_if_different
                "${OPENSSL_ROOT_DIR}/lib/ossl-modules/legacy.dll"
                "${CMAKE_INSTALL_PREFIX_CLEAN}/legacy.dll"
        )
    endif()

    if(DLL_COPY_COMMANDS)
        ExternalProject_Add_Step(azerothcore copy_runtime_dlls
            ${DLL_COPY_COMMANDS}
            DEPENDEES install
            COMMENT "Copying runtime Windows DLLs (MySQL, OpenSSL) to ${CMAKE_INSTALL_PREFIX_CLEAN}"
        )
    endif()

    # Copy MySQL server directory
    if(IS_DIRECTORY "${MYSQL_ROOT_DIR}")
        ExternalProject_Add_Step(azerothcore copy_mysql_distribution
            COMMAND ${CMAKE_COMMAND} -E copy_directory
                "${MYSQL_ROOT_DIR}"
                "${CMAKE_INSTALL_PREFIX_CLEAN}/mysql"
            DEPENDEES install
            COMMENT "Copying MySQL server distribution to ${CMAKE_INSTALL_PREFIX_CLEAN}/mysql"
        )
    endif()

    # Build Go startup launcher
    if(GO_EXECUTABLE)
        ExternalProject_Add_Step(azerothcore build_startup_tool
            COMMAND ${GO_EXECUTABLE} build -o "${CMAKE_INSTALL_PREFIX_CLEAN}/startup.exe" "./cmd/startup"
            WORKING_DIRECTORY "${CMAKE_CURRENT_SOURCE_DIR}"
            DEPENDEES install
            COMMENT "Building startup executable: ${CMAKE_INSTALL_PREFIX_CLEAN}/startup.exe"
        )
    endif()
else()
    if(GO_EXECUTABLE)
        ExternalProject_Add_Step(azerothcore build_startup_tool
            COMMAND ${GO_EXECUTABLE} build -o "${CMAKE_INSTALL_PREFIX_CLEAN}/startup" "./cmd/startup"
            WORKING_DIRECTORY "${CMAKE_CURRENT_SOURCE_DIR}"
            DEPENDEES install
            COMMENT "Building startup executable: ${CMAKE_INSTALL_PREFIX_CLEAN}/startup"
        )
    endif()
endif()
