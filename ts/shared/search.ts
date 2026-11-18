// Search (concept): one field for raid bosses, Pokemon, trainers, events and the site's own pages.
// Opened from the status bar or with "/". The data loads the first time search opens, never on a
// plain page view: raid bosses and events are small public feeds, Pokemon names come from the game
// data blob (ETag, so a repeat is a 304), trainers from the public directory page.
import { sticker, trimStickers } from './sticker';
import { feedback } from './feel';
import { loadGameData } from './gamedata';

interface Boss { pokemon_name: string; image_url?: string; types?: string[]; can_be_shiny?: boolean; }
interface Ev { eventID: string; name: string; heading: string; start: string | null; end: string | null; }
interface Item { kind: 'boss' | 'mon' | 'trainer' | 'event' | 'page'; name: string; sub: string; href: string; img?: string; types?: string[]; shiny?: boolean; key: string; }

const esc = (s: string) => s.replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c] as string));
const fold = (s: string) => s.toLowerCase().normalize('NFD').replace(/[̀-ͯ]/g, '');

let items: Item[] | null = null;
let loading: Promise<void> | null = null;

async function getJSON<T>(url: string): Promise<T | null> {
  try { const r = await fetch(url, { credentials: 'same-origin' }); return r.ok ? await r.json() as T : null; } catch { return null; }
}

function pageItems(box: HTMLElement): Item[] {
  // The site's own pages, from the strap and the More sheet (already gated by what is switched on).
  const seen = new Set<string>(), out: Item[] = [];
  const add = (a: HTMLAnchorElement) => {
    const href = a.getAttribute('href') || '';
    const name = (a.textContent || '').replace(/\s+/g, ' ').trim();
    if (!href.startsWith('/') || !name || seen.has(href)) return;
    seen.add(href);
    out.push({ kind: 'page', name, sub: box.dataset.page || '', href, key: fold(name) });
  };
  document.querySelectorAll<HTMLAnchorElement>('.fk-dock a.fk-item').forEach(add);
  document.querySelectorAll<HTMLTemplateElement>('#fk-tpl-more, #fk-tpl-account').forEach(t => t.content.querySelectorAll<HTMLAnchorElement>('a').forEach(add));
  return out;
}

function load(box: HTMLElement): Promise<void> {
  if (loading) return loading;
  loading = (async () => {
    const [raids, events, data, dir] = await Promise.all([
      getJSON<Record<string, Boss[]>>('/api/raids'),
      getJSON<Ev[]>('/api/events'),
      loadGameData().catch(() => null),
      fetch('/trainers', { credentials: 'same-origin' }).then(r => (r.ok ? r.text() : '')).catch(() => ''),
    ]);
    const out: Item[] = [];
    const bossNames = new Set<string>();
    for (const [tier, list] of Object.entries(raids || {})) {
      const tierName = tier === '6' ? box.dataset.mega || 'Mega' : (box.dataset.tier || 'Tier {n}').replace('{n}', tier);
      for (const b of list) {
        bossNames.add(b.pokemon_name);
        out.push({ kind: 'boss', name: b.pokemon_name, sub: (box.dataset.boss || '{tier} raid boss').replace('{tier}', tierName), href: '/raids#boss=' + encodeURIComponent(b.pokemon_name),
          img: b.image_url, types: b.types || [], shiny: !!b.can_be_shiny, key: fold(b.pokemon_name + ' ' + (b.types || []).join(' ')) });
      }
    }
    const mons = new Map<string, number>();
    for (const p of data?.pokemon ?? []) if (!mons.has(p.pokemon_name)) mons.set(p.pokemon_name, p.pokemon_id);
    for (const [name, id] of mons) {
      if (bossNames.has(name)) continue;
      out.push({ kind: 'mon', name, sub: '#' + String(id).padStart(4, '0') + ', ' + (box.dataset.mon || 'Pokemon'), href: '/shinies#mon=' + encodeURIComponent(name),
        img: '/api/pokemon-sprite/' + id + '.png', key: fold(name) });
    }
    if (dir) {
      const doc = new DOMParser().parseFromString(dir, 'text/html');
      doc.querySelectorAll('.trainer-card').forEach(card => {
        const link = card.querySelector<HTMLAnchorElement>('a.trainer-profile-link');
        const name = card.querySelector('.trainer-card-name')?.textContent?.trim();
        const avatar = card.querySelector<HTMLImageElement>('img.trainer-avatar-sprite')?.getAttribute('src') || undefined;
        if (link && name) out.push({ kind: 'trainer', name, sub: box.dataset.trainer || 'Trainer', href: link.getAttribute('href') || '/trainers', img: avatar, key: fold(name + ' ' + (link.getAttribute('href') || '')) });
      });
    }
    const now = Date.now();
    for (const e of events || []) {
      if (e.end && new Date(e.end).getTime() < now) continue;
      out.push({ kind: 'event', name: e.name, sub: (e.heading || box.dataset.event || 'Event'), href: '/events#event=' + encodeURIComponent(e.eventID), key: fold(e.name + ' ' + e.heading) });
    }
    items = out.concat(pageItems(box));
  })();
  return loading;
}

function render(box: HTMLElement, list: HTMLElement, term: string): void {
  const t = fold(term.trim());
  if (!items) { list.innerHTML = '<div class="fk-dots" aria-hidden="true"><i></i><i></i><i></i></div>'; return; }
  const pick = (kind: Item['kind'], n: number) => items!.filter(i => i.kind === kind && (!t || i.key.includes(t))).slice(0, n);
  // Empty field: what is out right now. With a term: everything that matches, bosses first.
  const found = t
    ? [...pick('boss', 6), ...pick('mon', 6), ...pick('trainer', 4), ...pick('event', 5), ...pick('page', 4)]
    : [...pick('boss', 5), ...pick('page', 6)];
  list.innerHTML = found.map(i => {
    const lead = i.kind === 'boss' || i.kind === 'mon'
      ? sticker(i.name, i.img, i.types || [], !!i.shiny, 38)
      : i.kind === 'trainer' && i.img
        ? '<img class="fk-res-av" src="' + esc(i.img) + '" alt="" loading="lazy">'
        : '<span class="fk-res-ic"><svg class="ic" aria-hidden="true"><use href="#i-' + (i.kind === 'event' ? 'events' : i.kind === 'trainer' ? 'trainers' : 'grid') + '"/></svg></span>';
    return '<a class="fk-result" role="option" href="' + esc(i.href) + '">' + lead + '<span><b>' + esc(i.name) + '</b><small>' + esc(i.sub) + '</small></span></a>';
  }).join('') || '<p class="fk-meta" style="padding:8px 4px">' + esc((box.dataset.none || 'Nothing matches "{q}".').replace('{q}', term.trim())) + '</p>';
  trimStickers(list);
}

export function setupSearch(): void {
  const box = document.getElementById('fk-search');
  const q = document.getElementById('fk-q') as HTMLInputElement | null;
  const list = document.getElementById('fk-results');
  const openBtn = document.getElementById('fk-open-search');
  if (!box || !q || !list || !openBtn) return;
  let last: HTMLElement | null = null;

  const open = () => {
    if (!box.hidden) return;
    last = document.activeElement as HTMLElement | null;
    box.hidden = false;
    q.value = '';
    render(box, list, '');
    feedback('open');
    load(box).then(() => render(box, list, q.value));
    window.setTimeout(() => q.focus(), 30);
  };
  const close = () => {
    if (box.hidden) return;
    box.hidden = true;
    feedback('close');
    last?.focus?.({ preventScroll: true });
  };

  openBtn.addEventListener('click', open);
  document.getElementById('fk-close-search')?.addEventListener('click', close);
  box.addEventListener('click', e => { if (e.target === box) close(); });
  q.addEventListener('input', () => render(box, list, q.value));
  q.addEventListener('keydown', e => {
    if (e.key === 'Enter') { const first = list.querySelector<HTMLAnchorElement>('a.fk-result'); if (first) first.click(); }
    if (e.key === 'ArrowDown') { e.preventDefault(); list.querySelector<HTMLAnchorElement>('a.fk-result')?.focus(); }
  });
  list.addEventListener('keydown', e => {
    const a = (e.target as Element).closest('a.fk-result') as HTMLAnchorElement | null;
    if (!a) return;
    if (e.key === 'ArrowDown') { e.preventDefault(); (a.nextElementSibling as HTMLElement | null)?.focus(); }
    if (e.key === 'ArrowUp') { e.preventDefault(); ((a.previousElementSibling as HTMLElement | null) || q).focus(); }
  });
  // A result on the current page (a hash link) still needs the overlay out of the way.
  list.addEventListener('click', () => { box.hidden = true; });
  document.addEventListener('keydown', e => {
    if (e.key === 'Escape' && !box.hidden) { close(); return; }
    const tag = (document.activeElement?.tagName || '').toLowerCase();
    if (e.key === '/' && !e.ctrlKey && !e.metaKey && !['input', 'textarea', 'select'].includes(tag) && !(document.activeElement as HTMLElement | null)?.isContentEditable) {
      e.preventDefault();
      open();
    }
  });
}
