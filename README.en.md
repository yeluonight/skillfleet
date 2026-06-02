# SkillFleet

[简体中文](./README.md) · English · [日本語](./README.ja.md) · [한국어](./README.ko.md)

![License](https://img.shields.io/badge/license-MIT-blue)
![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white)
![Platforms](https://img.shields.io/badge/platforms-linux%20%7C%20macOS%20%7C%20windows-lightgrey)

> One console to manage the Skills of every AI coding tool — across devices, versioned, reversible.

**SkillFleet** is a cross-device management console for AI IDE / CLI Skills. It pulls the Agent
Skills scattered across every machine and every tool into one versioned central repository, where
you edit, validate, diff and distribute them — then install, enable/disable and roll them back to
any device safely.

Supported tools: **Claude Code · Codex · OpenCode · Google Antigravity · Antigravity CLI ·
Pi coding agent · Generic Agent Skills**.

---

## ✨ Features

- 🗂️ **Unified repository** — multi-file Skills, immutable versions; every change starts as a Draft, full history.
- ✏️ **Built-in editor** — Monaco + live preview + frontmatter / structure validation.
- 🔗 **Source binding** — bind an upstream source and check for updates automatically.
- 🔍 **Drift tracking** — three-way diff of local vs repository vs upstream, see exactly who changed what.
- 🛡️ **Safe install** — automatic backup before any write, one-click rollback.
- 🎚️ **Enable/disable** — per-tool toggling via out-of-band config, never touching the Skill files themselves.
- 📦 **Zero-dependency distribution** — pure-Go single binary (SQLite is pure Go too), 6 prebuilt platforms, one-line install.
- 🌐 **Device orchestration** — agent enroll → scan & report → pull downlink jobs, manage your whole fleet centrally.

## 🏗️ Architecture

A Go Server single binary + a Go Agent single binary + a React/Vite WebUI (go:embed) + SQLite + a local file repository.

```text
┌──────────┐                    ┌──────────────┐
│  WebUI   │◀── browser ───────▶│              │──── SQLite (metadata)
│ (embed)  │                    │    Server    │
└──────────┘                    │ single binary│──── file repo (Skill versions)
                                └──────┬───────┘
                                       │ enroll / heartbeat / jobs
                          ┌────────────┼────────────┐
                          ▼            ▼            ▼
                      ┌────────┐  ┌────────┐  ┌────────┐
                      │ Agent  │  │ Agent  │  │ Agent  │   …one per device
                      └────────┘  └────────┘  └────────┘
                      local skills of claude-code / codex / opencode / …
```

SQLite uses `modernc.org/sqlite` (**pure Go, no CGO**), so the binaries are static and
dependency-free, and a single machine can cross-compile every platform with `CGO_ENABLED=0`.

---

## 🚀 Quick start

Prebuilt binaries are cross-compiled by GitHub Actions on every `v*` tag push (linux / macOS /
windows × amd64 / arm64) and published to
[GitHub Releases](https://github.com/yeluonight/skillfleet/releases).

### 1. Start the Server (control plane)

```bash
# Linux / macOS: install the server in one line
curl -fsSL \
  https://raw.githubusercontent.com/yeluonight/skillfleet/main/scripts/install.sh | SKILLFLEET_COMPONENT=server sh

skillfleet-server          # listens on :7890, prints a setup code to stderr on first run
```

Open `http://<host>:7890` and complete admin setup with the setup code from stderr. The server
runs out of the box (default data dir `~/.skillfleet/server`, no config.yaml required).

### 2. Install the Agent on each device

```bash
# Linux / macOS
curl -fsSL \
  https://raw.githubusercontent.com/yeluonight/skillfleet/main/scripts/install.sh | sh
```

```powershell
# Windows PowerShell
irm https://raw.githubusercontent.com/yeluonight/skillfleet/main/scripts/install.ps1 | iex
```

The installer detects your platform, verifies the SHA256, and installs to a directory on your PATH
(override with `INSTALL_DIR` / `SKILLFLEET_VERSION`).

### 3. Enroll the Agent and configure roots

```bash
# 1) Mint an enrollment token in the WebUI, then:
skillfleet-agent enroll http://<server>:7890 <token>

# 2) Approve the device on the WebUI Devices page

# 3) Run the long-lived loop (heartbeat + candidate root scan + inventory report + jobs)
skillfleet-agent
```

After the first report, go back to the WebUI Devices / Roots area and register the local skill
root this device is allowed to manage. Common candidates include:

```text
Claude Code  ~/.claude/skills
Codex        ~/.agents/skills
```

Once a root is registered, install / enable-disable jobs can write to that directory. If no
candidate is shown, or you need to allow a custom directory manually, use the CLI fallback on that
device:

```bash
skillfleet-agent roots scan
skillfleet-agent roots add -tool claude-code -scope user -path ~/.claude/skills
skillfleet-agent roots list
```

> `enroll` writes only the device identity, **not allowed_roots**. You must register a candidate
> root from the WebUI, or manually allow at least one directory with `roots add`; otherwise the
> agent can scan and report, but install / enable-disable jobs will fail because no target root can
> be resolved.

### Keep the Agent running (systemd user service example)

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
journalctl --user -u skillfleet-agent -f      # follow logs
```

> **Unsigned binaries**: the first Release is not code-signed / notarized. macOS may block the
> first run via Gatekeeper (clear it with `xattr -d com.apple.quarantine ./skillfleet-agent`),
> and Windows SmartScreen may warn (choose "Run anyway").

---

## 🛠️ Development

**Requirements**: Go ≥ 1.25 · Node.js ≥ 24 · npm ≥ 11 · Git ≥ 2.40

```bash
make dev          # run Server + WebUI dev together
make build        # single-binary build (with the embedded WebUI)
make test         # Go test + WebUI test
make lint         # golangci-lint + eslint
make release      # cross-compile all platforms (agent + server) to dist/release + SHA256SUMS
```

> `make release` / `make web-embed` overwrite the tracked placeholder stub at
> `internal/webui/embed/dist/index.html` with the real WebUI bundle. After a local build,
> **do not** commit that overwritten stub — restore it with `git checkout` before committing.

### Contributing

Issues and PRs are welcome. Development conventions:

- One Task = one commit; commit messages follow `<type>(<scope>): <summary>`.
- All file writes go through `internal/safefs`; Skill versions are immutable — edit via a Draft.
- `main` is always buildable; run `make test` and `make lint` before committing.

### Repository layout

```text
skill-fleet/
  apps/{server,agent,web}/         # Go Server / Go Agent / Vite+React WebUI
  internal/
    auth/ db/ api/ agentapi/        # auth / storage / HTTP
    adapters/{claudecode,codex,opencode,antigravity,antigravitycli,pi,generic}/
    skill/ safefs/ source/ deploy/ agentcfg/ agentstate/ audit/
  migrations/                       # *.sql, sequentially numbered
  scripts/install.sh install.ps1    # device-side fetch scripts
  .github/workflows/                # ci.yml + release.yml
```

---

## 📜 License

[MIT](./LICENSE) © 2026 SkillFleet contributors
