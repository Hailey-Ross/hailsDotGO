// The launcher (/): greeting over the hero banner, raid bosses by tier as stickers, the next
// Community Day countdown, what is happening now with time bars, the week ahead as a trail, and
// the shiny dex ring. Live data from /api/raids and /api/events; the counts come from the page.
import { sticker, trimStickers } from './shared/sticker';
import { hourNow } from './shared/theme';
import { fetchLiveWeather } from './shared/weatherfx';
import { loadGameData } from './shared/gamedata';
import { dexProgress } from './shared/dexprogress';

interface HomeI18n {
  greetLate: string; greetMorning: string; greetAfternoon: string; greetEvening: string; todaySuffix: string; todayLate: string; todayMorning: string; todayEvening: string;
  mega: string; tier: string; raidsNone: string;
  trailToday: string; trailHere: string; trailHereSub: string; trailBoosted: string;
  cdNone: string; cdToday: string; cdDays: string; cdTo: string; shinyAvailable: string;
  nowNone: string; nowLeft: string;
}
interface Boss { pokemon_name: string; image_url?: string; types?: string[]; can_be_shiny?: boolean; }
interface NamedMon { name: string; image: string; canBeShiny?: boolean; }
interface PogoEvent {
  eventID: string; name: string; eventType: string; heading: string; start: string | null; end: string | null;
  extraData?: { communityday?: { spawns?: NamedMon[]; shinies?: NamedMon[] } } | null;
}

const W = window as unknown as { HOME_I18N: HomeI18n; HOME_ME: string; SITE_LANG?: string };
const I = W.HOME_I18N;
const lang = document.documentElement.lang || undefined;
const reduce = window.matchMedia('(prefers-reduced-motion: reduce)');
const $ = (id: string) => document.getElementById(id);
const esc = (s: string) => s.replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c] as string));
const SPARK = '<svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M12 2l1.9 6.1L20 10l-6.1 1.9L12 18l-1.9-6.1L4 10l6.1-1.9z"/></svg>';

function fmt(d: Date, o: Intl.DateTimeFormatOptions): string {
  try { return d.toLocaleString(lang, o); } catch { return d.toLocaleString(undefined, o); }
}
const fmtDay = (d: Date) => fmt(d, { weekday: 'short', month: 'short', day: 'numeric' });
const fmtTime = (d: Date) => fmt(d, { hour: 'numeric', minute: '2-digit' });
function left(ms: number): string {
  if (ms <= 0) return '0m';
  const m = Math.floor(ms / 60000), dd = Math.floor(m / 1440), h = Math.floor(m % 1440 / 60), mm = m % 60;
  return dd ? dd + 'd ' + h + 'h' : h ? h + 'h ' + mm + 'm' : mm + 'm';
}
// Event times are the game's local wall clock ("2026-11-21T14:00:00.000", no zone) or UTC with Z;
// both parse correctly with Date.
const when = (s: string | null) => (s ? new Date(s) : null);

/* ---------- Greeting ---------- */
function greet(): void {
  const hr = hourNow(), now = new Date();
  const g = hr < 5 ? I.greetLate : hr < 12 ? I.greetMorning : hr < 18 ? I.greetAfternoon : I.greetEvening;
  // The line under the greeting follows the same part of the day (todaySuffix is the afternoon one).
  const line = hr < 5 ? I.todayLate : hr < 12 ? I.todayMorning : hr < 18 ? I.todaySuffix : I.todayEvening;
  const h1 = $('fk-hello');
  if (h1) h1.textContent = W.HOME_ME ? g + ', ' + W.HOME_ME : g;
  const sub = $('fk-today');
  if (sub) sub.textContent = fmt(now, { weekday: 'long', month: 'long', day: 'numeric' }) + '. ' + line;
}

/* ---------- Raid bosses by tier ---------- */
async function raids(): Promise<void> {
  const box = $('fk-raids-now');
  if (!box) return;
  try {
    const r = await fetch('/api/raids');
    const data: Record<string, Boss[]> = await r.json();
    const order = ['6', '5', '3', '1'].filter(t => data[t] && data[t].length);
    box.innerHTML = order.map(t => '<div class="fk-tier"><span class="fk-label">' + esc(t === '6' ? I.mega : I.tier.replace('{n}', t)) + '</span><div class="fk-tier-row">' +
      data[t].map(b => '<a class="fk-boss" href="/raids#boss=' + encodeURIComponent(b.pokemon_name) + '">' + sticker(b.pokemon_name, b.image_url, b.types || [], !!b.can_be_shiny) + '<span>' + esc(b.pokemon_name) + '</span></a>').join('') +
      '</div></div>').join('') || '<p class="fk-meta">' + esc(I.raidsNone) + '</p>';
    trimStickers(box);
  } catch {
    box.innerHTML = '<p class="fk-meta">' + esc(I.raidsNone) + '</p>';
  }
}

/* ---------- Events: Community Day, happening now, the trail ---------- */
let boostLine = '';

function countdownHTML(ms: number): string {
  if (ms <= 0) return esc(I.cdToday);
  const s = Math.floor(ms / 1000), dd = Math.floor(s / 86400), h = Math.floor(s % 86400 / 3600), m = Math.floor(s % 3600 / 60);
  return (dd ? dd + '<small>' + esc(I.cdDays) + '</small>' : '') + h + '<small>h</small>' + String(m).padStart(2, '0') + '<small>m</small>';
}

function tick(): void {
  const now = Date.now();
  document.querySelectorAll<HTMLElement>('[data-until]').forEach(el => { el.textContent = left(Number(el.dataset.until) - now) + ' ' + I.nowLeft; });
  document.querySelectorAll<HTMLElement>('[data-count]').forEach(el => { el.innerHTML = countdownHTML(Number(el.dataset.count) - now); });
}

function renderCD(evs: PogoEvent[]): void {
  const box = $('fk-cd');
  if (!box) return;
  const now = Date.now();
  const cd = evs.filter(e => e.eventType === 'community-day' && when(e.end) && when(e.end)!.getTime() > now)
    .sort((a, b) => when(a.start)!.getTime() - when(b.start)!.getTime())[0];
  if (!cd || !cd.start) { box.innerHTML = '<p class="fk-meta">' + esc(I.cdNone) + '</p>'; return; }
  const s = when(cd.start)!, e = when(cd.end);
  const mon = cd.extraData?.communityday?.spawns?.[0];
  const shiny = !!(cd.extraData?.communityday?.shinies?.length);
  box.innerHTML = sticker(mon?.name || cd.name, mon?.image, [], shiny, 62) +
    '<div style="min-width:0"><div class="fk-cd-time" data-count="' + s.getTime() + '"></div>' +
    '<div class="fk-meta" style="margin-top:6px">' + esc(cd.name) + ', ' + esc(fmtDay(s)) + ', ' + esc(fmtTime(s)) + (e ? ' ' + esc(I.cdTo) + ' ' + esc(fmtTime(e)) : '') + '</div>' +
    (shiny ? '<div class="fk-shiny" style="margin-top:4px">' + SPARK + esc(I.shinyAvailable) + '</div>' : '') + '</div>';
  trimStickers(box);
}

function renderNow(evs: PogoEvent[]): void {
  const box = $('fk-now');
  if (!box) return;
  const ms = Date.now();
  const live = evs.filter(e => e.start && e.end && when(e.start)!.getTime() <= ms && when(e.end)!.getTime() > ms && e.eventType !== 'season')
    .sort((a, b) => when(a.end)!.getTime() - when(b.end)!.getTime()).slice(0, 3);
  box.innerHTML = live.map(e => {
    const s = when(e.start)!.getTime(), en = when(e.end)!.getTime(), frac = (en - ms) / (en - s);
    return '<a class="fk-now-item" href="/events"><div class="row"><b>' + esc(e.name) + '</b><span data-until="' + en + '"></span></div><div class="fk-timebar"><i data-w="' + (frac * 100).toFixed(1) + '"></i></div></a>';
  }).join('') || '<p class="fk-meta">' + esc(I.nowNone) + '</p>';
  requestAnimationFrame(() => requestAnimationFrame(() => box.querySelectorAll<HTMLElement>('.fk-timebar i').forEach(i => { i.style.width = i.dataset.w + '%'; })));
}

function renderTrail(evs: PogoEvent[]): void {
  const el = $('fk-trail');
  if (!el) return;
  const ms = Date.now(), wk = ms + 9 * 864e5;
  const next = evs.filter(e => e.start && when(e.start)!.getTime() > ms && when(e.start)!.getTime() < wk)
    .sort((a, b) => when(a.start)!.getTime() - when(b.start)!.getTime()).slice(0, 6);
  const stops = [{ today: true, when: I.trailToday, what: I.trailHere, cat: boostLine || I.trailHereSub }]
    .concat(next.map(e => { const s = when(e.start)!; return { today: false, when: fmtDay(s) + ', ' + fmtTime(s), what: e.name, cat: e.heading }; }));
  const rowH = 78, pts = stops.map((_, i) => [24 + Math.sin(i * 1.25) * 12, rowH * i + rowH / 2]);
  let dPath = 'M' + pts[0][0] + ' ' + pts[0][1];
  for (let i = 1; i < pts.length; i++) {
    const a = pts[i - 1], b = pts[i];
    dPath += ' C' + a[0] + ' ' + (a[1] + rowH * 0.5) + ' ' + b[0] + ' ' + (b[1] - rowH * 0.5) + ' ' + b[0] + ' ' + b[1];
  }
  el.classList.remove('drawn');
  el.innerHTML = '<svg class="fk-path" viewBox="0 0 64 ' + rowH * stops.length + '" preserveAspectRatio="none" style="height:' + rowH * stops.length + 'px" aria-hidden="true"><path class="p" d="' + dPath + '"/><path class="p2" d="' + dPath + '"/></svg>' +
    stops.map((s, i) => '<div class="fk-stop' + (s.today ? ' today' : '') + '" style="--i:' + i + ';--px:' + pts[i][0].toFixed(1) + 'px"><span class="pin"></span><span class="when">' + esc(s.when) + '</span><span class="what">' + esc(s.what) + '</span><span class="cat">' + esc(s.cat) + '</span></div>').join('');
  const ink = el.querySelector('.p2') as SVGPathElement, len = ink.getTotalLength();
  ink.style.strokeDasharray = String(len);
  ink.style.strokeDashoffset = String(len);
  // The trail draws itself; the ink stops partway, at the stops still ahead.
  requestAnimationFrame(() => requestAnimationFrame(() => { ink.style.strokeDashoffset = String(reduce.matches ? 0 : len * 0.82); el.classList.add('drawn'); }));
}

async function events(): Promise<void> {
  if (!$('fk-cd') && !$('fk-trail')) return;
  let evs: PogoEvent[] = [];
  try { evs = await (await fetch('/api/events')).json(); } catch { /* shows the empty states */ }
  renderCD(evs); renderNow(evs); renderTrail(evs); tick();
  // Signed-in trainers: today's stop names the weather boost.
  if (W.HOME_ME) {
    const w = await fetchLiveWeather();
    if (w && w.boosts.length) { boostLine = I.trailBoosted.replace('{weather}', w.name).replace('{types}', w.boosts.join(', ')); renderTrail(evs); }
  }
}

/* ---------- Shiny dex ring ---------- */
// Counted exactly like the Dex page's ring (shared/dexprogress.ts): released cards, regional forms
// included, against the trainer's own log. Visitors see the total only.
async function dexRing(): Promise<void> {
  const svgEl = document.getElementById('fk-dex-ring');
  if (!svgEl) return;
  let have = 0, total = 0;
  try {
    const [data, mine] = await Promise.all([
      loadGameData(),
      W.HOME_ME ? fetch('/api/shinies', { credentials: 'same-origin' }).then(r => (r.ok ? r.json() : [])).catch(() => []) : Promise.resolve([]),
    ]);
    ({ have, total } = dexProgress(data, mine || []));
  } catch { return; }
  const totalEl = document.getElementById('fk-dex-total');
  if (totalEl) totalEl.textContent = total.toLocaleString(lang);
  const countEl = document.getElementById('fk-dex-count');
  if (countEl) countEl.textContent = (reduce.matches || !have ? have : 0).toLocaleString(lang);
  const segs = 30, on = total ? Math.round(have / total * segs) : 0, R = 42, gap = 4;
  let svg = '';
  for (let i = 0; i < segs; i++) {
    const a0 = (i / segs) * 360 - 90 + gap / 2, a1 = ((i + 1) / segs) * 360 - 90 - gap / 2, r0 = a0 * Math.PI / 180, r1 = a1 * Math.PI / 180;
    svg += '<path class="seg-arc" data-on="' + (i < on ? 1 : 0) + '" style="transition-delay:' + (i * 35) + 'ms" d="M' + (50 + R * Math.cos(r0)).toFixed(2) + ' ' + (50 + R * Math.sin(r0)).toFixed(2) + ' A' + R + ' ' + R + ' 0 0 1 ' + (50 + R * Math.cos(r1)).toFixed(2) + ' ' + (50 + R * Math.sin(r1)).toFixed(2) + '"/>';
  }
  svgEl.innerHTML = svg;
  const count = document.getElementById('fk-dex-count');
  if (count && have && !reduce.matches) {
    const t0 = performance.now();
    const step = (t: number) => {
      const p = Math.max(0, Math.min(1, (t - t0) / 1100)), e = 1 - Math.pow(1 - p, 3);
      count.textContent = Math.round(have * e).toLocaleString(lang);
      if (p < 1) requestAnimationFrame(step);
    };
    requestAnimationFrame(step);
  }
  window.setTimeout(() => svgEl.querySelectorAll('[data-on="1"]').forEach(p => p.classList.add('on')), 80);
}

greet();
// Keep the greeting and its line current if the page stays open across a change in the day.
window.setInterval(greet, 60_000);
dexRing();
raids();
events();
window.setInterval(tick, 1000);
window.setInterval(greet, 5 * 60_000);
