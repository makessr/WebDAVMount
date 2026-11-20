# WebDAVMount

## 源码目录

| 目录 | 内容 |
| --- | --- |
| [desktop/windows](desktop/windows) | Windows 挂载、托盘、连接管理和更新 |
| [frontend](frontend) | Windows 客户端界面 |
| [linux](linux) | Linux GTK 客户端与 Debian 打包 |
| [internal](internal) | Go 内部组件，包括挂载、配置与更新 |
| [cmd](cmd) | 独立命令行工具 |
| [build](build)、[scripts](scripts)、[.github/workflows](.github/workflows) | 安装模板、构建脚本和 CI |
| [docs](docs/README.md) | 开发与验证说明 |

根目录的 `main.go` 加载前端资源并启动 Windows 客户端，仍可从根目录运行 `go build .`。

Windows CI 优先使用运行器已有的兼容 Go 补丁版本。Go 模块和 npm 按依赖清单缓存，Go 编译缓存另按工具链版本区分，两个 Go 缓存均放在运行器临时目录。Debian CI 复用已下载的安装包，并在每次验收时刷新 APT 索引。Debian 缓存键跟随验收脚本，Windows CI 改动可继续使用已有的 Debian 缓存。


Windows x64 多网盘挂载客户端。将 WebDAV、SFTP、FTP、S3 兼容存储及支持的网盘挂载为 Windows 盘符，再用资源管理器访问文件。

## Linux

**MountBridge · SALANGHE** 是本项目的 Debian／XFCE 桌面版。它使用 rclone 和 FUSE，将网盘挂载为只读目录。参见 [Linux 安装与使用](linux/README.md) 和 [Linux 发行页](https://github.com/makessr/WebDAVMount/releases/tag/linux-v0.1.0-preview.1)。


## 安装

1. 安装 [WinFsp](https://github.com/winfsp/winfsp/releases)。系统缺少 WebView2 时，安装 [Microsoft Edge WebView2 Runtime](https://developer.microsoft.com/microsoft-edge/webview2/)。
2. 从 [GitHub Releases](https://github.com/makessr/WebDAVMount/releases/latest) 下载 Windows 安装包，或解压便携包后运行 WebDAVMount.exe。
3. 添加网盘连接，选择盘符并挂载。关闭窗口后可在托盘继续使用。

0.72.7 的在线更新优先使用 gh-proxy.com 和 ghproxy.net，GitHub 直连作为备用线路。客户端先校验更新清单签名，再核对安装包的 SHA-256。仍使用旧更新地址的版本需要手动安装 0.72.7，之后可通过客户端检查更新。

## 构建

在 Windows x64 上安装与 go.mod 匹配的 Go 工具链及 Node.js，再从仓库根目录运行以下命令。

    npm --prefix frontend ci
    npm --prefix frontend run build
    go build -trimpath -tags production -ldflags "-H windowsgui" -o WebDAVMount.exe .

运行时需要相邻的 rclone.exe。发行包附带的引擎及构建说明位于便携包中，精确版本的第三方源码在同一 Releases 版次的依赖源码包中。

## 贡献与发布

欢迎提交 Issue 和 Pull Request。较大改动请先通过 Issue 对齐范围。每次提交同步更新 README，PR 标题和说明使用中文，注明用户可见变化和已执行的验证。

Windows 提交前运行 `bun run check`，Linux 运行 `bun run check:linux`。发行时用 `bun run release:check <标签>` 核对 main 的 CI。详细流程见 [CONTRIBUTING.md](CONTRIBUTING.md)。


GitHub Actions 分别执行 Windows、Linux 和贡献规范检查。main 合并前必须通过三项检查。

Linux CI 验证 Debian 包安装、GTK 启动和读取 rclone 配置。Linux 发行前还需在支持 FUSE 的 Debian 环境实测挂载。

## 许可

WebDAVMount 自有源码采用 [Apache-2.0](LICENSE)，署名见 [NOTICE](NOTICE)。第三方组件适用各自的[许可和署名](build/portable/THIRD_PARTY_NOTICES.txt)。

项目主页与联系入口见 [SALANGHE](https://www.salanghe.com/)。