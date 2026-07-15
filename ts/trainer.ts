import { costumeShinyUrl, TINY_POKEMON } from "./shared/costumes";
import { unownLetter, UNOWN_LETTERS } from "./shared/regionalForms";

function trainerFetch(path: string, method: string, body?: unknown): Promise<Response> {
  const csrfToken = (document.querySelector('meta[name="csrf-token"]') as HTMLMetaElement)?.content ?? '';
  const headers: Record<string, string> = { 'X-CSRF-Token': csrfToken };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  return fetch(path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
}

// ── Feedback form submit ─────────────────────────────────────────
(function initFeedbackForm() {
  const form = document.getElementById('feedback-form') as HTMLFormElement | null;
  if (!form) return;

  const select = document.getElementById('feedback-select') as HTMLSelectElement | null;
  const submitBtn = document.getElementById('feedback-submit') as HTMLButtonElement | null;
  const statusEl = document.getElementById('feedback-form-status') as HTMLElement | null;

  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    if (!select || !select.value) {
      if (statusEl) { statusEl.textContent = 'Please select an option.'; statusEl.style.color = '#fc8181'; statusEl.style.display = ''; }
      return;
    }
    const optionId = parseInt(select.value, 10);
    if (submitBtn) submitBtn.disabled = true;

    const username = form.dataset.username ?? '';
    try {
      const res = await trainerFetch(`/api/feedback/${encodeURIComponent(username)}`, 'POST', { option_id: optionId });
      const d = await res.json().catch(() => ({})) as { ok?: boolean; error?: string };
      if (d.ok) {
        if (statusEl) { statusEl.textContent = 'Feedback saved! Reload to see your update.'; statusEl.style.color = 'var(--green)'; statusEl.style.display = ''; }
      } else {
        if (statusEl) { statusEl.textContent = d.error ?? 'Something went wrong.'; statusEl.style.color = '#fc8181'; statusEl.style.display = ''; }
      }
    } catch (_) {
      if (statusEl) { statusEl.textContent = 'Network error.'; statusEl.style.color = '#fc8181'; statusEl.style.display = ''; }
    }
    if (submitBtn) submitBtn.disabled = false;
  });
})();

// ── Delete own feedback ──────────────────────────────────────────
(function initDeleteOwn() {
  const btn = document.getElementById('feedback-delete-own') as HTMLButtonElement | null;
  if (!btn) return;
  btn.addEventListener('click', async () => {
    if (!confirm('Remove your feedback?')) return;
    const id = btn.dataset.id;
    if (!id) return;
    btn.disabled = true;
    const res = await trainerFetch(`/api/feedback/entry/${id}`, 'DELETE');
    const d = await res.json().catch(() => ({})) as { ok?: boolean };
    if (d.ok) {
      const card = document.getElementById(`fb-${id}`);
      if (card) card.remove();
      btn.remove();
      const form = document.getElementById('feedback-form');
      if (form) {
        const heading = form.previousElementSibling as HTMLElement | null;
        if (heading) heading.textContent = heading.textContent?.replace('Update', 'Leave') ?? '';
      }
    } else {
      btn.disabled = false;
    }
  });
})();

// ── Shiny collection ─────────────────────────────────────────────
(function initShinyCollection() {
  const container = document.getElementById('trainer-shiny-container');
  if (!container) return;

  const METHOD_ICONS: Record<string, string> = {
    wild: '🌿', egg: '🥚', raid: '⚔️', research: '📋',
    evolution: '⬆️', photobomb: '📸', trade: '🤝', go_pass: '🎫', go_tour: '🎟️',
  };
  const METHOD_LABELS: Record<string, string> = {
    wild: TRAINER_CTX.methodWild, egg: TRAINER_CTX.methodEgg,
    raid: TRAINER_CTX.methodRaid, research: TRAINER_CTX.methodResearch,
    evolution: TRAINER_CTX.methodEvolution, photobomb: TRAINER_CTX.methodPhotobomb,
    trade: TRAINER_CTX.methodTrade, go_pass: TRAINER_CTX.methodGoPass, go_tour: TRAINER_CTX.methodGoTour,
  };

  type ShinyItem = {
    // id is sent only when you are looking at your OWN collection, so the detail view can link
    // straight to the entry in the shiny checklist. Nobody else's payload carries it.
    id?: number;
    pokemon_id: string; form: string; region: string; costume: string;
    event_tag: string; method: string; sprite_url: string;
    caught_at: string;
    evolved_at: string | null;
  };

  const REGION_LABELS: Record<string, string> = {
    alolan: TRAINER_CTX.formAlolan,
    galarian: TRAINER_CTX.formGalarian,
    hisuian: TRAINER_CTX.formHisuian,
    paldean: TRAINER_CTX.formPaldean,
    therian: TRAINER_CTX.formTherian,
    origin: TRAINER_CTX.formOrigin,
    attack: TRAINER_CTX.formAttack,
    defense: TRAINER_CTX.formDefense,
    speed: TRAINER_CTX.formSpeed,
    sky: TRAINER_CTX.formSky,
    dusk_mane: TRAINER_CTX.formDuskMane,
    dawn_wings: TRAINER_CTX.formDawnWings,
    crowned_sword: TRAINER_CTX.formCrownedSword,
    crowned_shield: TRAINER_CTX.formCrownedShield,
    black: TRAINER_CTX.formBlack,
    white: TRAINER_CTX.formWhite,
    resolute: TRAINER_CTX.formResolute,
    midnight: TRAINER_CTX.formMidnight,
    dusk: TRAINER_CTX.formDusk,
    sandy_cloak: TRAINER_CTX.formSandyCloak,
    trash_cloak: TRAINER_CTX.formTrashCloak,
    low_key: TRAINER_CTX.formLowKey,
    pom_pom: TRAINER_CTX.formPomPom,
    pau: TRAINER_CTX.formPau,
    sensu: TRAINER_CTX.formSensu,
    blue_striped: TRAINER_CTX.formBlueStriped,
    white_striped: TRAINER_CTX.formWhiteStriped,
    wash: TRAINER_CTX.formWash,
    // A letter is the same glyph in every locale, so the Unown letters label themselves.
    ...Object.fromEntries(UNOWN_LETTERS.map((u) => [u.region, u.letter])),
  };

  // The dex id is only recoverable from the sprite URL: the payload has no dex field.
  function dexOf(item: ShinyItem): number {
    const m = /\/shiny\/(\d+)\.png$/.exec(item.sprite_url);
    return m ? parseInt(m[1], 10) : 0;
  }

  // Regional entries already point at their variant sprite, and costume art is only keyed by base
  // dex, so costumes are skipped for them. Resolving on the client (rather than trusting the
  // server's sprite_url) is what lets a costume named in the admin panel since the last deploy
  // still render: see COSTUME_LABELS in templates/trainer.html.
  function spriteFor(item: ShinyItem): { src: string; fallback: string; dexId: number } {
    const dexId = dexOf(item);
    const src = (item.costume && dexId && !item.region)
      ? (costumeShinyUrl(dexId, item.pokemon_id, item.costume) ?? item.sprite_url)
      : item.sprite_url;
    return { src, fallback: item.sprite_url, dexId };
  }

  // An Unown letter reads the other way round ("Unown F", not "F Unown"), so it has its own
  // template key. Everything else goes through regionalName.
  function displayName(item: ShinyItem): string {
    if (!item.region) return item.pokemon_id;
    const letter = unownLetter(item.region);
    if (letter) {
      return TRAINER_CTX.unownName
        .replace('{name}', item.pokemon_id)
        .replace('{letter}', letter);
    }
    return TRAINER_CTX.regionalName
      .replace('{region}', REGION_LABELS[item.region] ?? item.region)
      .replace('{name}', item.pokemon_id);
  }

  // ── Detail view ───────────────────────────────────────────────
  // Everything below is already in the payload the page downloads; the grid just never showed it.
  // Built with textContent throughout: `costume` and `event_tag` are free text the trainer typed.
  let modal: HTMLDivElement | null = null;

  function ensureModal(): HTMLDivElement {
    if (modal) return modal;
    modal = document.createElement('div');
    // sc-modal is the shiny checklist's modal, already styled globally, and main.css exempts it
    // from the body blur rule -- which matters because this is appended to <body>.
    modal.className = 'sc-modal';
    modal.addEventListener('click', (e) => { if (e.target === modal) closeDetail(); });
    document.body.appendChild(modal);
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape' && modal?.classList.contains('open')) closeDetail();
    });
    return modal;
  }

  function closeDetail(): void { modal?.classList.remove('open'); }

  function detailRow(label: string, value: string): HTMLDivElement {
    const row = document.createElement('div');
    row.style.cssText = 'display:flex;gap:0.75rem;width:100%;font-size:0.85rem';
    const k = document.createElement('span');
    k.style.cssText = 'color:var(--text-2);flex:0 0 5.5rem';
    k.textContent = label;
    const v = document.createElement('span');
    v.style.cssText = 'color:var(--text);flex:1;word-break:break-word';
    v.textContent = value;
    row.appendChild(k);
    row.appendChild(v);
    return row;
  }

  function openDetail(item: ShinyItem): void {
    const m = ensureModal();
    m.textContent = '';

    const inner = document.createElement('div');
    inner.className = 'sc-modal-inner';

    const close = document.createElement('button');
    close.className = 'sc-modal-close';
    close.textContent = '×';
    close.addEventListener('click', closeDetail);
    inner.appendChild(close);

    const { src, fallback, dexId } = spriteFor(item);
    const img = document.createElement('img');
    img.className = 'sc-modal-img' + (dexId && TINY_POKEMON.has(dexId) ? ' sprite-sm-poke' : '');
    img.src = src;
    if (src !== fallback) {
      img.onerror = () => { img.src = fallback; img.onerror = null; };
    }
    img.alt = item.pokemon_id;
    inner.appendChild(img);

    const name = document.createElement('div');
    name.className = 'sc-modal-name';
    name.textContent = displayName(item);
    inner.appendChild(name);

    if (item.form === 'shadow' || item.form === 'purified') {
      const badge = document.createElement('span');
      badge.className = 'trainer-shiny-form-badge';
      badge.textContent = item.form === 'shadow' ? TRAINER_CTX.formShadow : TRAINER_CTX.formPurified;
      inner.appendChild(badge);
    }

    const rows = document.createElement('div');
    rows.style.cssText = 'display:flex;flex-direction:column;gap:0.35rem;width:100%;margin-top:0.5rem';

    // Only what the trainer actually filled in. An empty row is noise, not information.
    if (item.costume) rows.appendChild(detailRow(TRAINER_CTX.costume, item.costume));
    if (item.event_tag) rows.appendChild(detailRow(TRAINER_CTX.eventTag, item.event_tag));
    if (item.method) {
      const icon = METHOD_ICONS[item.method] ? METHOD_ICONS[item.method] + ' ' : '';
      rows.appendChild(detailRow(TRAINER_CTX.method, icon + (METHOD_LABELS[item.method] ?? item.method)));
    }
    if (item.caught_at) {
      const d = new Date(item.caught_at);
      if (!isNaN(d.getTime())) {
        rows.appendChild(detailRow(
          TRAINER_CTX.caught,
          d.toLocaleDateString(undefined, { year: 'numeric', month: 'long', day: 'numeric' }),
        ));
      }
    }
    if (item.evolved_at) rows.appendChild(detailRow(TRAINER_CTX.evolved, '⬆'));
    inner.appendChild(rows);

    // Your own entry, so offer the way to change it. The id is only present on your own profile.
    if (TRAINER_CTX.isOwnProfile && item.id) {
      const edit = document.createElement('a');
      edit.className = 'btn-secondary';
      edit.style.cssText = 'margin-top:0.75rem;font-size:0.82rem;text-decoration:none';
      edit.href = '/shinies?entry=' + item.id;
      edit.textContent = TRAINER_CTX.shinyEdit;
      inner.appendChild(edit);
    }

    m.appendChild(inner);
    m.classList.add('open');
  }

  function renderItems(items: ShinyItem[]): void {
    container.innerHTML = '';
    if (!items.length) {
      const p = document.createElement('p');
      p.style.cssText = 'color:var(--text-2);font-style:italic;font-size:0.88rem';
      p.textContent = TRAINER_CTX.collectionEmpty;
      container.appendChild(p);
      return;
    }
    const grid = document.createElement('div');
    grid.className = 'trainer-shiny-grid';
    for (const item of items) {
      if (!item.sprite_url) continue;
      const cell = document.createElement('div');
      cell.className = 'trainer-shiny-item' + (item.evolved_at ? ' sc-evolved-card' : '');
      // A 48px sprite cannot show a costume, and the badges below it are only a summary of what the
      // trainer actually recorded. Click for the rest.
      cell.style.cursor = 'zoom-in';
      cell.title = TRAINER_CTX.shinyDetails;
      cell.addEventListener('click', () => openDetail(item));

      const img = document.createElement('img');
      const { src, fallback, dexId } = spriteFor(item);
      img.src = src;
      if (src !== fallback) {
        img.onerror = () => { img.src = fallback; img.onerror = null; };
      }
      if (dexId && TINY_POKEMON.has(dexId)) img.classList.add("sprite-sm-poke");
      img.alt = item.pokemon_id;
      img.width = 48; img.height = 48;
      cell.appendChild(img);
      if (item.evolved_at) {
        const evBadge = document.createElement('span');
        evBadge.className = 'trainer-evolved-badge';
        evBadge.textContent = '⬆';
        evBadge.title = TRAINER_CTX.evolved;
        cell.appendChild(evBadge);
      }
      const name = document.createElement('span');
      name.className = 'trainer-shiny-name';
      name.textContent = displayName(item);
      cell.appendChild(name);
      if (item.region) {
        const badge = document.createElement('span');
        badge.className = 'trainer-shiny-form-badge';
        badge.textContent = REGION_LABELS[item.region] ?? item.region;
        cell.appendChild(badge);
      }
      if (item.form === 'shadow' || item.form === 'purified') {
        const badge = document.createElement('span');
        badge.className = 'trainer-shiny-form-badge';
        badge.textContent = item.form === 'shadow' ? TRAINER_CTX.formShadow : TRAINER_CTX.formPurified;
        cell.appendChild(badge);
      }
      if (item.costume || item.event_tag) {
        const sub = document.createElement('span');
        sub.className = 'trainer-shiny-sub';
        const parts: string[] = [];
        if (item.costume)   parts.push(item.costume);
        if (item.event_tag) parts.push(item.event_tag);
        sub.textContent = parts.join(' · ');
        cell.appendChild(sub);
      }
      if (item.method) {
        const method = document.createElement('span');
        method.className = 'trainer-shiny-method';
        method.textContent = (METHOD_ICONS[item.method] ?? '') + ' ' + (METHOD_LABELS[item.method] ?? item.method);
        cell.appendChild(method);
      }
      grid.appendChild(cell);
    }
    container.appendChild(grid);
  }

  container.textContent = TRAINER_CTX.collectionLoading;

  fetch('/api/shinies/of/' + encodeURIComponent(TRAINER_CTX.username))
    .then((r) => r.ok ? r.json() as Promise<ShinyItem[]> : Promise.resolve([] as ShinyItem[]))
    .then((all) => {
      const LIMIT = 6;
      renderItems(all.slice(0, LIMIT));
      if (all.length > LIMIT) {
        const btn = document.createElement('button');
        btn.className = 'btn-secondary';
        btn.style.cssText = 'margin-top:0.75rem;font-size:0.82rem';
        btn.textContent = TRAINER_CTX.shinyShowAll.replace('{n}', String(all.length));
        btn.addEventListener('click', () => {
          const byDex = [...all].sort((a, b) => {
            const dexA = parseInt(/\/shiny\/(\d+)\.png$/.exec(a.sprite_url)?.[1] ?? '0', 10);
            const dexB = parseInt(/\/shiny\/(\d+)\.png$/.exec(b.sprite_url)?.[1] ?? '0', 10);
            return dexA - dexB;
          });
          renderItems(byDex);
          btn.remove();
        });
        container.appendChild(btn);
      }
    })
    .catch(() => { container.textContent = ''; });
})();

// ── Mod delete buttons ───────────────────────────────────────────
(function initModDelete() {
  if (!TRAINER_CTX.isMod) return;
  document.querySelectorAll<HTMLButtonElement>('.feedback-mod-delete').forEach((btn) => {
    btn.addEventListener('click', async () => {
      if (!confirm('Delete this feedback? This cannot be undone.')) return;
      const id = btn.dataset.id;
      if (!id) return;
      btn.disabled = true;
      const res = await trainerFetch(`/api/feedback/entry/${id}`, 'DELETE');
      const d = await res.json().catch(() => ({})) as { ok?: boolean };
      if (d.ok) {
        const card = document.getElementById(`fb-${id}`);
        if (card) card.remove();
      } else {
        btn.disabled = false;
      }
    });
  });
})();
