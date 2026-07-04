# hailsDotGO

A fan-made Pokémon GO companion web app built in Go.

- Live raid bosses and Max Battles with counter recommendations
- DPS calculator, moveset comparison, and PvP IV ranker (GL / UL / ML)
- IV Calculator with manual entry, form and Pokémon status support, and OCR screenshot scanning (CP, HP, dust, appraisal auto-detection)
- Events page with full details sourced from LeekDuck via ScrapedDuck
- Shiny Dex plus a personal shiny collection tracker
- Trainer Directory with dedicated profile pages, a real-time Raid Finder with matchmaking, lobbies, and a trust system
- Friends list, real-time raid notifications with 🔔 badge and optional ding sound, and blocked-user management
- Community feedback (positive/neutral/negative trainer reviews) visible on every trainer profile
- In-app bug reports ("Report Me Not") with a threaded reporter and staff messenger, labels, assignments, canned responses, and satisfaction ratings
- Player reporting for bad actors (spoofing, harassment, and more) routed to a shared moderator queue
- User accounts (open or invite-only registration) with signup email confirmation and self-service password reset by email, plus staff roles, strikes, tags, and awards
- Supporter store with optional donation perks (PayPal)
- Multi-language UI (English, Spanish, French, German, Japanese) with a built-in translator workspace, community application workflow, and automatic GitHub sync that keeps approved translations safe across updates
- Public JSON API with rate limits, plus an unthrottled private API for trusted consumers

---

## Documentation

Everything beyond the quick start below lives in the **[project wiki](https://github.com/Hailey-Ross/hailsDotGO/wiki)**:

| I want to... | Read this |
|---|---|
| Install and run my own instance | [Getting Started](https://github.com/Hailey-Ross/hailsDotGO/wiki/Getting-Started) |
| Look up an environment variable | [Configuration](https://github.com/Hailey-Ross/hailsDotGO/wiki/Configuration) |
| Understand the database and migrations | [Database Guide](https://github.com/Hailey-Ross/hailsDotGO/wiki/Database-Guide) |
| Deploy to a Linux server | [Deployment](https://github.com/Hailey-Ross/hailsDotGO/wiki/Deployment) |
| Run the site day to day | [Operations](https://github.com/Hailey-Ross/hailsDotGO/wiki/Operations) |
| Use the JSON API | [API Reference](https://github.com/Hailey-Ross/hailsDotGO/wiki/API-Reference) |
| Learn how a feature works | [Raids and Counters](https://github.com/Hailey-Ross/hailsDotGO/wiki/Raids-and-Counters), [Raid Finder](https://github.com/Hailey-Ross/hailsDotGO/wiki/Raid-Finder), [Social Features](https://github.com/Hailey-Ross/hailsDotGO/wiki/Social-Features), [Trust and Awards](https://github.com/Hailey-Ross/hailsDotGO/wiki/Trust-and-Awards), [Shiny Tracking](https://github.com/Hailey-Ross/hailsDotGO/wiki/Shiny-Tracking), [Trainer Directory](https://github.com/Hailey-Ross/hailsDotGO/wiki/Trainer-Directory), [Store](https://github.com/Hailey-Ross/hailsDotGO/wiki/Store) |
| Understand roles and permissions | [Accounts and Roles](https://github.com/Hailey-Ross/hailsDotGO/wiki/Accounts-and-Roles), [Admin Guide](https://github.com/Hailey-Ross/hailsDotGO/wiki/Admin-Guide) |
| Report a bug or a player, and triage reports | [Bug Reports](https://github.com/Hailey-Ross/hailsDotGO/wiki/Bug-Reports), [Player Reports](https://github.com/Hailey-Ross/hailsDotGO/wiki/Player-Reports) |
| Translate the site or add a language | [Localization](https://github.com/Hailey-Ross/hailsDotGO/wiki/Localization), [Translator Workspace](https://github.com/Hailey-Ross/hailsDotGO/wiki/Translator-Workspace) |
| Hack on the code | [Architecture](https://github.com/Hailey-Ross/hailsDotGO/wiki/Architecture), [Building and Development](https://github.com/Hailey-Ross/hailsDotGO/wiki/Building-and-Development), [Frontend Guide](https://github.com/Hailey-Ross/hailsDotGO/wiki/Frontend-Guide) |

---

## A Note on Stability

**Prefer [Releases](https://github.com/Hailey-Ross/hailsDotGO/releases) over cloning `main` directly.**

`main` reflects active development and is not guaranteed to be stable at all times, particularly during larger rewrites or feature implementations. Tagged releases are tested and represent known-good states of the app.

---

## Preview

**[pogo.hails.live](https://pogo.hails.live)** is live and free to use.

---

## Quick Start

You will need **Go 1.25+**, **Node.js 18+ with npm**, and **MySQL 8+**.

```bash
# 1. Clone and install dependencies
git clone https://github.com/Hailey-Ross/hailsDotGO.git
cd hailsDotGO
make setup

# 2. Configure environment
cp .env.example .env
# edit .env with your database credentials and SUPERADMIN_USER
# optional: RESEND_API_KEY and MAIL_FROM enable transactional email
# (password reset, signup confirmation); everything works without them

# 3. Create the database
mysql -u youruser -p yourdbname < schema.sql

# 4. Run locally (two terminals)
npm run watch   # terminal 1: recompile TypeScript on save
go run .        # terminal 2: run the Go server
```

Visit [http://localhost:8080](http://localhost:8080).

The [Getting Started](https://github.com/Hailey-Ross/hailsDotGO/wiki/Getting-Started) wiki page covers the rest: creating the first admin account, the CSRF key, and platform notes. Upgrading an existing install? Use the migrate tool instead of `schema.sql`:

```bash
go run ./cmd/migrate -from v0.1.4a   # baseline at your version, then apply what is pending
go run ./cmd/migrate                 # every upgrade after that
```

When you are ready to put it on a server, see [Deployment](https://github.com/Hailey-Ross/hailsDotGO/wiki/Deployment).

If you fork the repo, copy the provided `.gitignore` template so build output and secrets never get committed:

```bash
cp .gitignore.example .gitignore
```

---

## How It Works (the short version)

Game data (stats, moves, shinies, type chart) comes from [PoGoAPI](https://pogoapi.net) and refreshes every 6 hours, with embedded snapshots as an offline fallback. Official localized Pokémon names (French, German, Spanish, Japanese) come from [PokéAPI](https://pokeapi.co). Raid bosses and Max Battles come from [pokemon-go-api](https://github.com/pokemon-go-api/pokemon-go-api), and the events feed from [ScrapedDuck](https://github.com/bigfoott/ScrapedDuck) with sanitized detail pages from [LeekDuck](https://leekduck.com). All battle math runs client-side in TypeScript compiled by esbuild; accounts and everything persistent live in MySQL.

Full details, refresh schedules, and attribution: [Data Sources](https://github.com/Hailey-Ross/hailsDotGO/wiki/Data-Sources) and [Architecture](https://github.com/Hailey-Ross/hailsDotGO/wiki/Architecture).

---

## Disclaimer

Fan-made tool. Not affiliated with, endorsed by, or connected to Niantic or The Pokémon Company. Pokémon and all related names are trademarks of their respective owners.

---

Enjoy,  
Hails❤️
