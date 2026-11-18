// FlexDex Field Kit shell: loaded on every page by templates/base.html. Applies the theme and
// drives the status bar clock, the navigation strap's highlight pill and the sheets (account
// menu, More, Appearance). The markup is server rendered; this only adds behavior.
import { startTheme, settings, updateSettings, habitat, Mode, THEME_EVENT } from './shared/theme';
import { startTopo } from './shared/topo';
import { renderWeather, fetchLiveWeather, WeatherKind } from './shared/weatherfx';
import { mountHero, refreshHeroes } from './shared/hero';
import { feedback } from './shared/feel';
import { setupSearch } from './shared/search';

startTheme();

const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)');
const wide = window.matchMedia('(min-width: 760px)');
const $ = <T extends Element = HTMLElement>(sel: string, root: ParentNode = document) => root.querySelector(sel) as T | null;

/* ---------- Backgrounds: contour map, weather, hero banners ---------- */
const moving = () => settings().motion && !reduceMotion.matches;
const contour = () => getComputedStyle(document.documentElement).getPropertyValue('--contour').trim();
const topoCv = $<HTMLCanvasElement>('#fk-topo');
const topo = topoCv ? startTopo(topoCv, contour(), habitat().topo, moving()) : null;
const weatherBox = $('#fk-weather');
let liveKind: WeatherKind = '';
// The Appearance page can pin a weather to preview; 'live' shows the trainer's real weather.
const weatherKind = (): WeatherKind => { const w = settings().weather; return (w && w !== 'live' ? w : liveKind) as WeatherKind; };
const drawWeather = () => { if (weatherBox) renderWeather(weatherBox, weatherKind(), moving()); };
let lastMoving = moving(), lastWx = weatherKind();
drawWeather();

document.querySelectorAll<HTMLElement>('.fk-hero').forEach(mountHero);

// A theme, mode or motion change: recolor the map, rebuild the banners, restart or stop motion.
document.addEventListener(THEME_EVENT, () => {
  topo?.recolor(contour(), habitat().topo);
  refreshHeroes();
  const m = moving();
  if (m !== lastMoving) { lastMoving = m; topo?.setMoving(m); drawWeather(); }
  else if (weatherKind() !== lastWx) drawWeather();
  lastWx = weatherKind();
});
reduceMotion.addEventListener?.('change', () => { lastMoving = moving(); topo?.setMoving(lastMoving); drawWeather(); });

// The signed-in trainer's live in-game weather: the chip in the status bar and the weather layer.
const wchip = $('#fk-wchip');
if (wchip) {
  fetchLiveWeather().then(w => {
    if (!w) return;
    liveKind = w.kind;
    drawWeather();
    lastWx = weatherKind();
    const esc = (s: string) => s.replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c] as string));
    wchip.innerHTML = '<svg class="ic" aria-hidden="true"><use href="#i-wx-' + (w.kind || 'clear') + '"/></svg><span><b>' + esc(w.name) + '</b>' +
      (w.boosts.length ? '<span class="wtypes"> ' + esc(wchip.dataset.boosts || '') + ' ' + esc(w.boosts.join(', ')) + '</span>' : '') + '</span>';
    wchip.hidden = false;
  });
}

/* ---------- Clock ---------- */
const clock = $('#fk-clock');
const lang = (window as unknown as { SITE_LANG?: string }).SITE_LANG || document.documentElement.lang || undefined;
// The sign in pages show the time large over the landscape, with today's date.
const bigClock = $('[data-big-clock]'), bigDate = $('[data-big-date]');

function tick(): void {
  if (bigClock) {
    try { bigClock.textContent = new Date().toLocaleTimeString(lang, { hour: 'numeric', minute: '2-digit' }); } catch { bigClock.textContent = new Date().toLocaleTimeString(); }
  }
  if (bigDate) {
    try { bigDate.textContent = new Date().toLocaleDateString(lang, { weekday: 'long', month: 'long', day: 'numeric' }); } catch { bigDate.textContent = new Date().toDateString(); }
  }
  if (!clock) return;
  let text: string;
  try { text = new Date().toLocaleTimeString(lang, { hour: 'numeric', minute: '2-digit' }); }
  catch { text = new Date().toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' }); }
  if (clock.textContent !== text) clock.textContent = text;
}
tick();
// Wake on the next minute boundary, then every minute.
window.setTimeout(() => { tick(); window.setInterval(tick, 60_000); }, 60_000 - (Date.now() % 60_000) + 50);

/* ---------- Strap highlight pill ---------- */
const dock = $('.fk-dock');
const hop = $('.fk-hop');
const scroller = $('.fk-dock-scroll');

// The item for the open page. On phones a page that lives under More lights up More instead.
function currentItem(): HTMLElement | null {
  if (!dock) return null;
  let cur = $('.fk-item[aria-current="page"]', dock);
  if ((!cur || !cur.offsetParent) && !wide.matches) cur = $('.fk-item[data-slot="more"]', dock);
  return cur;
}

function placeHop(target: HTMLElement | null, jump: boolean): void {
  if (!dock || !hop) return;
  dock.querySelectorAll('.fk-item.lit').forEach(el => el.classList.remove('lit'));
  if (!target || !target.offsetParent) { hop.classList.remove('on'); return; }
  if (target.getAttribute('aria-current') !== 'page') target.classList.add('lit');
  // The pill lives in the strap's scrolling list on wider screens (so it scrolls with the items)
  // and straight in the bar on phones, where that list is display: contents and has no box.
  const box = scroller && getComputedStyle(scroller).display !== 'contents' ? scroller : dock;
  const br = box.getBoundingClientRect(), r = target.getBoundingClientRect();
  const w = Math.min(r.width, 58), h = r.height - 4;
  const x = r.left - br.left - box.clientLeft + box.scrollLeft + (r.width - w) / 2;
  const y = r.top - br.top - box.clientTop + box.scrollTop + 2;
  if (!jump) hop.style.transition = 'none';
  hop.style.width = w.toFixed(1) + 'px';
  hop.style.height = h.toFixed(1) + 'px';
  hop.style.transform = 'translate(' + x.toFixed(1) + 'px,' + y.toFixed(1) + 'px)';
  hop.classList.add('on');
  if (!jump) { void hop.offsetWidth; hop.style.transition = ''; }
  else if (!reduceMotion.matches) { hop.classList.remove('jump'); void hop.offsetWidth; hop.classList.add('jump'); }
}

// On a short screen the open page's item can sit below the fold of the strap: bring it into view.
function revealCurrent(): void {
  const cur = currentItem();
  if (!scroller || !cur || !wide.matches) return;
  const top = cur.offsetTop, bottom = top + cur.offsetHeight;
  if (top < scroller.scrollTop || bottom > scroller.scrollTop + scroller.clientHeight) {
    scroller.scrollTop = Math.max(0, top - (scroller.clientHeight - cur.offsetHeight) / 2);
  }
}
revealCurrent();
placeHop(currentItem(), false);
window.addEventListener('resize', () => { revealCurrent(); placeHop(currentItem(), false); });
document.fonts?.ready.then(() => placeHop(currentItem(), false));
// Slide the pill toward the item you tapped while the next page loads.
dock?.addEventListener('click', e => {
  const a = (e.target as Element).closest('a.fk-item') as HTMLElement | null;
  if (a && !e.defaultPrevented && !(e as MouseEvent).ctrlKey && !(e as MouseEvent).metaKey) { placeHop(a, true); feedback('hop'); }
});

/* ---------- Sheets ---------- */
const sheet = $('#fk-sheet');
const scrim = $('#fk-scrim');
const body = $('#fk-sheet-body');
const grab = $('#fk-grab');
let lastFocus: HTMLElement | null = null;
let openName = '';

function openSheet(name: string, content?: Node): void {
  const tpl = content ? null : $<HTMLTemplateElement>('#fk-tpl-' + name);
  if (!sheet || !scrim || !body || (!tpl && !content)) return;
  if (!openName) lastFocus = document.activeElement as HTMLElement | null;
  openName = name;
  body.replaceChildren(content || tpl!.content.cloneNode(true));
  sheet.dataset.sheet = name;
  sheet.scrollTop = 0;
  sheet.style.transform = '';
  sheet.setAttribute('aria-hidden', 'false');
  const title = body.querySelector('.fk-sheet-title, .fk-me-card b, h2');
  if (title) sheet.setAttribute('aria-label', title.textContent || '');
  if (!sheet.classList.contains('open')) feedback('open');
  sheet.classList.add('open');
  scrim.classList.add('open');
  if (name === 'appearance') syncAppearance();
  if (name === 'more') placeHop($('.fk-item[data-slot="more"]'), true);
  window.setTimeout(() => $('#fk-sheet-close')?.focus({ preventScroll: true }), 60);
}

function closeSheet(): void {
  if (!sheet || !scrim || !openName) return;
  openName = '';
  feedback('close');
  sheet.classList.remove('open');
  scrim.classList.remove('open');
  sheet.style.transform = '';
  sheet.setAttribute('aria-hidden', 'true');
  placeHop(currentItem(), true);
  lastFocus?.focus?.({ preventScroll: true });
}

// Pages open their own content in the sheet (a species field page, an event) through window.FK.
(window as unknown as { FK: unknown }).FK = {
  openSheet: (content: Node, name = 'page') => openSheet(name, content),
  closeSheet: () => closeSheet(),
};

/* ---------- Notifications inbox (the bell) ---------- */
// The bell opens an inbox sheet with the friend raid lobbies from /api/notifications; opening it
// marks them seen (the same localStorage set notifications.ts reads), so the badge clears.
interface LobbyNote { lobby_id: number; boss_name: string; boss_tier: number; state: string; host_username: string; host_trainer_name?: string; created_at: string; }
const NOTE_SEEN = 'notif_seen_lobbies', NOTE_DISMISSED = 'notif_dismissed';
const readSet = (k: string): Set<number> => { try { return new Set(JSON.parse(localStorage.getItem(k) || '[]')); } catch { return new Set(); } };

async function fillInbox(): Promise<void> {
  const box = body?.querySelector<HTMLElement>('.fk-notes');
  if (!box) return;
  let notes: LobbyNote[] = [];
  try {
    const r = await fetch('/api/notifications', { credentials: 'same-origin' });
    if (r.ok) notes = await r.json();
  } catch { /* shows the empty state */ }
  if (!Array.isArray(notes)) notes = [];
  const seen = readSet(NOTE_SEEN), dismissed = readSet(NOTE_DISMISSED);
  const visible = notes.filter(n => !dismissed.has(n.lobby_id));
  const esc = (s: string) => s.replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c] as string));
  const state = (st: string) => box.dataset['state' + st.charAt(0).toUpperCase() + st.slice(1)] || st;
  box.innerHTML = visible.map(n =>
    '<a class="fk-notif' + (seen.has(n.lobby_id) ? '' : ' unread') + '" href="/raidfinder"><span class="ic"><svg class="ic" aria-hidden="true"><use href="#i-raids"/></svg></span>' +
    '<span><b>' + esc(n.boss_name) + (n.boss_tier ? ' (T' + n.boss_tier + ')' : '') + '</b><span>' + esc(n.host_trainer_name || n.host_username) + ', ' + esc(state(n.state)) + '</span></span></a>').join('')
    || '<p class="fk-meta">' + esc(box.dataset.empty || '') + '</p>';
  // Seen now: the badge drops on the next poll.
  try {
    const all = readSet(NOTE_SEEN);
    notes.forEach(n => all.add(n.lobby_id));
    localStorage.setItem(NOTE_SEEN, JSON.stringify(Array.from(all).slice(-200)));
  } catch { /* not saved */ }
  const badge = $('#nav-notif-badge');
  if (badge) badge.style.display = 'none';
}

document.addEventListener('click', e => {
  const t = e.target as Element;
  const inbox = t.closest('[data-inbox]') as HTMLElement | null;
  if (inbox && !(e as MouseEvent).ctrlKey && !(e as MouseEvent).metaKey) { e.preventDefault(); openSheet('inbox'); fillInbox(); return; }
  const opener = t.closest('[data-sheet]') as HTMLElement | null;
  if (opener) { e.preventDefault(); openSheet(opener.dataset.sheet || ''); return; }
  if (t.closest('[data-close-sheet]')) closeSheet();
});
scrim?.addEventListener('click', closeSheet);
$('#fk-sheet-close')?.addEventListener('click', closeSheet);
document.addEventListener('keydown', e => {
  if (e.key === 'Escape' && openName) { closeSheet(); return; }
  // Keep Tab inside an open sheet.
  if (e.key === 'Tab' && openName && sheet) {
    const f = Array.from(sheet.querySelectorAll<HTMLElement>('a[href], button:not([disabled]), input:not([type="hidden"]), [tabindex]:not([tabindex="-1"])'))
      .filter(el => el.offsetParent);
    if (!f.length) return;
    const first = f[0], last = f[f.length - 1];
    if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
  }
});

// Show or hide a password (the eye button next to a password field).
document.addEventListener('click', e => {
  const b = (e.target as Element).closest('[data-show-pw]') as HTMLButtonElement | null;
  const input = b?.parentElement?.querySelector('input');
  if (!b || !input) return;
  const show = input.type === 'password';
  input.type = show ? 'text' : 'password';
  b.setAttribute('aria-pressed', String(show));
  const label = b.getAttribute('aria-label') || '';
  b.setAttribute('aria-label', b.dataset.labelHide || label);
  b.dataset.labelHide = label;
});

// Sign out asks first (the form carries its question in data-confirm).
document.addEventListener('submit', e => {
  const form = e.target as HTMLFormElement;
  const q = form.dataset?.confirm;
  if (q && !window.confirm(q)) e.preventDefault();
});

// Phones: drag the handle down to close.
(function dragToClose() {
  if (!grab || !sheet) return;
  let y0: number | null = null, dy = 0;
  grab.addEventListener('pointerdown', e => {
    if (wide.matches || (e.target as Element).closest('button')) return;
    y0 = e.clientY; dy = 0;
    sheet.classList.add('dragging');
    grab.setPointerCapture(e.pointerId);
  });
  grab.addEventListener('pointermove', e => {
    if (y0 == null) return;
    dy = Math.max(0, e.clientY - y0);
    sheet.style.transform = 'translateY(' + dy + 'px)';
  });
  const end = () => {
    if (y0 == null) return;
    y0 = null;
    sheet.classList.remove('dragging');
    if (dy > 110) closeSheet(); else sheet.style.transform = '';
  };
  grab.addEventListener('pointerup', end);
  grab.addEventListener('pointercancel', end);
})();

/* ---------- Appearance sheet ---------- */
const MODES: Mode[] = ['system', 'light', 'dark', 'sun'];
function syncAppearance(): void {
  const mode = settings().mode;
  body?.querySelectorAll<HTMLButtonElement>('[data-mode]').forEach(b => b.setAttribute('aria-pressed', String(b.dataset.mode === mode)));
}
body?.addEventListener('click', e => {
  const b = (e.target as Element).closest('[data-mode]') as HTMLElement | null;
  if (!b) return;
  const m = b.dataset.mode as Mode;
  if (MODES.includes(m)) { updateSettings({ mode: m }); syncAppearance(); }
});

/* ---------- Install as an app ---------- */
// The service worker keeps the offline page, icons and fonts, and nothing else (static/app/sw.js).
if ('serviceWorker' in navigator && (location.protocol === 'https:' || location.hostname === 'localhost' || location.hostname === '127.0.0.1')) {
  window.addEventListener('load', () => { navigator.serviceWorker.register('/sw.js').catch(() => { /* the site works without it */ }); });
}

// The browser's own bars match the kit: theme-color follows the theme's background. Colors are
// OKLCH, so read the resolved color back from a 1px canvas to hand the browser plain rgb.
const themeMeta = document.getElementById('fk-theme-color');
function syncThemeColor(): void {
  if (!themeMeta) return;
  try {
    const c = document.createElement('canvas');
    c.width = c.height = 1;
    const g = c.getContext('2d');
    if (!g) return;
    g.fillStyle = getComputedStyle(document.documentElement).getPropertyValue('--bg').trim() || '#161c19';
    g.fillRect(0, 0, 1, 1);
    const [r, gr, b] = g.getImageData(0, 0, 1, 1).data;
    themeMeta.setAttribute('content', 'rgb(' + r + ',' + gr + ',' + b + ')');
  } catch { /* keep the default */ }
}
syncThemeColor();
document.addEventListener(THEME_EVENT, syncThemeColor);

// Android and desktop Chrome: the browser offers installing; show our own Install buttons (account
// menu and More) only while that offer stands.
interface InstallPromptEvent extends Event { prompt(): Promise<void>; userChoice: Promise<{ outcome: string }>; }
let installOffer: InstallPromptEvent | null = null;
const syncInstallButtons = () => document.querySelectorAll<HTMLElement>('[data-install]').forEach(b => { b.hidden = !installOffer; });
window.addEventListener('beforeinstallprompt', e => { e.preventDefault(); installOffer = e as InstallPromptEvent; syncInstallButtons(); });
window.addEventListener('appinstalled', () => { installOffer = null; syncInstallButtons(); });
document.addEventListener('click', async e => {
  if (!(e.target as Element).closest('[data-install]') || !installOffer) return;
  closeSheet();
  const offer = installOffer;
  installOffer = null;
  await offer.prompt();
  syncInstallButtons();
});
// Sheets are copied in from templates on open, so their buttons need the current state then too.
new MutationObserver(syncInstallButtons).observe(body || document.body, { childList: true });

// iPhone and iPad: Safari never offers installing, so explain it once (Share, Add to Home Screen).
(function iosHint() {
  const nav = navigator as Navigator & { standalone?: boolean };
  const ios = /iPhone|iPad|iPod/.test(navigator.userAgent) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
  const standalone = nav.standalone || window.matchMedia('(display-mode: standalone)').matches;
  const KEY = 'flexdex-ios-hint';
  let seen = false;
  try { seen = localStorage.getItem(KEY) === '1'; } catch { seen = true; }
  const tpl = $<HTMLTemplateElement>('#fk-tpl-ios');
  if (!ios || standalone || seen || !tpl) return;
  window.setTimeout(() => {
    const node = tpl.content.firstElementChild?.cloneNode(true) as HTMLElement | undefined;
    if (!node) return;
    document.body.appendChild(node);
    try { localStorage.setItem(KEY, '1'); } catch { /* shown once per visit at worst */ }
    node.querySelector('[data-ios-dismiss]')?.addEventListener('click', () => node.remove());
  }, 2500);
})();

/* ---------- Sound and haptics on everyday controls ---------- */
// Segmented controls, tabs and chips tap; switches toggle (rising when on, falling when off).
document.addEventListener('click', e => {
  const t = e.target as Element;
  if (t.closest('.fk-seg button, .fk-seg a, .tab-btn, .fk-gen, .fk-day, .fk-chips button, .fk-hab, .raid-tab, .filter-chip')) feedback('tap');
});
document.addEventListener('change', e => {
  const t = e.target as HTMLInputElement;
  if (t.matches?.('.fk-switch, .settings-checkbox')) feedback('toggle', t.checked);
});

/* ---------- Search ---------- */
setupSearch();

/* ---------- Theme name tabs (concept: the sign-in card is labeled with the current theme) ---------- */
function themeTabs(): void {
  const id = settings().habitat;
  document.querySelectorAll<HTMLElement>('[data-theme-tab]').forEach((el) => {
    try {
      const names = JSON.parse(el.dataset.themeTab || '{}') as Record<string, string>;
      if (names[id]) el.dataset.tab = names[id];
    } catch { /* keep the default label */ }
  });
}
themeTabs();
document.addEventListener(THEME_EVENT, themeTabs);
