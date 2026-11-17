#!/usr/bin/env bash
# Cross-compiles macawlink_native.dll (x64) from Linux/macOS with zig (pip install ziglang).
# On Windows you can run the same command with zig, or build with MSVC (see README).
set -euo pipefail
cd "$(dirname "$0")"
ZIG="${ZIG:-python3 -m ziglang}"
OUT="${1:-../dist}"
mkdir -p "$OUT"
SRC="exports.cpp vcam_source.cpp ring_reader.cpp dshow_filter.cpp decoder.cpp audio.cpp"
FLAGS="-shared -O2 -std=c++17 -fno-exceptions -fno-rtti -Wall -Wno-unknown-pragmas -Wno-unused-function -DUNICODE -D_UNICODE"
LIBS="-lole32 -loleaut32 -lmfplat -lmf -lmfuuid -ladvapi32 -luser32 -lpropsys -lwinmm"
# 64-bit: used by macawlink.exe, Frame Server and 64-bit apps (Chrome, Edge, Teams, Zoom, OBS).
$ZIG c++ -target x86_64-windows-gnu $FLAGS $SRC macawlink_native.def $LIBS -o "$OUT/macawlink_native.dll"
echo "built $OUT/macawlink_native.dll"
# 32-bit: only so 32-bit apps on Windows 10 can see the DirectShow camera.
$ZIG c++ -target x86-windows-gnu $FLAGS $SRC macawlink_native32.def $LIBS -o "$OUT/macawlink_native32.dll"
echo "built $OUT/macawlink_native32.dll"
