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

skillfleet-server          # 监听 :47890，首次启动在 stderr 打印 setup code
```

打开 `http://<host>:47890`，用 stderr 里的 setup code 完成管理员初始化。Server 开箱即跑
（默认数据目录 `~/.skillfleet/server`，无需 config.yaml）。

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
`SKILLFLEET_VERSION` 覆盖）。

### 3. 注册 Agent 并配置 roots

```bash
# 1) 在 WebUI mint 一个 enrollment token，然后：
skillfleet-agent enroll http://<server>:47890 <token>

# 2) 回 WebUI 的 Devices 页批准该设备

# 3) 启动常驻 loop（心跳 + 扫描候选 roots + 上报 inventory + 领取下行任务）
skillfleet-agent
```

Agent 首次上报后，回到 WebUI 的 Devices / Roots 区域，从候选目录里注册本机允许管理的
skill root。常见候选包括：

```text
Claude Code  ~/.claude/skills
Codex        ~/.agents/skills
```

注册完成后，安装 / 启停任务才会写入对应目录。没有候选、或需要手动放行自定义目录时，
也可以在该设备上用 CLI 兜底：

```bash
skillfleet-agent roots scan
skillfleet-agent roots add -tool claude-code -scope user -path ~/.claude/skills
skillfleet-agent roots list
```

> `enroll` 只写设备身份，**不含 allowed_roots**；必须通过 WebUI 注册候选 root，
> 或用 `roots add` 手动放行至少一个目录，否则 agent 能扫描上报，但安装 / 启停任务会因
> 解析不到目标根而失败。

### 让 Agent 常驻（systemd 用户服务示例）

```ini
# ~/.config/systemd/user/skillfleet-agent.service
[Unit]
Description=SkillFleet agent
After=network-online.target

[Service]
ExecStart=%h/.local/bin/skillfleet-agent
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
```

```bash
systemctl --user enable --now skillfleet-agent
journalctl --user -u skillfleet-agent -f      # 看日志
```

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
