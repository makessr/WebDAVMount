# WebDAVMount

Windows x64 多网盘挂载客户端。将 WebDAV、SFTP、FTP、S3 兼容存储及支持的网盘挂载为 Windows 盘符，再用资源管理器访问文件。

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

## 许可

WebDAVMount 自有源码采用 [Apache-2.0](LICENSE)，署名见 [NOTICE](NOTICE)。第三方组件适用各自的[许可和署名](build/portable/THIRD_PARTY_NOTICES.txt)。

项目主页与联系入口见 [SALANGHE](https://www.salanghe.com/)。