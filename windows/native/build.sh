#!/usr/bin/env bash
# Cross-compiles macawlink_native.dll (x64) from Linux/macOS with zig (pip install ziglang).
# On Windows you can run the same command with zig, or build with MSVC (see README).
set -euo pipefail
cd "$(dirname "$0")"
ZIG="${ZIG:-python3 -m ziglang}"
OUT="${1:-../dist}"
mkdir -p "$OUT"
$ZIG c++ -target x86_64-windows-gnu -shared -O2 -std=c++17 \
  -fno-exceptions -fno-rtti -Wall -Wno-unknown-pragmas -Wno-unused-function \
  -DUNICODE -D_UNICODE \
  exports.cpp vcam_source.cpp decoder.cpp audio.cpp macawlink_native.def \
  -lole32 -loleaut32 -lmfplat -lmf -lmfuuid -ladvapi32 -luser32 -lpropsys \
  -o "$OUT/macawlink_native.dll"
echo "built $OUT/macawlink_native.dll"
