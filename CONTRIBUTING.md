# 参与 WebDAVMount / MountBridge

欢迎提交 Issue 和 Pull Request。较大的功能、架构或兼容性改动，请先用 Issue 对齐范围、验收条件和发行影响。

## 提交改动

1. 从最新的 `main` 创建工作分支，在该分支开发。
2. 每次提交同步更新根目录 [README](README.md)，记录该提交涉及的用户可见变化、用法或工程约定。
3. 在 Windows 执行 `bun run check`，覆盖前端测试和构建、Go 测试及 `go vet`。在 Linux 执行 `bun run check:linux`，检查 Python 源码及 Debian 包。也可使用 `npm run` 执行同名脚本。
4. Pull Request 标题和说明使用中文，填写「用户可见变化」「已执行的验证」。较大改动关联用于对齐范围的 Issue。
5. `main` 通过 Pull Request 接收已通过 CI 的合并。CI 会检查 Windows、Linux 以及 PR 的 README 和说明。

当前仓库使用 Go、Vue／TypeScript 和 Python。引入 Rust 后，相关 PR 和发行需增加 `cargo test --workspace` 和 `cargo clippy --workspace --all-targets -- -D warnings`，并把两项接入 CI。

## 发布前

1. 合并 PR 后等待 `main` 的 CI 完成。为准备发行的提交创建标签。
2. 运行 `bun run release:check <标签>`。它确认标签指向 `main` 历史中的提交，并检查该提交最近一次 `main` CI。
3. 按目标平台核对版本、依赖许可、源码材料和 SHA-256。以草稿发行上传附件，核对附件名称、大小和哈希。公开发行后重新下载并校验，涉及在线更新时最后发布更新清单。
4. 发行说明用中文写明用户可见变化、已通过的验证及待验收平台。
