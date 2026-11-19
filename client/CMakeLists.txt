cmake_minimum_required(VERSION 3.13)
# Take the version from the repository's VERSION file when building inside the repository.
set(PSNR_VERSION 0.0.0)
if(EXISTS "${CMAKE_CURRENT_SOURCE_DIR}/../VERSION")
  file(STRINGS "${CMAKE_CURRENT_SOURCE_DIR}/../VERSION" PSNR_VERSION_TEXT LIMIT_COUNT 1)
  # project(VERSION) takes only digits and dots, so keep the leading x.y.z of e.g. 0.9.0-rc1.
  string(REGEX MATCH "^[0-9]+\\.[0-9]+\\.[0-9]+" PSNR_VERSION_MATCH "${PSNR_VERSION_TEXT}")
  if(PSNR_VERSION_MATCH)
    set(PSNR_VERSION "${PSNR_VERSION_MATCH}")
  endif()
endif()
project(psnr VERSION ${PSNR_VERSION} LANGUAGES C)

# The client needs POSIX clock calls, so keep compiler extensions on (gnu11).
add_library(psnr STATIC psnr.c psnr_app.c)
add_library(psnr::psnr ALIAS psnr)
set_target_properties(psnr PROPERTIES C_STANDARD 11 C_EXTENSIONS ON)
target_include_directories(psnr PUBLIC
  $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>
  $<INSTALL_INTERFACE:include>)
if(WIN32)
  target_link_libraries(psnr PUBLIC ws2_32)
endif()

install(TARGETS psnr EXPORT psnrTargets ARCHIVE DESTINATION lib)
install(FILES psnr.h psnr_app.h DESTINATION include)
install(EXPORT psnrTargets NAMESPACE psnr:: DESTINATION lib/cmake/psnr)
install(FILES psnrConfig.cmake DESTINATION lib/cmake/psnr)
