cmake_minimum_required(VERSION 3.16)

project(PlayerbotsWorkspace NONE)

include(ExternalProject)

# 1. Install prefix configuration (default: dist/ in repository root)
if(NOT DEFINED CMAKE_INSTALL_PREFIX OR CMAKE_INSTALL_PREFIX_INITIALIZED_TO_DEFAULT)
    set(CMAKE_INSTALL_PREFIX "${CMAKE_CURRENT_SOURCE_DIR}/dist" CACHE PATH "Install directory prefix" FORCE)
endif()

# Find Go compiler
find_program(GO_EXECUTABLE go)

# 2. Set MySQL, Boost, and OpenSSL paths from environment variables
set(MYSQL_ROOT_DIR "$ENV{MYSQL_ROOT_DIR}")
set(BOOST_ROOT_DIR "$ENV{BOOST_ROOT_DIR}")
set(OPENSSL_ROOT_DIR "$ENV{OPENSSL_ROOT_DIR}")

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

if(DEFINED ENV{PACKAGE_VERSION})
    set(PACKAGE_VERSION "$ENV{PACKAGE_VERSION}")
else()
    set(PACKAGE_VERSION "dev")
endif()
set(PACKAGE_VERSION "${PACKAGE_VERSION}" CACHE STRING "Package version for installer and archives")
set(PACKAGE_OUTPUT_DIR "${CMAKE_CURRENT_SOURCE_DIR}/output" CACHE PATH "Directory for built packages and installers")

message(STATUS "=== Playerbots Workspace Build Configuration ===")
message(STATUS "  Version        : ${PACKAGE_VERSION}")
message(STATUS "  Install Prefix : ${CMAKE_INSTALL_PREFIX_CLEAN}")
message(STATUS "  Output Dir     : ${PACKAGE_OUTPUT_DIR}")
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

# 5. Post-install steps: Copy SQL migration files, DLLs, MySQL distribution, and build Go startup tool
ExternalProject_Add_Step(azerothcore copy_sql_files
    COMMAND ${CMAKE_COMMAND} -E copy_directory
        "${CMAKE_CURRENT_SOURCE_DIR}/azerothcore-wotlk/data/sql"
        "${CMAKE_INSTALL_PREFIX_CLEAN}/src/data/sql"
    COMMAND ${CMAKE_COMMAND} -E copy_directory
        "${CMAKE_CURRENT_SOURCE_DIR}/mod-playerbots/data/sql"
        "${CMAKE_INSTALL_PREFIX_CLEAN}/src/modules/mod-playerbots/data/sql"
    DEPENDEES install
    COMMENT "Copying SQL migration files to ${CMAKE_INSTALL_PREFIX_CLEAN}/src"
)

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

    # Copy and prune MySQL server directory
    if(IS_DIRECTORY "${MYSQL_ROOT_DIR}")
        ExternalProject_Add_Step(azerothcore copy_mysql_distribution
            COMMAND ${CMAKE_COMMAND} -E copy_directory
                "${MYSQL_ROOT_DIR}"
                "${CMAKE_INSTALL_PREFIX_CLEAN}/mysql"
            COMMAND ${CMAKE_COMMAND}
                "-DMYSQL_DIST_DIR=${CMAKE_INSTALL_PREFIX_CLEAN}/mysql"
                -P "${CMAKE_CURRENT_SOURCE_DIR}/cmake/CleanMySQLDist.cmake"
            DEPENDEES install
            COMMENT "Copying and pruning MySQL server distribution in ${CMAKE_INSTALL_PREFIX_CLEAN}/mysql"
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

    # 6. Microsoft Store MSIXUpload Packaging Target (Windows)
    file(GLOB WIN_SDK_DIRS
        "$ENV{WindowsSdkDir}/bin/*/x64"
        "$ENV{WindowsSdkVerBinPath}/x64"
        "$ENV{WindowsSdkBinPath}"
        "C:/Program Files (x86)/Windows Kits/10/bin/*/x64"
        "C:/Program Files/Windows Kits/10/bin/*/x64"
        "C:/Program Files (x86)/Windows Kits/10/App Certification Kit"
        "C:/Program Files/Windows Kits/10/App Certification Kit"
    )

    find_program(MAKEAPPX_EXECUTABLE makeappx
        HINTS ${WIN_SDK_DIRS}
        PATHS "C:/Program Files (x86)/Windows Kits/10/bin/10.0.26100.0/x64"
              "C:/Program Files (x86)/Windows Kits/10/bin/10.0.22621.0/x64"
              "C:/Program Files (x86)/Windows Kits/10/bin/10.0.19041.0/x64"
        DOC "Path to makeappx.exe"
    )

    if(MAKEAPPX_EXECUTABLE)
        message(STATUS "  MSIX Packaging : ENABLED (Store MSIXUpload)")
        message(STATUS "    MakeAppx     : ${MAKEAPPX_EXECUTABLE}")

        set(MSIX_LAYOUT_DIR "${CMAKE_CURRENT_BINARY_DIR}/msix-layout" CACHE PATH "Directory for MSIX packaging layout")
        set(MSIX_STAGING_DIR "${CMAKE_CURRENT_BINARY_DIR}/msixupload-staging" CACHE PATH "Directory for MSIXUpload staging")
        set(MSIX_PACKAGE_NAME "mod-playerbots-portable-${PACKAGE_VERSION}" CACHE STRING "Base name for MSIX packages")
        set(MSIX_OUTPUT_FILE "${PACKAGE_OUTPUT_DIR}/mod-playerbots-portable-${PACKAGE_VERSION}.msixupload")

        set(MSIXUPLOAD_COMMANDS
            COMMAND ${CMAKE_COMMAND} -E make_directory "${MSIX_LAYOUT_DIR}"
            COMMAND ${CMAKE_COMMAND} -E make_directory "${MSIX_STAGING_DIR}"
            COMMAND ${CMAKE_COMMAND} -E make_directory "${PACKAGE_OUTPUT_DIR}"
            COMMAND ${CMAKE_COMMAND} -E copy_directory "${CMAKE_INSTALL_PREFIX_CLEAN}" "${MSIX_LAYOUT_DIR}"
            COMMAND ${CMAKE_COMMAND} -E copy_if_different "${CMAKE_CURRENT_SOURCE_DIR}/AppxManifest.xml" "${MSIX_LAYOUT_DIR}/AppxManifest.xml"
        )

        if(EXISTS "${CMAKE_CURRENT_SOURCE_DIR}/assets")
            list(APPEND MSIXUPLOAD_COMMANDS
                COMMAND ${CMAKE_COMMAND} -E copy_directory "${CMAKE_CURRENT_SOURCE_DIR}/assets" "${MSIX_LAYOUT_DIR}/assets"
            )
        endif()

        # Step 1: Pack unsigned MSIX into staging directory (Microsoft Store signs it upon upload)
        list(APPEND MSIXUPLOAD_COMMANDS
            COMMAND "${MAKEAPPX_EXECUTABLE}" pack /v /d "${MSIX_LAYOUT_DIR}" /p "${MSIX_STAGING_DIR}/${MSIX_PACKAGE_NAME}.msix" /o
        )

        # Step 2: Create .msixupload container (ZIP archive containing the .msix) for Partner Center submission
        list(APPEND MSIXUPLOAD_COMMANDS
            COMMAND ${CMAKE_COMMAND} -E chdir "${MSIX_STAGING_DIR}" ${CMAKE_COMMAND} -E tar cf "${MSIX_OUTPUT_FILE}" --format=zip "${MSIX_PACKAGE_NAME}.msix"
        )

        # MSIXUpload Packaging Target
        add_custom_target(package_msixupload
            ${MSIXUPLOAD_COMMANDS}
            WORKING_DIRECTORY "${CMAKE_CURRENT_SOURCE_DIR}"
            COMMENT "Building and packaging Microsoft Store upload bundle: ${MSIX_OUTPUT_FILE}"
        )
    else()
        add_custom_target(package_msixupload
            COMMAND ${CMAKE_COMMAND} -E echo "Error: makeappx.exe not found. Please install the Windows 10/11 SDK to build the MSIX package."
            COMMAND ${CMAKE_COMMAND} -E false
            COMMENT "MSIXUpload packaging unavailable (makeappx.exe not found)"
        )
    endif()

    # 7. Portable ZIP Packaging Target
    set(PORTABLE_ZIP_FILE "${PACKAGE_OUTPUT_DIR}/mod-playerbots-portable-${PACKAGE_VERSION}.zip")

    find_program(SEVEN_ZIP_EXECUTABLE NAMES 7z 7za
        PATHS "C:/Program Files/7-Zip"
              "C:/Program Files (x86)/7-Zip"
              "$ENV{ProgramFiles}/7-Zip"
              "$ENV{ProgramFiles\(x86\)}/7-Zip"
        DOC "Path to 7-Zip executable"
    )

    if(SEVEN_ZIP_EXECUTABLE)
        message(STATUS "  7-Zip          : ${SEVEN_ZIP_EXECUTABLE} (Maximum Ultra Compression -mx=9)")

        add_custom_target(package_zip
            COMMAND ${CMAKE_COMMAND} -E make_directory "${PACKAGE_OUTPUT_DIR}"
            COMMAND ${CMAKE_COMMAND} -E rm -f "${PORTABLE_ZIP_FILE}"
            COMMAND "${SEVEN_ZIP_EXECUTABLE}" a -tzip -mx=9 -mmt=on -xr!*.pdb -xr!*.lib "${PORTABLE_ZIP_FILE}" "*"
            WORKING_DIRECTORY "${CMAKE_INSTALL_PREFIX_CLEAN}"
            COMMENT "Creating portable zip package with maximum compression: ${PORTABLE_ZIP_FILE}"
        )
    else()
        message(STATUS "  7-Zip          : NOT FOUND (falling back to cmake -E tar)")

        add_custom_target(package_zip
            COMMAND ${CMAKE_COMMAND} -E make_directory "${PACKAGE_OUTPUT_DIR}"
            COMMAND ${CMAKE_COMMAND} -E rm -f "${PORTABLE_ZIP_FILE}"
            COMMAND ${CMAKE_COMMAND} -E chdir "${CMAKE_INSTALL_PREFIX_CLEAN}" ${CMAKE_COMMAND} -E tar cf "${PORTABLE_ZIP_FILE}" --format=zip .
            WORKING_DIRECTORY "${CMAKE_CURRENT_SOURCE_DIR}"
            COMMENT "Creating portable zip package: ${PORTABLE_ZIP_FILE}"
        )
    endif()

    add_custom_target(package_portable
        DEPENDS package_zip
        COMMENT "Alias for package_zip target"
    )
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
