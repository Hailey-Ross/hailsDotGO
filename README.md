# hailsDotGO

A fan-made Pokémon GO companion web app built in Go.

- Live raid bosses and Max Battles with counter recommendations
- DPS calculator, moveset comparison, and PvP IV ranker (GL / UL / ML)
- IV Calculator with manual entry, form and Pokémon status support, and OCR screenshot scanning that reads CP, HP, dust, the level arc, and appraisal automatically, and can identify a nicknamed Pokémon from its candy
- Events page with full details sourced from LeekDuck via ScrapedDuck
- Shiny Dex plus a personal shiny collection tracker with regional forms (Alolan, Galarian, Hisuian, Paldean)
- Trainer Directory with dedicated profile pages, plus a real-time Raid Finder with matchmaking, lobbies, and a trust system (the Raid Finder is in early alpha)
- Friends list, real-time raid notifications with 🔔 badge and optional ding sound, and blocked-user management
- Community feedback (positive/neutral/negative trainer reviews) visible on every trainer profile
- In-app bug reports ("Report Me Not") with a threaded reporter and staff messenger, labels, assignments, canned responses, and satisfaction ratings
- Player reporting for bad actors (spoofing, harassment, and more) routed to a shared moderator queue
- User accounts (open or invite-only registration) with signup email confirmation and self-service password reset by email, plus staff roles, strikes, tags, and awards
- Supporter store with optional donation perks (PayPal)
- Multi-language UI (English, Spanish, French, German, Japanese) with a built-in translator workspace, community application workflow, and automatic GitHub sync that keeps approved translations safe across updates
- Public JSON API with rate limits, plus an unthrottled private API for trusted consumers
- **FlexDex**, an Android companion app on the same account and the same database, which scans a Pokémon straight off your screen while you play and carries almost the whole site as native screens

---

## Documentation

Everything beyond the quick start below lives in the **[project wiki](https://github.com/Hailey-Ross/hailsDotGO/wiki)**:

| I want to... | Read this |
|---|---|
| Install and run my own instance | [Getting Started](https://github.com/Hailey-Ross/hailsDotGO/wiki/Getting-Started) |
| Build from a tagged release or upgrade from a previous one | [Releases and Upgrading](https://github.com/Hailey-Ross/hailsDotGO/wiki/Releases-and-Upgrading) |
| Look up an environment variable | [Configuration](https://github.com/Hailey-Ross/hailsDotGO/wiki/Configuration) |
| Understand the database and migrations | [Database Guide](https://github.com/Hailey-Ross/hailsDotGO/wiki/Database-Guide) |
| Deploy to a Linux server | [Deployment](https://github.com/Hailey-Ross/hailsDotGO/wiki/Deployment) |
| Run the site day to day | [Operations](https://github.com/Hailey-Ross/hailsDotGO/wiki/Operations) |
| Use the JSON API | [API Reference](https://github.com/Hailey-Ross/hailsDotGO/wiki/API-Reference) |
| Use the Android companion app | [Companion App](https://github.com/Hailey-Ross/hailsDotGO/wiki/Companion-App) |
| Learn how a feature works | [Raids and Counters](https://github.com/Hailey-Ross/hailsDotGO/wiki/Raids-and-Counters), [Raid Finder](https://github.com/Hailey-Ross/hailsDotGO/wiki/Raid-Finder) (early alpha), [Social Features](https://github.com/Hailey-Ross/hailsDotGO/wiki/Social-Features), [Trust and Awards](https://github.com/Hailey-Ross/hailsDotGO/wiki/Trust-and-Awards), [Shiny Tracking](https://github.com/Hailey-Ross/hailsDotGO/wiki/Shiny-Tracking), [Trainer Directory](https://github.com/Hailey-Ross/hailsDotGO/wiki/Trainer-Directory), [Store](https://github.com/Hailey-Ross/hailsDotGO/wiki/Store) |
| Understand roles and permissions | [Accounts and Roles](https://github.com/Hailey-Ross/hailsDotGO/wiki/Accounts-and-Roles), [Admin Guide](https://github.com/Hailey-Ross/hailsDotGO/wiki/Admin-Guide) |
| Report a bug or a player, and triage reports | [Bug Reports](https://github.com/Hailey-Ross/hailsDotGO/wiki/Bug-Reports), [Player Reports](https://github.com/Hailey-Ross/hailsDotGO/wiki/Player-Reports) |
| Translate the site or add a language | [Localization](https://github.com/Hailey-Ross/hailsDotGO/wiki/Localization), [Translator Workspace](https://github.com/Hailey-Ross/hailsDotGO/wiki/Translator-Workspace) |
| Hack on the code | [Architecture](https://github.com/Hailey-Ross/hailsDotGO/wiki/Architecture), [Building and Development](https://github.com/Hailey-Ross/hailsDotGO/wiki/Building-and-Development), [Frontend Guide](https://github.com/Hailey-Ross/hailsDotGO/wiki/Frontend-Guide) |

---

## A Note on Stability

**Prefer [Releases](https://github.com/Hailey-Ross/hailsDotGO/releases) over cloning `main` directly.**

`main` reflects active development and is not guaranteed to be stable at all times, particularly during larger rewrites or feature implementations. Tagged releases are tested and represent known-good states of the app.

For how to build from a tagged release and how to upgrade an existing install to a newer one, see [Releases and Upgrading](https://github.com/Hailey-Ross/hailsDotGO/wiki/Releases-and-Upgrading).

---

## Preview

**[pogo.hails.app](https://pogo.hails.app)** is live and free to use.

---

## Companion App

**FlexDex is coming soon to an Android device near you.**

FlexDex is the Android companion app for hailsDotGO. It signs in to the same account, reads the same data, and writes to the same database as the website, so a shiny logged on your phone is on your profile before you put it down. It is now a near complete native client rather than a scanner with a browser attached.

**▶ [Watch a tour of build 67](https://assets.hails.cc/flexdex/preview-build-67.mp4)**  
<sub>Older: [tour of build 32](https://youtube.com/shorts/N03vO9PLuy8) (outdated, kept for reference)</sub>

The headline feature is the scanner. Tap a floating bubble while looking at any Pokémon in Pokémon GO and the app captures one frame and reads it on the device with Google ML Kit. No image ever leaves the phone: the only thing the server sees is a few hundred bytes of JSON for the IV solve itself. The bubble has an IV mode and a shiny mode, and successive taps on the same Pokémon accumulate into one reading, because a card never shows everything at once.

It recovers a nicknamed Pokémon from its candy label, corrects a species when the name and the numbers disagree, labels regional forms and asks rather than guessing when the card is ambiguous, traces the level arc when the CP is covered (gold Best Buddy arcs included), reads the appraisal bars and the caught date, works out shadow, purified and lucky from the power up cost, and tells a Mega or Primal from a spent cooldown by the magenta in the countdown. It solves IVs even with no dust cost entered, and re solves from the arc when a misread digit makes a CP impossible. Anything it deduced rather than read is highlighted, with a sentence saying where the value came from.

Beyond that:

- Pokémon Box, your saved collection three sprites across, with the exact spread, IV ring, dates and notes behind a tap
- Raid bosses by tier with CP and weather boosted ranges and shiny availability, expanding to their top counters, with the ranking math ported from the website and unit tested for parity
- The raid rotation schedule as a calendar, a day per row and a month out, linking through to the event behind each one
- Raid Finder queues and lobbies, with confirm, invite, attendance reporting, host rating, and reporting a player from inside the lobby (the Raid Finder itself is still early alpha)
- Shiny collection plus the full shiny dex as a searchable checklist of every species, forme, Unown letter and Vivillon pattern, with Pokédex genus and flavor text, working offline
- Catches logged with no signal: an add is queued on the phone and delivered when service returns, deduplicated by token so a retry can never double log
- The IV calculator, a DPS calculator and the PvP IV ranker, with the battle math running on the phone rather than being fetched
- Events with detail pages, and a bell that sets up to five reminders per event, resolved in your own timezone by the server
- Push notifications for the raid lifecycle and for event reminders
- The trainer directory, profiles, social, reports, sign up, password reset and the full admin panel as native screens; the store, translator workspace, credits and privacy stay web views on purpose
- Twelve theme colors, any color off a wheel, or the hue Android took from your wallpaper, in light, dark or system at three contrast levels, plus a bottom bar you choose the seats of yourself
- An update notice on launch when a newer build is out, linking straight to the tester list

Requires Android 8.0 or newer. It is in alpha (v0.1.0, build 67) and goes out through Firebase App Distribution to an invited tester group rather than a store listing, so there is no public download link yet.

Full detail, including what it deliberately cannot do yet: [Companion App](https://github.com/Hailey-Ross/hailsDotGO/wiki/Companion-App).

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
go run ./cmd/migrate -from v0.1.7c   # name the version you are on (v0.1.7b is the oldest supported), then apply what is pending
go run ./cmd/migrate                 # every upgrade after that
```

v0.1.8a makes no schema change, so upgrading from v0.1.7f is a rebuild and a restart. Coming from anything older still needs the migrate tool, because v0.1.7f changed the schema in three places. Full steps are on the [Releases and Upgrading](https://github.com/Hailey-Ross/hailsDotGO/wiki/Releases-and-Upgrading) wiki page.

When you are ready to put it on a server, see [Deployment](https://github.com/Hailey-Ross/hailsDotGO/wiki/Deployment).

If you fork the repo, copy the provided `.gitignore` template so build output and secrets never get committed:

```bash
cp .gitignore.example .gitignore
```

---

## How It Works (the short version)

Game data (stats, moves, shinies, type chart) comes from [PoGoAPI](https://pogoapi.net) and refreshes every 6 hours, with embedded snapshots as an offline fallback. Shiny *availability* is owned locally instead: the full National Dex ships embedded with per-species flags for whether a species is in Pokémon GO and whether its shiny is out, and admins correct it from the panel, so a shiny release does not need a rebuild. See [Data Sources](https://github.com/Hailey-Ross/hailsDotGO/wiki/Data-Sources). Official localized Pokémon names (French, German, Spanish, Japanese) come from [PokéAPI](https://pokeapi.co). Raid bosses and Max Battles come from [pokemon-go-api](https://github.com/pokemon-go-api/pokemon-go-api), and the events feed from [ScrapedDuck](https://github.com/bigfoott/ScrapedDuck) with sanitized detail pages from [LeekDuck](https://leekduck.com). All battle math runs client-side in TypeScript compiled by esbuild; accounts and everything persistent live in MySQL.

Full details, refresh schedules, and attribution: [Data Sources](https://github.com/Hailey-Ross/hailsDotGO/wiki/Data-Sources) and [Architecture](https://github.com/Hailey-Ross/hailsDotGO/wiki/Architecture).

---

## Disclaimer

Fan-made tool. Not affiliated with, endorsed by, or connected to Niantic or The Pokémon Company. Pokémon and all related names are trademarks of their respective owners.

---

Enjoy,  
Hails❤️
