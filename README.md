# hailsDotGO

A fan-made Pokémon GO companion web app. Live at **[pogo.hails.live](https://pogo.hails.live)**.

## Features

- **Current Raids** — Browse active raid bosses across all tiers (T1, T3, T5, Mega/Primal), filter by tier and type, and tap any boss to see the top 20 counters ranked by DPS and TDO
- **DPS Calculator** — Compare Pokémon movesets and calculate which attacker deals the most damage; includes a bulk compare table
- **PvP IV Ranker** — Check stat product rankings across Great League (1500 CP), Ultra League (2500 CP), and Master League for any Pokémon and IV spread
- **Shinies** — Browse all currently available shiny Pokémon and how to encounter them

All game mechanics (DPS, TDO, CP, type effectiveness) are calculated client-side for instant results with no per-interaction network requests.

## Tech stack

| Layer | Technology |
|---|---|
| Backend | Go 1.22, [chi](https://github.com/go-chi/chi) router |
| Templating | Go `html/template` |
| Frontend | TypeScript 5, compiled with [esbuild](https://esbuild.github.io/) |
| Styling | Hand-written CSS |
| Effects | [particles.js](https://vincentgarreau.com/particles.js/) |
| Reverse proxy | Caddy (TLS termination) |
| Process manager | systemd |

## Data sources

| Source | Data |
|---|---|
| [PoGoAPI](https://pogoapi.net) | Pokémon base stats, moves, shiny registry, type effectiveness, CP multipliers |
| [ScrapedDuck](https://github.com/bigfoott/ScrapedDuck) (LeekDuck) | Live raid boss list |
| [PokeAPI sprites](https://github.com/PokeAPI/sprites) | Pokémon sprite images |

Game data is fetched from PoGoAPI on server startup and refreshed every 6 hours. Raid data is fetched from ScrapedDuck on each `/api/raids` request and cached for 5 minutes.

## Prerequisites

- [Go 1.22+](https://go.dev/dl/)
- [Node.js 18+](https://nodejs.org/) and npm

## Local development

```bash
git clone https://github.com/Hailey-Ross/hailsDotGO.git
cd hailsDotGO

# Install npm dependencies
make setup

# Copy the example env file
cp .env.example .env
```

Then run two processes side by side:

```bash
# Terminal 1 — watch and recompile TypeScript on change
npm run watch

# Terminal 2 — run the Go server
go run .
```

Visit [http://localhost:8080](http://localhost:8080).

## Building

```bash
# Build for the current platform
make build         # outputs ./hailsDotGO

# Cross-compile for Linux (e.g. from Windows)
$env:GOOS = "linux"; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
go build -ldflags="-s -w" -o hailsDotGO-linux .
```

## Deployment

`deploy.ps1` automates the full deploy cycle:

1. Cross-compiles a Linux binary
2. Bundles TypeScript with esbuild
3. SCPs the binary, templates, static assets, and systemd service file to the VPS over SSH
4. Restarts the systemd service

**Requirements:**

- `.env` file with `VPS_HOST` and `VPS_USER` set (see `.env.example`)
- SSH key at `~/.ssh/hailsdotgo` authorized on the server

```powershell
.\deploy.ps1
```

## Environment variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `PORT` | No | `8080` | HTTP listen port |
| `VPS_HOST` | Deploy only | — | VPS hostname or IP |
| `VPS_USER` | Deploy only | — | SSH username on the VPS |

## Disclaimer

Fan-made tool. Not affiliated with, endorsed by, or connected to Niantic or The Pokémon Company. Pokémon and all related names are trademarks of their respective owners.
