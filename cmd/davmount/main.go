package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

func mountArgs(remote, drive, cache string) ([]string, error) {
	if !regexp.MustCompile(`^[A-Za-z0-9_-]+:[^\r\n]*$`).MatchString(remote) {
		return nil, fmt.Errorf("远程路径格式应为连接名:目录")
	}
	if !regexp.MustCompile(`^[D-Zd-z]:$`).MatchString(drive) {
		return nil, fmt.Errorf("盘符应为 D: 至 Z:")
	}
	if cache == "" {
		return nil, fmt.Errorf("缓存目录不能为空")
	}
	return []string{"mount", remote, drive, "--read-only", "--vfs-cache-mode", "full", "--cache-dir", cache, "--vfs-cache-max-size", "2G", "--dir-cache-time", "1m", "--log-level", "NOTICE"}, nil
}

func run() error {
	flags := flag.NewFlagSet("davmount", flag.ContinueOnError)
	engine := flags.String("rclone", "", "rclone.exe 路径（默认查找同目录及 PATH）")
	config := flags.String("config", "", "可选的 rclone 配置文件路径")
	remote := flags.String("remote", "", "远程路径，例如 nutstore:测试目录")
	drive := flags.String("drive", "W:", "挂载盘符")
	base, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	cache := flags.String("cache", filepath.Join(base, "WebDAVMount"), "本地缓存目录")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("用法: davmount [参数] doctor|config|list|mount；mount 为只读，Ctrl+C 卸载")
	}
	if *engine == "" {
		exe, _ := os.Executable()
		local := filepath.Join(filepath.Dir(exe), "rclone.exe")
		if _, err := os.Stat(local); err == nil {
			*engine = local
		} else {
			*engine = "rclone"
		}
	}
	var args []string
	switch flags.Arg(0) {
	case "doctor":
		args = []string{"version"}
	case "config":
		args = []string{"config"}
	case "list":
		if *remote == "" {
			args = []string{"listremotes"}
		} else {
			args = []string{"lsjson", *remote, "--max-depth", "1"}
		}
	case "mount":
		var err error
		args, err = mountArgs(*remote, *drive, *cache)
		if err != nil {
			return err
		}
		if _, err = os.Stat(*drive + `\`); err == nil {
			return fmt.Errorf("盘符 %s 已占用", *drive)
		}
		fmt.Println("只读挂载启动中；此窗口需保持运行，按 Ctrl+C 结束。")
	default:
		return fmt.Errorf("未知命令")
	}
	if *config != "" {
		args = append(args, "--config", *config)
	}
	cmd := exec.Command(*engine, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}
