<img src="assets/k9s.png" alt="k9s-ai">

## K9s AI — Kubernetes CLI with Built-in AI Assistant

K9s AI is a terminal UI for managing Kubernetes clusters with an integrated AI assistant powered by **GitHub Copilot**. Ask questions about your cluster, diagnose failing pods, audit RBAC policies, and optimize resources — all without leaving your terminal.

Built on top of [K9s](https://github.com/derailed/k9s), it adds:

- **AI Chat** — Conversational assistant with direct cluster access (`:ai`)
- **Skills** — Focused tool groups for diagnostics, security, and optimization
- **Model Selection** — Switch between available Copilot models on the fly
- **BYOK** — Bring your own OpenAI, Azure, or self-hosted API keys
- **GitHub Auth** — Automatic authentication via Copilot device flow (no `gh` CLI required)

---

## Installation

K9s AI is available on **macOS**, **Linux**, and **Windows**.

### Homebrew (macOS / Linux)

```shell
brew tap orshohat1/k9s-ai
brew install k9s-ai
```

### Scoop (Windows)

```powershell
scoop bucket add k9s-ai https://github.com/orshohat1/k9s-ai
scoop install k9s-ai
```

### APT (Debian / Ubuntu)

```shell
curl -LO https://github.com/orshohat1/k9s-ai/releases/latest/download/k9s-ai_linux_amd64.deb
sudo dpkg -i k9s-ai_linux_amd64.deb
```

### YUM / DNF (Fedora / RHEL)

```shell
sudo rpm -i https://github.com/orshohat1/k9s-ai/releases/latest/download/k9s-ai_linux_amd64.rpm
```

### Snap (Linux)

```shell
sudo snap install k9s-ai
```

### Binary Downloads

Download archives for all platforms from [GitHub Releases](https://github.com/orshohat1/k9s-ai/releases):

| Platform | Archive |
|----------|---------|
| macOS (Apple Silicon) | `k9s-ai_Darwin_arm64.tar.gz` |
| macOS (Intel) | `k9s-ai_Darwin_amd64.tar.gz` |
| Linux (x86_64) | `k9s-ai_Linux_amd64.tar.gz` |
| Linux (ARM64) | `k9s-ai_Linux_arm64.tar.gz` |
| Linux (ppc64le) | `k9s-ai_Linux_ppc64le.tar.gz` |
| Linux (s390x) | `k9s-ai_Linux_s390x.tar.gz` |
| FreeBSD (x86_64) | `k9s-ai_Freebsd_amd64.tar.gz` |
| FreeBSD (ARM64) | `k9s-ai_Freebsd_arm64.tar.gz` |
| Windows (x86_64) | `k9s-ai_Windows_amd64.zip` |
| Windows (ARM64) | `k9s-ai_Windows_arm64.zip` |

---

## Quick Start

After installing, you need to connect k9s-ai to an AI provider. There are two options:

### Option A: GitHub Copilot (recommended)

If you have a [GitHub Copilot](https://github.com/features/copilot) subscription:

```shell
# Just run k9s-ai — it handles authentication automatically
k9s-ai
```

On first launch, the Copilot SDK opens a browser for GitHub device-flow login. No `gh` CLI or extra tools needed — everything is built in.

Type `:ai` and start chatting. That's it.

### Option B: Bring Your Own API Key

No Copilot subscription? Use any compatible provider (OpenAI, Anthropic, Azure, Ollama, etc.).

**Interactive setup (recommended):** Run k9s-ai and type `:byok` to configure your provider, API key, and model through an interactive form. Navigate fields with `Tab`, open dropdowns with `Enter`, and press `Esc` to go back. Settings are saved to your config file automatically.

**Manual setup:** Edit your config file directly:

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
| **GitHub Copilot** | A [Copilot subscription](https://github.com/features/copilot) | Run k9s-ai — on first launch it opens a browser for GitHub device-flow login. Token is cached automatically. |
| **BYOK** | An API key from any OpenAI-compatible provider | Set `provider.apiKey` in `~/.config/k9s/config.yaml` (or `K9S_AI_API_KEY` env var) |

For Copilot, you can also set a GitHub token explicitly instead of using the device flow:

```yaml
# ~/.config/k9s/config.yaml
k9s:
  ai:
    githubToken: ghp_xxxxxxxxxxxxxxxxxxxx
```

---

## Bring Your Own Key (BYOK)

Don't have GitHub Copilot? Use your own API keys with any compatible provider.

### Supported Providers

| Provider | Type Value | Notes |
|----------|-----------|-------|
| OpenAI | `openai` | OpenAI API and OpenAI-compatible endpoints |
| Azure OpenAI / Azure AI Foundry | `azure` | Azure-hosted models |
| Anthropic | `anthropic` | Claude models |
| Ollama | `openai` | Local models via OpenAI-compatible API |
| Microsoft Foundry Local | `openai` | Run AI models locally on your device via OpenAI-compatible API |
| Other OpenAI-compatible | `openai` | vLLM, LiteLLM, LM Studio, etc. |

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

### Anthropic

```yaml
k9s:
  ai:
    enabled: true
    model: claude-sonnet-4-20250514
    provider:
      type: anthropic
      baseURL: https://api.anthropic.com
      apiKey: sk-ant-xxxxxxxxxxxxxxxx
```

### Self-Hosted (Ollama, Microsoft Foundry Local, vLLM, LiteLLM, etc.)

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

## Commands

| Command | Description |
|---------|-------------|
| `:ai` | Open the AI chat assistant |
| `:ai models` | Browse and switch between available models (Copilot only) |
| `:byok` | Interactive BYOK provider setup — navigate with `Tab`, select with `Enter`, `Esc` to cancel |
| **`Shift-A`** | **Open AI chat with the context of the currently selected resource** |

> **💡 Pro Tip: Context-Aware AI with `Shift-A`**
>
> The fastest way to get AI help for a specific workload is to **select any resource** (Pod, Deployment, Service, etc.) and press **`Shift-A`**. The AI chat opens pre-loaded with the full context of that resource — its spec, status, events, and related objects. This means you can ask questions like *"Why is this pod crash-looping?"* or *"Is this deployment configured correctly?"* and the AI already knows exactly which resource you're talking about.
>
> Instead of copying YAML or describing your problem manually, just navigate to the resource, hit `Shift-A`, and start chatting.

## Model Selection

For **Copilot** users, list all available models:

```
:ai models
```

This opens a picker showing available models with the active one marked. Press `Enter` to switch.

For **BYOK** users, set the model in your config or via `:byok`:

To set a default model in config:

```yaml
k9s:
  ai:
    model: gpt-4.1  # or claude-sonnet-4, o3-mini, etc.
```

---

## Building From Source

K9s AI requires Go 1.25+.

```shell
git clone https://github.com/orshohat1/k9s-ai.git
cd k9s-ai
make build
./execs/k9s-ai
```

## Running with Docker

Docker images are not yet published. You can build your own:

```shell
git clone https://github.com/orshohat1/k9s-ai.git
cd k9s-ai
docker build -t k9s-ai:local -f k9s-ai/Dockerfile .
docker run --rm -it -v ~/.kube/config:/root/.kube/config k9s-ai:local
```

---

## Credits

K9s AI is built on top of [K9s](https://github.com/derailed/k9s) by [Fernand Galiana](https://github.com/derailed) and the [GitHub Copilot SDK](https://github.com/github/copilot-sdk).

---

## Contributing

* File an issue first prior to submitting a PR
* Ensure all exported items are properly commented
* If applicable, submit a test suite against your PR

