// /playtest: turns the long guide into one step at a time. Without this script every step
// is simply shown in order, so the page never depends on it.
//
// Remembered in this browser only (localStorage, wrapped: a private window just forgets):
// which step you are on, which cards you ticked, your account answer and the view mode.

type PTState = { step: number; ticks: Record<string, boolean>; account?: 'yes' | 'no'; all?: boolean; done?: boolean };

const KEY = 'flexdex-playtest';
const root = document.getElementById('pt');

function load(): PTState {
  try {
    const s = JSON.parse(localStorage.getItem(KEY) || '{}');
    return { step: Number.isInteger(s.step) ? s.step : -1, ticks: s.ticks && typeof s.ticks === 'object' ? s.ticks : {}, account: s.account, all: !!s.all, done: !!s.done };
  } catch { return { step: -1, ticks: {} }; }
}
function save(s: PTState): void { try { localStorage.setItem(KEY, JSON.stringify(s)); } catch { /* private mode */ } }

if (root) {
  const st = load();
  const signedIn = root.dataset.signedIn === '1';
  if (signedIn) st.account = 'yes';
  const steps = Array.from(root.querySelectorAll<HTMLElement>('[data-pt-step]'));
  const rail = root.querySelector<HTMLElement>('.fk-pt-rail')!;
  const railBtns = Array.from(rail.querySelectorAll<HTMLButtonElement>('[data-pt-goto]'));
  const goBtn = root.querySelector<HTMLButtonElement>('[data-pt-go]')!;
  const modeBtn = root.querySelector<HTMLButtonElement>('[data-pt-mode]')!;
  const doneCard = document.getElementById('pt-done')!;
  const count = root.querySelector<HTMLElement>('[data-pt-count]')!;
  const bar = root.querySelector<HTMLElement>('.fk-pt-bar i')!;
  const ticks = Array.from(root.querySelectorAll<HTMLInputElement>('[data-pt-tick]'));
  const accountStep = steps.findIndex(s => s.id === 'pt-step-account');

  // Which device: Android phones follow along here, an iPhone cannot take part, anything
  // else is told to open the page on the phone (and can copy the link).
  const ua = navigator.userAgent;
  const device = /Android/i.test(ua) ? 'android' : /iPhone|iPad|iPod/i.test(ua) ? 'iphone' : 'desktop';
  root.querySelector<HTMLElement>(`[data-pt-device="${device}"]`)?.removeAttribute('hidden');

  const firstStep = () => (st.account === 'yes' && accountStep === 0 ? 1 : 0);

  function paintTicks(): void {
    let n = 0;
    for (const t of ticks) {
      t.checked = !!st.ticks[t.dataset.ptTick!];
      t.closest('.fk-pt-card')?.classList.toggle('is-done', t.checked);
      if (t.checked) n++;
    }
    const skip = st.account === 'yes' ? steps[accountStep]?.querySelectorAll('[data-pt-tick]').length || 0 : 0;
    const total = ticks.length - skip;
    const done = Math.min(n, total);
    count.textContent = (root!.dataset.tProgress || '{done} / {total}').replace('{done}', String(done)).replace('{total}', String(total));
    bar.style.width = `${total ? Math.round((done / total) * 100) : 0}%`;
    steps.forEach((s, i) => {
      const own = Array.from(s.querySelectorAll<HTMLInputElement>('[data-pt-tick]'));
      railBtns[i]?.classList.toggle('is-done', own.length > 0 && own.every(t => t.checked));
    });
  }

  function show(): void {
    const guided = st.step >= 0 && !st.all;
    root!.classList.toggle('is-guided', guided);
    rail.hidden = !(st.step >= 0 || st.all);
    goBtn.hidden = st.step >= 0 && !st.all;
    modeBtn.hidden = false;
    modeBtn.textContent = st.all ? (root!.dataset.tGuided || '') : (root!.dataset.tAll || '');
    root!.querySelectorAll<HTMLElement>('.fk-pt-nav').forEach(n => { n.hidden = !guided; });
    steps.forEach((s, i) => { s.hidden = guided && (st.done || i !== st.step); });
    doneCard.hidden = !st.done;
    railBtns.forEach((b, i) => {
      b.classList.toggle('is-current', guided && !st.done && i === st.step);
      b.classList.toggle('is-skipped', st.account === 'yes' && i === accountStep);
    });
    root!.querySelectorAll<HTMLButtonElement>('[data-pt-account]').forEach(b => b.classList.toggle('is-picked', b.dataset.ptAccount === st.account));
    paintTicks();
  }

  function go(i: number, scroll = true): void {
    st.step = Math.max(0, Math.min(steps.length - 1, i));
    st.all = false;
    st.done = false;
    save(st);
    show();
    if (scroll) rail.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }

  root.addEventListener('click', (e) => {
    const el = (e.target as HTMLElement).closest<HTMLElement>('button, a');
    if (!el || !root.contains(el)) return;
    if (el.dataset.ptAccount) {
      st.account = el.dataset.ptAccount as 'yes' | 'no';
      save(st);
      show();
    } else if (el.hasAttribute('data-pt-go')) {
      go(st.step >= 0 ? st.step : firstStep());
    } else if (el.hasAttribute('data-pt-mode')) {
      st.all = !st.all;
      if (!st.all && st.step < 0) st.step = firstStep();
      save(st);
      show();
    } else if (el.dataset.ptGoto !== undefined) {
      go(Number(el.dataset.ptGoto));
    } else if (el.hasAttribute('data-pt-next')) {
      go(st.step + 1);
    } else if (el.hasAttribute('data-pt-prev')) {
      go(st.step - 1 === accountStep && st.account === 'yes' ? st.step : st.step - 1);
    } else if (el.hasAttribute('data-pt-finish')) {
      st.done = true;
      save(st);
      show();
      doneCard.scrollIntoView({ behavior: 'smooth', block: 'start' });
    } else if (el.hasAttribute('data-pt-reset')) {
      st.step = firstStep(); st.done = false; st.ticks = {};
      save(st);
      show();
      rail.scrollIntoView({ behavior: 'smooth', block: 'start' });
    } else if (el.dataset.ptHelp) {
      // "Stuck?" opens the matching answer rather than just scrolling to a closed one.
      const d = document.getElementById(`pt-help-${el.dataset.ptHelp}`) as HTMLDetailsElement | null;
      if (d) { e.preventDefault(); d.open = true; d.scrollIntoView({ behavior: 'smooth', block: 'center' }); }
    } else if (el.hasAttribute('data-pt-copy')) {
      const label = el.textContent;
      navigator.clipboard?.writeText(location.origin + location.pathname).then(() => {
        el.textContent = root.dataset.tCopied || label;
        setTimeout(() => { el.textContent = label; }, 2000);
      }).catch(() => { /* the address bar still has it */ });
    } else if (el.hasAttribute('data-pt-zoom')) {
      const img = el.querySelector('img');
      if (img) openZoom(img.currentSrc || img.src);
    }
  });

  root.addEventListener('change', (e) => {
    const t = e.target as HTMLInputElement;
    if (!t.dataset.ptTick) return;
    if (t.checked) st.ticks[t.dataset.ptTick] = true; else delete st.ticks[t.dataset.ptTick];
    save(st);
    paintTicks();
  });

  // Zoom: the screenshot at full height, closed by its button, a tap anywhere or Escape.
  const zoom = root.querySelector<HTMLElement>('[data-pt-zoombox]')!;
  const zoomImg = zoom.querySelector('img')!;
  let lastFocus: HTMLElement | null = null;
  function openZoom(src: string): void {
    lastFocus = document.activeElement as HTMLElement | null;
    zoomImg.src = src;
    zoom.hidden = false;
    zoom.querySelector<HTMLButtonElement>('[data-pt-zoomclose]')?.focus();
  }
  function closeZoom(): void {
    zoom.hidden = true;
    zoomImg.removeAttribute('src');
    lastFocus?.focus();
  }
  zoom.addEventListener('click', closeZoom);
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && !zoom.hidden) closeZoom(); });

  show();
}
