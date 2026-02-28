# K9s AI

**K9s with built-in AI/GitHub Copilot assistant for Kubernetes.**

K9s AI extends [K9s](https://github.com/derailed/k9s) with an integrated AI assistant powered by GitHub Copilot, providing intelligent diagnostics, security auditing, and resource optimization directly in your terminal.

## Installation

### Homebrew (macOS / Linux)

```bash
brew install or-shohat/k9s-ai/k9s-ai
```

### Scoop (Windows)

```powershell
scoop bucket add k9s-ai https://github.com/or-shohat/scoop-k9s-ai
scoop install k9s-ai
```

### APT (Debian / Ubuntu)

```bash
# Download the .deb from the latest release
curl -LO https://github.com/or-shohat/k9s-ai/releases/latest/download/k9s-ai_linux_amd64.deb
sudo dpkg -i k9s-ai_linux_amd64.deb
```

### YUM / DNF (Fedora / RHEL)

```bash
sudo rpm -i https://github.com/or-shohat/k9s-ai/releases/latest/download/k9s-ai_linux_amd64.rpm
```

### Snap

```bash
sudo snap install k9s-ai
```

### Docker

```bash
docker run --rm -it -v ~/.kube:/root/.kube orshohat/k9s-ai:latest
```

### Go Install

```bash
go install github.com/or-shohat/k9s-ai@latest
```

### Binary Releases (all platforms)

Download from [GitHub Releases](https://github.com/or-shohat/k9s-ai/releases).

| Platform | Archive |
|----------|---------|
| macOS (Apple Silicon) | `k9s-ai_darwin_arm64.tar.gz` |
| macOS (Intel) | `k9s-ai_darwin_amd64.tar.gz` |
| Linux (x86_64) | `k9s-ai_linux_amd64.tar.gz` |
| Linux (ARM64) | `k9s-ai_linux_arm64.tar.gz` |
| Windows (x86_64) | `k9s-ai_windows_amd64.zip` |
| Windows (ARM64) | `k9s-ai_windows_arm64.zip` |

## Features

| Feature | Command | Description |
|---------|---------|-------------|
| AI Chat | `:ai` / `:chat` / `:copilot` | Open AI chat with full tool access |
| Model Picker | `:ai models` / `Ctrl-N` | Browse & switch Copilot models |
| Skills | `:ai skill <name>` | Activate a skill group |
| Diagnostics Skill | `:ai skill diagnostics` | Root-cause analysis focus |
| Security Skill | `:ai skill security` | RBAC & policy auditing focus |
| Optimization Skill | `:ai skill optimization` | Resource & cost optimization focus |

## Authentication

K9s AI authenticates with GitHub Copilot using one of these methods (in priority order):

1. **Config file** — set `githubToken` in `~/.config/k9s/config.yaml`
2. **Environment variables** — `K9S_AI_GITHUB_TOKEN`, `COPILOT_GITHUB_TOKEN`, `GH_TOKEN`, or `GITHUB_TOKEN`
3. **GitHub CLI** — automatically uses your `gh` login (default)

### BYOK (Bring Your Own Key)

Use your own OpenAI, Azure, or custom provider:

```yaml
# ~/.config/k9s/config.yaml
k9s:
  ai:
    enabled: true
    model: gpt-4.1
    provider:
      type: openai
      baseURL: https://api.openai.com/v1
      apiKey: sk-...
```

## Configuration

```yaml
# ~/.config/k9s/config.yaml
k9s:
  ai:
    enabled: true
    model: gpt-4.1
    streaming: true
    maxContextLines: 500
    autoDiagnose: false
    reasoningEffort: medium      # low | medium | high | xhigh
    activeSkill: ""              # diagnostics | security | optimization
    githubToken: ""              # or use env vars
    # useLoggedInUser: true      # use gh CLI auth (default)
    provider:                    # optional BYOK
      type: openai
      baseURL: https://api.openai.com/v1
      apiKey: sk-...
```

## Keybindings (in AI Chat)

| Key | Action |
|-----|--------|
| `Enter` | Send message |
| `Ctrl-C` | Clear chat |
| `Ctrl-R` | Reset session |
| `Ctrl-S` | Save chat to file |
| `Ctrl-F` | Toggle fullscreen |
| `Ctrl-N` | Open model picker |
| `Esc` | Back |

## License

Apache-2.0 — same as K9s.
