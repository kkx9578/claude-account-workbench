# Claude Account Workbench

面向 Windows 的 Claude 桌面账号工作台：用中文界面管理已登录账号、查看用量、绑定 Chrome 个人资料，并在切换账号时保留本机 Code 聊天入口。

[下载](https://github.com/kkx9578/claude-account-workbench/releases) · [English](README.en.md) · [更新记录](CHANGELOG.md) · [参与开发](CONTRIBUTING.md) · [安全报告](SECURITY.md)

![账号列表、当前账号、额度和管理入口；截图中的邮箱均为 example.com 虚构数据](docs/screenshots/accounts-demo.png)

*截图使用虚构账号，不包含真实登录信息。*

## 支持范围

- **Windows x64**；已在 Windows 11 验证。当前不提供 macOS、Linux 或 Windows ARM 原生版本。
- 已安装官方 **Claude 桌面客户端**。
- 系统已安装 **Microsoft Edge WebView2 Runtime**，用于显示工作台界面。
- 添加账号和绑定浏览器资料需要 **Google Chrome**。
- Code 会话接续和相关授权需要账号具备 **Claude Code 使用权限**，例如支持该功能的 Pro 等套餐；具体资格以官方当前说明为准。普通聊天登录完成后，可能仍需在桌面客户端打开 Code 页面完成首次授权。

请使用你自己的账号，并自行在官方客户端和浏览器中完成登录。工作台不提供账号注册或代登录服务。

## 下载与启动

1. 在 [Releases](https://github.com/kkx9578/claude-account-workbench/releases) 下载 Windows ZIP 并解压，或下载 `claude-account-workbench-windows-amd64.exe`。
2. 如果正在使用旧版工作台或上游账号切换器，先从托盘退出旧管理器；Claude 中的账号可以保持登录。
3. 运行 `claude-account-workbench-windows-amd64.exe`。如果系统缺少 WebView2 Runtime，请先安装微软官方运行时。

关闭工作台窗口后，程序会留在托盘。右键托盘可重新打开窗口；完全退出请使用托盘中的退出命令。

更新时退出旧管理器、替换程序文件，再重新启动。账号数据位于独立的用户目录，见下文“本地数据”。

## 第一次使用

1. 打开官方 Claude 桌面客户端，登录第一个账号。
2. 如需使用 Code，在桌面客户端打开 Code 页面并完成授权。
3. 回到工作台，点击 **重新识别账号**，读取当前邮箱和套餐。
4. 点击 **刷新额度**，查询当前账号的5小时和每周用量。
5. 在该账号的 **管理** 中选择 Chrome 个人资料，点击 **保存绑定**。

账号身份和额度分别刷新。显示 Pro 等套餐不代表额度已经查询过；额度仍为“待检查”时，可以直接点击“刷新额度”。

## 添加另一个已有账号

1. 保存并停止 Claude 中正在执行的任务。若旧设置启用了自动切换，先在工作台改为手动模式。
2. 为目标账号准备独立的 Chrome 个人资料，然后点击 **添加已有账号**。
3. 填写目标 Claude 邮箱、选择对应 Chrome 资料，确认网络可用和任务已停止。管理器会先保存当前登录，再打开同一个 Claude 客户端的登录页，无需先手动注销。
4. 在官方 Claude 和对应 Chrome 资料中完成登录与邮箱验证。密码、验证码和双重验证信息只填写在官方页面。
5. 回到工作台，进入核对步骤，点击 **重新识别账号**。实际邮箱与目标邮箱一致后，点击 **确认并保存账号**。
6. 若提示需要同步原聊天，先保存并停止任务，再确认同步。该步骤会重启 Claude。

一份 Chrome 个人资料只能绑定一个账号。打开指定资料不会改变 Windows 默认浏览器；若登录链接落在其他窗口，请手动复制到对应资料中继续。

工作台使用已有网络环境。菜单中的“打开 easyAI”仅是为已经安装该工具的用户保留的可选入口；不要求使用 easyAI，也不检测网络出口或修改网络设置。

## 日常切换与聊天接续

保存并停止当前任务 → 点击目标账号的 **切换** → 确认重启 → 等待完成 → 在 Claude 中打开原来的 Code 聊天。

切换流程保存当前账号的登录快照、恢复目标账号，并同步本机 Code 会话索引和项目分组。原有会话ID、本地会话正文和代码目录继续使用。也可以通过 **更多操作 → 同步聊天入口** 手动同步；该操作同样会重启 Claude。

适用边界：

- 接续的是**同一台电脑上的 Code 会话入口**。普通云端聊天、Cowork 会话和正在运行的任务不属于接续保证范围。
- 已保存的登录可能过期或被撤销，届时需要重新登录。
- Chrome 资料绑定与登录快照分开管理，不等于独立 Windows 用户、虚拟机或独立公网IP，也不保证平台不会限制账号。

首次使用建议先在测试项目中完成一次 A → B → A，确认账号、原聊天和代码目录符合预期，再用于正式工作。

## 身份刷新、额度刷新与等待时间

| 操作 | 用途 | 执行范围 |
| --- | --- | --- |
| 重新识别账号 | 登录或授权完成后，更新邮箱、名称和套餐 | 当前账号身份；操作本身不重启 Claude |
| 刷新额度 | 更新5小时和每周已用比例 | 当前账号额度；操作本身不切号或重启 Claude |
| 自动额度检查 | 保持缓存更新 | 当前账号通常每15分钟，其他账号每30分钟 |

两种手动刷新都可跳过普通自动检查间隔，各自有15秒防重复间隔。身份刷新不会推迟额度检查。

真实限流仍需等待：收到 HTTP 429 后至少暂停30分钟，连续发生时延长至60、120分钟；服务器指定更长时间时，按服务器时间等待。等待状态会保存在本地，重启不会清除。并发检查不会排队重复发送。

界面显示缓存的**已用比例**。没有缓存时显示“待检查”，不按0处理。更新时间可在“管理”中查看，重置时间可悬停查看。

## 常见问题

| 现象 | 处理方式 |
| --- | --- |
| Claude 已登录，但提示没有 Code 授权 | 在桌面客户端打开 Code 页面并完成首次授权，然后点击“重新识别账号”。普通聊天登录与 Code 授权是两个状态。 |
| 套餐已识别，额度仍为“待检查” | 点击“刷新额度”。重新识别账号只查询身份。 |
| 按钮暂时不可用 | 查看是否正在执行操作、处于15秒防重复间隔，或仍在服务器限流等待中。 |
| 提示检查结果未能完整保存 | 检查本地配置目录的写入权限和磁盘空间。保留原文件以便排查；不要反复删除状态或登录快照来绕过暂停。对外报告时仅提供脱敏信息。 |
| Chrome 资料列表为空 | 先打开官方 Chrome 并创建个人资料，再重新打开添加窗口。 |
| 提示管理器已在运行 | 从托盘打开已有窗口；替换版本前先退出旧管理器。 |

## 本地数据与安全

程序沿用 `%USERPROFILE%\.claude-account-switcher` 保存管理器数据：

| 内容 | 用途 |
| --- | --- |
| `state.json` | 账号元数据、额度缓存、检查时间和限流状态 |
| `ui-settings.json` | Chrome 绑定和待核对的目标邮箱 |
| `accounts/`、`backups/` | 登录快照与操作前备份，按敏感文件保管 |
| `switcher.log` | 运行诊断；分享前检查并移除邮箱、路径等个人信息 |

Claude 的会话正文和项目文件继续保留在原位置。Chrome 发现只读取个人资料的目录、名称和邮箱元数据；账号切换使用的是 Claude 桌面客户端的登录快照。

**不要把整个数据目录、登录快照、Cookie、令牌或原始配置提交到 GitHub，也不要放进公开 Issue。** 漏洞请通过 [私密安全报告](https://github.com/kkx9578/claude-account-workbench/security/advisories/new) 提交，详见 [SECURITY.md](SECURITY.md)。

## 从源码构建

在 Windows x64 上使用经验证的 **Go 1.26.9**：

```powershell
git clone https://github.com/kkx9578/claude-account-workbench.git
cd claude-account-workbench
go vet ./...
go test -count=1 ./...
go build -tags production -trimpath -ldflags "-H=windowsgui -s -w" -o claude-account-workbench-windows-amd64.exe .
```

运行构建结果仍需要系统 WebView2 Runtime。`production` 标签用于正式构建。默认账号流程回归测试使用虚构账号、临时目录和模拟HTTP响应；部分依赖本机环境的检查可能跳过。

贡献流程和验证要求见 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 来源与版本

本项目基于 [SnlperStripes/claude-account-switcher v0.3.1](https://github.com/SnlperStripes/claude-account-switcher/releases/tag/v0.3.1)，保留其账号快照与本地 Code 会话同步基础，并增加中文主窗口、Chrome绑定、登录核对、手动身份／额度刷新及持久化节流保护。

首次公开版本为 **v0.1.0**，功能对应此前的本地简洁版2.2；公开仓库使用独立版本号。该版本不宣称已合并上游 v0.3.2 及之后的全部兼容修复。Claude 后续更改本地存储格式或接口时，可能需要新的适配。

许可证为 **Apache-2.0**。上游署名与修改说明见 [LICENSE](LICENSE) 和 [NOTICE](NOTICE)，依赖许可证见 [THIRD_PARTY_LICENSES](THIRD_PARTY_LICENSES/README.md)。本项目与 Anthropic 无隶属关系。
