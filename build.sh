#!/usr/bin/env bash
# LanShare 一键构建脚本
# 用法：
#   ./build.sh            # dev 构建：带控制台窗口，便于调试输出
#   ./build.sh release    # release 构建：隐藏控制台（真正的 GUI 单文件 exe）
set -euo pipefail
cd "$(dirname "$0")"
source ./build-env.sh

MODE="${1:-dev}"
LDFLAGS="-s -w"
if [ "$MODE" = "release" ]; then
    LDFLAGS="$LDFLAGS -H windowsgui"
    echo "==> 构建模式: release (GUI 无控制台)"
else
    echo "==> 构建模式: dev (带控制台)"
fi

echo "==> 使用工具链: $(go version | awk '{print $3}') + $(gcc --version | head -1 | awk '{print $1,$2,$3}')"
go build -trimpath -ldflags "$LDFLAGS" -o LanShare.exe .
echo "==> 完成: $(pwd)/LanShare.exe ($(du -h LanShare.exe | cut -f1))"
