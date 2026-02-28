<img src="assets/k9s.png" alt="k9s-ai">

## K9s AI — Kubernetes CLI with Built-in AI Assistant

K9s AI is a terminal UI for managing Kubernetes clusters with an integrated AI assistant powered by **GitHub Copilot**. Ask questions about your cluster, diagnose failing pods, audit RBAC policies, and optimize resources — all without leaving your terminal.

Built on top of [K9s](https://github.com/derailed/k9s), it adds:

- **AI Chat** — Conversational assistant with direct cluster access (`:ai`)
- **Skills** — Focused tool groups for diagnostics, security, and optimization
- **Model Selection** — Switch between available Copilot models on the fly
- **BYOK** — Bring your own OpenAI, Azure, or self-hosted API keys
- **GitHub Auth** — Automatic authentication via `gh` CLI or config file

---

## Installation

K9s AI is available on **macOS**, **Linux**, and **Windows**.

### Homebrew (macOS / Linux)

```shell
brew install or-shohat/k9s-ai/k9s-ai
```

### Scoop (Windows)

```powershell
scoop bucket add k9s-ai https://github.com/or-shohat/scoop-k9s-ai
scoop install k9s-ai
```

### APT (Debian / Ubuntu)

```shell
curl -LO https://github.com/or-shohat/k9s-ai/releases/latest/download/k9s-ai_linux_amd64.deb
sudo dpkg -i k9s-ai_linux_amd64.deb
```

### YUM / DNF (Fedora / RHEL)

```shell
sudo rpm -i https://github.com/or-shohat/k9s-ai/releases/latest/download/k9s-ai_linux_amd64.rpm
```

### Snap (Linux)

```shell
sudo snap install k9s-ai
```

### Docker

```shell
docker run --rm -it -v ~/.kube:/root/.kube orshohat/k9s-ai:latest
```

### Go Install

```shell
go install github.com/or-shohat/k9s-ai@latest
```

### Binary Downloads

Download archives for all platforms from [GitHub Releases](https://github.com/or-shohat/k9s-ai/releases):

| Platform | Archive |
|----------|---------|
| macOS (Apple Silicon) | `k9s-ai_darwin_arm64.tar.gz` |
| macOS (Intel) | `k9s-ai_darwin_amd64.tar.gz` |
| Linux (x86_64) | `k9s-ai_linux_amd64.tar.gz` |
| Linux (ARM64) | `k9s-ai_linux_arm64.tar.gz` |
| Windows (x86_64) | `k9s-ai_windows_amd64.zip` |
| Windows (ARM64) | `k9s-ai_windows_arm64.zip` |

---

## Quick Start

After installing, you need to connect k9s-ai to an AI provider. There are two options:

### Option A: GitHub Copilot (recommended)

If you have a [GitHub Copilot](https://github.com/features/copilot) subscription:

```shell
# 1. Install the GitHub CLI (if you don't have it)
brew install gh

# 2. Log in to your GitHub account
gh auth login

# 3. Run k9s-ai — it automatically uses your gh session
k9s-ai
```

Type `:ai` and start chatting. That's it.

### Option B: Bring Your Own API Key

No Copilot subscription? Use any OpenAI-compatible provider (OpenAI, Anthropic, Azure, Ollama, etc.):

```shell
# 1. Create the k9s config directory
mkdir -p ~/.config/k9s

# 2. Add your provider config
cat >> ~/.config/k9s/config.yaml << 'EOF'
k9s:
  ai:
    enabled: true
    model: gpt-4.1
    provider:
      type: openai
      baseURL: https://api.openai.com/v1
      apiKey: sk-your-key-here
EOF

# 3. Run k9s-ai
k9s-ai
```

See [BYOK examples](#bring-your-own-key-byok) below for Azure, Ollama, and other providers.

---

## Authentication

K9s AI supports two authentication paths:

| Path | What you need | How it works |
|------|---------------|--------------|
| **GitHub Copilot** | A [Copilot subscription](https://github.com/features/copilot) + `gh` CLI | Run `gh auth login` once — k9s-ai picks up your session automatically |
| **BYOK** | An API key from any OpenAI-compatible provider | Set `provider.apiKey` in `~/.config/k9s/config.yaml` (or `K9S_AI_API_KEY` env var) |

For Copilot, you can also set a GitHub token explicitly instead of using the `gh` CLI:

```yaml
# ~/.config/k9s/config.yaml
k9s:
  ai:
    githubToken: ghp_xxxxxxxxxxxxxxxxxxxx
```

---

## Bring Your Own Key (BYOK)

Don't have GitHub Copilot? Use your own API keys with any OpenAI-compatible provider.

### OpenAI

```yaml
k9s:
  ai:
    enabled: true
    model: gpt-4.1
    provider:
      type: openai
      baseURL: https://api.openai.com/v1
      apiKey: sk-xxxxxxxxxxxxxxxx  # or set K9S_AI_API_KEY env var
```

### Azure OpenAI

```yaml
k9s:
  ai:
    enabled: true
    model: gpt-4.1
    provider:
      type: azure
      baseURL: https://your-resource.openai.azure.com
      apiKey: your-azure-key
      azure:
        apiVersion: "2024-06-01"
```

### Self-Hosted (Ollama, LM Studio, vLLM, etc.)

```yaml
k9s:
  ai:
    enabled: true
    model: llama3
    provider:
      type: openai
      baseURL: http://localhost:11434/v1
      apiKey: not-needed
```

> **Tip:** API keys can also be set via `K9S_AI_API_KEY` env var. Bearer tokens via `K9S_AI_BEARER_TOKEN`.

---

## Model Selection

K9s AI can list all models available on your Copilot account:

```
:ai models
```

This opens a picker showing available models with the active one marked. Press `Enter` to switch. You can also press `Ctrl-N` from within the AI chat view.

To set a default model in config:

```yaml
k9s:
  ai:
    model: gpt-4.1  # or claude-sonnet-4, o3-mini, etc.
```

---

## Skills

Skills are focused tool + prompt bundles that optimize the AI for specific tasks. When a skill is active, the AI only has access to relevant tools and receives a specialized system prompt — making responses more focused and accurate.

| Skill | Command | Focus |
|-------|---------|-------|
| **diagnostics** | `:ai skill diagnostics` | Root-cause analysis: CrashLoopBackOff, OOMKilled, ImagePullBackOff, scheduling |
| **security** | `:ai skill security` | RBAC auditing, privilege escalation, overly permissive bindings, network policies |
| **optimization** | `:ai skill optimization` | Resource right-sizing, HPA recommendations, cost optimization, node utilization |

To set a default skill:

```yaml
k9s:
  ai:
    activeSkill: diagnostics
```

Use `:ai` with no skill argument to clear the active skill and restore all tools.

---

## AI Commands

| Command | Description |
|---------|-------------|
| `:ai` / `:chat` / `:copilot` | Open the AI chat |
| `:ai models` | Browse and switch AI models |
| `:ai skill diagnostics` | Activate diagnostics skill |
| `:ai skill security` | Activate security skill |
| `:ai skill optimization` | Activate optimization skill |

### AI Chat Keybindings

| Key | Action |
|-----|--------|
| `Enter` | Send message |
| `Ctrl-C` | Clear chat history |
| `Ctrl-R` | Reset AI session |
| `Ctrl-S` | Save chat to file |
| `Ctrl-F` | Toggle fullscreen |
| `Ctrl-N` | Open model picker |
| `Esc` | Back to previous view |

---

## AI Tools

The AI assistant has access to these Kubernetes-aware tools that it calls autonomously:

| Tool | Description |
|------|-------------|
| `get_resource` | Fetch a specific resource by GVR, name, and namespace (returns YAML) |
| `list_resources` | List resources of a given type with optional label selectors |
| `describe_resource` | Full kubectl-style describe including events and conditions |
| `get_logs` | Container logs with tail lines, previous containers for crash analysis |
| `get_events` | Cluster events filtered by namespace, resource, or type (Normal/Warning) |
| `get_cluster_health` | Node count, pod status summary, server version |
| `get_pod_diagnostics` | Container states, restarts, exit codes, probes, resource limits |
| `check_rbac` | Verify if a user/service account can perform a specific action |

The AI chains these tools automatically. For example, when you ask *"Why is my pod crashing?"*, it will:
1. Run `get_pod_diagnostics` to check container states and exit codes
2. Run `get_events` looking for Warning events
3. Run `get_logs` with `previous=true` to get crash logs
4. Synthesize a root-cause analysis with suggested fixes

---

## Full Configuration Reference

```yaml
# ~/.config/k9s/config.yaml
k9s:
  ai:
    # Enable/disable AI features. Default: true
    enabled: true

    # Default model. Default: gpt-4.1
    model: gpt-4.1

    # Enable streaming responses. Default: true
    streaming: true

    # Max lines of context sent to AI. Default: 500
    maxContextLines: 500

    # Auto-diagnose unhealthy resources. Default: false
    autoDiagnose: false

    # Reasoning effort for supported models: low, medium, high, xhigh
    reasoningEffort: medium

    # Active skill: diagnostics, security, optimization, or "" for all tools
    activeSkill: ""

    # GitHub token for Copilot auth (leave empty to use gh CLI)
    # githubToken: ghp_xxx

    # BYOK provider (optional — omit to use GitHub Copilot)
    provider:
      type: openai           # openai, azure, or custom
      baseURL: ""            # API endpoint URL
      apiKey: ""             # or K9S_AI_API_KEY env var
      bearerToken: ""        # or K9S_AI_BEARER_TOKEN env var
      wireApi: ""            # optional: override wire protocol
      azure:                 # Azure-specific options
        apiVersion: ""
```

### Disabling AI

```yaml
k9s:
  ai:
    enabled: false
```

---

## All Keyboard Shortcuts

K9s AI inherits all standard K9s keybindings. Here are the most common ones:

| Action | Command | Notes |
|--------|---------|-------|
| Show help | `?` | |
| Show all resource aliases | `ctrl-a` | |
| Quit | `:q` / `ctrl-c` | |
| Go back | `esc` | |
| View a resource | `:`pod⏎ | Accepts singular, plural, short-name |
| View in namespace | `:`pod ns-x⏎ | |
| Filter resources | `/`filter⏎ | Regex supported |
| Fuzzy find | `/`-f filter⏎ | |
| Switch context | `:`ctx⏎ | |
| Switch namespace | `:`ns⏎ | |
| Delete resource | `ctrl-d` | TAB+ENTER to confirm |
| View YAML | `y` | |
| View logs | `l` | |
| Shell into container | `s` | Pods only |
| Describe resource | `d` | |
| Edit resource | `e` | |
| Port forward | `shift-f` | |
| **Open AI Chat** | **`:ai`** | |
| **AI Models** | **`:ai models`** | |
| **AI Skill** | **`:ai skill <name>`** | |

---

## Building From Source

K9s AI requires Go 1.25+.

```shell
git clone https://github.com/or-shohat/k9s-ai.git
cd k9s-ai
make build
./execs/k9s-ai
```

### Running with Docker

```shell
docker run --rm -it -v ~/.kube/config:/root/.kube/config orshohat/k9s-ai:latest
```

Build your own image:

```shell
docker build -t k9s-ai:local -f k9s-ai/Dockerfile .
docker run --rm -it -v ~/.kube/config:/root/.kube/config k9s-ai:local
```

---

## PreFlight Checks

* K9s AI uses 256 colors terminal mode. On \*Nix systems make sure TERM is set:

    ```shell
    export TERM=xterm-256color
    ```

* Set your editor for resource editing:

    ```shell
    export KUBE_EDITOR=vim
    ```

* Works best with Kubernetes 1.28+

---

## Credits

K9s AI is built on top of [K9s](https://github.com/derailed/k9s) by [Fernand Galiana](https://github.com/derailed) and the [GitHub Copilot SDK](https://github.com/github/copilot-sdk).

---

## Contributing

* File an issue first prior to submitting a PR
* Ensure all exported items are properly commented
* If applicable, submit a test suite against your PR

---

## License

[Apache v2.0](http://www.apache.org/licenses/LICENSE-2.0)
