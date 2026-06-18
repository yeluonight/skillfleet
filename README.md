# SkillFleet

简体中文 · [English](./README.en.md) · [日本語](./README.ja.md) · [한국어](./README.ko.md)

![License](https://img.shields.io/badge/license-MIT-blue)
![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white)
![Platforms](https://img.shields.io/badge/platforms-linux%20%7C%20macOS%20%7C%20windows-lightgrey)

> 一个控制台，统一管理所有 AI 编程工具的 Skills——跨设备、可版本化、可回滚。

**SkillFleet** 是跨设备 AI IDE / CLI Skills 管理控制台。它把散落在每台机器、每个工具里的
Agent Skills 收拢进一个带版本的中央仓库，让你在 Web 界面里编辑、校验、对比、分发，并安全地
安装、启停、回滚到任意设备上。

支持工具：**Claude Code · Codex · OpenCode · Google Antigravity · Antigravity CLI ·
Pi coding agent · Generic Agent Skills**。

---

## ✨ 特性

- 🗂️ **统一仓库** —— 多文件 Skill，版本不可变；任何修改先开 Draft，历史可追溯。
- ✏️ **内置编辑器** —— Monaco + 实时预览 + frontmatter / 结构校验。
- 🔗 **来源绑定** —— 绑定上游来源，自动检查更新。
- 🔍 **漂移追踪** —— 本地修改 vs 仓库 vs 上游三方对比，一眼看清谁改了什么。
- 🛡️ **安全安装** —— 写入前自动备份，支持一键回滚。
- 🎚️ **启停管理** —— per-tool 启用 / 禁用，带外配置，不污染 Skill 文件本身。
- 📦 **零依赖分发** —— 纯 Go 单二进制（SQLite 也是纯 Go），6 平台预编译，一行安装。
- 🌐 **设备编排** —— agent enroll → 扫描上报 → 领取下行任务，集中纳管全部设备。

## 🏗️ 架构

Go Server 单二进制 + Go Agent 单二进制 + React/Vite WebUI（go:embed） + SQLite + 本地文件仓库。

```text
┌──────────┐                    ┌──────────────┐
│  WebUI   │◀── 浏览器访问 ────▶│              │──── SQLite（元数据）
│ (embed)  │                    │    Server    │
└──────────┘                    │  单二进制     │──── 文件仓库（Skill 版本）
                                └──────┬───────┘
                                       │ enroll / heartbeat / jobs
                          ┌────────────┼────────────┐
                          ▼            ▼            ▼
                      ┌────────┐  ┌────────┐  ┌────────┐
                      │ Agent  │  │ Agent  │  │ Agent  │   …每台设备一个
                      └────────┘  └────────┘  └────────┘
                      claude-code / codex / opencode / … 的本地 skills
```

SQLite 用 `modernc.org/sqlite`（**纯 Go，无 CGO**），因此二进制静态、无运行时依赖，
`CGO_ENABLED=0` 一台机器即可交叉编译全部平台。

---

## 🚀 快速开始

预编译二进制由 GitHub Actions 在 push `v*` tag 时交叉编译（linux / macOS / windows ×
amd64 / arm64），发布到 [GitHub Releases](https://github.com/yeluonight/skillfleet/releases)。

### 1. 启动 Server（控制端）

```bash
# Linux / macOS：一行安装 server
curl -fsSL \
  https://raw.githubusercontent.com/yeluonight/skillfleet/main/scripts/install.sh | SKILLFLEET_COMPONENT=server sh

skillfleet-server          # 首次前台运行，监听 :47890，并在 stderr 打印 setup code
```

打开 `http://<host>:47890`，用 stderr 里的 setup code 完成管理员初始化。Server 开箱即跑
（默认数据目录 `~/.skillfleet/server`，无需 config.yaml）。setup 完成后再次运行
`skillfleet-server` 会自动转为后台服务，终端可直接关闭；调试时可用
`skillfleet-server -foreground` 强制前台运行。

让 server 常驻（Linux 走 systemd 用户服务；Windows 走任务计划程序登录自启）：

```bash
skillfleet-server start     # 自动写后台服务并启动
skillfleet-server status    # 看监听地址 / 数据目录 / DB 大小 / setup 是否完成
skillfleet-server stop      # 停服务
```

### 2. 在每台设备上安装 Agent

```bash
# Linux / macOS
curl -fsSL \
  https://raw.githubusercontent.com/yeluonight/skillfleet/main/scripts/install.sh | sh
```

```powershell
# Windows PowerShell
irm https://raw.githubusercontent.com/yeluonight/skillfleet/main/scripts/install.ps1 | iex
```

安装脚本自动识别平台、校验 SHA256、装到 PATH 上的目录（可用 `INSTALL_DIR` /
`SKILLFLEET_VERSION` 覆盖）。重复运行即升级：脚本对比已装版本，相同则跳过，不同则
自动停服务 → 替换二进制 → 重启服务。`curl|sh` / `irm|iex` 管道模式会直接升级；只有
手动在交互式终端运行 `sh scripts/install.sh` 时才会询问确认。

### 3. 注册 Agent 并配置 roots

```bash
# 1) 在 WebUI mint 一个 enrollment token，然后：
skillfleet-agent enroll http://<server>:47890 <token>

# 2) 回 WebUI 的 Devices 页批准该设备

# 3) 启动后台服务（心跳 + 扫描候选 roots + 上报 inventory + 领取下行任务）
skillfleet-agent
```

Agent 首次上报后，它会自动扫描本机、把发现的 skill 目录作为**候选**上报。接下来需要放行
**至少一个目录**，agent 才能执行写操作（安装 / 启停 skill）。**两种方式二选一即可**：

**方式 A（推荐）—— 在 WebUI 注册候选**

到 WebUI 的 Devices / Roots 区域，在候选列表里点「注册」。点一下就完成：server 会下发一个
注册任务，agent 下次心跳领到后**自动**写入本机配置，你无需在 agent 那边做任何事。常见候选：

```text
Claude Code  ~/.claude/skills
Codex        ~/.agents/skills
```

**方式 B（兜底）—— 在该设备上用 CLI 手动放行**

当 WebUI 没扫到候选，或你想放行一个自定义目录时，直接在那台设备上敲命令：

```bash
skillfleet-agent roots scan                                            # 列出本机候选
skillfleet-agent roots add -tool claude-code -scope user -path ~/.claude/skills  # 手动放行
skillfleet-agent roots list                                            # 确认已放行
```

> 用了 A 就不必再用 B，反之亦然。
>
> `enroll` 只写设备身份，**不含 allowed_roots**——这就是为什么装好后必须再放行目录。
> 在放行任何目录之前，agent 能扫描上报（只读），但安装 / 启停任务会因解析不到目标根而失败。

### 让 Agent 常驻

Agent 内置守护命令：Linux 走 systemd 用户服务，Windows 走任务计划程序登录自启。完成
enroll 后直接运行 `skillfleet-agent` 会自动转为后台服务；调试时用 `skillfleet-agent -foreground`
强制前台运行。

```bash
skillfleet-agent start      # 安装并启动后台服务
skillfleet-agent status     # 看服务状态（active / PID / unit 路径）
skillfleet-agent restart    # 重启
skillfleet-agent stop       # 停止

# Linux：让 systemd 用户服务在登出后仍存活（用户服务需开启 linger）：
loginctl enable-linger "$USER"

journalctl --user -u skillfleet-agent -f      # Linux 看日志（默认日志走 stderr → journald）
```

Windows 下 `start` 会创建当前用户的登录自启任务（无需管理员权限）：

```powershell
skillfleet-agent start
skillfleet-agent status
skillfleet-agent stop
```

macOS 目前不自动写 launchd。需要常驻时可手动创建 `~/Library/LaunchAgents/com.skillfleet.agent.plist`，
把 `ProgramArguments` 指向 `skillfleet-agent -foreground`，再运行：

```bash
launchctl load ~/Library/LaunchAgents/com.skillfleet.agent.plist
launchctl start com.skillfleet.agent
```

前台调试时可调日志：`skillfleet-agent -foreground -log-level debug -log-format json -log-file /tmp/sf.log`。

> **未签名二进制提示**：首版 Release 未做代码签名 / notarization。macOS 首次运行可能被
> Gatekeeper 拦截（`xattr -d com.apple.quarantine ./skillfleet-agent` 解除），Windows
> SmartScreen 可能告警（选「仍要运行」）。

---

## 🛠️ 开发

**依赖**：Go ≥ 1.25 · Node.js ≥ 24 · npm ≥ 11 · Git ≥ 2.40

```bash
make dev          # Server + WebUI dev 同时跑
make build        # 单二进制构建（含 embed 的 WebUI）
make test         # Go test + WebUI test
make lint         # golangci-lint + eslint
make release      # 交叉编译全平台 agent + server 到 dist/release + SHA256SUMS
```

> `make release` / `make web-embed` 会用真实 WebUI bundle 覆盖
> `internal/webui/embed/dist/index.html`（仓库里跟踪的是占位 stub）。本地构建后
> **不要**把这个被覆盖的 stub 提交进去——commit 前 `git checkout` 还原它。

### 贡献

欢迎 issue 与 PR。开发约定：

- 一个 Task = 一个 commit，commit message 用 `<type>(<scope>): <summary>` 格式。
- 所有文件写入须经 `internal/safefs`；Skill 版本不可变，编辑先开 Draft。
- 主分支 `main` 始终可构建，提交前跑 `make test` 与 `make lint`。

### 仓库结构

```text
skill-fleet/
  apps/{server,agent,web}/         # Go Server / Go Agent / Vite+React WebUI
  internal/
    auth/ db/ api/ agentapi/        # 认证 / 存储 / HTTP
    adapters/{claudecode,codex,opencode,antigravity,antigravitycli,pi,generic}/
    skill/ safefs/ source/ deploy/ agentcfg/ agentstate/ audit/
  migrations/                       # *.sql 顺序编号
  scripts/install.sh install.ps1    # 设备端拉取脚本
  .github/workflows/                # ci.yml + release.yml
```

---

## 📜 License

[MIT](./LICENSE) © 2026 SkillFleet contributors
