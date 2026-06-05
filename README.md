# hailsDotGO

A fan-made Pokémon GO companion web app with raid counters, a DPS calculator, PvP IV rankings, a shiny availability tracker, a personal shiny collection tracker, a Trainer Directory, and a live Raid Finder for coordinating remote raids.

This project includes:
- Real-time raid boss listings with inline counter recommendations
- DPS calculator and bulk moveset comparison
- PvP IV ranker across all three leagues
- Full shiny availability tracker with obtain-method detail and normal vs shiny sprite comparison
- Personal shiny collection: log every shiny you've caught with method tracking and stats
- Trainer Directory: opt in to share your trainer code publicly, searchable by trainer name
- Raid Finder: post and join remote raids in real time with friend-code sharing, confirm/invite flow, and post-raid ratings
- User accounts with registration (open or invite-only), login, and an admin panel
- Credits and changelog tab

---

## Preview

**[pogo.hails.live](https://pogo.hails.live)** live and free to use.

---

## What You'll Need

- **Go 1.25+**
  - [go.dev/dl](https://go.dev/dl/)
- **Node.js 18+ and npm**
  - [nodejs.org](https://nodejs.org/)
- **MySQL 8+** *(for user accounts and the shiny collection tracker)*
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

### 3. Database
User accounts, sessions, shiny collection entries, site settings, and invite tokens are stored in MySQL. Apply `schema.sql` to set up the tables:

```bash
mysql -u youruser -p < schema.sql
```

Then set the `DB_HOST`, `DB_USER`, `DB_PASS`, and `DB_NAME` environment variables to point the app at your database.

**First admin setup:** Registration is closed by default. To create your first account:

1. Open registration temporarily:
   ```sql
   UPDATE site_settings SET setting_value = '1' WHERE setting_key = 'registration_open';
   ```
2. Set `SUPERADMIN_USER=yourusername` in `.env` using the username you plan to register with.
3. Start the server and register at `/register`.
4. You now have full admin access. Close registration from the admin panel whenever you want.

---

### 4. API Layer
- `GET /api/data` → all game data combined (stats, moves, types, shinies)
- `GET /api/raids` → current raid bosses grouped by tier
- `POST /api/refresh` → manually trigger a data re-fetch outside the 6-hour cycle

---

### 5. Frontend (TypeScript)
All calculations run **client-side**: DPS, TDO, type effectiveness, CP math, IV stat products. The page loads once, gets the data, and does everything locally from there.

TypeScript source lives in `ts/` and compiles to `static/js/` via esbuild.

---

### 6. Pages
| Route | What it does |
|---|---|
| `/` | Home |
| `/raids` | Active raid bosses + inline counters |
| `/dps` | DPS calculator + compare table |
| `/pvp` | IV stat product ranker (GL / UL / ML) |
| `/events` | Shiny Pokémon availability tracker |
| `/shinies` | Your personal shiny collection (login required) |
| `/trainers` | Trainer Directory and Raid Finder |
| `/settings` | Trainer profile: name, pronouns, avatar, friend code, location privacy (login required) |
| `/credits` | About, data sources, and changelog |
| `/changelog` | Redirects to `/credits?tab=changelog` |
| `/login` | Sign in |
| `/register` | Create an account (open or invite-only) |
| `/admin` | Admin panel: registration toggle, invite generation, user management (mod+ required) |

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

Generate a CSRF key and paste it in:

```bash
openssl rand -hex 32
```

Set `CSRF_KEY` to the output. Without it the app still runs, but CSRF tokens are regenerated on every restart (breaking any active login sessions).

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

> **Note:** Requires `VPS_HOST`, `VPS_USER`, `SUPERADMIN_USER`, and `CSRF_KEY` set in `.env`, and an SSH key at `~/.ssh/hailsdotgo` authorized on the server.

---

## Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `PORT` | No | `8080` | HTTP listen port |
| `DB_HOST` | Yes | | MySQL host and port (e.g. `localhost:3306`) |
| `DB_USER` | Yes | | MySQL username |
| `DB_PASS` | Yes | | MySQL password |
| `DB_NAME` | Yes | | MySQL database name |
| `SUPERADMIN_USER` | Yes | | Username that always has admin privileges; required to start |
| `CSRF_KEY` | No | random | 64-char hex string for CSRF protection; generate with `openssl rand -hex 32` |
| `OPENWEATHER_KEY` | No | | OpenWeatherMap API key for weather boost data |
| `VPS_HOST` | Deploy only | | VPS hostname or IP |
| `VPS_USER` | Deploy only | | SSH username on the VPS |
| `VPS_PASS` | No | | VPS password for manual reference; not read by `deploy.ps1`, which uses SSH key auth |

---

## Data Sources

| Source | What it provides |
|---|---|
| [PoGoAPI](https://pogoapi.net) | Pokémon stats, moves, shinies, type effectiveness, CP multipliers |
| [ScrapedDuck](https://github.com/bigfoott/ScrapedDuck) | Live raid boss data (sourced from LeekDuck) |
| [PokéAPI](https://pokeapi.co) | Pokémon sprites, Pokédex flavor text, genus, legendary/mythical flags, in-game cries |
| [OpenWeatherMap](https://openweathermap.org) | Current weather data for Pokémon GO weather boost detection |
| [Pokémon Showdown](https://pokemonshowdown.com) | Trainer class sprites for trainer profile avatars |

---

## Disclaimer

Fan-made tool. Not affiliated with, endorsed by, or connected to Niantic or The Pokémon Company. Pokémon and all related names are trademarks of their respective owners.

---

Enjoy,  
Hails❤️
