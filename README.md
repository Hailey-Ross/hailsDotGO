# hailsDotGO

A fan-made Pokémon GO companion web app built in Go.

- Raid boss listings with counter recommendations
- DPS calculator and bulk moveset comparison
- PvP IV ranker (GL / UL / ML)
- Shiny availability tracker with normal vs shiny sprite comparison
- Personal shiny collection tracker
- Trainer Directory with searchable profiles
- Raid Finder: post and join remote raids with queue, lobby, and post-raid rating system
- Weather boost display based on your saved city
- User accounts with registration (open or invite-only)
- Per-page maintenance toggles from the admin panel
- Tag system: superadmins create/edit/delete tags; mods and above assign them to users
- Supporter store with optional donation perks (PayPal, sandbox/live toggle)
- Custom tag requests: supporters submit a tag name and color (staff-reviewed); weekly submission cooldown and color change rate limit enforced
- Account suspension with optional staff-entered reason shown to the user on login
- Admin Users tab: mini-card grid with click-to-open modal detail view and full controls
- Multi-language: English, Spanish, French, German

---

## A Note on Stability

**Prefer [Releases](https://github.com/Hailey-Ross/hailsDotGO/releases) over cloning `main` directly.**

`main` reflects active development and is not guaranteed to be stable at all times, particularly during larger rewrites or feature implementations. Tagged releases are tested and represent known-good states of the app.

---

## Preview

**[pogo.hails.live](https://pogo.hails.live)** is live and free to use.

---

## What You'll Need

- **Go 1.25+** ([go.dev/dl](https://go.dev/dl/))
- **Node.js 18+ and npm** ([nodejs.org](https://nodejs.org/))
- **MySQL 8+** for accounts, shiny collections, and all persistent data
- **An SSH key** *(deployment only)* expected at `~/.ssh/hailsdotgo`

---

## How It Works

### 1. Game Data
On startup, the server fetches Pokémon stats, moves, shinies, type effectiveness, and CP multipliers from [PoGoAPI](https://pogoapi.net) and caches them in memory. Data refreshes every 6 hours. If PoGoAPI is unreachable at startup, the server falls back to embedded snapshot data so the app can still serve requests.

### 2. Raid Data
Live raid bosses are fetched from [ScrapedDuck](https://github.com/bigfoott/ScrapedDuck) (sourced from LeekDuck), cached to disk, and refreshed every 4 hours starting at midnight Mountain Time (12:00 AM, 4:00 AM, 8:00 AM, 12:00 PM, 4:00 PM, 8:00 PM). A stale cache is used if the upstream fetch fails.

### 3. Database
All persistent data lives in MySQL: accounts, sessions, shiny collections, trainer profiles, raid posts, tags, store purchases, and site settings.

### 4. Frontend
All calculations (DPS, TDO, type effectiveness, CP math, IV stat products) run client-side. TypeScript source lives in `ts/` and compiles to `static/js/` via esbuild.

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

---

### 3. Configure environment

```bash
cp .env.example .env
```

Edit `.env` with your values. See [Environment Variables](#environment-variables) below.

Generate a CSRF key and set it as `CSRF_KEY`:

```bash
openssl rand -hex 32
```

Without `CSRF_KEY` the app will still run, but tokens reset on every restart and break active sessions.

---

### 4. Set up the database

```bash
mysql -u youruser -p yourdbname < schema.sql
```

`schema.sql` contains all base tables followed by migration blocks as SQL comments. For a fresh install, apply the base tables then run every migration block in order. The schema automatically seeds all `page_*_enabled` settings to `1` so no manual SQL is needed to enable pages.

**First admin account:**

1. Temporarily open registration:
   ```sql
   UPDATE site_settings SET setting_value = '1' WHERE setting_key = 'registration_open';
   ```
2. Set `SUPERADMIN_USER=yourusername` in `.env` using the username you plan to register with.
3. Start the server and register at `/register`.
4. Close registration from the admin panel when done.

---

### 5. Run locally

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

`deploy.ps1` handles the full cycle: cross-compiles a Linux binary, bundles TypeScript, SCPs all files to the VPS, and restarts the systemd service.

```powershell
.\deploy.ps1
```

Requires `VPS_HOST`, `VPS_USER`, `SUPERADMIN_USER`, and `CSRF_KEY` in `.env`, and an SSH key at `~/.ssh/hailsdotgo` authorized on the server. If using the store, also set the four `PAYPAL_*` vars; the deploy script writes them to the server's `app.env`.

---

## Store Setup (Optional)

The store is disabled by default. To enable:

1. Set `PAYPAL_CLIENT_ID`, `PAYPAL_CLIENT_SECRET`, `PAYPAL_MODE`, and `PAYPAL_WEBHOOK_ID` in `.env`.
2. Enable from the admin panel, or directly:
   ```sql
   UPDATE site_settings SET setting_value = '1' WHERE setting_key = 'store_enabled';
   ```
3. Run the store migration block in `schema.sql` to seed the default items (Supporter Pack and Priority Pass), or insert your own into `store_items`.

---

## Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `PORT` | No | `8080` | HTTP listen port |
| `DB_HOST` | Yes | | MySQL host and port (e.g. `localhost:3306`) |
| `DB_USER` | Yes | | MySQL username |
| `DB_PASS` | Yes | | MySQL password |
| `DB_NAME` | Yes | | MySQL database name |
| `SUPERADMIN_USER` | Yes | | Username with permanent superadmin privileges |
| `CSRF_KEY` | No | random | 64-char hex string for CSRF protection |
| `PAYPAL_CLIENT_ID` | Store only | | PayPal REST API client ID |
| `PAYPAL_CLIENT_SECRET` | Store only | | PayPal REST API client secret |
| `PAYPAL_MODE` | Store only | `sandbox` | `sandbox` or `live` |
| `PAYPAL_WEBHOOK_ID` | Store only | | PayPal webhook ID for payment confirmation |
| `VPS_HOST` | Deploy only | | VPS hostname or IP |
| `VPS_USER` | Deploy only | | SSH username on the VPS |
| `VPS_PASS` | No | | For manual reference only; not used by `deploy.ps1` |

---

## User Roles

| Role | Permissions |
|---|---|
| `user` | Default for all registered accounts |
| `tester` | Raid rank label "PKMN Scientist"; sorted above regular users in the Trainer Directory |
| `moderator` | Admin panel access: strikes, raid bans, directory hide, tag assignment |
| `admin` | All mod actions + invite generation, rename/suspend users (with optional reason), password reset, role changes, page toggles |
| `superadmin` | Set via `SUPERADMIN_USER` env var; all admin capabilities + tag create/edit/delete; immune to admin actions |

Staff (mod and above) cannot be raid-banned or hidden from the directory.

---

## Data Sources

| Source | What it provides |
|---|---|
| [PoGoAPI](https://pogoapi.net) | Pokémon stats, moves, shinies, type effectiveness, CP multipliers |
| [ScrapedDuck](https://github.com/bigfoott/ScrapedDuck) | Live raid boss data (sourced from LeekDuck) |
| [PokéAPI](https://pokeapi.co) | Sprites, Pokédex text, genus, legendary/mythical flags, cries |
| [Open-Meteo](https://open-meteo.com) | Weather data for Pokémon GO weather boost detection (no API key required) |
| [Pokémon Showdown](https://pokemonshowdown.com) | Trainer class sprites for avatars |
| [Dreamstone Mysteries](https://github.com/dsmyst/dreamstone-mysteries) | GBA-style trainer sprites |

---

## Disclaimer

Fan-made tool. Not affiliated with, endorsed by, or connected to Niantic or The Pokémon Company. Pokémon and all related names are trademarks of their respective owners.

---

Enjoy,  
Hails❤️
