# PromptRail

CLI tool to upload, share, and view Claude Code sessions.

Upload any Claude Code conversation to the cloud and share it with a link. Sessions are stored as processed, render-ready blobs on Cloudflare R2 with access control. View conversations in a rich terminal TUI or share them via `username/session-id`.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/ashmaster/promptrail/main/install.sh | sh
```

Or build from source:

```sh
git clone https://github.com/ashmaster/promptrail.git
cd promptrail
make cli
# binary at bin/pt
```

## Quick Start

```sh
# authenticate with GitHub
pt login

# browse your local Claude Code sessions
pt list

# view a session in the terminal
pt view df0712b1

# upload a session (interactive picker if no ID given)
pt upload

# browse uploaded sessions, toggle public/private
pt list --remote

# view a remote session
pt view ashmaster/df0712b1
```

## Commands

| Command | Description |
|---------|-------------|
| `pt login` | Authenticate with GitHub OAuth |
| `pt logout` | Remove stored credentials |
| `pt list` | Interactive browser for local Claude sessions |
| `pt list --remote` | Interactive browser for uploaded sessions with access control |
| `pt upload [session-id]` | Upload a session — opens a picker if no ID given |
| `pt view [id]` | View a local session in a scrollable TUI |
| `pt view user/id` | View a remote session (public or your own private) |

### View flags

| Flag | Description |
|------|-------------|
| `--expand-agents` | Show full subagent conversations inline |
| `--show-thinking` | Show Claude's thinking blocks |
| `--raw` | Output the processed session as JSON |

## What Gets Uploaded

PromptRail processes the raw Claude Code JSONL into a clean, render-ready format:

- User messages and assistant responses grouped into conversation turns
- Tool calls (Bash, Read, Write, Edit, Grep, Glob) with inputs and results paired together
- Subagent conversations inlined inside their parent tool call
- Large tool results truncated to 10KB
- Home directory paths sanitized to `~/`
- `.env` file contents automatically redacted before upload

## Access Control

Sessions are **private** by default (only you can view them). Toggle to **public** in the remote list TUI by pressing `p`, or set it during upload:

```sh
pt upload --access public
```

## Architecture

```
pt (CLI)          →  Backend (Go, Fly.io)  →  Supabase Postgres
                                           →  Cloudflare R2
```

- **CLI** parses local JSONL, renders in terminal, uploads via presigned URLs
- **Backend** handles auth, session metadata, presigned URL generation — never touches blob data
- **Postgres** stores users, session metadata, access control
- **R2** stores gzipped session blobs — zero egress fees

## Self-Hosting

### Prerequisites

- Go 1.21+
- PostgreSQL (or Supabase free tier)
- Cloudflare R2 bucket
- GitHub OAuth App

### Setup

1. Create a GitHub OAuth App at **Settings > Developer Settings > OAuth Apps**
   - Callback URL: `https://your-domain/auth/github/callback`

2. Create a Cloudflare R2 bucket and API token

3. Copy `.env.example` to `.env` and fill in the values:

```sh
cp .env.example .env
```

4. Run the server:

```sh
make dev-server
```

5. Point the CLI at your server:

```sh
export PT_BACKEND_URL=https://your-domain
pt login
```

## Development

```sh
# build CLI
make cli

# build server
make server

# run server with hot reload
make dev-server

# release (via goreleaser)
git tag v0.1.0
git push origin v0.1.0
```

## License

MIT
