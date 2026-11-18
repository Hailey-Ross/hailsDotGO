// The species field page: shared everywhere a Pokemon opens (raids, the Dex). A sticker that
// drops in, the catalog number, types and shiny status, CP notes, base stats as bars in the type
// colors, where to find it, page specific sections (counters, your box), and actions: Log a
// shiny (which thumps the inked stamp into the header), Open in Dex, Compare in Calc.
import { TYPE_COLORS } from './typecolors';
import { sticker, trimStickers } from './sticker';
import { stampSVG } from './stamp';
import { feedback } from './feel';

export interface SpeciesText {
  shinyAvailable: string; noShiny: string; baseStats: string; whereToFind: string;
  logShiny: string; logged: string; stampHeader: string; stampWord: string; logFailed: string;
}

export interface SpeciesView {
  name: string;
  kicker: string;                       // "No. 0354, Mega raid"
  types: string[];
  image?: string;
  shiny: boolean;
  notes: { k: string; v: number }[];    // CP notes
  stats?: [number, number, number];     // base ATK, DEF, STA
  where?: { label: string; detail: string };
  logged?: boolean;
  log?: () => Promise<boolean>;         // present when this trainer can log this shiny here
  links: { label: string; href: string }[];
  sections?: Node[];                    // page specific: flavor, your box, counters
}

const esc = (s: string) => s.replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c] as string));
const SPARK = '<svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M12 2l1.9 6.1L20 10l-6.1 1.9L12 18l-1.9-6.1L4 10l6.1-1.9z"/></svg>';
const reduce = window.matchMedia('(prefers-reduced-motion: reduce)');

function chip(t: string): string {
  return '<span class="fk-chip" style="--tc:' + (TYPE_COLORS[t] || '#888') + '"><i></i>' + esc(t) + '</span>';
}

export function speciesPage(v: SpeciesView, T: SpeciesText): HTMLElement {
  const lang = document.documentElement.lang || undefined;
  const t1 = TYPE_COLORS[v.types[0]] || '#9099A1', t2 = TYPE_COLORS[v.types[1] || v.types[0]] || t1;
  const el = document.createElement('article');
  el.className = 'fk-species';
  el.style.setProperty('--t1', t1);
  el.style.setProperty('--t2', t2);
  el.innerHTML =
    '<div class="fk-sp-top">' + sticker(v.name, v.image, v.types, v.shiny, 104).replace('class="fk-sticker"', 'class="fk-sticker drop-in"') +
      '<div style="min-width:0"><span class="fk-label">' + esc(v.kicker) + '</span><h2>' + esc(v.name) + '</h2><div class="row">' + v.types.map(chip).join('') +
      (v.shiny ? '<span class="fk-shiny">' + SPARK + esc(T.shinyAvailable) + '</span>' : '<span class="fk-meta">' + esc(T.noShiny) + '</span>') + '</div></div>' +
      stampSVG(!!v.logged, T.stampHeader, T.stampWord, lang) + '</div>' +
    (v.notes.length ? '<div class="fk-notes">' + v.notes.map(n => '<div class="fk-note-tile"><div class="v" data-num="' + n.v + '">0</div><div class="k">' + esc(n.k) + '</div></div>').join('') + '</div>' : '') +
    (v.stats ? '<div><div class="fk-sub">' + esc(T.baseStats) + '</div><div class="fk-stats">' + ['ATK', 'DEF', 'STA'].map((k, i) =>
      '<div class="fk-stat"><span class="k">' + k + '</span><span class="bar"><span class="fill" data-w="' + Math.min(100, Math.round(v.stats![i] / 330 * 100)) + '"></span></span><span class="v" data-num="' + v.stats![i] + '">0</span></div>').join('') + '</div></div>' : '') +
    (v.where ? '<div><div class="fk-sub">' + esc(T.whereToFind) + '</div><div class="fk-where"><span>' + esc(v.where.label) + '</span><span>' + esc(v.where.detail) + '</span></div></div>' : '') +
    '<div class="fk-sp-sections"></div>' +
    '<div class="fk-actions">' + (v.log && v.shiny ? '<button type="button" class="fk-btn" data-log>' + esc(v.logged ? T.logged : T.logShiny) + '</button>' : '') +
      v.links.map(l => '<a class="fk-btn ghost" href="' + esc(l.href) + '">' + esc(l.label) + '</a>').join('') + '</div>';
  const secs = el.querySelector('.fk-sp-sections')!;
  for (const n of v.sections || []) secs.appendChild(n);
  trimStickers(el);

  const logBtn = el.querySelector<HTMLButtonElement>('[data-log]');
  if (logBtn && v.log) {
    if (v.logged) logBtn.disabled = true;
    logBtn.addEventListener('click', async () => {
      if (logBtn.disabled) return;
      logBtn.disabled = true;
      const ok = await v.log!();
      if (!ok) { logBtn.disabled = false; logBtn.textContent = T.logFailed; return; }
      logBtn.textContent = T.logged;
      const stamp = el.querySelector('.fk-stamp');
      if (stamp) { stamp.classList.remove('on'); void (stamp as HTMLElement).getBoundingClientRect(); stamp.classList.add('on'); }
      feedback('stamp');
    });
  }
  return el;
}

/** Count the numbers up and grow the stat bars, once the page is on screen. */
export function animateSpecies(box: ParentNode): void {
  const lang = document.documentElement.lang || undefined;
  box.querySelectorAll<HTMLElement>('[data-num]').forEach((n, i) => {
    const to = +(n.dataset.num || 0);
    if (reduce.matches) { n.textContent = to.toLocaleString(lang); return; }
    window.setTimeout(() => {
      const t0 = performance.now();
      const step = (t: number) => {
        const p = Math.max(0, Math.min(1, (t - t0) / 800)), e = 1 - Math.pow(1 - p, 3);
        n.textContent = Math.round(to * e).toLocaleString(lang);
        if (p < 1) requestAnimationFrame(step);
      };
      requestAnimationFrame(step);
    }, 200 + i * 40);
  });
  window.setTimeout(() => box.querySelectorAll<HTMLElement>('.fill').forEach(f => { f.style.width = f.dataset.w + '%'; }), reduce.matches ? 0 : 250);
}
