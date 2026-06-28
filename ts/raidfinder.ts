// Raid Finder v2: per-boss matchmaking queue + host lobbies.
// Strings come from window.RF2 (template-rendered i18n); page context from
// window.RAID_CTX. Fast poll (4s) drives the live lobby/queue screens; the
// idle overview refreshes on a slow poll (30s) while the Raids tab is open.

import { createPicker } from './shared/picker';
import type { Picker, PickerEntry } from './shared/picker';
import { loadGameData, pokeSprite, pokeName } from './shared/gamedata';
import type { GameData } from './shared/types';

type Dict = Record<string, string>;
declare const RF2: Dict;
declare const RAID_CTX: { loggedIn: boolean; isMod: boolean; canCustom: boolean; canPreQueue: boolean; username: string };

interface RaidBoss {
  pokemon_name: string;
}

interface LobbyMember {
  user_id: number;
  username: string;
  staff_badge?: string;
  special_rank?: string;
  state: string;
  trust_tier: string;
  raid_rank: string;
  trainer_name?: string;
  trainer_code?: string;
  confirm_deadline?: string;
}

interface LobbyHost {
  username: string;
  staff_badge?: string;
  special_rank?: string;
  trust_tier: string;
  raid_rank: string;
  trainer_name?: string;
  trainer_code?: string;
}

interface Lobby {
  id: number;
  boss_name: string;
  boss_tier: number;
  is_custom: boolean;
  note: string;
  weather_boosted: boolean;
  max_members: number;
  state: string;
  invite_deadline?: string;
  host: LobbyHost;
  members: LobbyMember[];
  my_state?: string;
  my_deadline?: string;
}

interface QueueInfo {
  boss_name: string;
  boss_tier: number;
  position: number;
  total: number;
  priority: boolean;
}

interface FeedbackDue {
  lobby_id: number;
  boss_name: string;
}

interface RaidState {
  server_time: string;
  mode: string;
  cooldown_until?: string;
  queue?: QueueInfo;
  lobby?: Lobby;
  feedback_due: FeedbackDue[];
}

interface OverviewBoss {
  boss_name: string;
  boss_tier: number;
  open_lobbies: number;
  queue_len: number;
}

interface CustomLobbyInfo {
  id: number;
  boss_name: string;
  boss_tier: number;
  note: string;
  host: string;
  queue_len: number;
}

interface Overview {
  bosses: OverviewBoss[];
  custom_lobbies: CustomLobbyInfo[];
}

const CSRF_TOKEN = (document.querySelector('meta[name="csrf-token"]') as HTMLMetaElement)?.content ?? '';

const root = document.getElementById('raid-finder-root');

const FAST_POLL_MS = 4000;
const SLOW_POLL_MS = 30000;

let bosses: Record<string, RaidBoss[]> | null = null;
let serverOffset = 0; // server clock minus client clock, from the last poll
let fastTimer: number | null = null;
let slowTimer: number | null = null;
let countdownTimer: number | null = null;
let deadlineRefreshQueued = false;
let loaded = false;
let idleShellBuilt = false; // idle screen renders once; polls only reload the lobby list frame

function esc(s: unknown): string {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}

function fmtCode(c: string): string {
  return c && c.length === 12 ? c.slice(0, 4) + ' ' + c.slice(4, 8) + ' ' + c.slice(8) : c;
}

// reportFlagHTML returns a "report player" flag for a username, or '' for self/empty.
// The global player-report modal (ts/playerreport.ts) handles clicks via delegation.
function reportFlagHTML(username: string): string {
  const me = (window as unknown as { ME_USERNAME?: string }).ME_USERNAME;
  if (!username || username === me) return '';
  return ' <button type="button" class="report-flag" data-report-user="' + esc(username) +
    '" title="Report player" aria-label="Report player">⚑</button>';
}

function api(method: string, url: string, body?: unknown): Promise<any> {
  const headers: Dict = { 'X-CSRF-Token': CSRF_TOKEN };
  if (body) headers['Content-Type'] = 'application/json';
  return fetch(url, { method, headers, body: body ? JSON.stringify(body) : undefined })
    .then((r) => r.json());
}

function tierLabel(tier: number): string {
  const map: Record<number, string> = { 1: 'T1', 3: 'T3', 5: 'T5', 6: 'Mega' };
  return map[tier] ? '[' + map[tier] + '] ' : '';
}

function staffIcon(badge?: string): string {
  const icons: Dict = { superadmin: '👑', admin: '💎', moderator: '🏅', tester: '🧪' };
  const labels: Dict = { superadmin: RF2.roleSuper, admin: RF2.roleAdmin, moderator: RF2.roleMod, tester: RF2.roleTester };
  return badge && icons[badge] ? '<span class="staff-icon" title="' + labels[badge] + '">' + icons[badge] + '</span>' : '';
}

function specialBadge(rank?: string): string {
  if (rank === 'trusted') return '<span class="special-badge special-badge--trusted" title="' + RF2.rankTrusted + '">✅ ' + RF2.rankTrusted + '</span>';
  if (rank === 'content_creator') return '<span class="special-badge special-badge--cc" title="' + RF2.rankCC + '">🎬 ' + RF2.rankCC + '</span>';
  return '';
}

function trustBadge(tier: string): string {
  const labels: Dict = { trusted: RF2.trustTrusted, neutral: RF2.trustNeutral, caution: RF2.trustCaution };
  if (!labels[tier]) return '';
  return '<span class="trust-badge trust-badge--' + esc(tier) + '">' + labels[tier] + '</span>';
}

function rankCls(rank: string): string {
  const r = rank.toLowerCase();
  if (r.startsWith('league master')) return 'league-master';
  if (r.startsWith('champion')) return 'champion';
  if (r.startsWith('elite four')) return 'elite-four';
  if (r.startsWith('gym leader')) return 'gym-leader';
  if (r.startsWith('trainer')) return 'trainer';
  if (r.startsWith('youngster')) return 'youngster';
  if (r === 'pkmn prof') return 'pkmn-prof';
  return r.replace(/\s+/g, '-');
}

function rankBadge(rank?: string): string {
  if (!rank) return '';
  return '<span class="rank-badge rank-' + rankCls(rank) + '">' + esc(rank) + '</span>';
}

function memberBadges(m: { staff_badge?: string; special_rank?: string; trust_tier?: string; raid_rank?: string }): string {
  return staffIcon(m.staff_badge) + specialBadge(m.special_rank) + (m.trust_tier ? ' ' + trustBadge(m.trust_tier) : '') + (m.raid_rank ? ' ' + rankBadge(m.raid_rank) : '');
}

function copyButton(code: string): HTMLButtonElement {
  const btn = document.createElement('button');
  btn.className = 'trainer-copy-btn';
  btn.type = 'button';
  btn.textContent = RF2.copy;
  btn.addEventListener('click', () => {
    navigator.clipboard.writeText(code).then(() => {
      btn.textContent = RF2.copied;
      btn.classList.add('trainer-copy-btn--done');
      setTimeout(() => {
        btn.textContent = RF2.copy;
        btn.classList.remove('trainer-copy-btn--done');
      }, 1500);
    });
  });
  return btn;
}

const POKEBALL_SVG =
  '<svg viewBox="0 0 64 64" fill="none" xmlns="http://www.w3.org/2000/svg">' +
  '<circle cx="32" cy="32" r="30" stroke="rgba(255,255,255,0.15)" stroke-width="2"/>' +
  '<path class="ball-top" d="M2 32 A30 30 0 0 1 62 32 Z" fill="#9b59b6" opacity="0.85"/>' +
  '<path d="M2 32 A30 30 0 0 0 62 32 Z" fill="rgba(255,255,255,0.82)"/>' +
  '<line x1="2" y1="32" x2="62" y2="32" stroke="rgba(255,255,255,0.2)" stroke-width="3"/>' +
  '<circle cx="32" cy="32" r="8" fill="rgba(0,0,0,0.6)" stroke="rgba(255,255,255,0.2)" stroke-width="2"/>' +
  '<circle cx="32" cy="32" r="4" fill="rgba(255,255,255,0.15)"/></svg>';

// Elements carrying data-deadline (ISO timestamp) get their text updated
// every 250ms from the server-corrected clock. Crossing zero schedules one
// state refresh so the UI follows the authoritative server transition.
function tickCountdowns(): void {
  const els = document.querySelectorAll<HTMLElement>('[data-deadline]');
  if (!els.length) return;
  const now = Date.now() + serverOffset;
  els.forEach((el) => {
    const dl = Date.parse(el.dataset.deadline || '');
    if (isNaN(dl)) return;
    const left = Math.max(0, Math.floor((dl - now) / 1000));
    const m = Math.floor(left / 60);
    const s = left % 60;
    el.textContent = m + ':' + (s < 10 ? '0' : '') + s;
    el.classList.toggle('raid-countdown--low', left <= 30);
    if (left <= 0 && !deadlineRefreshQueued) {
      deadlineRefreshQueued = true;
      setTimeout(() => {
        deadlineRefreshQueued = false;
        fetchState();
      }, 800);
    }
  });
}

function stopTimers(): void {
  if (fastTimer !== null) { clearInterval(fastTimer); fastTimer = null; }
  if (slowTimer !== null) { clearInterval(slowTimer); slowTimer = null; }
}

function setPolling(mode: string): void {
  if (!RAID_CTX.loggedIn) return;
  if (mode === 'idle' || mode === 'feedback') {
    if (fastTimer !== null) { clearInterval(fastTimer); fastTimer = null; }
    if (slowTimer === null) slowTimer = window.setInterval(refreshIfRaidTab, SLOW_POLL_MS);
  } else {
    if (slowTimer !== null) { clearInterval(slowTimer); slowTimer = null; }
    if (fastTimer === null) fastTimer = window.setInterval(fetchState, FAST_POLL_MS);
  }
}

function refreshIfRaidTab(): void {
  const panel = document.getElementById('tab-raids');
  if (panel) {
    if (panel.classList.contains('active')) fetchState();
  } else {
    fetchState(); // dedicated /raidfinder page -- always active
  }
}

document.addEventListener('visibilitychange', () => {
  if (document.hidden) {
    stopTimers();
  } else if (loaded) {
    fetchState();
  }
});

function loadBosses(): Promise<Record<string, RaidBoss[]>> {
  if (bosses) return Promise.resolve(bosses);
  return fetch('/api/raids').then((r) => r.json()).then((d) => { bosses = d; return d; });
}

function fetchState(): void {
  if (!RAID_CTX.loggedIn) {
    renderIdle(null);
    return;
  }
  api('GET', '/api/raid/state')
    .then((st: RaidState) => {
      if (st && (st as any).error) { renderError(); return; }
      serverOffset = Date.parse(st.server_time) - Date.now();
      setPolling(st.mode);
      renderFromState(st);
    })
    .catch(renderError);
}

function renderError(): void {
  idleShellBuilt = false;
  if (root) root.innerHTML = '<div class="error-state">' + esc(RF2.errorLoad) + '</div>';
}

function renderFromState(st: RaidState): void {
  switch (st.mode) {
    case 'queued':
      renderQueued(st);
      break;
    case 'matched':
    case 'member':
      renderMemberLobby(st);
      break;
    case 'hosting':
      renderHostLobby(st);
      break;
    default:
      renderIdle(st);
  }
}

// The idle screen renders its shell (banner, forms, overview frame) once.
// Subsequent polls only swap the dynamic slot (cooldown, feedback prompts)
// and reload the open-lobbies frame, so typed form input survives.
function renderIdle(st: RaidState | null): void {
  if (!root) return;
  if (idleShellBuilt) {
    updateIdleDynamic(st);
    refreshOverview();
    return;
  }
  loadBosses().then(() => {
    if (!root) return;
    root.innerHTML = '';
    idleShellBuilt = true;

    if (!RAID_CTX.loggedIn) {
      const banner = document.createElement('div');
      banner.className = 'trainers-join-banner fade-up';
      banner.innerHTML = '<p><a href="/login">' + esc(RF2.loginCta) + '</a> ' + esc(RF2.or) + ' <a href="/register">' + esc(RF2.signupCta) + '</a> ' + esc(RF2.loginToUse) + '</p>';
      root.appendChild(banner);
    }

    const dyn = document.createElement('div');
    dyn.id = 'raid-dyn';
    root.appendChild(dyn);

    if (RAID_CTX.loggedIn) {
      // Pre-queueing for bosses without a host is a staff / super donator perk;
      // everyone else queues from the open lobbies list below.
      if (RAID_CTX.canPreQueue) root.appendChild(buildQueueForm());
      root.appendChild(buildHostForm());
    }

    root.appendChild(buildOverviewSection());
    updateIdleDynamic(st);
    refreshOverview();
  }).catch(renderError);
}

function updateIdleDynamic(st: RaidState | null): void {
  const dyn = document.getElementById('raid-dyn');
  if (!dyn) return;
  dyn.innerHTML = '';

  if (st && st.cooldown_until) {
    const cd = document.createElement('div');
    cd.className = 'raid-queue-status raid-queue-declined fade-up';
    cd.innerHTML = esc(RF2.cooldownMsg) + ' <span class="raid-countdown" data-deadline="' + esc(st.cooldown_until) + '"></span>';
    dyn.appendChild(cd);
  }

  if (st && st.feedback_due && st.feedback_due.length) {
    st.feedback_due.forEach((f) => dyn.appendChild(buildFeedbackCard(f)));
  }
}

function bossSelect(id: string): HTMLSelectElement {
  const sel = document.createElement('select');
  sel.className = 'search-input raid-boss-select';
  sel.id = id;
  const blank = document.createElement('option');
  blank.value = '';
  blank.textContent = RF2.bossSelect;
  sel.appendChild(blank);
  const tierLabels: Dict = { '1': RF2.tier1, '3': RF2.tier3, '5': RF2.tier5, '6': RF2.tier6 };
  ['6', '5', '3', '1'].forEach((tier) => {
    const list = bosses && bosses[tier];
    if (!list || !list.length) return;
    const grp = document.createElement('optgroup');
    grp.label = tierLabels[tier] || 'Tier ' + tier;
    list.forEach((b) => {
      const opt = document.createElement('option');
      opt.value = b.pokemon_name;
      opt.dataset.tier = tier;
      opt.textContent = b.pokemon_name;
      grp.appendChild(opt);
    });
    sel.appendChild(grp);
  });
  return sel;
}

function formError(form: HTMLElement): HTMLElement {
  let el = form.querySelector<HTMLElement>('.raid-form-error');
  if (!el) {
    el = document.createElement('p');
    el.className = 'raid-form-error';
    form.appendChild(el);
  }
  el.style.display = 'none';
  return el;
}

function showFormError(el: HTMLElement, msg: string): void {
  if (msg.toLowerCase().indexOf('settings') !== -1) {
    el.innerHTML = esc(msg) + ' <a href="/settings">' + esc(RF2.goSettings) + '</a>';
  } else {
    el.textContent = msg;
  }
  el.style.display = '';
}

// Pre-queue card (staff / super donator): queue for any current boss before
// a host opens a lobby for it.
function buildQueueForm(): HTMLElement {
  const card = document.createElement('div');
  card.className = 'raid-post-form-card fade-up';
  card.innerHTML = '<h3>' + esc(RF2.prequeueTitle) + '</h3><p class="raid-form-hint">' + esc(RF2.prequeueHint) + '</p>';

  const form = document.createElement('form');
  form.className = 'raid-post-form';
  const field = document.createElement('div');
  field.className = 'raid-post-field';
  field.appendChild(bossSelect('rq-boss'));
  form.appendChild(field);

  const btn = document.createElement('button');
  btn.type = 'submit';
  btn.className = 'btn-primary raid-post-btn';
  btn.textContent = RF2.btnQueue;
  form.appendChild(btn);

  const err = formError(form);
  form.addEventListener('submit', (e) => {
    e.preventDefault();
    const sel = form.querySelector<HTMLSelectElement>('#rq-boss')!;
    const boss = sel.value.trim();
    if (!boss) return;
    const tier = parseInt((sel.options[sel.selectedIndex] as HTMLOptionElement)?.dataset.tier || '0') || 0;
    api('POST', '/api/raid/queue', { boss_name: boss, boss_tier: tier }).then((d) => {
      if (d.error) showFormError(err, d.error);
      else fetchState();
    });
  });

  card.appendChild(form);
  return card;
}

function buildHostForm(): HTMLElement {
  const card = document.createElement('div');
  card.className = 'raid-post-form-card fade-up';
  card.innerHTML = '<h3>' + esc(RF2.hostTitle) + '</h3>';

  const form = document.createElement('form');
  form.className = 'raid-post-form';

  const bossField = document.createElement('div');
  bossField.className = 'raid-post-field';
  const bossLbl = document.createElement('label');
  bossLbl.htmlFor = 'rh-boss';
  bossLbl.textContent = RF2.bossLbl;
  bossField.appendChild(bossLbl);
  const sel = bossSelect('rh-boss');
  bossField.appendChild(sel);
  form.appendChild(bossField);

  // Custom raid (staff / special rank / top trust only). The boss field is a
  // search picker over the full dex plus Shadow/Mega/Dynamax variants, mounted
  // lazily on first toggle so the idle screen never fetches the game data.
  let customCheck: HTMLInputElement | null = null;
  let customPicker: Picker | null = null;
  if (RAID_CTX.canCustom) {
    const customField = document.createElement('div');
    customField.className = 'raid-post-field raid-wx-field';
    const cl = document.createElement('label');
    cl.className = 'raid-wx-label';
    customCheck = document.createElement('input');
    customCheck.type = 'checkbox';
    cl.appendChild(customCheck);
    cl.appendChild(document.createTextNode(' ' + RF2.customToggle));
    customField.appendChild(cl);
    const customWrap = document.createElement('div');
    customWrap.style.display = 'none';
    customField.appendChild(customWrap);
    customCheck.addEventListener('change', () => {
      customWrap.style.display = customCheck!.checked ? '' : 'none';
      sel.disabled = customCheck!.checked;
      if (customCheck!.checked && !customPicker) {
        mountCustomBossPicker(customWrap).then((p) => { customPicker = p; });
      }
    });
    form.appendChild(customField);
  }

  const wxField = document.createElement('div');
  wxField.className = 'raid-post-field raid-wx-field';
  const wxLabel = document.createElement('label');
  wxLabel.className = 'raid-wx-label';
  const wxCheck = document.createElement('input');
  wxCheck.type = 'checkbox';
  wxLabel.appendChild(wxCheck);
  wxLabel.appendChild(document.createTextNode(' ' + RF2.weatherBoost));
  wxField.appendChild(wxLabel);
  form.appendChild(wxField);

  const sizeField = document.createElement('div');
  sizeField.className = 'raid-post-field raid-post-field--sm';
  const sizeLbl = document.createElement('label');
  sizeLbl.htmlFor = 'rh-size';
  sizeLbl.textContent = RF2.maxMembers;
  const sizeInput = document.createElement('input');
  sizeInput.className = 'search-input';
  sizeInput.id = 'rh-size';
  sizeInput.type = 'number';
  sizeInput.min = '1';
  sizeInput.max = '20';
  sizeInput.value = '5';
  sizeField.appendChild(sizeLbl);
  sizeField.appendChild(sizeInput);
  form.appendChild(sizeField);

  const noteField = document.createElement('div');
  noteField.className = 'raid-post-field';
  const noteLbl = document.createElement('label');
  noteLbl.htmlFor = 'rh-note';
  noteLbl.innerHTML = esc(RF2.noteLbl) + ' <span class="raid-optional">' + esc(RF2.optional) + '</span>';
  const noteInput = document.createElement('input');
  noteInput.className = 'search-input';
  noteInput.id = 'rh-note';
  noteInput.maxLength = 160;
  noteField.appendChild(noteLbl);
  noteField.appendChild(noteInput);
  form.appendChild(noteField);

  const btn = document.createElement('button');
  btn.type = 'submit';
  btn.className = 'btn-primary raid-post-btn';
  btn.textContent = RF2.btnHost;
  form.appendChild(btn);

  const err = formError(form);
  form.addEventListener('submit', (e) => {
    e.preventDefault();
    const isCustom = !!(customCheck && customCheck.checked);
    let boss = '';
    let tier = 0;
    if (isCustom) {
      // Prefer the picked entry; free text still works for unlisted bosses.
      boss = (customPicker ? (customPicker.getSelected()?.label ?? customPicker.input.value) : '').trim().slice(0, 64);
    } else {
      boss = sel.value.trim();
      tier = parseInt((sel.options[sel.selectedIndex] as HTMLOptionElement)?.dataset.tier || '0') || 0;
    }
    if (!boss) return;
    api('POST', '/api/raid/lobbies', {
      boss_name: boss,
      boss_tier: tier,
      is_custom: isCustom,
      note: noteInput.value.trim(),
      max_members: parseInt(sizeInput.value) || 5,
      weather_boosted: wxCheck.checked,
    }).then((d) => {
      if (d.error) showFormError(err, d.error);
      else fetchState();
    });
  });

  card.appendChild(form);
  return card;
}

// Custom raids can target any species in any form, so the picker list is the
// full dex expanded with Shadow, Mega, and Dynamax variants. Base names rank
// first; typing a prefix ("mega ch") narrows straight to that variant.
function customBossEntries(data: GameData): PickerEntry[] {
  const variants = [
    { prefix: '', tag: '', group: 1 },
    { prefix: 'Shadow', tag: 'Shadow', group: 2 },
    { prefix: 'Mega', tag: 'Mega', group: 3 },
    { prefix: 'Dynamax', tag: 'Dmax', group: 4 },
  ];
  const out: PickerEntry[] = [];
  const seen = new Set<string>();
  for (const p of data.pokemon ?? []) {
    if (seen.has(p.pokemon_name)) continue;
    seen.add(p.pokemon_name);
    const label = pokeName(data, p.pokemon_name);
    const sprite = pokeSprite(p.pokemon_id);
    const types = data.pokemonTypes?.[p.pokemon_name] ?? [];
    for (const v of variants) {
      out.push({
        key: v.prefix ? v.prefix + ':' + p.pokemon_name : p.pokemon_name,
        name: v.prefix ? v.prefix + ' ' + p.pokemon_name : p.pokemon_name,
        label: v.prefix ? v.prefix + ' ' + label : label,
        sprite,
        types,
        tag: v.tag || undefined,
        group: v.group,
      });
    }
  }
  return out;
}

function mountCustomBossPicker(wrap: HTMLElement): Promise<Picker> {
  wrap.textContent = '…';
  return loadGameData()
    .then((data) => {
      wrap.textContent = '';
      const picker = createPicker({
        entries: customBossEntries(data),
        placeholder: RF2.customBossPh,
        maxResults: 12,
        onSelect: () => {},
      });
      picker.input.maxLength = 64;
      wrap.appendChild(picker.root);
      return picker;
    })
    .catch(() => {
      // Game data unavailable: degrade to the old plain text input.
      wrap.textContent = '';
      const input = document.createElement('input');
      input.type = 'text';
      input.className = 'search-input';
      input.placeholder = RF2.customBossPh;
      input.maxLength = 64;
      wrap.appendChild(input);
      const fallback: Picker = {
        root: wrap,
        input,
        getSelected: () => null,
        clear: () => { input.value = ''; },
        focus: () => input.focus(),
        isDropdownOpen: () => false,
      };
      return fallback;
    });
}

// Static overview shell: header + hint + the frame div that refreshOverview
// reloads in place.
function buildOverviewSection(): HTMLElement {
  const wrap = document.createElement('div');
  const header = document.createElement('div');
  header.className = 'raid-list-header';
  header.innerHTML = '<span class="raid-list-title">' + esc(RF2.overviewTitle) + '</span>';
  const refreshBtn = document.createElement('button');
  refreshBtn.className = 'raid-refresh-btn';
  refreshBtn.textContent = RF2.refresh;
  refreshBtn.addEventListener('click', refreshOverview);
  header.appendChild(refreshBtn);
  wrap.appendChild(header);

  if (RAID_CTX.loggedIn && !RAID_CTX.canPreQueue) {
    const hint = document.createElement('p');
    hint.className = 'raid-form-hint';
    hint.textContent = RF2.queueListHint;
    wrap.appendChild(hint);
  }

  const frame = document.createElement('div');
  frame.id = 'raid-overview-frame';
  wrap.appendChild(frame);
  return wrap;
}

function queueJoinButton(bossName: string, bossTier: number): HTMLButtonElement {
  const joinBtn = document.createElement('button');
  joinBtn.className = 'btn-secondary raid-action-btn';
  joinBtn.textContent = RF2.btnQueue;
  joinBtn.addEventListener('click', () => {
    api('POST', '/api/raid/queue', { boss_name: bossName, boss_tier: bossTier }).then((d) => {
      if (!d.error) fetchState();
      else refreshOverview();
    });
  });
  return joinBtn;
}

function refreshOverview(): void {
  const frame = document.getElementById('raid-overview-frame');
  if (!frame) return;
  fetch('/api/raid/overview')
    .then((r) => r.json())
    .catch(() => ({ bosses: [], custom_lobbies: [] }))
    .then((ov: Overview) => {
      const cur = document.getElementById('raid-overview-frame');
      if (!cur) return; // screen changed while fetching
      cur.innerHTML = '';

      const items = (ov.bosses || []).filter((b) => b.open_lobbies > 0 || b.queue_len > 0);
      const customs = ov.custom_lobbies || [];
      if (!items.length && !customs.length) {
        const empty = document.createElement('div');
        empty.className = 'raid-posts-empty fade-up';
        empty.textContent = RF2.noActivity;
        cur.appendChild(empty);
        return;
      }

      const list = document.createElement('div');
      list.className = 'raid-posts-list';
      items.sort((a, b) => b.boss_tier - a.boss_tier || a.boss_name.localeCompare(b.boss_name));
      items.forEach((b) => {
        const row = document.createElement('div');
        row.className = 'raid-overview-row fade-up';
        row.innerHTML =
          '<span class="raid-post-boss">' + esc(tierLabel(b.boss_tier)) + esc(b.boss_name) + '</span>' +
          '<span class="raid-overview-counts">' + b.open_lobbies + ' ' + esc(RF2.openLobbies) + ' · ' + b.queue_len + ' ' + esc(RF2.inQueue) + '</span>';
        // Normal users queue from here; the server requires an open lobby.
        if (RAID_CTX.loggedIn && b.open_lobbies > 0) {
          row.appendChild(queueJoinButton(b.boss_name, b.boss_tier));
        }
        list.appendChild(row);
      });
      customs.forEach((c) => {
        const row = document.createElement('div');
        row.className = 'raid-overview-row raid-overview-row--custom fade-up';
        row.innerHTML =
          '<span class="raid-post-boss">' + esc(tierLabel(c.boss_tier)) + esc(c.boss_name) + ' <span class="raid-custom-badge">' + esc(RF2.customBadge) + '</span></span>' +
          '<span class="raid-overview-counts">' + esc(RF2.hostedBy) + ' ' + esc(c.host) + (c.note ? ' · ' + esc(c.note) : '') + '</span>';
        if (RAID_CTX.loggedIn) {
          row.appendChild(queueJoinButton(c.boss_name, c.boss_tier));
        }
        list.appendChild(row);
      });
      cur.appendChild(list);
    });
}


function renderQueued(st: RaidState): void {
  if (!root || !st.queue) return;
  const q = st.queue;
  idleShellBuilt = false;
  root.innerHTML = '';
  const card = document.createElement('div');
  card.className = 'raid-wait-card fade-up';
  card.innerHTML =
    '<div class="pokeball-spinner">' + POKEBALL_SVG + '</div>' +
    '<h3>' + esc(RF2.waitingTitle) + '</h3>' +
    '<div class="raid-wait-boss">' + esc(tierLabel(q.boss_tier)) + esc(q.boss_name) + '</div>' +
    '<div class="raid-wait-pos">' + esc(RF2.queuePos) + ' <strong>#' + q.position + '</strong> ' + esc(RF2.queueOf) + ' ' + q.total +
    (q.priority ? ' <span class="raid-priority-badge">' + esc(RF2.priorityBadge) + '</span>' : '') + '</div>';

  const leave = document.createElement('button');
  leave.className = 'raid-leave-btn';
  leave.textContent = RF2.btnLeaveQueue;
  leave.addEventListener('click', () => {
    api('DELETE', '/api/raid/queue').then(fetchState);
  });
  card.appendChild(leave);
  root.appendChild(card);
}


function lobbyHeader(lb: Lobby): HTMLElement {
  const top = document.createElement('div');
  top.className = 'raid-post-top';
  const wrap = document.createElement('div');
  wrap.className = 'raid-post-boss-wrap';
  wrap.innerHTML = '<span class="raid-post-boss">' + esc(tierLabel(lb.boss_tier)) + esc(lb.boss_name) + '</span>' +
    (lb.is_custom ? ' <span class="raid-custom-badge">' + esc(RF2.customBadge) + '</span>' : '') +
    (lb.weather_boosted ? ' <span class="raid-wx-badge">' + esc(RF2.badgeBoosted) + '</span>' : '');
  top.appendChild(wrap);
  const count = document.createElement('span');
  count.className = 'raid-post-players';
  const confirmed = lb.members.filter((m) => m.state === 'confirmed').length;
  count.textContent = confirmed + ' / ' + lb.max_members;
  top.appendChild(count);
  return top;
}

function countdownRow(labelKey: string, deadline: string): HTMLElement {
  const row = document.createElement('div');
  row.className = 'raid-countdown-row';
  row.innerHTML = esc(RF2[labelKey]) + ' <span class="raid-countdown" data-deadline="' + esc(deadline) + '"></span>';
  return row;
}


function renderMemberLobby(st: RaidState): void {
  if (!root || !st.lobby) return;
  const lb = st.lobby;
  idleShellBuilt = false;
  root.innerHTML = '';

  const card = document.createElement('div');
  card.className = 'raid-post-card raid-lobby-card fade-up';
  card.appendChild(lobbyHeader(lb));

  if (lb.note) {
    const note = document.createElement('p');
    note.className = 'raid-post-note';
    note.textContent = lb.note;
    card.appendChild(note);
  }

  // Host row with code + copy
  const hostRow = document.createElement('div');
  hostRow.className = 'raid-host-code-row';
  hostRow.innerHTML = esc(RF2.addHost) + ' <strong>' + esc(lb.host.trainer_name || lb.host.username) + '</strong>' +
    memberBadges(lb.host) + reportFlagHTML(lb.host.username) + ' ' + esc(RF2.addHost2) + '&nbsp;' +
    '<span class="trainer-code">' + esc(fmtCode(lb.host.trainer_code || '')) + '</span>&nbsp;';
  if (lb.host.trainer_code) hostRow.appendChild(copyButton(lb.host.trainer_code));
  card.appendChild(hostRow);

  // My confirm flow
  if (lb.my_state === 'matched' && lb.my_deadline) {
    card.appendChild(countdownRow('confirmWindow', lb.my_deadline));
    const btn = document.createElement('button');
    btn.className = 'btn-primary raid-confirm-btn';
    btn.textContent = RF2.confirmBtn;
    btn.addEventListener('click', () => {
      api('POST', '/api/raid/lobbies/' + lb.id + '/confirm').then((d) => {
        if (!d.error) fetchState();
      });
    });
    card.appendChild(btn);
  } else if (lb.state === 'full' && lb.invite_deadline) {
    const msg = document.createElement('div');
    msg.className = 'raid-status-msg raid-status-msg--ok';
    msg.textContent = RF2.memberFullMsg;
    card.appendChild(msg);
    card.appendChild(countdownRow('inviteWindow', lb.invite_deadline));
  } else if (lb.state === 'raiding') {
    const msg = document.createElement('div');
    msg.className = 'raid-status-msg raid-status-msg--ok';
    msg.textContent = RF2.raidingMsg;
    card.appendChild(msg);
  } else {
    const msg = document.createElement('div');
    msg.className = 'raid-status-msg';
    msg.textContent = RF2.waitingFill;
    card.appendChild(msg);
  }

  // Remote pass warning + counter tip
  const warn = document.createElement('div');
  warn.className = 'raid-remote-warning';
  warn.textContent = RF2.passWarning;
  card.appendChild(warn);
  const tip = document.createElement('div');
  tip.className = 'raid-counter-tip';
  tip.innerHTML = esc(RF2.prepMsg) + ' <a href="/raids">' + esc(RF2.prepRaids) + '</a> ' + esc(RF2.prepMsg2) + ' <strong>' + esc(lb.boss_name) + '</strong>' + esc(RF2.prepMsg3);
  card.appendChild(tip);

  card.appendChild(buildMemberList(lb, false));

  const footer = document.createElement('div');
  footer.className = 'raid-post-footer';
  const leave = document.createElement('button');
  leave.className = 'raid-leave-btn';
  leave.textContent = RF2.btnLeave;
  leave.addEventListener('click', () => {
    api('POST', '/api/raid/lobbies/' + lb.id + '/leave').then(fetchState);
  });
  footer.appendChild(leave);
  card.appendChild(footer);

  root.appendChild(card);
}

function buildMemberList(lb: Lobby, isHost: boolean): HTMLElement {
  const wrap = document.createElement('div');
  wrap.className = 'raid-joiners-list';
  const title = document.createElement('div');
  title.className = 'raid-joiners-title';
  title.textContent = RF2.membersLbl + ' (' + lb.members.length + ' / ' + lb.max_members + ')';
  wrap.appendChild(title);

  lb.members.forEach((m) => {
    const row = document.createElement('div');
    row.className = 'raid-joiner-row' + (m.state === 'matched' ? ' raid-joiner-row--gray' : '');

    const name = document.createElement('span');
    name.className = 'raid-joiner-name';
    name.innerHTML = esc(m.username) + memberBadges(m) + reportFlagHTML(m.username);
    row.appendChild(name);

    if (m.state === 'matched') {
      const pending = document.createElement('span');
      pending.className = 'raid-joiner-badge raid-joiner-badge--pending';
      pending.innerHTML = esc(RF2.unconfirmed) + (m.confirm_deadline ? ' <span class="raid-countdown" data-deadline="' + esc(m.confirm_deadline) + '"></span>' : '');
      row.appendChild(pending);
    } else {
      const ready = document.createElement('span');
      ready.className = 'raid-joiner-badge raid-joiner-badge--confirmed';
      ready.textContent = RF2.confirmedBadge;
      row.appendChild(ready);
    }

    // Host sees confirmed members' trainer details for in-game invites.
    if (isHost && m.state === 'confirmed' && m.trainer_code) {
      const code = document.createElement('span');
      code.className = 'trainer-code';
      code.textContent = (m.trainer_name ? m.trainer_name + ' · ' : '') + fmtCode(m.trainer_code);
      row.appendChild(code);
      row.appendChild(copyButton(m.trainer_code));
    }

    if (isHost) {
      const kick = document.createElement('button');
      kick.className = 'raid-decline-btn';
      kick.textContent = RF2.btnKick;
      kick.addEventListener('click', () => {
        api('POST', '/api/raid/lobbies/' + lb.id + '/kick', { user_id: m.user_id }).then(fetchState);
      });
      row.appendChild(kick);
    }

    wrap.appendChild(row);
  });
  return wrap;
}


function renderHostLobby(st: RaidState): void {
  if (!root || !st.lobby) return;
  const lb = st.lobby;
  idleShellBuilt = false;
  root.innerHTML = '';

  const card = document.createElement('div');
  card.className = 'raid-post-card raid-lobby-card fade-up';
  card.appendChild(lobbyHeader(lb));

  if (lb.note) {
    const note = document.createElement('p');
    note.className = 'raid-post-note';
    note.textContent = lb.note;
    card.appendChild(note);
  }

  if (lb.state === 'raiding') {
    card.appendChild(buildReportForm(lb));
  } else {
    if (lb.state === 'full' && lb.invite_deadline) {
      const msg = document.createElement('div');
      msg.className = 'raid-status-msg raid-status-msg--ok';
      msg.textContent = RF2.fullMsg;
      card.appendChild(msg);
      card.appendChild(countdownRow('inviteWindow', lb.invite_deadline));
      const invitedBtn = document.createElement('button');
      invitedBtn.className = 'btn-primary raid-confirm-btn';
      invitedBtn.textContent = RF2.btnInvited;
      invitedBtn.addEventListener('click', () => {
        api('POST', '/api/raid/lobbies/' + lb.id + '/invited').then((d) => {
          if (!d.error) fetchState();
        });
      });
      card.appendChild(invitedBtn);
    } else {
      const msg = document.createElement('div');
      msg.className = 'raid-status-msg';
      msg.textContent = RF2.hostWaiting;
      card.appendChild(msg);
    }

    card.appendChild(buildMemberList(lb, true));

    const footer = document.createElement('div');
    footer.className = 'raid-post-footer';
    const cancel = document.createElement('button');
    cancel.className = 'raid-delete-btn';
    cancel.textContent = RF2.btnCancel;
    cancel.addEventListener('click', () => {
      api('DELETE', '/api/raid/lobbies/' + lb.id).then(fetchState);
    });
    footer.appendChild(cancel);
    card.appendChild(footer);
  }

  root.appendChild(card);
}

function buildReportForm(lb: Lobby): HTMLElement {
  const wrap = document.createElement('div');
  wrap.className = 'raid-rate-section';
  const title = document.createElement('div');
  title.className = 'raid-rate-label';
  title.textContent = RF2.reportTitle;
  wrap.appendChild(title);

  const confirmed = lb.members.filter((m) => m.state === 'confirmed');
  const rows: { userID: number; attended: HTMLInputElement; leftEarly: HTMLInputElement }[] = [];
  confirmed.forEach((m) => {
    const row = document.createElement('div');
    row.className = 'raid-joiner-row';
    const name = document.createElement('span');
    name.className = 'raid-joiner-name';
    name.innerHTML = esc(m.username) + memberBadges(m) + reportFlagHTML(m.username);
    row.appendChild(name);

    const attLabel = document.createElement('label');
    attLabel.className = 'raid-report-check';
    const att = document.createElement('input');
    att.type = 'checkbox';
    att.checked = true;
    attLabel.appendChild(att);
    attLabel.appendChild(document.createTextNode(' ' + RF2.reportAttended));
    row.appendChild(attLabel);

    const leftLabel = document.createElement('label');
    leftLabel.className = 'raid-report-check';
    const left = document.createElement('input');
    left.type = 'checkbox';
    leftLabel.appendChild(left);
    leftLabel.appendChild(document.createTextNode(' ' + RF2.reportLeftEarly));
    row.appendChild(leftLabel);

    rows.push({ userID: m.user_id, attended: att, leftEarly: left });
    wrap.appendChild(row);
  });

  const submit = document.createElement('button');
  submit.className = 'btn-primary raid-confirm-btn';
  submit.textContent = RF2.btnReport;
  submit.addEventListener('click', () => {
    const members = rows.map((r) => ({ user_id: r.userID, attended: r.attended.checked, left_early: r.leftEarly.checked }));
    api('POST', '/api/raid/lobbies/' + lb.id + '/report', { members }).then((d) => {
      if (!d.error) fetchState();
    });
  });
  wrap.appendChild(submit);
  return wrap;
}


function buildFeedbackCard(f: FeedbackDue): HTMLElement {
  const card = document.createElement('div');
  card.className = 'raid-post-form-card raid-feedback-card fade-up';
  card.innerHTML = '<h3>' + esc(RF2.feedbackTitle) + ' <strong>' + esc(f.boss_name) + '</strong></h3>';

  let success = true;
  let vote = 'none';

  const successRow = document.createElement('div');
  successRow.className = 'raid-feedback-row';
  const okBtn = document.createElement('button');
  okBtn.className = 'filter-chip active';
  okBtn.type = 'button';
  okBtn.textContent = RF2.feedbackSuccess;
  const failBtn = document.createElement('button');
  failBtn.className = 'filter-chip';
  failBtn.type = 'button';
  failBtn.textContent = RF2.feedbackFailed;
  okBtn.addEventListener('click', () => { success = true; okBtn.classList.add('active'); failBtn.classList.remove('active'); });
  failBtn.addEventListener('click', () => { success = false; failBtn.classList.add('active'); okBtn.classList.remove('active'); });
  successRow.appendChild(okBtn);
  successRow.appendChild(failBtn);
  card.appendChild(successRow);

  const voteRow = document.createElement('div');
  voteRow.className = 'raid-feedback-row';
  const commend = document.createElement('button');
  commend.className = 'filter-chip';
  commend.type = 'button';
  commend.textContent = '👍 ' + RF2.btnCommend;
  const skip = document.createElement('button');
  skip.className = 'filter-chip active';
  skip.type = 'button';
  skip.textContent = RF2.btnSkip;
  const dislike = document.createElement('button');
  dislike.className = 'filter-chip';
  dislike.type = 'button';
  dislike.textContent = '👎 ' + RF2.btnDislike;
  function pick(v: string, btn: HTMLElement) {
    vote = v;
    [commend, skip, dislike].forEach((b) => b.classList.remove('active'));
    btn.classList.add('active');
  }
  commend.addEventListener('click', () => pick('commend', commend));
  skip.addEventListener('click', () => pick('none', skip));
  dislike.addEventListener('click', () => pick('dislike', dislike));
  voteRow.appendChild(commend);
  voteRow.appendChild(skip);
  voteRow.appendChild(dislike);
  card.appendChild(voteRow);

  const submit = document.createElement('button');
  submit.className = 'btn-primary raid-confirm-btn';
  submit.textContent = RF2.btnFeedback;
  submit.addEventListener('click', () => {
    api('POST', '/api/raid/lobbies/' + f.lobby_id + '/feedback', { raid_success: success, host_vote: vote })
      .then(() => fetchState());
  });
  card.appendChild(submit);
  return card;
}


const weatherEmoji: Dict = {
  'Clear': '☀️', 'Partly Cloudy': '⛅', 'Overcast': '☁️',
  'Rainy': '🌧️', 'Snow': '❄️', 'Fog': '🌫️', 'Windy': '💨', 'Extreme': '⚠️',
};

function loadWeatherBanner(): void {
  if (!RAID_CTX.loggedIn) return;
  fetch('/api/weather').then((r) => r.json()).then((w) => {
    const slot = document.getElementById('weather-banner-slot');
    if (!slot || !w.pogo_weather) return;
    const emoji = weatherEmoji[w.pogo_weather] || '🌡️';
    const types = (w.boosted_types || []).join(', ');
    let text = '<strong>' + esc(w.pogo_weather) + '</strong>';
    if (types) text += ': ' + esc(types) + ' ' + esc(RF2.wxSuffix);
    slot.innerHTML = '<div class="weather-banner"><span class="weather-banner-icon">' + emoji +
      '</span><span class="weather-banner-text">' + text + '</span></div>';
  }).catch(() => {});
}


function initRaidFinder(): void {
  if (loaded || !root) return;
  loaded = true;
  loadWeatherBanner();
  fetchState();
  if (countdownTimer === null) countdownTimer = window.setInterval(tickCountdowns, 250);
  setPolling('idle');
}

const tabs = document.getElementById('trainers-tabs');
if (tabs) {
  tabs.addEventListener('click', (e) => {
    const btn = (e.target as HTMLElement).closest('.tab-btn') as HTMLElement | null;
    if (btn && btn.dataset.tab === 'raids') initRaidFinder();
  });
  // Active member/host states matter even before the tab is opened: a cheap
  // initial state fetch promotes the fast poll if the user is mid-raid.
  if (RAID_CTX.loggedIn && root) {
    api('GET', '/api/raid/state').then((st: RaidState) => {
      if (st && st.mode && st.mode !== 'idle' && st.mode !== 'feedback') initRaidFinder();
    }).catch(() => {});
  }
} else if (root) {
  initRaidFinder();
}
