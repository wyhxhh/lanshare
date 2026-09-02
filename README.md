<p align="center">
  <img src="assets/icon_raw/app-1024.png" width="120" alt="LanShare icon"/>
</p>

<h1 align="center">LanShare · 局域网文件共享</h1>

<p align="center">
  单文件 exe 的局域网文件共享工具 —— 原生桌面 GUI + 系统托盘，
  同一 WiFi 下的手机 / 电脑用浏览器打开即可浏览与下载。
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT"/></a>
  <img src="https://img.shields.io/badge/Go-1.27+-00ADD8.svg" alt="Go 1.27+"/>
  <img src="https://img.shields.io/badge/UI-Fyne%20v2-2F6BFF.svg" alt="Fyne v2"/>
  <img src="https://img.shields.io/badge/platform-Windows%20amd64-17A05C.svg" alt="Platform"/>
</p>

## 简介

LanShare 是一款面向局域网场景的**只读文件共享**工具：选定一个目录即可启动共享，
其他设备通过浏览器访问 `http://<本机IP>:端口`，目录浏览、文件下载、
整目录 ZIP 打包、断点续传一次搞定。**无需安装任何客户端、无需登录、无需上传权限**。

采用 Go + [Fyne](https://fyne.io/) v2 构建，交付为**单个自包含 exe**
（网页模板与图标全部内嵌），双击即用，不写系统 PATH、不装运行时。

## 功能特性

- 📁 **目录浏览**：网页文件列表，面包屑导航，隐藏文件过滤
- ⬇️ **文件下载**：支持 HTTP Range **断点续传**（下载工具/浏览器均可续传）
- 📦 **整目录 ZIP**：流式打包下载，中文文件名编码正确，Zip64 自动启用
- 🔒 **可选访问密码**：全局访问口令，未授权访问重定向到登录页
- 🚫 **只读共享**：仅下载不提供上传，从机制上杜绝被写坏
- 👥 **多人高并发**：压测 32 客户端 × 8 任务 = 256 请求 **0 失败**，
  回环吞吐约 80 MB/s（瓶颈在网卡/WiFi 而非服务端）
- 🖥️ **原生 GUI**：Fyne 桌面界面，字段上置标签、实时指标（累计发送/活跃请求）、访问日志面板
- 🧭 **系统托盘**：关窗不退出，常驻托盘，菜单可打开地址/退出
- 🛡️ **多层安全防护**：路径穿越（`../`、盘符、UNC）、Windows 保留名、
  尾部点空格、符号链接逃逸、隐藏文件，全部在入口拦截并 404/403
- 💾 **配置持久化**：目录 / 端口 / 密码自动记忆（`%APPDATA%\LanShare\config.json`）
- 🔂 **单实例互斥**：二次启动弹「已在运行」提示，不会双开
- 🎨 **品牌图标**：程序化绘制的应用图标，窗口 / 任务栏 / 托盘 / exe 文件四端统一

## 下载

源码仓库不含编译产物。正式发布包（exe + SHA256 + 分发 zip）请见
[Releases](../../releases) 页面；也支持自行构建（见下）。

## 构建

### 前置条件

| 依赖 | 说明 |
|---|---|
| Go ≥ 1.27 | 开发语言 |
| MinGW-w64 gcc | Windows 下 Fyne 依赖 CGO（glfw/OpenGL），需 `CGO_ENABLED=1` |
| go-winres（可选） | 仅在需要重新生成 exe 图标资源时使用；仓库已提交生成好的 `.syso` |

> `go.mod` 的 `go 1.27.1` 为开发环境版本；使用较新 Go 一般可直接编译。

### 步骤

```bash
git clone https://github.com/wyhxhh/lanshare.git && cd lanshare

# 已提交 rsrc_windows_amd64.syso，直接构建即可（exe 自带图标）
go build -trimpath -ldflags "-s -w -H windowsgui" -o LanShare.exe .

# 若修改了图标需要重新生成资源（可选）：
#   go install github.com/tc-hib/go-winres@latest
#   go-winres simply --icon assets/icon/app.ico
```

### 测试

```bash
go test ./...                       # 全量单元 + 集成测试
LSH_LOAD=1 go test ./internal/server/ -run TestConcurrentMixedLoad -v   # 并发压测
```

## 使用

1. 双击 `LanShare.exe` 启动；
2. 「共享设置」里点「浏览…」选择要共享的目录（调用 **Windows 原生文件夹选择框**）；
3. 点「启动服务」，记下「访问地址」卡片中的局域网地址；
4. 手机 / 另一台电脑连同一 WiFi，浏览器打开该地址即可浏览下载。

详细图文说明（含防火墙放行步骤）见分发包内的 [使用说明.txt](使用说明.txt)。

## 安全模型

- **只读**：服务端不提供任何上传 / 删除 / 改名接口；
- **路径校验**：所有请求路径经 `cleanSegments` 归一化 + 真实路径前缀校验
  （`EvalSymlinks` + 大小写不敏感 `withinPrefix`），目录外访问一律 404；
- **隐藏过滤**：Windows 隐藏文件 / 隐藏目录不出现在列表，直接请求同样 404；
- **明文 HTTP 与密码取舍**：定位为可信局域网内共享，密码仅作访问门槛
  （明文传输）；如需公网暴露请自行加反向代理 / VPN。

## 项目结构

```
lanshare/
├── main.go                      # 入口：启动 GUI
├── internal/
│   ├── gui/                     # Fyne 桌面界面
│   │   ├── gui.go               #   主窗口布局 / 启停 / 刷新循环
│   │   ├── theme.go             #   品牌浅色主题（颜色 / 字号 / 中文字体）
│   │   ├── panel.go             #   卡片 / 胶囊 / 字段块等视觉组件
│   │   ├── pickdir_windows.go   #   Windows 原生目录选择框（SHBrowseForFolderW）
│   │   ├── single_win.go        #   单实例互斥（CreateMutexW）
│   │   ├── netaddr.go           #   局域网 IPv4 枚举（过滤虚拟网卡）
│   │   ├── config.go            #   配置持久化
│   │   └── icon.go              #   应用图标内嵌（go:embed）
│   └── server/                  # HTTP 共享核心（纯 net/http，可独立测试）
│       ├── server.go            #   路由 / 统计 / 访问日志
│       ├── pathutil.go          #   路径安全归一化与校验
│       ├── list.go              #   目录列表页渲染 / 下载响应头
│       ├── zip.go               #   流式整目录 ZIP
│       ├── auth.go              #   访问密码
│       └── *_test.go            #   14+ 项测试（含攻击矩阵与并发压测）
├── assets/
│   ├── templates/               # 网页模板（列表页 / 登录页，go:embed）
│   ├── icon/                    # exe 资源图标（多尺寸 .ico）
│   ├── icon_raw/                # 图标源（1024 PNG + 程序化绘制脚本）
│   └── embed.go                 # go:embed 资源入口
├── build.sh                     # 一键构建（dev / release）
├── release.sh                   # 一键发布（构建 + 归档 + SHA256 + 分发 zip）
└── 使用说明.txt                  # 面向终端用户的分发文档
```

## 许可

[MIT](LICENSE) © 2026 LanShare Contributors
