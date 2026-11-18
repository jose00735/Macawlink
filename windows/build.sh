#!/usr/bin/env bash
# Builds the Windows receiver bundle into windows/dist/:
#   macawlink.exe, macawlink_native.dll, adb (platform-tools), icon, NOTICE.
# Works on Linux/macOS (cross-compiles) and on Windows (Git Bash) with Go + zig installed:
#   pip install ziglang   # provides the C++ cross-compiler for the native DLL
set -euo pipefail
cd "$(dirname "$0")"
DIST="${DIST:-dist}"
mkdir -p "$DIST"
DIST="$(cd "$DIST" && pwd)"

echo "==> tests"
go vet ./...
go test ./...

echo "==> native DLL"
./native/build.sh "$DIST"
rm -f "$DIST"/*.pdb "$DIST"/*.lib

echo "==> macawlink.exe"
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -H windowsgui" -o "$DIST/macawlink.exe" ./cmd/macawlink
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o "$DIST/fakephone.exe" ./cmd/fakephone
cp ../assets/icon/macawlink.ico "$DIST/"

echo "==> adb (Android platform-tools for Windows)"
if [ ! -f "$DIST/adb.exe" ]; then
  tmp=$(mktemp -d)
  curl -fsSL -o "$tmp/pt.zip" https://dl.google.com/android/repository/platform-tools-latest-windows.zip
  unzip -q -j "$tmp/pt.zip" platform-tools/adb.exe platform-tools/AdbWinApi.dll platform-tools/AdbWinUsbApi.dll platform-tools/NOTICE.txt -d "$tmp/pt"
  cp "$tmp/pt/adb.exe" "$tmp/pt/AdbWinApi.dll" "$tmp/pt/AdbWinUsbApi.dll" "$DIST/"
  cp "$tmp/pt/NOTICE.txt" "$DIST/NOTICE-adb.txt"
  rm -rf "$tmp"
fi
cat > "$DIST/NOTICE.txt" <<'EOF'
Macawlink — use your Android phone as a webcam and microphone.
adb.exe, AdbWinApi.dll and AdbWinUsbApi.dll are from the Android SDK Platform-Tools
(Android Open Source Project, Apache License 2.0); see NOTICE-adb.txt.
EOF
echo "==> MacawlinkSetup.exe"
VERSION=$(sed -n 's/^const Version = "\(.*\)"/\1/p' internal/app/app.go)
rm -f cmd/setup/payload.zip
(cd "$DIST" && zip -q -9 "$OLDPWD/cmd/setup/payload.zip" macawlink.exe macawlink_native.dll macawlink_native32.dll \
  adb.exe AdbWinApi.dll AdbWinUsbApi.dll macawlink.ico NOTICE.txt NOTICE-adb.txt)
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -H windowsgui" -o "$DIST/../MacawlinkSetup-$VERSION.exe" ./cmd/setup
rm -f cmd/setup/payload.zip
ls -la "$DIST" "$DIST/../MacawlinkSetup-$VERSION.exe"
