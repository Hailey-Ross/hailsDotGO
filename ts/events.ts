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

// Detail modal (one shared overlay, shiny-modal backdrop pattern)

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
// cached for the session. Empty string in the cache means "use extraData fallback".
const detailCache = new Map<string, string>();

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
  if (cached !== undefined) {
    if (cached) fillDetail(container, cached);
    else renderExtraData(container, ev);
    return;
  }
  container.appendChild(el("p", "event-detail-loading", EV.loadingDetails));
  try {
    const res = await fetch("/api/events/" + encodeURIComponent(ev.eventID));
    if (!res.ok) throw new Error(`detail fetch failed: ${res.status}`);
    const data = (await res.json()) as { html?: string } | null;
    const html = data && typeof data.html === "string" ? data.html : "";
    detailCache.set(ev.eventID, html);
    if (!html) throw new Error("empty detail");
    fillDetail(container, html);
  } catch {
    if (!detailCache.has(ev.eventID)) detailCache.set(ev.eventID, "");
    container.innerHTML = "";
    renderExtraData(container, ev);
  }
}

function renderExtraData(container: HTMLElement, ev: PogoEvent) {
  container.className = ""; // drop scraped-content padding; sections pad themselves
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

// Cards

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

  // Countdown chip: "starts in" for upcoming, "ends in" for current.
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

// Init

async function init() {
  try {
    const res = await fetch("/api/events");
    if (!res.ok) throw new Error(`events fetch failed: ${res.status}`);
    const events = (await res.json()) as PogoEvent[] | null;
    if (!Array.isArray(events)) {
      app.innerHTML = `<div class="error-state">${EV.error}</div>`;
      return;
    }

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

init();
