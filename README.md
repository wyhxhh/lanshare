<p align="center">
  <img src="assets/icon_raw/app-1024.png" width="96" alt="LanShare"/>
</p>

# LanShare

在局域网里传文件的小工具。一台电脑上选定目录、把服务开起来，同一 WiFi 下的手机和电脑用浏览器打开地址就能浏览、下载，不用装任何客户端。

Windows 单文件 exe，Go + Fyne 写的桌面程序。网页模板和图标都打进 exe 里，拷走一个文件就能用，不装运行时，也不写系统 PATH。

## 功能

- 目录浏览、文件下载，支持断点续传（HTTP Range）
- 文件夹整包下载：服务端边读边打 ZIP，中文文件名不乱码，超大目录自动走 Zip64
- 可选访问密码；不设的话局域网内任何人可读
- 只读共享，没有上传、删除接口，别人动不了你目录里的东西
- 关窗进系统托盘继续共享，程序单实例运行
- 共享目录、端口、密码会自动记住，下次启动还是那套配置

并发上压测过：32 个客户端共 256 个请求零失败，本机回环约 80 MB/s。实际瓶颈基本在网卡和 WiFi，不在这个程序。

## 下载与构建

仓库不含编译产物，直接用现成的 exe 请看 [Releases](../../releases)。

自己构建需要 Windows 上的 [Go](https://go.dev/dl/)（≥ 1.27）和 MinGW-w64 gcc —— Fyne 在 Windows 依赖 CGO（glfw/OpenGL）。图标资源（`.syso`）已随仓库提交，克隆下来直接 build 即可：

```bash
git clone https://github.com/wyhxhh/lanshare.git
cd lanshare
go build -trimpath -ldflags "-s -w -H windowsgui" -o LanShare.exe .
```

跑测试：

```bash
go test ./...
# 并发压测（默认跳过，不影响日常回归）：
LSH_LOAD=1 go test ./internal/server/ -run TestConcurrentMixedLoad -v
```

## 使用

1. 双击 LanShare.exe；
2. 「浏览…」选要共享的目录（Windows 原生目录框）；
3. 想改端口、设密码的话顺手填一下；
4. 点「启动服务」，把「访问地址」里形如 `http://192.168.x.x:8000` 的地址发给同事，或用手机浏览器打开。

更详细的图文说明（含防火墙放行）在分发包的「使用说明.txt」里。

## 安全上的处理

共享出去的目录别人能读到，路径这块能挡的都挡了：

- `..`、盘符、UNC、`\?\`、Windows 保留名、尾部点空格、符号链接逃逸——入口统一校验，越界一律 404
- 隐藏文件不进列表，直接拿路径猜也访问不到
- 访问密码只是「门槛」性质：明文 HTTP，面向可信局域网；要暴露到公网，请自己在前面加反向代理或 VPN

## 代码结构

```
lanshare/
├── main.go                      # 入口，启动 GUI
├── rsrc_windows_amd64.syso      # exe 图标资源（go-winres 生成，随仓库提交）
├── internal/
│   ├── gui/                     # Fyne 桌面界面
│   │   ├── gui.go               #   主窗口、启停、状态刷新
│   │   ├── theme.go             #   主题色
│   │   ├── fonts.go             #   运行时加载系统字体（中文显示）
│   │   ├── panel.go             #   卡片、胶囊等小组件
│   │   ├── config.go            #   配置持久化
│   │   ├── netaddr.go           #   局域网地址枚举（过滤虚拟网卡）
│   │   ├── pickdir_windows.go   #   Windows 原生目录选择框
│   │   ├── single_win.go        #   单实例互斥（其他平台见 *_other.go）
│   │   └── icon/                #   内嵌应用图标
│   └── server/                  # HTTP 共享核心，与 GUI 解耦，可独立测试
│       ├── server.go            #   路由、统计、访问日志
│       ├── pathutil.go          #   路径归一化与安全校验
│       ├── list.go              #   目录列表、下载响应头
│       ├── zip.go               #   流式目录 ZIP
│       ├── auth.go              #   访问密码
│       └── *_test.go            #   常规测试 + 路径攻击矩阵 + 并发压测
├── assets/                      # go:embed 内嵌资源
│   ├── templates/               #   网页模板（列表页 / 登录页）
│   ├── icon/                    #   app.ico（exe 资源用）
│   ├── icon_raw/                #   图标源 + build_icons.py 绘制脚本
│   └── README.md
├── build.sh                     # 开发/发布构建
├── release.sh                   # 一键发布：构建 + 归档 + SHA256 + 分发 zip
└── 使用说明.txt                  # 面向终端用户的分发文档
```

## License

[MIT](LICENSE)
