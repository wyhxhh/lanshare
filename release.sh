#!/usr/bin/env bash
# LanShare 一键发布：release 构建 → 归档到 dist/ → SHA256 → 生成分发 zip。
# 用法：bash release.sh
set -euo pipefail
cd "$(dirname "$0")"
source ./build-env.sh

VERSION="0.2.1"
DIST="dist"
EXE_NAME="LanShare-v${VERSION}-win64.exe"

echo "==> 1/4 release 构建 v${VERSION}（无控制台 GUI 单文件）"
go build -trimpath -ldflags "-s -w -H windowsgui" -o LanShare.exe .
ls -la LanShare.exe

mkdir -p "$DIST"
echo "==> 2/4 归档 $EXE_NAME"
cp -f LanShare.exe "$DIST/$EXE_NAME"

echo "==> 3/4 SHA256"
( cd "$DIST" && sha256sum "$EXE_NAME" > "$EXE_NAME.sha256" )
cat "$DIST/$EXE_NAME.sha256"

echo "==> 4/4 生成分发 zip（含 exe 与使用说明）"
python - "$DIST" "$EXE_NAME" << 'PY'
import sys, zipfile, os, hashlib

dist, exe = sys.argv[1], sys.argv[2]
exe_path = os.path.join(dist, exe)
zip_path = os.path.join(dist, os.path.basename(exe).replace(".exe", ".zip"))

def sha(p):
    h = hashlib.sha256()
    with open(p, "rb") as f:
        for c in iter(lambda: f.read(1 << 20), b""):
            h.update(c)
    return h.hexdigest()

with zipfile.ZipFile(zip_path, "w", zipfile.ZIP_DEFLATED) as z:
    z.write(exe_path, "LanShare.exe")
    z.write("使用说明.txt", "使用说明.txt")

with open(zip_path + ".sha256", "w", encoding="utf-8") as f:
    f.write(sha(zip_path) + "  " + os.path.basename(zip_path) + "\n")

print("zip:", zip_path, f"({os.path.getsize(zip_path) / 1024 / 1024:.1f} MB)")
print("zip sha256:", sha(zip_path))
PY

echo "==> 完成。dist/ 内容："
ls -la "$DIST"
