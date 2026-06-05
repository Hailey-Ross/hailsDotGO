# hailsDotGO

A fan-made Pokémon GO companion web app with raid counters, a DPS calculator, PvP IV rankings, and a shiny tracker.

This project includes:
- Real-time raid boss listings with inline counter recommendations
- DPS calculator and bulk moveset comparison
- PvP IV ranker across all three leagues
- Full shiny availability tracker
- A changelog so you can follow along with updates

---

## Preview

**[pogo.hails.live](https://pogo.hails.live)** live and free to use.

---

## What You'll Need

- **Go 1.22+**
  - [go.dev/dl](https://go.dev/dl/)
- **Node.js 18+ and npm**
  - [nodejs.org](https://nodejs.org/)
- **An SSH key** *(deployment only)*
  - Expected at `~/.ssh/hailsdotgo`

---

## How It Works

### 1. Data Fetching (Go backend)
On startup, the server pulls game data from [PoGoAPI](https://pogoapi.net) and caches it in memory:
- Pokémon base stats
- Fast and charged moves
- Shiny availability
- Type effectiveness
- CP multipliers

Data refreshes automatically every 6 hours.

---

### 2. Raid Data
Live raid bosses are fetched from [ScrapedDuck](https://github.com/bigfoott/ScrapedDuck) (sourced from LeekDuck) and cached for 5 minutes per request.

---

### 3. API Layer
- `GET /api/data` → all game data combined (stats, moves, types, shinies)
- `GET /api/raids` → current raid bosses grouped by tier
- `POST /api/refresh` → manually trigger a data re-fetch outside the 6-hour cycle

---

### 4. Frontend (TypeScript)
All calculations run **client-side**: DPS, TDO, type effectiveness, CP math, IV stat products. The page loads once, gets the data, and does everything locally from there.

TypeScript source lives in `ts/` and compiles to `static/js/` via esbuild.

---

### 5. Pages
| Route | What it does |
|---|---|
| `/` | Home |
| `/raids` | Active raid bosses + inline counters |
| `/dps` | DPS calculator + compare table |
| `/pvp` | IV stat product ranker (GL / UL / ML) |
| `/events` | Shiny Pokémon tracker |
| `/changelog` | Running log of site updates |

---

## Setup

### 1. Clone the repo

```bash
git clone https://github.com/Hailey-Ross/hailsDotGO.git
cd hailsDotGO
```

---

### 2. Install dependencies

```bash
make setup
```

This runs `npm install`.

---

### 3. Configure environment

```bash
cp .env.example .env
```

Edit `.env` with your values (see [Environment Variables](#environment-variables) below).

---

### 4. Run locally

Two processes need to run side by side:

```bash
# Terminal 1: watch and recompile TypeScript on save
npm run watch

# Terminal 2: run the Go server
go run .
```

Visit [http://localhost:8080](http://localhost:8080)

---

## Building

```bash
# Build for your current platform
make build
# outputs ./hailsDotGO

# Cross-compile for Linux (from Windows)
$env:GOOS = "linux"; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
go build -ldflags="-s -w" -o hailsDotGO-linux .
```

---

## Deployment

`deploy.ps1` handles the full deploy cycle:

1. Cross-compiles a Linux binary
2. Bundles TypeScript via esbuild
3. SCPs the binary, templates, static assets, and systemd unit to the VPS
4. Restarts the systemd service

```powershell
.\deploy.ps1
```

> **Note:** Requires `VPS_HOST` and `VPS_USER` set in `.env`, and an SSH key at `~/.ssh/hailsdotgo` authorized on the server.

---

## Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `PORT` | No | `8080` | HTTP listen port |
| `VPS_HOST` | Deploy only | | VPS hostname or IP |
| `VPS_USER` | Deploy only | | SSH username on the VPS |

---

## Data Sources

| Source | What it provides |
|---|---|
| [PoGoAPI](https://pogoapi.net) | Pokémon stats, moves, shinies, type effectiveness, CP multipliers |
| [ScrapedDuck](https://github.com/bigfoott/ScrapedDuck) | Live raid boss data (sourced from LeekDuck) |
| [PokeAPI sprites](https://github.com/PokeAPI/sprites) | Pokémon sprite images |

---

## Disclaimer

Fan-made tool. Not affiliated with, endorsed by, or connected to Niantic or The Pokémon Company. Pokémon and all related names are trademarks of their respective owners.

---

Enjoy,  
Hails❤️
