# MountBridge · SALANGHE

Debian／XFCE 桌面版通过 rclone 和 FUSE 将网盘挂载为只读目录。
支持范围取决于本机安装的 rclone，可配置 WebDAV、SFTP、S3 兼容存储及 rclone 提供的其他后端。

## 安装与使用

AMD64（x64）与 ARM64 共用同一个 `_all.deb` 安装包，APT 按本机架构安装 rclone 和 FUSE。

1. 下载本项目的 `salanghe-mountbridge_0.1.0.preview1_all.deb`。在下载目录运行 `sudo apt install ./salanghe-mountbridge_0.1.0.preview1_all.deb`，由 apt 安装 rclone、FUSE 3 和 GTK 依赖。
2. 用普通用户从 XFCE 应用菜单打开 **MountBridge**。点击「配置网盘」进入 rclone 交互配置，完成后回到软件点击「刷新」。已有 rclone 配置可直接读取。
3. 点击「挂载」和「打开目录」，通过 Thunar 或系统默认文件管理器访问。关闭应用窗口会卸载由本应用管理的目录。卸载失败时请关闭正在使用挂载目录的程序。挂载引擎异常退出时，客户端会显示「挂载已中断」。如有残留挂载，先点「卸载」，再重新挂载。

挂载目录位于 `${XDG_DATA_HOME:-~/.local/share}/com.salanghe.MountBridge/mounts/`。凭据由 rclone 管理。已有加密配置需要按 rclone 的方式向应用提供解密密码。

Linux 预览通过 GitHub Releases 更新。Windows 安装包和在线更新使用 Windows 发行渠道。

## 从源码构建

在 Debian 上从仓库根目录运行 `python3 linux/package.py --output dist-linux`。依赖由 Debian 软件源提供。应用本身采用 Apache-2.0，署名见仓库根目录的 NOTICE。

## 验收实际挂载

在有 FUSE 的 Debian 环境安装候选包后，从仓库根目录以普通用户运行 `python3 linux/smoke_test.py`。测试使用本机临时 WebDAV 和 SFTP 服务，覆盖同时挂载、读取、只读写入拒绝、单独卸载、重新挂载及关闭窗口卸载。命令需要可用的 GTK 会话。

使用 Docker 在隔离 Debian 中复测时，从仓库根目录运行以下命令。测试容器需要宿主提供 `/dev/fuse`。

```bash
docker run --rm --device /dev/fuse --cap-add SYS_ADMIN \
  --mount "type=bind,source=$PWD,target=/source,readonly" debian:bookworm-slim \
  bash /source/scripts/ci-debian-smoke.sh --fuse
```

Docker 的默认 AppArmor 配置会拒绝容器内挂载。启用该配置的测试宿主可为上面的隔离容器加上 `--security-opt apparmor=unconfined`。托管 CI 在原生 AMD64 和 ARM64 的 Ubuntu 虚拟机内以普通用户验证实际 FUSE 挂载，另在 Debian 容器验证安装和启动。
