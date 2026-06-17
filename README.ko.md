# SkillFleet

[简体中文](./README.md) · [English](./README.en.md) · [日本語](./README.ja.md) · 한국어

![License](https://img.shields.io/badge/license-MIT-blue)
![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white)
![Platforms](https://img.shields.io/badge/platforms-linux%20%7C%20macOS%20%7C%20windows-lightgrey)

> 모든 AI 코딩 도구의 Skills를 하나의 콘솔에서 관리 — 기기 간, 버전 관리, 롤백 가능.

**SkillFleet**는 AI IDE / CLI Skills를 기기 전반에 걸쳐 관리하는 콘솔입니다. 여러 머신과 여러
도구에 흩어진 Agent Skills를 버전 관리되는 하나의 중앙 저장소로 모아, 웹 UI에서 편집·검증·
비교·배포하고, 어떤 기기에든 안전하게 설치·활성화/비활성화·롤백할 수 있습니다.

지원 도구: **Claude Code · Codex · OpenCode · Google Antigravity · Antigravity CLI ·
Pi coding agent · Generic Agent Skills**.

---

## ✨ 특징

- 🗂️ **통합 저장소** — 다중 파일 Skill, 버전은 불변. 모든 변경은 Draft로 시작, 전체 이력 추적.
- ✏️ **내장 에디터** — Monaco + 실시간 미리보기 + frontmatter / 구조 검증.
- 🔗 **소스 바인딩** — 업스트림 소스에 연결하고 업데이트를 자동 확인.
- 🔍 **드리프트 추적** — 로컬 vs 저장소 vs 업스트림 3-way 비교로 누가 무엇을 바꿨는지 한눈에.
- 🛡️ **안전한 설치** — 쓰기 전 자동 백업, 원클릭 롤백.
- 🎚️ **활성화 / 비활성화** — 도구별 토글을 out-of-band 설정으로 처리, Skill 파일 자체는 건드리지 않음.
- 📦 **의존성 없는 배포** — 순수 Go 단일 바이너리(SQLite도 순수 Go), 6개 플랫폼 사전 빌드, 한 줄 설치.
- 🌐 **기기 오케스트레이션** — agent enroll → 스캔 보고 → 다운링크 작업 수신으로 전체 기기를 중앙 관리.

## 🏗️ 아키텍처

Go Server 단일 바이너리 + Go Agent 단일 바이너리 + React/Vite WebUI(go:embed) + SQLite + 로컬 파일 저장소.

```text
┌──────────┐                    ┌──────────────┐
│  WebUI   │◀── 브라우저 ──────▶│              │──── SQLite (메타데이터)
│ (embed)  │                    │    Server    │
└──────────┘                    │ 단일 바이너리 │──── 파일 저장소 (Skill 버전)
                                └──────┬───────┘
                                       │ enroll / heartbeat / jobs
                          ┌────────────┼────────────┐
                          ▼            ▼            ▼
                      ┌────────┐  ┌────────┐  ┌────────┐
                      │ Agent  │  │ Agent  │  │ Agent  │   …기기마다 하나
                      └────────┘  └────────┘  └────────┘
                      claude-code / codex / opencode / … 의 로컬 skills
```

SQLite는 `modernc.org/sqlite`(**순수 Go, CGO 불필요**)를 사용하므로 바이너리는 정적이고
의존성이 없으며, `CGO_ENABLED=0`로 한 대의 머신에서 모든 플랫폼을 크로스 컴파일할 수 있습니다.

---

## 🚀 빠른 시작

사전 빌드 바이너리는 `v*` 태그를 push할 때마다 GitHub Actions가 크로스 컴파일하여(linux / macOS /
windows × amd64 / arm64) [GitHub Releases](https://github.com/yeluonight/skillfleet/releases)에
게시합니다.

### 1. Server 시작 (컨트롤 플레인)

```bash
# Linux / macOS: server를 한 줄로 설치
curl -fsSL \
  https://raw.githubusercontent.com/yeluonight/skillfleet/main/scripts/install.sh | SKILLFLEET_COMPONENT=server sh

skillfleet-server          # 포그라운드 실행, :47890 에서 수신, 최초 실행 시 stderr에 setup code 출력
```

`http://<host>:47890`을 열고 stderr의 setup code로 관리자 초기화를 완료합니다. Server는 별도
설정 없이 바로 동작합니다(기본 데이터 디렉터리 `~/.skillfleet/server`, config.yaml 불필요).

server를 상주시키기 (Linux systemd 사용자 서비스):

```bash
skillfleet-server start     # unit을 작성하고 서비스 시작
skillfleet-server status    # 수신 주소 / 데이터 디렉터리 / DB 크기 / setup 완료 여부 표시
skillfleet-server stop      # 서비스 중지
```

### 2. 각 기기에 Agent 설치

```bash
# Linux / macOS
curl -fsSL \
  https://raw.githubusercontent.com/yeluonight/skillfleet/main/scripts/install.sh | sh
```

```powershell
# Windows PowerShell
irm https://raw.githubusercontent.com/yeluonight/skillfleet/main/scripts/install.ps1 | iex
```

설치 스크립트는 플랫폼을 자동 인식하고 SHA256을 검증한 뒤 PATH에 있는 디렉터리에
설치합니다(`INSTALL_DIR` / `SKILLFLEET_VERSION`으로 재정의 가능). 다시 실행하면 그 자리에서
업그레이드합니다: 설치된 버전과 비교하여 동일하면 건너뛰고, 다르면(대화형 확인 또는 `--yes`)
서비스 중지 → 바이너리 교체 → 서비스 재시작을 수행합니다.

### 3. Agent 등록 및 roots 설정

```bash
# 1) WebUI에서 enrollment token을 발급한 뒤:
skillfleet-agent enroll http://<server>:47890 <token>

# 2) WebUI의 Devices 페이지에서 해당 기기를 승인

# 3) 상주 loop 실행 (하트비트 + 후보 roots 스캔 + inventory 보고 + 다운링크 작업 수신)
skillfleet-agent
```

최초 보고 후 agent는 자동으로 기기를 스캔하여 발견한 skill 디렉터리를 **후보**로 보고합니다.
이어서 agent가 쓰기 작업(설치 / 활성화·비활성화)을 수행하려면 **최소 하나의 디렉터리를 허용**
해야 합니다. **아래 두 가지 방법 중 하나만 하면 되며, 둘 다 할 필요는 없습니다:**

**방법 A (권장) — WebUI에서 후보 등록**

WebUI의 Devices / Roots 영역에서 후보의 "등록"을 클릭합니다. 클릭 한 번이면 끝입니다: server가
등록 작업을 발행하고, agent가 다음 하트비트에서 **자동으로** 로컬 설정에 기록합니다. agent 쪽에서
할 일은 없습니다. 일반적인 후보:

```text
Claude Code  ~/.claude/skills
Codex        ~/.agents/skills
```

**방법 B (대체) — 해당 기기에서 CLI로 허용**

WebUI에 후보가 표시되지 않거나 사용자 지정 디렉터리를 허용하려면, 해당 기기에서 직접 명령을
실행합니다:

```bash
skillfleet-agent roots scan                                            # 로컬 후보 나열
skillfleet-agent roots add -tool claude-code -scope user -path ~/.claude/skills  # 수동 허용
skillfleet-agent roots list                                            # 확인
```

> A를 사용했다면 B는 필요 없고, 그 반대도 마찬가지입니다.
>
> `enroll`은 기기 신원만 기록하며 **allowed_roots는 포함하지 않습니다** — 그래서 설치 후 디렉터리를
> 허용해야 합니다. 허용하기 전까지 agent는 스캔·보고(읽기 전용)는 할 수 있어도, 설치 /
> 활성화·비활성화 작업은 대상 root를 해석하지 못해 실패합니다.

### Agent 상주 실행

Agent에는 서비스 관리 명령이 내장되어 있습니다(Linux는 systemd 사용자 서비스 사용 — unit
작성과 시작이 자동, 수동 작성 불필요):

```bash
skillfleet-agent start      # systemd 사용자 서비스를 설치하고 시작
skillfleet-agent status     # 서비스 상태 (active / PID / unit 경로)
skillfleet-agent restart    # 재시작
skillfleet-agent stop       # 중지

# 로그아웃 후에도 서비스를 유지 (사용자 서비스에는 linger 필요):
loginctl enable-linger "$USER"

journalctl --user -u skillfleet-agent -f      # 로그 확인 (기본 로그는 stderr → journald)
```

포그라운드 디버깅 시 로그를 조정할 수 있습니다: `skillfleet-agent -log-level debug -log-format json -log-file /tmp/sf.log`.

> **서명되지 않은 바이너리 안내**: 초기 Release는 코드 서명 / notarization을 하지 않았습니다.
> macOS는 최초 실행 시 Gatekeeper가 차단할 수 있고(`xattr -d com.apple.quarantine ./skillfleet-agent`
> 로 해제), Windows에서는 SmartScreen이 경고할 수 있습니다("실행" 선택).

---

## 🛠️ 개발

**요구 사항**: Go ≥ 1.25 · Node.js ≥ 24 · npm ≥ 11 · Git ≥ 2.40

```bash
make dev          # Server + WebUI dev 동시 실행
make build        # 단일 바이너리 빌드 (WebUI embed 포함)
make test         # Go test + WebUI test
make lint         # golangci-lint + eslint
make release      # 모든 플랫폼 크로스 컴파일 (agent + server) → dist/release + SHA256SUMS
```

> `make release` / `make web-embed`는 추적 중인 플레이스홀더 stub
> `internal/webui/embed/dist/index.html`을 실제 WebUI 번들로 덮어씁니다. 로컬 빌드 후
> 덮어쓰인 stub을 **커밋하지 마세요** — 커밋 전에 `git checkout`으로 복원합니다.

### 기여

Issue와 PR을 환영합니다. 개발 규칙:

- 1 Task = 1 commit, commit 메시지는 `<type>(<scope>): <summary>` 형식.
- 모든 파일 쓰기는 `internal/safefs` 경유. Skill 버전은 불변 — 편집은 Draft로 시작.
- `main`은 항상 빌드 가능하게. 커밋 전에 `make test`와 `make lint` 실행.

### 저장소 구조

```text
skill-fleet/
  apps/{server,agent,web}/         # Go Server / Go Agent / Vite+React WebUI
  internal/
    auth/ db/ api/ agentapi/        # 인증 / 저장소 / HTTP
    adapters/{claudecode,codex,opencode,antigravity,antigravitycli,pi,generic}/
    skill/ safefs/ source/ deploy/ agentcfg/ agentstate/ audit/
  migrations/                       # *.sql, 순차 번호
  scripts/install.sh install.ps1    # 기기 측 다운로드 스크립트
  .github/workflows/                # ci.yml + release.yml
```

---

## 📜 License

[MIT](./LICENSE) © 2026 SkillFleet contributors
