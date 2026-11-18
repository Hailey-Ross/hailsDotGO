// Messaging (Trade Assist, plan Phase 10): trade conversations, connect requests and the preset
// message composer. Everything a trainer sends is a preset; the server builds the details.
// Texts come from TRT (templates/messages.html). The same endpoints serve the Android app.
import { openSheet, closeSheet } from './shared/fk';
import { feedback } from './shared/feel';

interface Strings {
  [k: string]: string | Record<string, string>;
}
declare const TRT: Strings & {
  state: Record<string, string>; when: Record<string, string>; btn: Record<string, string>; preset: Record<string, string>;
};
const S = (k: string): string => (TRT[k] as string) || k;

const CSRF = (document.querySelector('meta[name="csrf-token"]') as HTMLMetaElement)?.content ?? '';

interface Offer { shiny_id?: number; label: string; }
interface Side { username?: string; offer: Offer | null; confirm: string; shares_list: boolean; available?: boolean; }
interface ThreadItem { id: number; state: string; me: Side; them: Side; unread: boolean; last_message_at: string; }
interface Message { id: number; from: 'me' | 'them' | 'system'; key: string; params?: Record<string, unknown>; at: string; }
interface Thread extends ThreadItem { messages: Message[]; can_agree: boolean; review_modified: boolean; my_side: 'a' | 'b'; }
interface Contact { id: number; username: string; trainer_name: string; state: string; days_left: number; }
interface Contacts { incoming: Contact[]; outgoing: Contact[]; connected: Contact[]; }
interface ListShiny { id: number; label: string; sprite_url?: string; }

const body = document.getElementById('fk-msg-body') as HTMLElement;
const tabsEl = document.getElementById('fk-msg-tabs') as HTMLElement;
let pollTimer: number | undefined;
// Every navigation (a tab, a conversation) takes a new number; an answer that arrives for an older
// one is dropped, so a slow load can never pull the page back somewhere the trainer has left.
let nav = 0;
// One write at a time: a second tap while a send is in flight is ignored, not sent twice.
let busy = false;
// The last conversation drawn, to skip redrawing an unchanged one on the poll (keeps focus and
// stops screen readers from re-reading it).
let lastDrawn = '';

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
}

async function api<T>(path: string, method = 'GET', data?: unknown): Promise<{ ok: boolean; status: number; json: T & { error?: string; code?: string } }> {
  const r = await fetch(path, {
    method,
    credentials: 'same-origin',
    headers: data !== undefined ? { 'Content-Type': 'application/json', 'X-CSRF-Token': CSRF } : { 'X-CSRF-Token': CSRF },
    body: data !== undefined ? JSON.stringify(data) : undefined,
  });
  const json = await r.json().catch(() => ({}));
  return { ok: r.ok, status: r.status, json };
}

// A short notice at the top of the body (errors from the server are already in the trainer's language).
function toast(text: string, bad = true): void {
  document.querySelector('.fk-msg-toast')?.remove();
  const t = el('p', 'fk-msg-toast' + (bad ? ' bad' : ''), text);
  t.setAttribute('role', 'status');
  // Outside the body that gets redrawn, so a redraw does not wipe it.
  body.before(t);
  window.setTimeout(() => t.remove(), 5000);
}

function fmtWhen(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return '';
  const today = new Date();
  return d.toDateString() === today.toDateString()
    ? d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' })
    : d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

function statePill(state: string): HTMLElement {
  return el('span', 'fk-msg-state s-' + state, TRT.state[state] || state);
}

// ---------- Tabs ----------

type Tab = 'active' | 'closed' | 'requests';
function currentTab(): Tab {
  const t = new URLSearchParams(location.search).get('tab');
  return t === 'closed' || t === 'requests' ? t : 'active';
}

function setURL(params: Record<string, string>): void {
  const u = new URL(location.href);
  u.search = new URLSearchParams(params).toString();
  history.replaceState(null, '', u.toString());
}

function markTab(tab: Tab | null): void {
  tabsEl.querySelectorAll<HTMLButtonElement>('button[data-tab]').forEach((b) => b.setAttribute('aria-pressed', String(b.dataset.tab === tab)));
}

// Run one write, ignoring taps while another is in flight.
async function once(fn: () => Promise<void>): Promise<void> {
  if (busy) return;
  busy = true;
  try { await fn(); } finally { busy = false; }
}

tabsEl.addEventListener('click', (e) => {
  const b = (e.target as Element).closest('button[data-tab]') as HTMLButtonElement | null;
  if (!b) return;
  feedback('tap');
  showTab(b.dataset.tab as Tab);
});

function showTab(tab: Tab): void {
  stopPoll();
  nav++;
  lastDrawn = '';
  setURL(tab === 'active' ? {} : { tab });
  markTab(tab);
  if (tab === 'requests') renderRequests();
  else renderList(tab);
}

async function refreshCounts(): Promise<void> {
  const r = await api<{ unread: { messages: number; contacts: number } }>('/api/trades/notices');
  if (!r.ok) return;
  const set = (k: string, n: number) => {
    const b = tabsEl.querySelector<HTMLElement>('[data-count="' + k + '"]');
    if (b) { b.textContent = String(n); b.hidden = n <= 0; }
  };
  set('active', r.json.unread.messages);
  set('requests', r.json.unread.contacts);
}

// ---------- Conversation list ----------

async function renderList(box: 'active' | 'closed'): Promise<void> {
  const mine = nav;
  const r = await api<ThreadItem[]>('/api/trades/threads' + (box === 'closed' ? '?box=closed' : ''));
  if (mine !== nav) return;
  body.textContent = '';
  if (!r.ok) { toast(r.json.error || S('error')); return; }
  const list = r.json as unknown as ThreadItem[];
  if (!list.length) {
    body.appendChild(el('p', 'fk-msg-empty', S(box === 'closed' ? 'emptyClosed' : 'emptyActive')));
    return;
  }
  const ul = el('div', 'fk-msg-list');
  for (const t of list) {
    const row = el('button', 'fk-msg-row' + (t.unread ? ' unread' : ''));
    row.type = 'button';
    const top = el('span', 'fk-msg-row-top');
    top.appendChild(el('b', undefined, '@' + (t.them.username || '')));
    top.appendChild(statePill(t.state));
    top.appendChild(el('span', 'fk-meta', fmtWhen(t.last_message_at)));
    row.appendChild(top);
    const deal = el('span', 'fk-msg-row-deal');
    deal.textContent = S('youGive') + ': ' + (t.me.offer?.label || S('nothingYet')) + '  ·  ' + S('youGet') + ': ' + (t.them.offer?.label || S('nothingYet'));
    row.appendChild(deal);
    row.addEventListener('click', () => openThread(t.id));
    ul.appendChild(row);
  }
  body.appendChild(ul);
}

// ---------- Requests ----------

async function renderRequests(): Promise<void> {
  const mine = nav;
  const r = await api<Contacts>('/api/trades/contacts');
  if (mine !== nav) return;
  body.textContent = '';
  if (!r.ok) { toast(r.json.error || S('error')); return; }
  const c = r.json as unknown as Contacts;
  if (!c.incoming.length && !c.outgoing.length && !c.connected.length) {
    body.appendChild(el('p', 'fk-msg-empty', S('emptyRequests')));
    return;
  }
  const section = (title: string, items: Contact[], build: (x: Contact, row: HTMLElement) => void) => {
    if (!items.length) return;
    body.appendChild(el('div', 'fk-sub', title));
    const ul = el('div', 'fk-msg-list');
    for (const x of items) {
      const row = el('div', 'fk-msg-row static');
      const top = el('span', 'fk-msg-row-top');
      top.appendChild(el('b', undefined, '@' + x.username));
      if (x.trainer_name) top.appendChild(el('span', 'fk-meta', x.trainer_name));
      row.appendChild(top);
      build(x, row);
      ul.appendChild(row);
    }
    body.appendChild(ul);
  };
  section(S('incoming'), c.incoming, (x, row) => {
    const acts = el('span', 'fk-msg-acts');
    const yes = el('button', 'fk-btn small', S('accept'));
    const no = el('button', 'fk-btn small ghost', S('decline'));
    yes.type = no.type = 'button';
    yes.addEventListener('click', () => answer(x.id, 'accept'));
    no.addEventListener('click', () => answer(x.id, 'decline'));
    acts.append(yes, no);
    row.appendChild(acts);
  });
  section(S('outgoing'), c.outgoing, (x, row) => {
    row.appendChild(el('span', 'fk-meta', x.state === 'waiting' ? S('waiting') : S('notAvailable')));
  });
  section(S('connected'), c.connected, (x, row) => {
    const acts = el('span', 'fk-msg-acts');
    acts.appendChild(el('span', 'fk-meta', S('daysLeft').replace('{n}', String(x.days_left))));
    const go = el('button', 'fk-btn small', S('startTrade'));
    go.type = 'button';
    go.addEventListener('click', () => startTrade(x.username));
    acts.appendChild(go);
    row.appendChild(acts);
  });
}

async function answer(id: number, verb: 'accept' | 'decline'): Promise<void> {
  if (busy) return;
  busy = true;
  const r = await api('/api/trades/contacts/' + id + '/' + verb, 'POST', {});
  busy = false;
  if (!r.ok) { toast(r.json.error || S('error')); return; }
  feedback('tap');
  refreshCounts();
  renderRequests();
}

async function startTrade(username: string): Promise<void> {
  if (busy) return;
  busy = true;
  const r = await api<{ id: number }>('/api/trades/threads', 'POST', { username });
  busy = false;
  if (!r.ok) { toast(r.json.error || S('error')); return; }
  openThread((r.json as { id: number }).id);
}

// ---------- One conversation ----------

function nameOf(t: Thread, side: string | undefined): string {
  return side === t.my_side ? S('you') : '@' + (t.them.username || '');
}

function messageText(t: Thread, m: Message): string {
  const p = (m.params || {}) as Record<string, unknown>;
  const side = typeof p.side === 'string' ? p.side : undefined;
  // A server message about the viewer has its own "You" wording (never "We reminded You").
  let text = (side && side === t.my_side && TRT.preset[m.key + '_me']) || TRT.preset[m.key] || m.key;
  const fill: Record<string, string> = {
    pokemon: String(p.pokemon ?? ''),
    code: String(p.code ?? ''),
    when: TRT.when[String(p.when ?? '')] || String(p.when ?? ''),
    before: String(p.before ?? ''),
    after: String(p.after ?? ''),
    name: side ? nameOf(t, side) : '',
  };
  for (const k of Object.keys(fill)) text = text.split('{' + k + '}').join(fill[k]);
  return text;
}

// Which presets the composer offers in each state (mirrors the server's rules; it decides anyway).
const COMPOSER: Record<string, { group: string; keys: string[] }[]> = {
  open: [
    { group: 'groupPlan', keys: ['when', 'when_ok', 'when_other', 'friend_code', 'need_good_friends', 'need_stardust'] },
    { group: 'groupDeal', keys: ['none_fit', 'decline', 'cancel'] },
  ],
  agreed: [
    { group: 'groupPlan', keys: ['when', 'when_ok', 'when_other', 'friend_code', 'need_good_friends', 'need_stardust', 'ready'] },
    { group: 'groupDeal', keys: ['cancel'] },
  ],
  completed: [{ group: 'groupClose', keys: ['thanks_done'] }],
};

function send(t: Thread, payload: Record<string, unknown>): Promise<void> {
  return once(async () => {
    const r = await api('/api/trades/threads/' + t.id + '/messages', 'POST', payload);
    if (!r.ok) { toast(r.json.error || S('error')); return; }
    feedback('tap');
    stickToBottom = true;
    await openThread(t.id, true);
  });
}

function confirmTrade(t: Thread, done: boolean): Promise<void> {
  return once(async () => {
    const r = await api('/api/trades/threads/' + t.id + '/confirm', 'POST', { done });
    if (!r.ok) { toast(r.json.error || S('error')); return; }
    feedback(done ? 'stamp' : 'tap');
    stickToBottom = true;
    await openThread(t.id, true);
  });
}

// Scroll the log to the newest message on the next draw (after the trainer's own message).
let stickToBottom = true;

// A picker sheet: the trainer's own Willing to Trade list, or the partner's visible one.
async function pick(t: Thread, mine: boolean): Promise<void> {
  const page = el('div', 'fk-msg-pick');
  page.appendChild(el('h2', 'fk-sheet-title', mine ? S('pickTitleMine') : S('pickTitleTheirs').replace('{name}', '@' + (t.them.username || ''))));
  let items: ListShiny[] = [];
  if (mine) {
    const r = await api<Array<{ id: number; pokemon_id: string; form: string; region: string; costume: string; event_tag: string; for_trade?: boolean }>>('/api/shinies');
    if (r.ok) {
      items = (r.json as unknown as Array<{ id: number; pokemon_id: string; form: string; region: string; costume: string; event_tag: string; for_trade?: boolean }>)
        .filter((s) => s.for_trade)
        .map((s) => {
          const extra = [s.region, s.form, s.costume, s.event_tag].filter(Boolean);
          return { id: s.id, label: s.pokemon_id + (extra.length ? ' (' + extra.join(', ') + ')' : '') };
        });
    }
  } else {
    const r = await api<{ shinies: ListShiny[] }>('/api/trades/threads/' + t.id + '/list');
    if (r.ok) items = (r.json as { shinies: ListShiny[] }).shinies || [];
  }
  if (!items.length) {
    page.appendChild(el('p', 'fk-meta', mine ? S('pickEmptyMine') : S('pickEmptyTheirs').replace('{name}', '@' + (t.them.username || ''))));
  }
  const grid = el('div', 'fk-msg-pick-grid');
  for (const it of items) {
    const b = el('button', 'fk-msg-pick-item');
    b.type = 'button';
    if (it.sprite_url) {
      const img = el('img');
      img.src = it.sprite_url;
      img.alt = '';
      img.loading = 'lazy';
      b.appendChild(img);
    }
    b.appendChild(el('span', undefined, it.label));
    b.addEventListener('click', () => {
      closeSheet();
      send(t, { preset_key: mine ? 'offer_mine' : 'ask_other', shiny_id: it.id });
    });
    grid.appendChild(b);
  }
  page.appendChild(grid);
  openSheet(page);
}

function pickTime(t: Thread): void {
  const page = el('div', 'fk-msg-pick');
  page.appendChild(el('h2', 'fk-sheet-title', S('pickTime')));
  const row = el('div', 'fk-msg-chips');
  for (const k of ['today', 'this_evening', 'tomorrow', 'this_weekend']) {
    const b = el('button', 'fk-btn small ghost', TRT.when[k] || k);
    b.type = 'button';
    b.addEventListener('click', () => { closeSheet(); send(t, { preset_key: 'when', when: k }); });
    row.appendChild(b);
  }
  page.appendChild(row);
  openSheet(page);
}

async function openThread(id: number, quiet = false): Promise<void> {
  stopPoll();
  const mine = ++nav;
  if (!quiet) { lastDrawn = ''; stickToBottom = true; }
  setURL({ thread: String(id) });
  markTab(null);
  const r = await api<Thread>('/api/trades/threads/' + id);
  if (mine !== nav) return; // the trainer has gone somewhere else meanwhile
  if (!r.ok) {
    showTab('active');
    if (!quiet) toast(r.json.error || S('error'));
    return;
  }
  renderThread(r.json as unknown as Thread);
  refreshCounts();
  // Keep it fresh while it is open and on screen; it stops itself once this view is left.
  pollTimer = window.setInterval(() => {
    if (mine !== nav) { stopPoll(); return; }
    if (document.visibilityState === 'visible') openThreadQuiet(id, mine);
  }, 20000);
}

async function openThreadQuiet(id: number, mine: number): Promise<void> {
  if (busy) return;
  const r = await api<Thread>('/api/trades/threads/' + id);
  if (r.ok && mine === nav) renderThread(r.json as unknown as Thread);
}

function stopPoll(): void {
  if (pollTimer) window.clearInterval(pollTimer);
  pollTimer = undefined;
}

function renderThread(t: Thread): void {
  const sig = JSON.stringify([t.state, t.me, t.them, t.can_agree, t.review_modified, t.messages.length, t.messages[t.messages.length - 1]?.id]);
  if (sig === lastDrawn) return;
  lastDrawn = sig;
  const oldLog = body.querySelector<HTMLElement>('.fk-msg-log');
  const atBottom = !oldLog || oldLog.scrollHeight - oldLog.scrollTop - oldLog.clientHeight < 40;
  const keepScroll = oldLog?.scrollTop;
  body.textContent = '';
  const partner = t.them.username || '';
  const live = t.them.available !== false;

  // Header: back, partner, state, Report and Block.
  const head = el('div', 'fk-msg-thead');
  const back = el('button', 'fk-btn small ghost', '← ' + S('back'));
  back.type = 'button';
  back.addEventListener('click', () => showTab(['completed', 'unconfirmed', 'declined', 'cancelled'].includes(t.state) ? 'closed' : 'active'));
  head.appendChild(back);
  const who = el('span', 'fk-msg-who');
  who.appendChild(el('b', undefined, '@' + partner));
  who.appendChild(statePill(t.state));
  head.appendChild(who);
  if (partner) {
    const acts = el('span', 'fk-msg-acts');
    const rep = el('button', 'fk-btn small ghost', S('report'));
    rep.type = 'button';
    rep.setAttribute('data-report-user', partner);
    rep.setAttribute('data-report-thread', String(t.id));
    const blk = el('button', 'fk-btn small ghost', S('block'));
    blk.type = 'button';
    blk.addEventListener('click', async () => {
      if (!confirm(S('blockConfirm').replace('{name}', '@' + partner))) return;
      const r = await api('/api/social/' + encodeURIComponent(partner) + '/block', 'POST', {});
      if (!r.ok) { toast(r.json.error || S('error')); return; }
      showTab('active');
    });
    acts.append(rep, blk);
    head.appendChild(acts);
  }
  body.appendChild(head);

  if (!live && (t.state === 'open' || t.state === 'agreed')) body.appendChild(el('p', 'fk-msg-note', S('unavailable')));

  // The deal: what each side gives, and the actions that change it.
  const deal = el('div', 'fk-card fk-msg-deal');
  const sideBox = (label: string, offer: Offer | null) => {
    const b = el('div', 'fk-msg-offer');
    b.appendChild(el('span', 'fk-sub', label));
    b.appendChild(el('b', offer ? undefined : 'fk-meta', offer?.label || S('nothingYet')));
    return b;
  };
  const offers = el('div', 'fk-msg-offers');
  offers.append(sideBox(S('youGive'), t.me.offer), sideBox(S('youGet'), t.them.offer));
  deal.appendChild(offers);
  const canChange = live && (t.state === 'open' || t.state === 'agreed') && t.me.confirm === 'none' && t.them.confirm === 'none';
  if (live && (t.state === 'open' || t.state === 'agreed')) {
    const row = el('div', 'fk-msg-chips');
    if (canChange) {
      const mineB = el('button', 'fk-btn small ghost', S('offerMine'));
      const theirsB = el('button', 'fk-btn small ghost', S('pickTheirs'));
      mineB.type = theirsB.type = 'button';
      mineB.addEventListener('click', () => pick(t, true));
      theirsB.addEventListener('click', () => pick(t, false));
      row.append(mineB, theirsB);
    }
    const share = el('button', 'fk-btn small ghost', t.me.shares_list ? S('stopShare') : S('shareList'));
    share.type = 'button';
    share.addEventListener('click', () => send(t, { preset_key: t.me.shares_list ? 'stop_share_list' : 'share_list' }));
    row.appendChild(share);
    if (t.can_agree && !t.review_modified) {
      const ag = el('button', 'fk-btn small', S('agree'));
      ag.type = 'button';
      ag.addEventListener('click', () => send(t, { preset_key: 'agree' }));
      row.appendChild(ag);
    }
    deal.appendChild(row);
  }
  body.appendChild(deal);

  // "Trade modified": the partner edited the shiny they offer; this trainer reviews it.
  if (t.review_modified && live && t.state === 'open') {
    const last = [...t.messages].reverse().find((m) => m.key === 'sys_offer_modified');
    const card = el('div', 'fk-card fk-msg-review');
    card.appendChild(el('b', undefined, S('reviewTitle')));
    if (last) card.appendChild(el('p', undefined, messageText(t, last)));
    const row = el('div', 'fk-msg-chips');
    const ok = el('button', 'fk-btn small', S('reviewOk'));
    const no = el('button', 'fk-btn small ghost', S('reviewNo'));
    ok.type = no.type = 'button';
    ok.disabled = !t.can_agree;
    ok.addEventListener('click', () => send(t, { preset_key: 'agree' }));
    no.addEventListener('click', () => send(t, { preset_key: 'none_fit' }));
    row.append(ok, no);
    card.appendChild(row);
    body.appendChild(card);
  }

  // Agreed: did it happen?
  if (t.state === 'agreed') {
    const card = el('div', 'fk-card fk-msg-confirm');
    if (t.me.confirm === 'none') {
      card.appendChild(el('b', undefined, S('confirmTitle')));
      const row = el('div', 'fk-msg-chips');
      const yes = el('button', 'fk-btn small', S('confirmYes'));
      const no = el('button', 'fk-btn small ghost', S('confirmNo'));
      yes.type = no.type = 'button';
      yes.addEventListener('click', () => confirmTrade(t, true));
      no.addEventListener('click', () => confirmTrade(t, false));
      row.append(yes, no);
      card.appendChild(row);
    } else {
      card.appendChild(el('p', 'fk-meta', S('waitingPartner').replace('{name}', '@' + partner)));
    }
    body.appendChild(card);
  }

  // The conversation itself.
  const log = el('div', 'fk-msg-log');
  for (const m of t.messages) {
    const b = el('div', 'fk-msg-bubble ' + m.from);
    b.appendChild(el('span', undefined, messageText(t, m)));
    b.appendChild(el('time', 'fk-meta', fmtWhen(m.at)));
    log.appendChild(b);
  }
  body.appendChild(log);
  // After layout, or the log has no height to scroll yet.
  const toBottom = stickToBottom || atBottom || keepScroll == null;
  stickToBottom = false;
  requestAnimationFrame(() => { log.scrollTop = toBottom ? log.scrollHeight : (keepScroll ?? 0); });

  // Composer: grouped preset buttons for this state.
  const groups = live ? COMPOSER[t.state] || [] : [];
  if (groups.length) {
    const comp = el('div', 'fk-msg-compose');
    comp.appendChild(el('span', 'fk-sub', S('say')));
    for (const g of groups) {
      const keys = g.keys.filter((k) =>
        !(k === 'decline' && t.me.offer) && // decline is the asked trainer's no
        !(k === 'cancel' && t.state === 'agreed' && t.me.confirm !== 'none')); // already answered
      if (!keys.length) continue;
      comp.appendChild(el('span', 'fk-msg-group', S(g.group)));
      const row = el('div', 'fk-msg-chips');
      for (const k of keys) {
        const b = el('button', 'fk-btn small ghost', TRT.btn[k] || k);
        b.type = 'button';
        b.addEventListener('click', () => (k === 'when' ? pickTime(t) : send(t, { preset_key: k })));
        row.appendChild(b);
      }
      comp.appendChild(row);
    }
    body.appendChild(comp);
  }
}

// ---------- Start ----------

(function start(): void {
  const q = new URLSearchParams(location.search);
  const thread = Number(q.get('thread'));
  refreshCounts();
  if (thread > 0) openThread(thread);
  else showTab(currentTab());
})();
