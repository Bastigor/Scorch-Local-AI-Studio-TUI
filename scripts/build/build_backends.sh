#!/usr/bin/env bash
set -euo pipefail

OUT_DIR="${1:-./studio-data/bin}"
BACKEND="${2:-cpu}"
mkdir -p "$OUT_DIR"

LLAMA_DIR="${LLAMA_CPP_DIR:-/tmp/llama.cpp}"
SD_DIR="${SD_CPP_DIR:-/tmp/stable-diffusion.cpp}"

if [ ! -d "$LLAMA_DIR" ]; then
  git clone --depth 1 https://github.com/ggml-org/llama.cpp.git "$LLAMA_DIR"
fi
if [ ! -d "$SD_DIR" ]; then
  git clone --depth 1 https://github.com/leejet/stable-diffusion.cpp.git "$SD_DIR"
fi

CMAKE_FLAGS=(-DCMAKE_BUILD_TYPE=Release)
case "$BACKEND" in
  cuda)
    CMAKE_FLAGS+=(-DGGML_CUDA=ON -DSD_CUDA=ON)
    ;;
  vulkan)
    CMAKE_FLAGS+=(-DGGML_VULKAN=ON -DSD_VULKAN=ON)
    ;;
  metal)
    CMAKE_FLAGS+=(-DGGML_METAL=ON -DSD_METAL=ON)
    ;;
esac

cmake -S "$LLAMA_DIR" -B "$LLAMA_DIR/build" "${CMAKE_FLAGS[@]}" -DLLAMA_SERVER=ON
cmake --build "$LLAMA_DIR/build" --target llama-server -j"$(nproc 2>/dev/null || sysctl -n hw.ncpu)"

cmake -S "$SD_DIR" -B "$SD_DIR/build" "${CMAKE_FLAGS[@]}"
cmake --build "$SD_DIR/build" -j"$(nproc 2>/dev/null || sysctl -n hw.ncpu)"

find "$LLAMA_DIR/build" -name "llama-server" -type f -exec cp {} "$OUT_DIR/" \;
find "$SD_DIR/build" -name "sd" -type f -exec cp {} "$OUT_DIR/" \;

echo "Binários instalados em $OUT_DIR"
