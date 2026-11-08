#!/usr/bin/env bash
# 构建 Windows 图形版单文件 exe（在 Mac 上交叉编译，需 mingw-w64）。
# 用法：./scripts/build-windows.sh [输出名，缺省 dist/cs2coach-gui.exe]
set -euo pipefail
cd "$(dirname "$0")/.."

OUT="${1:-dist/cs2coach-gui.exe}"
if ! command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1; then
  echo "缺少交叉工具链，先执行：brew install mingw-w64" >&2
  exit 1
fi

mkdir -p "$(dirname "$OUT")"
echo "编译资源（图标/版本信息/清单）…"
x86_64-w64-mingw32-windres -i cmd/cs2coach-gui/app.rc -o cmd/cs2coach-gui/app.syso -O coff
echo "构建 Windows exe …"
GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc \
  go build -trimpath -ldflags="-H windowsgui" -o "$OUT" ./cmd/cs2coach-gui
ls -la "$OUT"
echo "完成：${OUT}（双击即用；目标机需 WebView2，Win10/11 自带）"
