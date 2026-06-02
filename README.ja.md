# SkillFleet

[简体中文](./README.md) · [English](./README.en.md) · 日本語 · [한국어](./README.ko.md)

![License](https://img.shields.io/badge/license-MIT-blue)
![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white)
![Platforms](https://img.shields.io/badge/platforms-linux%20%7C%20macOS%20%7C%20windows-lightgrey)

> すべての AI コーディングツールの Skills を 1 つのコンソールで管理 — デバイス横断・バージョン管理・ロールバック可能。

**SkillFleet** は、AI IDE / CLI の Skills を横断管理するコンソールです。各マシン・各ツールに
散らばった Agent Skills を 1 つのバージョン管理付き中央リポジトリに集約し、Web UI で編集・
検証・差分比較・配布を行い、任意のデバイスへ安全にインストール・有効化/無効化・ロールバック
できます。

対応ツール：**Claude Code · Codex · OpenCode · Google Antigravity · Antigravity CLI ·
Pi coding agent · Generic Agent Skills**。

---

## ✨ 特徴

- 🗂️ **統一リポジトリ** — 複数ファイルの Skill、バージョンは不変。変更はまず Draft から、履歴を完全に追跡。
- ✏️ **組み込みエディタ** — Monaco + ライブプレビュー + frontmatter / 構造バリデーション。
- 🔗 **ソースバインディング** — 上流ソースに紐付け、更新を自動チェック。
- 🔍 **ドリフト追跡** — ローカル vs リポジトリ vs 上流の 3-way 差分で、誰が何を変えたか一目瞭然。
- 🛡️ **安全なインストール** — 書き込み前に自動バックアップ、ワンクリックでロールバック。
- 🎚️ **有効化 / 無効化** — ツールごとに帯域外（out-of-band）設定で切替。Skill ファイル本体は汚さない。
- 📦 **依存ゼロの配布** — 純 Go の単一バイナリ（SQLite も純 Go）、6 プラットフォームのプリビルド、ワンライナーでインストール。
- 🌐 **デバイスオーケストレーション** — agent enroll → スキャン報告 → 下り（downlink）ジョブ取得で、全デバイスを集中管理。

## 🏗️ アーキテクチャ

Go Server 単一バイナリ + Go Agent 単一バイナリ + React/Vite WebUI（go:embed） + SQLite + ローカルファイルリポジトリ。

```text
┌──────────┐                    ┌──────────────┐
│  WebUI   │◀── ブラウザ ──────▶│              │──── SQLite（メタデータ）
│ (embed)  │                    │    Server    │
└──────────┘                    │ 単一バイナリ  │──── ファイルリポジトリ（Skill バージョン）
                                └──────┬───────┘
                                       │ enroll / heartbeat / jobs
                          ┌────────────┼────────────┐
                          ▼            ▼            ▼
                      ┌────────┐  ┌────────┐  ┌────────┐
                      │ Agent  │  │ Agent  │  │ Agent  │   …デバイスごとに 1 つ
                      └────────┘  └────────┘  └────────┘
                      claude-code / codex / opencode / … のローカル skills
```

SQLite は `modernc.org/sqlite`（**純 Go、CGO 不要**）を使用するため、バイナリは静的で依存なし。
`CGO_ENABLED=0` で 1 台のマシンから全プラットフォームをクロスコンパイルできます。

---

## 🚀 クイックスタート

プリビルドバイナリは `v*` タグの push ごとに GitHub Actions がクロスコンパイルし（linux / macOS /
windows × amd64 / arm64）、[GitHub Releases](https://github.com/yeluonight/skillfleet/releases)
に公開されます。

### 1. Server を起動（コントロールプレーン）

```bash
# Linux / macOS：server をワンラインでインストール
curl -fsSL \
  https://raw.githubusercontent.com/yeluonight/skillfleet/main/scripts/install.sh | SKILLFLEET_COMPONENT=server sh

skillfleet-server          # :47890 で待受、初回起動時に stderr へ setup code を出力
```

`http://<host>:47890` を開き、stderr の setup code で管理者初期化を完了します。Server は
そのまま動作します（デフォルトデータディレクトリ `~/.skillfleet/server`、config.yaml 不要）。

### 2. 各デバイスに Agent をインストール

```bash
# Linux / macOS
curl -fsSL \
  https://raw.githubusercontent.com/yeluonight/skillfleet/main/scripts/install.sh | sh
```

```powershell
# Windows PowerShell
irm https://raw.githubusercontent.com/yeluonight/skillfleet/main/scripts/install.ps1 | iex
```

インストーラはプラットフォームを自動判別し、SHA256 を検証して PATH 上のディレクトリに
インストールします（`INSTALL_DIR` / `SKILLFLEET_VERSION` で上書き可能）。

### 3. Agent を登録し、roots を設定

```bash
# 1) WebUI で enrollment token を発行し、次を実行：
skillfleet-agent enroll http://<server>:47890 <token>

# 2) WebUI の Devices ページでそのデバイスを承認

# 3) 常駐ループを起動（ハートビート + 候補 roots スキャン + inventory 報告 + ジョブ取得）
skillfleet-agent
```

初回報告後、WebUI の Devices / Roots で、このデバイスに管理を許可するローカル skill root を
候補から登録します。代表的な候補は次のとおりです：

```text
Claude Code  ~/.claude/skills
Codex        ~/.agents/skills
```

root 登録後、インストール / 有効化・無効化ジョブがそのディレクトリへ書き込めるようになります。
候補が表示されない場合や、カスタムディレクトリを手動で許可したい場合は、そのデバイスで CLI を
使って登録できます：

```bash
skillfleet-agent roots scan
skillfleet-agent roots add -tool claude-code -scope user -path ~/.claude/skills
skillfleet-agent roots list
```

> `enroll` はデバイス ID のみを書き込み、**allowed_roots は含みません**。WebUI で候補 root を
> 登録するか、`roots add` で少なくとも 1 つのディレクトリを手動許可してください。そうしないと
> agent はスキャン報告はできても、インストール / 有効化・無効化ジョブは対象 root を解決できず失敗します。

### Agent を常駐させる（systemd ユーザーサービスの例）

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
journalctl --user -u skillfleet-agent -f      # ログを追う
```

> **未署名バイナリについて**：初版 Release はコード署名 / notarization をしていません。macOS は
> 初回起動時に Gatekeeper でブロックされることがあり（`xattr -d com.apple.quarantine ./skillfleet-agent`
> で解除）、Windows では SmartScreen が警告することがあります（「実行」を選択）。

---

## 🛠️ 開発

**要件**：Go ≥ 1.25 · Node.js ≥ 24 · npm ≥ 11 · Git ≥ 2.40

```bash
make dev          # Server + WebUI dev を同時に起動
make build        # 単一バイナリビルド（WebUI を embed）
make test         # Go test + WebUI test
make lint         # golangci-lint + eslint
make release      # 全プラットフォームをクロスコンパイル（agent + server）→ dist/release + SHA256SUMS
```

> `make release` / `make web-embed` は、追跡されているプレースホルダ stub
> `internal/webui/embed/dist/index.html` を実際の WebUI バンドルで上書きします。ローカル
> ビルド後、その上書きされた stub を**コミットしないでください** — コミット前に `git checkout`
> で元に戻します。

### コントリビュート

Issue / PR を歓迎します。開発上の取り決め：

- 1 Task = 1 commit、commit メッセージは `<type>(<scope>): <summary>` 形式。
- ファイル書き込みは必ず `internal/safefs` 経由。Skill バージョンは不変 — 編集は Draft から。
- `main` は常にビルド可能に。コミット前に `make test` と `make lint` を実行。

### リポジトリ構成

```text
skill-fleet/
  apps/{server,agent,web}/         # Go Server / Go Agent / Vite+React WebUI
  internal/
    auth/ db/ api/ agentapi/        # 認証 / ストレージ / HTTP
    adapters/{claudecode,codex,opencode,antigravity,antigravitycli,pi,generic}/
    skill/ safefs/ source/ deploy/ agentcfg/ agentstate/ audit/
  migrations/                       # *.sql、連番
  scripts/install.sh install.ps1    # デバイス側の取得スクリプト
  .github/workflows/                # ci.yml + release.yml
```

---

## 📜 License

[MIT](./LICENSE) © 2026 SkillFleet contributors
