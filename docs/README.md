# 开发文档

Windows 桌面实现位于 [desktop/windows](../desktop/windows)，界面位于 [frontend](../frontend)。桌面入口嵌入前端构建产物，先运行 `bun run check`，再运行 `go build .`。定向验证桌面包时，从仓库根目录运行 `go test ./desktop/windows`。

Linux GTK 客户端和 Debian 打包脚本位于 [linux](../linux)。在 Debian 环境可以运行 `bun run check:linux`。

跨模块组件放在 [internal](../internal)，独立命令放在 [cmd](../cmd)。修改构建路径时，请同时检查 [scripts](../scripts)、[build](../build) 和 [CI 工作流](../.github/workflows)。

提交与发布步骤见 [贡献指南](../CONTRIBUTING.md)。
