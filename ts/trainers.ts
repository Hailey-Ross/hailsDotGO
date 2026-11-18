// Trainer directory (concept): tapping a trainer opens their card as a sheet, with the friend code,
// their favorite Pokemon and "Raid together", instead of leaving the directory. Everything comes
// from the card already on the page; "Full profile" still goes to /trainer/<name>.
import { openSheet } from './shared/fk';
import { sticker, trimStickers } from './shared/sticker';
import { feedback } from './shared/feel';

const T = (window as unknown as { TR_SHEET: Record<string, string> }).TR_SHEET || {};

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
}

function openTrainer(card: HTMLElement): void {
  const name = card.querySelector('.trainer-card-name')?.textContent?.trim() || card.dataset.name || '';
  const link = card.querySelector<HTMLAnchorElement>('a.trainer-profile-link');
  const avatar = card.querySelector<HTMLImageElement>('img.trainer-avatar-sprite');
  const fav = card.querySelector<HTMLImageElement>('img.trainer-fav-sprite');
  const code = card.querySelector('.trainer-card-code')?.textContent?.trim() || '';
  const pronouns = card.querySelector('.trainer-card-pronouns')?.textContent?.trim() || '';

  const page = el('article', 'fk-species fk-trainer-sheet');
  const top = el('div', 'fk-prof-top');
  if (avatar) {
    const av = el('img', 'fk-prof-av');
    av.src = avatar.src;
    av.alt = '';
    top.appendChild(av);
  } else {
    top.appendChild(el('span', 'fk-av fk-prof-av', name.slice(0, 2).toUpperCase()));
  }
  const head = el('div');
  head.appendChild(el('span', 'fk-label', T.kicker || 'Trainer'));
  head.appendChild(el('h2', undefined, name));
  if (pronouns) head.appendChild(el('div', 'fk-meta', pronouns));
  const tags = card.querySelector('.trainer-tags');
  if (tags) head.appendChild(tags.cloneNode(true));
  top.appendChild(head);
  page.appendChild(top);

  if (code) {
    page.appendChild(el('div', 'fk-sub', T.code || 'Friend code'));
    const row = el('div', 'fk-code');
    row.appendChild(el('span', undefined, code));
    const copy = el('button', 'fk-btn small ghost', T.copy || 'Copy');
    copy.type = 'button';
    copy.addEventListener('click', () => {
      navigator.clipboard?.writeText(code.replace(/\s+/g, '')).then(() => { copy.textContent = T.copied || 'Copied'; feedback('tap'); }).catch(() => undefined);
    });
    row.appendChild(copy);
    page.appendChild(row);
  }

  if (fav) {
    page.appendChild(el('div', 'fk-sub', T.favorite || 'Favorite Pokémon'));
    const show = el('div', 'fk-showcase');
    const b = el('div', 'fk-boss');
    b.innerHTML = sticker(fav.alt || '', fav.src, [], false, 64);
    b.appendChild(el('span', undefined, fav.alt || ''));
    show.appendChild(b);
    page.appendChild(show);
    trimStickers(show);
  }

  const actions = el('div', 'fk-actions');
  if (T.raidHref) {
    const raid = el('a', 'fk-btn', T.raid || 'Raid together');
    raid.href = T.raidHref;
    actions.appendChild(raid);
  }
  if (link) {
    const full = el('a', 'fk-btn ghost', T.full || 'Full profile');
    full.href = link.href;
    actions.appendChild(full);
  }
  page.appendChild(actions);
  openSheet(page, 'trainer');
}

document.addEventListener('click', (e) => {
  const t = e.target as Element;
  const card = t.closest<HTMLElement>('.trainer-card');
  // Links and buttons inside the card (profile link, grant award, staff tools) keep their own job.
  if (!card || t.closest('a, button, input, select, textarea')) return;
  openTrainer(card);
});
document.querySelectorAll<HTMLElement>('.trainer-card').forEach((card) => {
  card.tabIndex = 0;
  card.setAttribute('role', 'button');
  card.addEventListener('keydown', (e) => {
    if ((e.key === 'Enter' || e.key === ' ') && e.target === card) { e.preventDefault(); openTrainer(card); }
  });
});
