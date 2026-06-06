# hailsDotGO

A fan-made Pokémon GO companion web app with raid counters, a DPS calculator, PvP IV rankings, a shiny availability tracker, a personal shiny collection tracker, a Trainer Directory, and a live Raid Finder for coordinating remote raids.

This project includes:
- Real-time raid boss listings with inline counter recommendations
- DPS calculator and bulk moveset comparison (with type-effectiveness target picker)
- PvP IV ranker across all three leagues
- Full shiny availability tracker with obtain-method detail and normal vs shiny sprite comparison
- Personal shiny collection: log every shiny you've caught with method tracking and stats
- Trainer Directory: all registered users appear; set `profile_public` to expose your trainer code and full profile details, searchable by trainer name; sorted by online status, staff rank, super-donator status, raid XP, then alphabetically
- Raid Finder: post and join remote raids with a queue system, accept/decline flow, lobby view with co-raider list, XP-based ranking, weighted host ratings, and post-raid rating system
- Current weather boost shown in the Raid Finder based on your saved city
- User accounts with registration (open or invite-only), login, and an admin panel
- Per-page maintenance toggle: admins can disable individual pages (Raids, DPS, PvP, Events, Trainers, Trainer Directory section, Raid Finder section, Shinies) from the admin panel; disabled pages show a maintenance screen instead
- Tag management: superadmins create/edit/delete tags with custom names and colors; mods and above can assign tags to any user
- Supporter store with optional donation perks: Supporter Pack, raid queue priority, and custom profile tag (PayPal, sandbox/live toggle)
- Multi-language interface: English, Spanish, French, German (cookie-persisted, synced to account)
- Credits and changelog tab

---

## A Note on Stability

**Prefer [Releases](https://github.com/Hailey-Ross/hailsDotGO/releases) over cloning `main` directly.**

The `main` branch reflects active development and is not guaranteed to be stable at all times, particularly during larger rewrites or feature implementations. Tagged releases are tested and represent known-good states of the app.

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

Data refreshes automatically every 6 hours. If PoGoAPI is unreachable at startup, the server falls back to embedded snapshot data compiled into the binary so the app can serve requests immediately.

---

### 2. Raid Data
Live raid bosses are fetched from [ScrapedDuck](https://github.com/bigfoott/ScrapedDuck) (sourced from LeekDuck). The response is cached to disk so a restart never shows "temporarily unavailable." Raids refresh once daily at noon Mountain Time; a stale on-disk cache is used if the upstream fetch fails.

---

### 3. Database
All persistent data is stored in MySQL: user accounts, sessions, shiny collections, trainer profiles, raid posts and joins, tags, store purchases, and site settings.

`schema.sql` contains the base `CREATE TABLE` statements followed by all migration blocks as SQL comments. For a **fresh install**, apply the base tables and then run every migration block in order (uncomment and execute each block). The schema automatically seeds all `page_*_enabled` site settings to `1` (all pages on), so no manual SQL is needed to enable pages after a fresh install.

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

**Store setup (optional):** The supporter store is disabled by default. To enable it:

1. Set the four `PAYPAL_*` variables in `.env` (client ID, client secret, mode, webhook ID).
2. Enable the store from the admin panel, or directly:
   ```sql
   UPDATE site_settings SET setting_value = '1' WHERE setting_key = 'store_enabled';
   ```
3. The migration block in `schema.sql` seeds two default store items (Supporter Pack and Priority Pass). Run it if you haven't already, or insert your own items into `store_items`.

---

### 4. API Layer

**Public** (rate-limited per IP):
- `GET /api/data` → all game data combined (stats, moves, types, shinies, Pokémon types)
- `GET /api/raids` → current raid bosses grouped by tier
- `GET /api/pokemon` → Pokémon base stats list
- `GET /api/moves` → fast and charged move data

**Private** (requires API access permission, no rate limit):
- `GET /api/private/data`, `/api/private/raids`, `/api/private/pokemon`, `/api/private/moves` → same as public

**Protected:**
- `POST /api/refresh` → manually trigger a data re-fetch (requires API access, globally rate-limited)

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
| `/store` | Supporter store: donation perks, custom profile tag, raid queue priority |
| `/credits` | About, data sources, and changelog |
| `/changelog` | Redirects to `/credits?tab=changelog` |
| `/login` | Sign in |
| `/register` | Create an account (open or invite-only) |
| `/admin` | Admin panel: registration toggle, invite generation, page maintenance toggles, tag management, user management (mod+ required) |

---

### User Roles

| Role | Permissions |
|---|---|
| `user` | Default for all registered accounts |
| `tester` | Raid rank label "PKMN Scientist"; sorted above regular users in Trainer Directory |
| `moderator` | Admin panel access: strikes, raid bans, directory hide, tag assignment |
| `admin` | All mod actions + invite generation, user rename/disable, password reset, role changes, page maintenance toggles |
| `superadmin` | Set via `SUPERADMIN_USER` env var; all admin capabilities + tag create/edit/delete; cannot be targeted by admin actions |

Staff roles are protected: mods, admins, and superadmins cannot be raid-banned or hidden from the directory.

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

> **Note:** Requires `VPS_HOST`, `VPS_USER`, `SUPERADMIN_USER`, and `CSRF_KEY` set in `.env`, and an SSH key at `~/.ssh/hailsdotgo` authorized on the server. If using the store, also set the four `PAYPAL_*` vars — they are written to the server's `app.env` by the deploy script.

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
| `OPENWEATHER_KEY` | No | | *(Unused — weather now uses Open-Meteo, which requires no API key)* |
| `PAYPAL_CLIENT_ID` | Store only | | PayPal REST API client ID |
| `PAYPAL_CLIENT_SECRET` | Store only | | PayPal REST API client secret |
| `PAYPAL_MODE` | Store only | `sandbox` | `sandbox` or `live` |
| `PAYPAL_WEBHOOK_ID` | Store only | | PayPal webhook ID for server-side payment confirmation |
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
| [Open-Meteo](https://open-meteo.com) | Current weather data for Pokémon GO weather boost detection (no API key required) |
| [Pokémon Showdown](https://pokemonshowdown.com) | Trainer class sprites for trainer profile avatars |
| [Dreamstone Mysteries](https://github.com/dsmyst/dreamstone-mysteries) (dsmyst) | GBA-style trainer sprites bundled as local static files |

---

## Disclaimer

Fan-made tool. Not affiliated with, endorsed by, or connected to Niantic or The Pokémon Company. Pokémon and all related names are trademarks of their respective owners.

---

Enjoy,  
Hails❤️
