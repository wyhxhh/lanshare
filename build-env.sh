#!/usr/bin/env bash
# LanShare 构建环境 —— 全部使用隔离目录工具链，不污染系统 PATH。
# 用法：在 bash 中 `source ./build-env.sh` 后即可直接使用 go / gcc。
#
# ⚠️ 注意：本脚本面向【作者本机】的隔离工具链布局（路径硬编码），
#    仅 build.sh / release.sh 等本地脚本 source 它。
#    普通用户克隆后不需要此脚本 —— 安装官方 Go + MinGW-w64 gcc 后
#    直接 `go build` 即可（见 README「构建」章节）。
#
# 工具链布局（均装在 C:\Users\Administrator\.workbuddy\binaries\ 隔离目录）：
#   go/          Go 1.27.1（解压即用）
#   msys64/      msys2 环境（ucrt64/bin 内为 MinGW-w64 gcc，Fyne 的 CGO 依赖）
#   gopath/      Go 模块缓存（避免写入用户默认 GOPATH）

export LSH_GO="/c/Users/Administrator/.workbuddy/binaries/go/go/bin"
export LSH_GCC="/c/Users/Administrator/.workbuddy/binaries/msys64/ucrt64/bin"
export LSH_MSYS="/c/Users/Administrator/.workbuddy/binaries/msys64/usr/bin"

export PATH="$LSH_GO:$LSH_GCC:$PATH"

# Go 依赖走国内代理；模块缓存隔离到 binaries/gopath
export GOPROXY="https://goproxy.cn,direct"
export GOPATH="C:/Users/Administrator/.workbuddy/binaries/gopath"

# Fyne 在 Windows 需要 CGO 调用 glfw（OpenGL 窗口）
export CGO_ENABLED=1

# msys2 的 gcc 可执行名与 PATH 一致，无需 CC 别名
export CC="gcc"
