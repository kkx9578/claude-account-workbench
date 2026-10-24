# 参与开发

欢迎提交可复现的问题、界面改进和小范围修复。项目仓库为 [kkx9578/claude-account-workbench](https://github.com/kkx9578/claude-account-workbench)，当前开发与验证目标是 Windows x64。

## 提交问题

普通问题请在本仓库创建 Issue，提供：

- 工作台版本、Windows版本、Claude桌面版本及安装渠道。
- 最小操作步骤、期望结果与实际结果。
- 是否涉及账号切换、身份识别、额度刷新或聊天索引。
- 必要的脱敏错误文本或截图。用 `example.com` 邮箱替代真实账号，遮盖个人目录与项目内容。

不要上传完整日志包、`state.json`、`ui-settings.json`、Claude原始配置、Cookie、登录快照或任何令牌。安全问题使用 [私密报告入口](https://github.com/kkx9578/claude-account-workbench/security/advisories/new)，参见 [SECURITY.md](SECURITY.md)。

## 开发与验证

使用 Windows x64 和 Go 1.26.9。界面运行需要系统 WebView2 Runtime；普通单元测试不要求启动真实 Claude 或 Chrome。

```powershell
go vet ./...
go test -count=1 ./...
go build -tags production -trimpath -ldflags "-H=windowsgui -s -w" -o claude-account-workbench-windows-amd64.exe .
```

提交前对改动的 Go 文件运行 `gofmt`。修复行为错误时增加能够重现问题的回归测试；UI改动应检查正常、等待、失败、取消和小窗口状态。

## 代码入口

| 文件 | 主要职责 |
| --- | --- |
| `main.go`、`ui_windows.go` | 启动、单实例、Wails窗口与托盘生命周期 |
| `ui.go`、`frontend/` | 界面绑定、登录核对、Chrome绑定与交互 |
| `chrome.go` | Chrome资料元数据发现及指定资料启动 |
| `switcher.go` | 账号状态、定时检查、切换与持久化 |
| `identity_refresh.go`、`usage.go` | 手动身份／额度刷新、API边界与授权读取 |
| `profiles.go`、`desktop.go` | 登录快照、备份及本地配置操作 |
| `sessions.go`、`spaces.go` | 本地Code会话索引与项目分组同步 |
| `*_test.go` | 临时目录、虚构账号及HTTP边界回归测试 |

## 涉及账号或文件操作的改动

保持以下行为，并用可复现测试验证相关改动：

- 身份与额度分别刷新；手动请求只作用于当前账号。
- 真实429等待、并发保护和未完成检查标记不能被普通刷新或重启绕过。
- 请求前保存失败不发送HTTP；结果保存失败不报告成功，也不覆盖原来的有效缓存。
- 请求期间本地账号改变时，不把响应关联到错误账号。
- 新的识别入口不得意外重启Claude；需要重启的操作须有明确提示与确认。
- 原会话ID、正文和代码目录保持不变；仅在需要时同步索引与分组。
- 密码、验证码、Cookie和令牌不得进入前端载荷、日志、截图或测试提交。

账号流程测试请复用临时目录和内存HTTP transport。不要让默认测试访问个人登录文件、调用真实账号API、关闭Claude或启动真实浏览器。环境相关的只读探测需要明确跳过条件。

## Pull Request

一次PR聚焦一个问题，说明触发条件、修改后的行为、验证命令与尚未验证的环境。对新增依赖说明用途、维护情况、许可证和集成成本，并更新相应许可证材料。

保留上游版权和来源说明。项目基于上游 v0.3.1；引入后续上游修复时，注明来源提交及已验证的兼容范围，不把部分移植描述成整体升级。

本仓库使用 Apache-2.0。提交的改动应与该许可证兼容，详见 [LICENSE](LICENSE) 和 [NOTICE](NOTICE)。
