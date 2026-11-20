"""GTK desktop entry for MountBridge by SALANGHE."""

import os
import subprocess
import sys
import threading

import gi

gi.require_version("Gtk", "3.0")
from gi.repository import Gio, GLib, Gtk, Pango

from mounts import APP_ID, RCLONE, Mounts, list_remotes


class Window(Gtk.ApplicationWindow):
    def __init__(self, application, mounts):
        super().__init__(application=application, title="MountBridge · SALANGHE")
        self.set_icon_name(APP_ID)
        self.set_default_size(640, 410)
        self.set_border_width(14)
        self.mounts = mounts
        self.remotes = []
        self.last_view = None
        self.closing = False
        self.loading = False
        self.refresh_timer = GLib.timeout_add(450, self.refresh)
        self.connect("delete-event", self.close_requested)
        self.connect("destroy", self.destroyed)

        layout = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=10)
        toolbar = Gtk.Box(spacing=8)
        title = Gtk.Label(label="网盘挂载 · 只读", xalign=0)
        toolbar.pack_start(title, True, True, 0)
        self.button(toolbar, "配置网盘", self.configure)
        self.reload_button = self.button(toolbar, "刷新", self.load_remotes)
        layout.pack_start(toolbar, False, False, 0)
        scroll = Gtk.ScrolledWindow()
        self.rows = Gtk.ListBox(selection_mode=Gtk.SelectionMode.NONE)
        scroll.add(self.rows)
        layout.pack_start(scroll, True, True, 0)
        self.status = Gtk.Label(xalign=0, wrap=True)
        layout.pack_start(self.status, False, False, 0)
        self.add(layout)
        self.show_all()
        self.load_remotes()

    def button(self, box, label, action, *args):
        button = Gtk.Button(label=label)
        button.connect("clicked", action, *args)
        box.pack_start(button, False, False, 0)
        return button

    def configure(self, _button):
        try:
            subprocess.Popen(["/usr/bin/x-terminal-emulator", "-e", RCLONE, "config"])
            self.status.set_text("在终端配置网盘后，回到此处点击「刷新」。")
        except OSError:
            self.status.set_text("无法打开终端。可在 XFCE 终端运行 rclone config。")

    def load_remotes(self, _button=None):
        if self.loading:
            return
        self.loading = True
        self.reload_button.set_sensitive(False)
        self.status.set_text("正在读取 rclone 网盘…")

        def worker():
            try:
                GLib.idle_add(self.remotes_loaded, list_remotes(), False)
            except (OSError, ValueError, subprocess.SubprocessError):
                GLib.idle_add(self.remotes_loaded, None, True)

        threading.Thread(target=worker, daemon=True).start()

    def remotes_loaded(self, remotes, failed):
        if not self.get_application():
            return
        self.loading = False
        self.reload_button.set_sensitive(True)
        if failed:
            self.status.set_text("无法读取 rclone 配置。请检查配置权限或加密配置密码。")
        else:
            self.remotes = remotes
            self.status.set_text("点击「配置网盘」添加连接。" if not remotes else "")
        self.refresh()

    def run_action(self, _button, operation, remote):
        try:
            operation(remote)
            self.status.set_text("")
        except ValueError as error:
            self.status.set_text(str(error))
        except OSError:
            self.status.set_text("无法启动挂载工具，请检查目录权限和运行依赖。")
        self.refresh()

    def open_directory(self, remote):
        Gio.AppInfo.launch_default_for_uri(self.mounts.path(remote).as_uri())

    def refresh(self):
        try:
            active = self.mounts.refresh()
        except OSError:
            self.status.set_text("无法读取系统挂载状态，请检查 /proc 是否可用。")
            return True
        pending = self.mounts.operations
        errors = self.mounts.errors
        if self.closing and not pending:
            if not active:
                self.destroy()
                return False
            self.closing = False
            self.status.set_text("仍有挂载在使用，请关闭相关程序后重试。")
        view = [
            (remote, remote in active, remote in self.mounts.interrupted,
             pending[remote].kind if remote in pending else "", errors.get(remote, ""))
            for remote in sorted(set(self.remotes) | set(active) | set(pending), key=str.casefold)
        ]
        if view == self.last_view:
            return True
        self.last_view = view
        for row in self.rows.get_children():
            self.rows.remove(row)
        for remote, mounted, interrupted, operation, error in view:
            line = Gtk.Box(spacing=8, margin=7)
            labels = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=3)
            name = Gtk.Label(label=remote, xalign=0, ellipsize=Pango.EllipsizeMode.END)
            status = "正在挂载…" if operation == "mount" else "正在卸载…" if operation else "已挂载" if mounted else "未挂载"
            if interrupted and not operation:
                status = "挂载已中断，请卸载后重新挂载。" if mounted else "挂载已中断，可以重新挂载。"
            labels.pack_start(name, False, False, 0)
            labels.pack_start(Gtk.Label(label=error or status, xalign=0, wrap=True), False, False, 0)
            line.pack_start(labels, True, True, 0)
            if mounted and not operation:
                if not interrupted:
                    self.button(line, "打开目录", self.open_clicked, remote)
                self.button(line, "卸载", self.run_action, self.mounts.unmount, remote)
            elif not operation:
                self.button(line, "挂载", self.run_action, self.mounts.mount, remote)
            self.rows.add(line)
        self.rows.show_all()
        return True

    def open_clicked(self, _button, remote):
        try:
            if remote in self.mounts.refresh() and remote not in self.mounts.interrupted:
                self.open_directory(remote)
        except (OSError, GLib.Error):
            self.status.set_text("无法打开文件管理器。")

    def close_requested(self, *_args):
        if self.closing or self.mounts.operations:
            self.status.set_text("请等待当前挂载操作完成。")
            return True
        try:
            active = self.mounts.refresh()
            if not active:
                return False
            for remote in active:
                self.mounts.unmount(remote)
            self.closing = True
        except (ValueError, OSError):
            self.status.set_text("无法完成卸载，请检查挂载状态后重试。")
        return True

    def destroyed(self, *_args):
        GLib.source_remove(self.refresh_timer)
        self.mounts.lock.close()


class Application(Gtk.Application):
    def __init__(self):
        super().__init__(application_id=APP_ID)

    def do_activate(self):
        window = self.get_active_window()
        if window:
            window.present()
            return
        try:
            if os.getuid() == 0:
                raise PermissionError("请用普通桌面用户启动 MountBridge。")
            Window(self, Mounts())
        except OSError:
            dialog = Gtk.MessageDialog(
                message_type=Gtk.MessageType.ERROR, buttons=Gtk.ButtonsType.CLOSE,
                text="无法启动 MountBridge。请使用普通账户，并检查本机数据目录及是否有其他实例。",
            )
            dialog.run()
            dialog.destroy()


if __name__ == "__main__":
    sys.exit(Application().run(sys.argv))
