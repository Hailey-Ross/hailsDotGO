// Events page: current and upcoming Pokemon GO events from /api/events
// (ScrapedDuck feed, LeekDuck data). Cards grouped Current/Upcoming with a
// click-to-open detail modal.

declare var EV: Record<string, string>;
declare var SITE_LANG: string;

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

// ScrapedDuck timestamps are timezone-less ISO strings ("2026-06-20T14:00:00.000").
// JS parses those as local time, which matches the local-time semantics of most events.
function parseLocal(s: string | null): Date | null {
  if (!s) return null;
  const d = new Date(s);
  return isNaN(d.getTime()) ? null : d;
}

const dateFmt = new Intl.DateTimeFormat(typeof SITE_LANG !== "undefined" ? SITE_LANG : "en", {
  weekday: "short", month: "short", day: "numeric", hour: "numeric", minute: "2-digit",
});

function relTime(ms: number): string {
  const totalMin = Math.max(1, Math.floor(ms / 60000));
  const d = Math.floor(totalMin / 1440);
  const h = Math.floor((totalMin % 1440) / 60);
  const m = totalMin % 60;
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (cls) node.className = cls;
  if (text !== undefined) node.textContent = text;
  return node;
}

const modal = el("div", "shiny-modal");
const modalInner = el("div", "event-modal-inner");
modal.appendChild(modalInner);
document.body.appendChild(modal);
modal.addEventListener("click", (e) => { if (e.target === modal) modal.classList.remove("open"); });
document.addEventListener("keydown", (e) => { if (e.key === "Escape") modal.classList.remove("open"); });

function monChip(mon: NamedMon): HTMLElement {
  const chip = el("div", "event-poke");
  const img = el("img");
  img.src = mon.image;
  img.alt = mon.name;
  img.loading = "lazy";
  img.decoding = "async";
  img.onerror = () => { img.style.display = "none"; };
  chip.appendChild(img);
  const label = el("span", undefined, mon.canBeShiny ? `${mon.name} ✨` : mon.name);
  if (mon.canBeShiny) label.title = EV.canBeShiny;
  chip.appendChild(label);
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

function openModal(ev: PogoEvent) {
  modalInner.innerHTML = "";

  if (ev.image) {
    const img = el("img", "event-modal-img");
    img.src = ev.image;
    img.alt = ev.name;
    img.onerror = () => { img.style.display = "none"; };
    modalInner.appendChild(img);
  }

  const head = el("div", "event-modal-head");
  head.appendChild(el("span", "event-badge", ev.heading || ev.eventType));
  head.appendChild(el("h2", "event-modal-name", ev.name));
  modalInner.appendChild(head);

  const start = parseLocal(ev.start);
  const end = parseLocal(ev.end);
  const times = el("div", "event-times event-modal-times");
  if (start) times.appendChild(el("div", undefined, `${EV.starts}: ${dateFmt.format(start)}`));
  if (end) times.appendChild(el("div", undefined, `${EV.ends}: ${dateFmt.format(end)}`));
  modalInner.appendChild(times);

  // Add just this event to a calendar, with the correct time in the viewer's zone.
  // The Google button is a plain calendar.google.com web link on every platform
  // (the same reliable kind the whole-feed Subscribe button uses); a browser
  // cannot launch the Google Calendar app's event editor via an intent. On a
  // phone the .ics link is the reliable native-app path, so it leads.
  const googleHref = start ? googleCalendarUrl(ev) : null;
  if (start) {
    const cal = el("div", "event-cal-actions");
    if (googleHref) {
      const g = el("a", isMobile ? "sub-btn" : "sub-btn sub-btn-primary", EV.calGoogle);
      g.href = googleHref;
      g.target = "_blank";
      g.rel = "noopener";
      cal.appendChild(g);
    }
    const dl = el("a", isMobile ? "sub-btn sub-btn-primary" : "sub-btn", isMobile ? EV.calAdd : EV.calDownload);
    dl.href = eventICSUrl(ev);
    cal.appendChild(dl);
    modalInner.appendChild(cal);
  }

  // Full details: scraped LeekDuck content when available, extraData fallback otherwise.
  const body = el("div", "event-detail-html");
  modalInner.appendChild(body);
  loadDetail(ev, body);

  if (ev.link) {
    const linkWrap = el("div", "event-modal-link");
    const a = el("a", undefined, EV.leekduck + " ↗");
    a.href = ev.link;
    a.target = "_blank";
    a.rel = "noopener";
    linkWrap.appendChild(a);
    modalInner.appendChild(linkWrap);
  }

  modal.classList.add("open");
}

function buildCard(ev: PogoEvent, now: number): HTMLElement {
  const card = el("div", "event-card");

  if (ev.image) {
    const img = el("img", "event-card-img");
    img.src = ev.image;
    img.alt = ev.name;
    img.loading = "lazy";
    img.decoding = "async";
    img.onerror = () => { img.style.display = "none"; };
    card.appendChild(img);
  }

  const body = el("div", "event-card-body");
  body.appendChild(el("span", "event-badge", ev.heading || ev.eventType));
  body.appendChild(el("h3", "event-card-name", ev.name));

  const start = parseLocal(ev.start);
  const end = parseLocal(ev.end);
  const times = el("div", "event-times");
  if (start) times.appendChild(el("div", undefined, `${EV.starts}: ${dateFmt.format(start)}`));
  if (end) times.appendChild(el("div", undefined, `${EV.ends}: ${dateFmt.format(end)}`));
  body.appendChild(times);

  const upcoming = start !== null && start.getTime() > now;
  const target = upcoming ? start : end;
  if (target) {
    const chip = el("span", "event-countdown");
    chip.dataset.target = String(target.getTime());
    chip.dataset.prefix = upcoming ? EV.startsIn : EV.endsIn;
    chip.textContent = `${chip.dataset.prefix} ${relTime(target.getTime() - now)}`;
    body.appendChild(chip);
  }

  card.appendChild(body);
  card.addEventListener("click", () => openModal(ev));
  return card;
}

function renderSection(title: string, events: PogoEvent[], now: number): HTMLElement {
  const sec = el("div", "events-section");
  sec.appendChild(el("h2", undefined, title));
  if (!events.length) {
    sec.appendChild(el("p", "empty-state", EV.none));
    return sec;
  }
  const grid = el("div", "event-grid");
  for (const ev of events) grid.appendChild(buildCard(ev, now));
  sec.appendChild(grid);
  return sec;
}

function tickCountdowns() {
  const now = Date.now();
  app.querySelectorAll<HTMLElement>(".event-countdown").forEach((chip) => {
    const target = Number(chip.dataset.target);
    if (!target) return;
    const ms = target - now;
    if (ms <= 0) {
      chip.style.display = "none";
      return;
    }
    chip.textContent = `${chip.dataset.prefix} ${relTime(ms)}`;
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

    const now = Date.now();
    const current: PogoEvent[] = [];
    const upcoming: PogoEvent[] = [];
    for (const ev of events) {
      const start = parseLocal(ev.start);
      const end = parseLocal(ev.end);
      if (end && end.getTime() < now) continue; // past
      if (start && start.getTime() > now) {
        upcoming.push(ev);
      } else {
        current.push(ev);
      }
    }
    // Current: ending soonest first (no end time sorts last).
    current.sort((a, b) => {
      const ea = parseLocal(a.end)?.getTime() ?? Infinity;
      const eb = parseLocal(b.end)?.getTime() ?? Infinity;
      return ea - eb;
    });
    // Upcoming: starting soonest first.
    upcoming.sort((a, b) => (parseLocal(a.start)?.getTime() ?? 0) - (parseLocal(b.start)?.getTime() ?? 0));

    app.innerHTML = "";
    app.appendChild(renderSection(EV.current, current, now));
    app.appendChild(renderSection(EV.upcoming, upcoming, now));
    setInterval(tickCountdowns, 60000);
  } catch (err) {
    app.innerHTML = `<div class="error-state">${EV.error}</div>`;
    console.error(err);
  }
}

initSubscribe();
init();
