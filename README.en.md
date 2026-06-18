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

skillfleet-server          # first run stays foreground, listens on :47890, prints setup code to stderr
```

Open `http://<host>:47890` and complete admin setup with the setup code from stderr. The server
runs out of the box (default data dir `~/.skillfleet/server`, no config.yaml required). After setup,
running `skillfleet-server` again starts it as a background service; use `skillfleet-server -foreground`
for terminal debugging.

Keep the server running (Linux systemd user service; Windows scheduled task at logon):

```bash
skillfleet-server start     # writes + starts the background service
skillfleet-server status    # shows bind address / data dir / DB size / whether setup is complete
skillfleet-server stop      # stops the service
```

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
(override with `INSTALL_DIR` / `SKILLFLEET_VERSION`). Re-running it upgrades in place: it compares
the installed version, skips if identical, otherwise stops the service → replaces the binary →
restarts the service. `curl|sh` / `irm|iex` pipeline installs upgrade directly; only running the
script manually in an interactive terminal asks for confirmation.

### 3. Enroll the Agent and configure roots

```bash
# 1) Mint an enrollment token in the WebUI, then:
skillfleet-agent enroll http://<server>:47890 <token>

# 2) Approve the device on the WebUI Devices page

# 3) Start the background service (heartbeat + candidate root scan + inventory report + jobs)
skillfleet-agent
```

After the first report, the agent automatically scans the device and reports the skill directories
it finds as **candidates**. You then need to allow **at least one directory** before the agent can
perform writes (install / enable-disable). **Pick one of the two ways below — you don't need both:**

**Option A (recommended) — register a candidate in the WebUI**

In the WebUI Devices / Roots area, click "Register" on a candidate. That single click is all it
takes: the server enqueues a registration job, and the agent writes it into its local config
**automatically** on the next heartbeat — you do nothing on the agent side. Common candidates:

```text
Claude Code  ~/.claude/skills
Codex        ~/.agents/skills
```

**Option B (fallback) — allow a directory via the CLI on that device**

When the WebUI shows no candidate, or you want to allow a custom directory, run the commands
directly on that device:

```bash
skillfleet-agent roots scan                                            # list local candidates
skillfleet-agent roots add -tool claude-code -scope user -path ~/.claude/skills  # allow manually
skillfleet-agent roots list                                            # confirm
```

> If you used A you don't need B, and vice versa.
>
> `enroll` writes only the device identity, **not allowed_roots** — which is why you must allow a
> directory after install. Until you do, the agent can scan and report (read-only), but install /
> enable-disable jobs fail because no target root can be resolved.

### Keep the Agent running

The agent ships built-in service commands (Linux uses a systemd user service, Windows uses a
scheduled task at logon). After enrolment, running `skillfleet-agent` starts the background service;
use `skillfleet-agent -foreground` for terminal debugging:

```bash
skillfleet-agent start      # installs + starts the background service
skillfleet-agent status     # service status (active / PID / unit path)
skillfleet-agent restart    # restart
skillfleet-agent stop       # stop

# Keep the service alive after logout (user services need linger):
loginctl enable-linger "$USER"

journalctl --user -u skillfleet-agent -f      # follow logs (default logging goes to stderr → journald)
```

For foreground debugging you can tune logging: `skillfleet-agent -foreground -log-level debug -log-format json -log-file /tmp/sf.log`.

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
