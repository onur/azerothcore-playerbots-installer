# CleanMySQLDist.cmake - Strips PDB debug symbols, static libraries, and development files from MySQL dist directory

if(NOT DEFINED MYSQL_DIST_DIR OR NOT EXISTS "${MYSQL_DIST_DIR}")
    message(STATUS "CleanMySQLDist: Target directory does not exist or not specified: ${MYSQL_DIST_DIR}")
    return()
endif()

message(STATUS "CleanMySQLDist: Pruning debug symbols and development files from: ${MYSQL_DIST_DIR}")

# 1. Remove all PDB debug symbol files
file(GLOB_RECURSE PDB_FILES "${MYSQL_DIST_DIR}/*.pdb")
if(PDB_FILES)
    list(LENGTH PDB_FILES PDB_COUNT)
    message(STATUS "CleanMySQLDist: Removing ${PDB_COUNT} .pdb files...")
    file(REMOVE ${PDB_FILES})
endif()

# 2. Remove static libraries (*.lib), export files (*.exp), and linker state files (*.ilk)
file(GLOB_RECURSE BUILD_FILES
    "${MYSQL_DIST_DIR}/*.lib"
    "${MYSQL_DIST_DIR}/*.exp"
    "${MYSQL_DIST_DIR}/*.ilk"
)
if(BUILD_FILES)
    list(LENGTH BUILD_FILES BUILD_COUNT)
    message(STATUS "CleanMySQLDist: Removing ${BUILD_COUNT} .lib/.exp/.ilk build files...")
    file(REMOVE ${BUILD_FILES})
endif()

# 3. Remove development-only directories: include headers, documentation, debug plugins
set(DEV_DIRS
    "${MYSQL_DIST_DIR}/include"
    "${MYSQL_DIST_DIR}/docs"
    "${MYSQL_DIST_DIR}/lib/plugin/debug"
)

foreach(DEV_DIR IN LISTS DEV_DIRS)
    if(EXISTS "${DEV_DIR}")
        message(STATUS "CleanMySQLDist: Removing directory ${DEV_DIR}...")
        file(REMOVE_RECURSE "${DEV_DIR}")
    endif()
endforeach()

message(STATUS "CleanMySQLDist: MySQL dist directory cleanup complete.")
