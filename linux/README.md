# MountBridge · SALANGHE

Debian／XFCE 桌面版通过 rclone 和 FUSE 将网盘挂载为只读目录。
支持范围取决于本机安装的 rclone，可配置 WebDAV、SFTP、S3 兼容存储及 rclone 提供的其他后端。

## 安装与使用

1. 下载本项目的 `salanghe-mountbridge_0.1.0.preview1_all.deb`。在下载目录运行 `sudo apt install ./salanghe-mountbridge_0.1.0.preview1_all.deb`，由 apt 安装 rclone、FUSE 3 和 GTK 依赖。
2. 用普通用户从 XFCE 应用菜单打开 **MountBridge**。点击「配置网盘」进入 rclone 交互配置，完成后回到软件点击「刷新」。已有 rclone 配置可直接读取。
3. 点击「挂载」和「打开目录」，通过 Thunar 或系统默认文件管理器访问。关闭应用窗口会卸载由本应用管理的目录。卸载失败时请关闭正在使用挂载目录的程序。

挂载目录位于 `${XDG_DATA_HOME:-~/.local/share}/com.salanghe.MountBridge/mounts/`。凭据由 rclone 管理。已有加密配置需要按 rclone 的方式向应用提供解密密码。

Linux 预览通过 GitHub Releases 更新。Windows 安装包和在线更新使用 Windows 发行渠道。

## 从源码构建

在 Debian 上从仓库根目录运行 `python3 linux/package.py --output dist-linux`。依赖由 Debian 软件源提供。应用本身采用 Apache-2.0，署名见仓库根目录的 NOTICE。
