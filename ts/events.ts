// Events page: current and upcoming Pokemon GO events from /api/events
// (ScrapedDuck feed, LeekDuck data). Cards grouped Current/Upcoming with a
// click-to-open detail modal.

declare var EV: Record<string, string>;
declare var SITE_LANG: string;

import { parseLocal, relTime, weekdayFmt, siteLang } from "./shared/time";
import { openSheet } from "./shared/fk";
import { sticker, trimStickers } from "./shared/sticker";

// Type colors for the event stickers, from the current raid feed (most event bosses are out right
// now); anything else gets the sticker's neutral ring.
const monTypes = new Map<string, string[]>();
const raidFeed: NamedMon[] = [];
if (typeof fetch !== "undefined") {
  fetch("/api/raids").then((r) => (r.ok ? r.json() : {})).then((d: Record<string, { pokemon_name: string; types?: string[] }[]>) => {
    for (const list of Object.values(d || {})) for (const b of list as { pokemon_name: string; types?: string[]; image_url?: string; can_be_shiny?: boolean }[]) {
      if (b.types?.length) monTypes.set(b.pokemon_name, b.types);
      raidFeed.push({ name: b.pokemon_name, image: b.image_url || "", canBeShiny: !!b.can_be_shiny });
    }
  }).catch(() => undefined);
}

const app = document.getElementById("events-app")!;

interface NamedMon {
  name: string;
  image: string;
  canBeShiny?: boolean;
}

interface TextImage {
  text: string;
  image: string;
}

interface ResearchStep {
  name: string;
  step: number;
  tasks: { text: string; reward: TextImage }[];
  rewards: TextImage[];
}

interface EventExtra {
  generic?: { hasSpawns: boolean; hasFieldResearchTasks: boolean };
  spotlight?: { name: string; canBeShiny: boolean; image: string; bonus: string };
  breakthrough?: { name: string; canBeShiny: boolean; image: string };
  raidbattles?: { bosses: NamedMon[]; shinies: NamedMon[] };
  communityday?: {
    spawns: NamedMon[];
    bonuses: TextImage[];
    bonusDisclaimers: string[];
    shinies: NamedMon[];
    specialresearch: ResearchStep[];
  };
}

interface PogoEvent {
  eventID: string;
  name: string;
  eventType: string;
  heading: string;
  link: string;
  image: string;
  start: string | null;
  end: string | null;
  extraData: EventExtra | null;
}

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (cls) node.className = cls;
  if (text !== undefined) node.textContent = text;
  return node;
}

// Concept: the event's Pokemon as stickers (type ring, shiny badge), name underneath.
function monChip(mon: NamedMon): HTMLElement {
  const chip = el("div", "event-poke fk-boss");
  chip.innerHTML = sticker(mon.name, mon.image || undefined, monTypes.get(mon.name) || [], !!mon.canBeShiny, 58);
  const label = el("span", undefined, mon.name);
  if (mon.canBeShiny) chip.title = EV.canBeShiny;
  chip.appendChild(label);
  trimStickers(chip);
  return chip;
}

function detailSection(title: string): HTMLElement {
  const sec = el("div", "event-detail-section");
  sec.appendChild(el("h3", undefined, title));
  return sec;
}

function monSection(title: string, mons: NamedMon[]): HTMLElement | null {
  if (!mons || !mons.length) return null;
  const sec = detailSection(title);
  const grid = el("div", "event-poke-grid");
  for (const mon of mons) grid.appendChild(monChip(mon));
  sec.appendChild(grid);
  return sec;
}

// Full event details: sanitized LeekDuck page HTML, lazy-loaded per event and
// cached for the session. Only a real, non-empty result lives in here.
const detailCache = new Map<string, string>();

// Failures and empty bodies are cooled down, never cached for the session. The
// server refills its scraped page cache after every deploy or restart at a
// deliberate 1500ms per page, so for roughly a minute and a half
// /api/events/{id} legitimately has nothing to serve for a page it has not
// reached yet. Remembering that as "no detail" would pin the degraded fallback
// for the rest of the trainer's session over a warmup that ends seconds later,
// and only a full reload would clear it. The cooldown is what keeps the retry
// cheap: the few events that really have no detail page do not refetch on every
// click, and we stay well under the endpoint's 30 requests per 2 minutes.
const DETAIL_RETRY_MS = 60000;
const detailFailedAt = new Map<string, number>();

function fillDetail(container: HTMLElement, html: string) {
  container.innerHTML = html; // sanitized server-side with bluemonday
  container.querySelectorAll("img").forEach((img) => {
    img.loading = "lazy";
    img.decoding = "async";
    img.onerror = () => { img.style.display = "none"; };
  });
}

async function loadDetail(ev: PogoEvent, container: HTMLElement) {
  const cached = detailCache.get(ev.eventID);
  if (cached) {
    fillDetail(container, cached);
    return;
  }
  const failedAt = detailFailedAt.get(ev.eventID);
  if (failedAt !== undefined) {
    // A negative age means the clock moved backwards under us (an NTP
    // correction, a resume from sleep), so treat that as expired too. The worst
    // a clock jump can cost is one extra fetch; it can never wedge the cooldown
    // open forever.
    const age = Date.now() - failedAt;
    if (age >= 0 && age < DETAIL_RETRY_MS) {
      renderExtraData(container, ev);
      return;
    }
    detailFailedAt.delete(ev.eventID);
  }
  container.appendChild(el("p", "event-detail-loading", EV.loadingDetails));
  try {
    const res = await fetch("/api/events/" + encodeURIComponent(ev.eventID));
    if (!res.ok) throw new Error(`detail fetch failed: ${res.status}`);
    const data = (await res.json()) as { html?: string } | null;
    const html = data && typeof data.html === "string" ? data.html : "";
    if (!html) throw new Error("empty detail");
    detailCache.set(ev.eventID, html);
    fillDetail(container, html);
  } catch {
    detailFailedAt.set(ev.eventID, Date.now());
    container.innerHTML = "";
    renderExtraData(container, ev);
  }
}

function renderExtraData(container: HTMLElement, ev: PogoEvent) {
  // Swap the scraped-content class for the fallback's own: the sections below
  // pad themselves, so the scraped side padding goes, but the subtree still
  // needs a class to hang an image size cap on.
  container.className = "event-detail-extra";
  const before = container.childElementCount;
  const x = ev.extraData;
  if (x) {
    if (x.communityday) {
      const cd = x.communityday;
      if (cd.bonuses && cd.bonuses.length) {
        const sec = detailSection(EV.bonuses);
        for (const b of cd.bonuses) {
          const row = el("div", "event-bonus");
          if (b.image) {
            const img = el("img");
            img.src = b.image;
            img.alt = "";
            img.loading = "lazy";
            img.onerror = () => { img.style.display = "none"; };
            row.appendChild(img);
          }
          row.appendChild(el("span", undefined, b.text));
          sec.appendChild(row);
        }
        container.appendChild(sec);
      }
      const spawns = monSection(EV.spawns, cd.spawns);
      if (spawns) container.appendChild(spawns);
      const shinies = monSection(EV.shinies, cd.shinies);
      if (shinies) container.appendChild(shinies);
      if (cd.specialresearch && cd.specialresearch.length) {
        const sec = detailSection(EV.research);
        for (const step of cd.specialresearch) {
          const det = el("details", "event-research-step");
          det.appendChild(el("summary", undefined, step.name));
          for (const task of step.tasks || []) {
            const row = el("div", "event-bonus");
            row.appendChild(el("span", undefined, `${task.text}: ${task.reward?.text ?? ""}`));
            det.appendChild(row);
          }
          for (const reward of step.rewards || []) {
            const row = el("div", "event-bonus");
            if (reward.image) {
              const img = el("img");
              img.src = reward.image;
              img.alt = "";
              img.loading = "lazy";
              img.onerror = () => { img.style.display = "none"; };
              row.appendChild(img);
            }
            row.appendChild(el("span", undefined, reward.text));
            det.appendChild(row);
          }
          sec.appendChild(det);
        }
        container.appendChild(sec);
      }
      if (cd.bonusDisclaimers && cd.bonusDisclaimers.length) {
        for (const text of cd.bonusDisclaimers) {
          container.appendChild(el("p", "event-disclaimer", text));
        }
      }
    }
    if (x.raidbattles) {
      const bosses = monSection(EV.bosses, x.raidbattles.bosses);
      if (bosses) container.appendChild(bosses);
      const shinies = monSection(EV.shinies, x.raidbattles.shinies);
      if (shinies) container.appendChild(shinies);
    }
    if (x.spotlight) {
      const sec = detailSection(EV.spotlight);
      const grid = el("div", "event-poke-grid");
      grid.appendChild(monChip({ name: x.spotlight.name, image: x.spotlight.image, canBeShiny: x.spotlight.canBeShiny }));
      sec.appendChild(grid);
      if (x.spotlight.bonus) sec.appendChild(el("p", undefined, x.spotlight.bonus));
      container.appendChild(sec);
    }
    if (x.breakthrough) {
      const sec = monSection(EV.spawns, [x.breakthrough]);
      if (sec) container.appendChild(sec);
    }
  }
  // Most events only carry extraData.generic (two booleans, nothing worth
  // drawing), so every branch above can miss and leave the modal body blank.
  // Say so instead, and offer the source page when the feed gave us one.
  // No link here: showModal already appends a "View on LeekDuck" link directly
  // below this container, so adding one would stack two identical links.
  if (container.childElementCount === before) {
    const empty = el("div", "event-detail-section");
    empty.appendChild(el("p", "empty-state", EV.detailNone));
    container.appendChild(empty);
  }
}

// Single-event calendar export. Mirrors internal/handlers/events_ics.go: a raw
// feed time keeps its "Z" (UTC) or stays floating (local wall clock) so calendar
// apps show it at the right time in the viewer's own zone.
function toICSStamp(raw: string | null): string | null {
  if (!raw) return null;
  const m = raw.trim().match(/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(Z)?$/);
  if (!m) return null;
  return `${m[1]}${m[2]}${m[3]}T${m[4]}${m[5]}${m[6]}${m[7] || ""}`;
}

function pad2(n: number): string { return n < 10 ? "0" + n : String(n); }

// start + 1 hour in the same flavour (UTC keeps Z, floating stays local); used
// only for the rare event that has no end time.
function plusOneHour(raw: string): string {
  const utc = raw.trim().endsWith("Z");
  const d = new Date(raw);
  d.setTime(d.getTime() + 3600000);
  if (utc) {
    return `${d.getUTCFullYear()}${pad2(d.getUTCMonth() + 1)}${pad2(d.getUTCDate())}T${pad2(d.getUTCHours())}${pad2(d.getUTCMinutes())}${pad2(d.getUTCSeconds())}Z`;
  }
  return `${d.getFullYear()}${pad2(d.getMonth() + 1)}${pad2(d.getDate())}T${pad2(d.getHours())}${pad2(d.getMinutes())}${pad2(d.getSeconds())}`;
}

// Platform detection drives how the add-to-calendar buttons behave: on a phone
// the .ics link opens the native calendar app, so it becomes the primary action.
const ua = typeof navigator !== "undefined" ? navigator.userAgent : "";
const isAndroid = /Android/i.test(ua);
const isIOS = /iPhone|iPad|iPod/i.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1);
const isMobile = isAndroid || isIOS;

// Real URL for a single event's .ics, served with calendar headers so a phone
// hands it to its native calendar app (Apple Calendar / Google Calendar).
function eventICSUrl(ev: PogoEvent): string {
  return "/events/event.ics?id=" + encodeURIComponent(ev.eventID);
}

function googleCalendarUrl(ev: PogoEvent): string | null {
  const startStamp = toICSStamp(ev.start);
  if (!ev.start || !startStamp) return null;
  const endStamp = toICSStamp(ev.end) || plusOneHour(ev.start);
  let details = ev.heading || "";
  if (ev.link) details = details ? details + "\n\n" + ev.link : ev.link;
  // Stamps are digits + T + optional Z, so the slash between them stays literal.
  const parts = [
    "action=TEMPLATE",
    "text=" + encodeURIComponent(ev.name),
    "dates=" + startStamp + "/" + endStamp,
  ];
  if (details) parts.push("details=" + encodeURIComponent(details));
  // Floating (no Z) events: tell Google which zone the wall time is in, so it
  // lands at the same clock time for this user. UTC events are already absolute.
  if (!startStamp.endsWith("Z")) {
    try {
      const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
      if (tz) parts.push("ctz=" + encodeURIComponent(tz));
    } catch { /* no tz available; Google assumes UTC */ }
  }
  return "https://calendar.google.com/calendar/render?" + parts.join("&");
}

// The event field page, opened in the shell's sheet: what it is, when, how long is left, add it
// to a calendar, then the full details (scraped event page, or the feed's own data).
function openModal(ev: PogoEvent) {
  const start = parseLocal(ev.start);
  const end = parseLocal(ev.end);
  const now = Date.now();
  const live = !!start && start.getTime() <= now && (!end || end.getTime() > now);

  const page = el("article", "fk-species fk-event");
  page.style.setProperty("--t1", "var(--accent)");
  page.style.setProperty("--t2", "var(--land2)");

  const top = el("div", "fk-sp-top");
  const headText = el("div");
  headText.style.minWidth = "0";
  headText.appendChild(el("span", "fk-label", ev.heading || prettyType(ev.eventType)));
  headText.appendChild(el("h2", undefined, ev.name));
  const row = el("div", "row");
  row.appendChild(el("span", "fk-chip", live ? EV.now : EV.next));
  headText.appendChild(row);
  top.appendChild(headText);
  page.appendChild(top);

  // Concept: the event's Pokemon as a row of stickers under the header.
  const x = ev.extraData;
  const mons: NamedMon[] = [];
  if (x?.raidbattles?.bosses) mons.push(...x.raidbattles.bosses);
  if (x?.spotlight) mons.push({ name: x.spotlight.name, image: x.spotlight.image, canBeShiny: x.spotlight.canBeShiny });
  if (x?.communityday?.spawns) mons.push(...x.communityday.spawns);
  // Some raid events arrive with an empty boss list: fall back to the current bosses the title names.
  if (!mons.length && x?.raidbattles) {
    const title = ev.name.toLowerCase();
    for (const b of raidFeed) if (title.includes(b.name.toLowerCase())) mons.push(b);
  }
  if (mons.length) {
    const strip = el("div", "event-poke-grid fk-ev-stickers");
    const seen = new Set<string>();
    for (const m of mons) if (!seen.has(m.name) && seen.size < 12) { seen.add(m.name); strip.appendChild(monChip(m)); }
    page.appendChild(strip);
  }

  if (ev.image) {
    const img = el("img", "fk-event-img");
    img.src = ev.image;
    img.alt = "";
    img.onerror = () => { img.remove(); };
    page.appendChild(img);
  }

  const notes = el("div", "fk-notes");
  const note = (v: string, k: string, until?: number) => {
    const n = el("div", "fk-note-tile");
    const vEl = el("div", "v", v);
    vEl.style.fontSize = "15px";
    if (until) { vEl.dataset.until = String(until); vEl.classList.add("fk-left"); }
    n.append(vEl, el("div", "k", k));
    notes.appendChild(n);
  };
  if (start) note(dayFmt(start), EV.startsAt.replace("{t}", timeFmt(start)));
  if (end) note(dayFmt(end), EV.endsAt.replace("{t}", timeFmt(end)));
  const target = live ? end : start;
  if (target) note(relTime(target.getTime() - now), live ? EV.leftLabel : EV.untilStarts, target.getTime());
  page.appendChild(notes);

  // Add just this event to a calendar, with the correct time in the viewer's zone. The
  // Google button is a plain calendar.google.com web link on every platform; a browser
  // cannot launch the Google Calendar app's event editor. On a phone the .ics link is the
  // reliable native-app path, so it leads.
  if (start) {
    const cal = el("div", "fk-actions");
    const googleHref = googleCalendarUrl(ev);
    const dl = el("a", isMobile ? "fk-btn" : "fk-btn ghost", isMobile ? EV.calAdd : EV.calDownload);
    dl.href = eventICSUrl(ev);
    if (googleHref) {
      const g = el("a", isMobile ? "fk-btn ghost" : "fk-btn", EV.calGoogle);
      g.href = googleHref;
      g.target = "_blank";
      g.rel = "noopener";
      if (isMobile) cal.append(dl, g); else cal.append(g, dl);
    } else cal.appendChild(dl);
    page.appendChild(cal);
  }

  // Full details: scraped LeekDuck content when available, extraData fallback otherwise.
  const details = el("div");
  details.appendChild(el("div", "fk-sub", EV.details));
  const body = el("div", "event-detail-html");
  details.appendChild(body);
  page.appendChild(details);
  loadDetail(ev, body);

  if (ev.link) {
    const a = el("a", "fk-btn ghost", EV.leekduck + " ↗");
    a.href = ev.link;
    a.target = "_blank";
    a.rel = "noopener";
    const wrap = el("div", "fk-actions");
    wrap.appendChild(a);
    page.appendChild(wrap);
  }

  openSheet(page);
}

const dayFmt = (d: Date) => d.toLocaleDateString(siteLang(), { weekday: "short", month: "short", day: "numeric" });
const timeFmt = (d: Date) => d.toLocaleTimeString(siteLang(), { hour: "numeric", minute: "2-digit" });

// An event as an index card tabbed with its kind: name, when, a time bar while it runs, and
// how long is left (or to go).
function buildCard(ev: PogoEvent, now: number, i: number): HTMLElement {
  const start = parseLocal(ev.start);
  const end = parseLocal(ev.end);
  const live = !!start && start.getTime() <= now;
  const card = el("button", "fk-card fk-ev fk-settle" + (live ? " live" : ""));
  card.type = "button";
  card.style.setProperty("--i", String(Math.min(i, 12)));
  card.dataset.tab = ev.heading || prettyType(ev.eventType);
  card.appendChild(el("span", "nm", ev.name));
  if (start) {
    const same = !!end && start.toDateString() === end.toDateString();
    const whenText = dayFmt(start) + ", " + timeFmt(start) + (end ? " " + EV.to + " " + (same ? timeFmt(end) : dayFmt(end) + ", " + timeFmt(end)) : "");
    card.appendChild(el("span", "when", whenText));
  }
  if (live && start && end) {
    const bar = el("span", "fk-timebar");
    const fill = el("i");
    fill.dataset.w = (((end.getTime() - now) / (end.getTime() - start.getTime())) * 100).toFixed(1);
    bar.appendChild(fill);
    card.appendChild(bar);
  }
  const target = live ? end : start;
  if (target) {
    const foot = el("span", "foot");
    const lft = el("span", "left fk-left", relTime(target.getTime() - now));
    lft.dataset.until = String(target.getTime());
    foot.append(lft, el("span", "fk-meta", live ? EV.left : EV.toGo));
    card.appendChild(foot);
  }
  card.addEventListener("click", () => openModal(ev));
  return card;
}

let evView: "all" | "now" | "next" = "all";
let evDay: string | null = null;
let feed: PogoEvent[] = [];
const keyOf = (d: Date) => d.getFullYear() + "-" + d.getMonth() + "-" + d.getDate();

function overlaps(ev: PogoEvent, from: Date, to: Date): boolean {
  const s = parseLocal(ev.start), e = parseLocal(ev.end);
  if (!s) return false;
  return s < to && (e ? e : new Date(s.getTime() + 3600000)) > from;
}

// The week ahead as seven day tiles (dots show how busy each day is; tap one to filter), the
// All / Happening now / Coming up switch, then the cards.
function renderEvents(): void {
  const now = Date.now();
  app.innerHTML = "";

  const today = new Date(); today.setHours(0, 0, 0, 0);
  const week = el("div", "fk-week");
  for (let i = 0; i < 7; i++) {
    const d = new Date(today); d.setDate(today.getDate() + i);
    const dEnd = new Date(d); dEnd.setDate(d.getDate() + 1);
    const n = feed.filter((ev) => overlaps(ev, d, dEnd)).length;
    const day = el("button", "fk-day" + (i === 0 ? " today" : ""));
    day.type = "button";
    day.dataset.day = keyOf(d);
    day.setAttribute("aria-pressed", String(evDay === keyOf(d)));
    day.append(el("span", "dw", weekdayFmt.format(d)), el("span", "dn", String(d.getDate())));
    const dots = el("span", "ev-dots");
    for (let k = 0; k < Math.min(n, 3); k++) dots.appendChild(el("i"));
    day.appendChild(dots);
    day.addEventListener("click", () => { evDay = evDay === keyOf(d) ? null : keyOf(d); renderEvents(); });
    week.appendChild(day);
  }
  app.appendChild(week);

  const seg = el("div", "fk-seg fk-ev-seg");
  seg.setAttribute("role", "group");
  for (const [v, label] of [["all", EV.all], ["now", EV.now], ["next", EV.next]] as const) {
    const b = el("button", undefined, label);
    b.type = "button";
    b.setAttribute("aria-pressed", String(evView === v));
    b.addEventListener("click", () => { evView = v; renderEvents(); });
    seg.appendChild(b);
  }
  app.appendChild(seg);

  let dayFrom: Date | null = null, dayTo: Date | null = null;
  if (evDay) {
    const p = evDay.split("-").map(Number);
    dayFrom = new Date(p[0], p[1], p[2]); dayTo = new Date(dayFrom); dayTo.setDate(dayFrom.getDate() + 1);
  }
  const list = feed.filter((ev) => {
    const s = parseLocal(ev.start), e = parseLocal(ev.end);
    if (e && e.getTime() < now) return false;
    const live = !s || s.getTime() <= now;
    if (evView === "now" && !live) return false;
    if (evView === "next" && live) return false;
    if (dayFrom && dayTo && !overlaps(ev, dayFrom, dayTo)) return false;
    return true;
  }).sort((a, b) => {
    // Running events first, ending soonest; then what is coming, starting soonest.
    const sa = parseLocal(a.start)?.getTime() ?? 0, sb = parseLocal(b.start)?.getTime() ?? 0;
    const la = sa <= now, lb = sb <= now;
    if (la !== lb) return la ? -1 : 1;
    if (la) return (parseLocal(a.end)?.getTime() ?? Infinity) - (parseLocal(b.end)?.getTime() ?? Infinity);
    return sa - sb;
  });

  const grid = el("div", "fk-evs");
  list.forEach((ev, i) => grid.appendChild(buildCard(ev, now, i)));
  if (!list.length) grid.appendChild(el("p", "fk-empty", evDay ? EV.noneDay : EV.none));
  app.appendChild(grid);
  requestAnimationFrame(() => requestAnimationFrame(() => grid.querySelectorAll<HTMLElement>(".fk-timebar i").forEach((i) => { i.style.width = i.dataset.w + "%"; })));
}

// A link from search (/events#event=ID) opens that event's field page.
function openEventFromHash(): void {
  const m = (location.hash || "").match(/event=([^&]+)/);
  if (!m) return;
  const ev = feed.find((e) => e.eventID === decodeURIComponent(m[1]));
  if (ev) openModal(ev);
}

function tickCountdowns() {
  const now = Date.now();
  document.querySelectorAll<HTMLElement>(".fk-left[data-until]").forEach((n) => {
    const ms = Number(n.dataset.until) - now;
    n.textContent = relTime(Math.max(0, ms));
  });
}

// "pokemon-go-fest" becomes "Pokemon Go Fest". Feed types are lowercase words
// joined by hyphens, and a type Niantic has just invented has no locale key to
// look up, so the slug itself is the only label we can offer.
function prettyType(slug: string): string {
  return slug
    .split("-")
    .filter((word) => word.length > 0)
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}

// Set once the subscribe panel is wired, then called by init() with the loaded
// feed so the panel can offer the event types the curated checkboxes miss.
let addFeedTypes: ((events: PogoEvent[]) => void) | null = null;

// Calendar subscription panel: build a webcal:// URL for the .ics feed from the
// checked event-type toggles, wire Copy, and point the "Add to Google Calendar"
// link at it. The feed itself handles floating-vs-UTC times correctly.
function initSubscribe() {
  const panel = document.getElementById("subscribe");
  if (!panel) return;
  const boxes = Array.from(panel.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'));
  const allBox = boxes.find((b) => b.dataset.type === "*");
  const specific = boxes.filter((b) => b.dataset.type !== "*");
  const maybeUrl = document.getElementById("sub-url") as HTMLInputElement | null;
  const maybeCopy = document.getElementById("sub-copy") as HTMLButtonElement | null;
  const maybeGoogle = document.getElementById("sub-google") as HTMLAnchorElement | null;
  if (!maybeUrl || !maybeCopy || !maybeGoogle) return;
  // Bind to non-null consts so the narrowing survives into the nested closures.
  const urlField = maybeUrl, copyBtn = maybeCopy, googleLink = maybeGoogle;

  const host = location.host;

  function update() {
    const everything = !!allBox && allBox.checked;
    const types: string[] = [];
    if (!everything) {
      for (const b of specific) {
        if (!b.checked) continue;
        for (const t of (b.dataset.type || "").split(",")) {
          const tok = t.trim();
          if (tok && !types.includes(tok)) types.push(tok);
        }
      }
    }
    // Encoded because these values come from the feed, not from us. An unencoded
    // "#" would truncate the copied link at the fragment and silently change what
    // the trainer subscribed to.
    const query = !everything && types.length ? "?types=" + encodeURIComponent(types.join(",")) : "";
    const webcal = "webcal://" + host + "/events/calendar.ics" + query;
    const https = "https://" + host + "/events/calendar.ics" + query;
    urlField.value = webcal;
    googleLink.href = "https://calendar.google.com/calendar/r?cid=" + encodeURIComponent(webcal);
    // Keep the https form reachable for a plain "download the .ics" fallback.
    urlField.dataset.https = https;
  }

  if (allBox) {
    allBox.addEventListener("change", () => {
      if (allBox.checked) specific.forEach((b) => (b.checked = false));
      update();
    });
  }
  function wireSpecific(b: HTMLInputElement) {
    b.addEventListener("change", () => {
      if (b.checked && allBox) allBox.checked = false;
      update();
    });
  }
  for (const b of specific) wireSpecific(b);

  // The checkboxes in the template are curated bundles with translated labels,
  // so they only ever cover the types we knew about when they were written. The
  // live feed carries more (GO Fest, seasons, one-off formats), and a trainer
  // who ticks specific types would never see those land in their calendar. So
  // once the feed is in, anything no curated box claims gets its own generated
  // checkbox, off by default, wired exactly like the curated ones.
  addFeedTypes = (events: PogoEvent[]) => {
    const wrap = document.getElementById("sub-types");
    if (!wrap) return;
    const covered = new Set<string>();
    for (const b of specific) {
      for (const t of (b.dataset.type || "").split(",")) {
        const tok = t.trim();
        if (tok) covered.add(tok);
      }
    }
    // "Everything" is a wildcard, not a claim on any one type.
    covered.delete("*");
    const anchor = allBox ? allBox.closest("label") : null;
    for (const ev of events) {
      const type = (ev.eventType || "").trim();
      if (!type || covered.has(type)) continue;
      // Only offer a type the server's ?types= filter will actually accept. It
      // drops an unrecognised token silently, which would leave a trainer with a
      // subscription that quietly returns nothing. Mirrors eventTypePattern in
      // internal/handlers/events_ics.go.
      if (!/^[a-z0-9_-]{1,64}$/.test(type)) continue;
      covered.add(type);
      const box = el("input");
      box.type = "checkbox";
      box.dataset.type = type;
      const label = el("label", "sub-type");
      label.appendChild(box);
      label.appendChild(el("span", undefined, prettyType(type)));
      // Keep the dashed "Everything" toggle last, where the panel expects it.
      if (anchor) wrap.insertBefore(label, anchor);
      else wrap.appendChild(label);
      specific.push(box);
      wireSpecific(box);
    }
    update();
  };

  copyBtn.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(urlField.value);
    } catch {
      urlField.select();
      document.execCommand("copy");
    }
    copyBtn.textContent = EV.subCopied;
    setTimeout(() => { copyBtn.textContent = EV.subCopy; }, 1800);
  });

  update();
}

async function init() {
  try {
    const res = await fetch("/api/events");
    if (!res.ok) throw new Error(`events fetch failed: ${res.status}`);
    const events = (await res.json()) as PogoEvent[] | null;
    if (!Array.isArray(events)) {
      app.innerHTML = `<div class="error-state">${EV.error}</div>`;
      return;
    }
    if (addFeedTypes) addFeedTypes(events);

    feed = events;
    renderEvents();
    openEventFromHash();
    window.addEventListener("hashchange", openEventFromHash);
    setInterval(tickCountdowns, 60000);
  } catch (err) {
    app.innerHTML = `<div class="error-state">${EV.error}</div>`;
    console.error(err);
  }
}

initSubscribe();
init();
