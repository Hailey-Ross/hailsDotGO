// /playtest: an interactive guide to the Android open beta. Without this script every step and
// screen is simply shown in order, so the page never depends on it.
//
// Guided mode shows one screen at a time: a big screenshot with a "tap here" ring, one
// instruction, and "Done, next" (or a swipe, or the arrow keys). Finishing a step throws a
// little confetti and a cheer. "Show every step on one page" turns it back into a long page.
//
// Remembered in this browser only (localStorage, wrapped: a private window just forgets):
// the step and screen you are on, which screens you ticked, your account answer, the view mode.

type PTState = {
  step: number;
  card: number;
  ticks: Record<string, boolean>;
  account?: 'yes' | 'no';
  all?: boolean;
  done?: boolean;
};

const KEY = 'flexdex-playtest';
const root = document.getElementById('pt');
const calm = window.matchMedia('(prefers-reduced-motion: reduce)').matches;

function load(): PTState {
  try {
    const s = JSON.parse(localStorage.getItem(KEY) || '{}');
    return {
      step: Number.isInteger(s.step) ? s.step : -1,
      card: Number.isInteger(s.card) ? s.card : 0,
      ticks: s.ticks && typeof s.ticks === 'object' ? s.ticks : {},
      account: s.account, all: !!s.all, done: !!s.done,
    };
  } catch { return { step: -1, card: 0, ticks: {} }; }
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
  const toast = root.querySelector<HTMLElement>('[data-pt-toast]')!;
  const confettiBox = root.querySelector<HTMLElement>('[data-pt-confetti]')!;
  const accountStep = steps.findIndex(s => s.id === 'pt-step-account');
  const cheers = (root.dataset.tCheers || 'Nice!').split('|');

  const cardsOf = (i: number) => Array.from(steps[i]?.querySelectorAll<HTMLElement>('[data-pt-card]') || []);

  // Which device: Android phones follow along here, an iPhone cannot take part, anything
  // else is told to open the page on the phone (and can copy the link).
  const ua = navigator.userAgent;
  const device = /Android/i.test(ua) ? 'android' : /iPhone|iPad|iPod/i.test(ua) ? 'iphone' : 'desktop';
  root.querySelector<HTMLElement>(`[data-pt-device="${device}"]`)?.removeAttribute('hidden');

  const firstStep = () => (st.account === 'yes' && accountStep === 0 ? 1 : 0);
  const guided = () => st.step >= 0 && !st.all;

  function paintTicks(): void {
    let n = 0;
    for (const t of ticks) {
      t.checked = !!st.ticks[t.dataset.ptTick!];
      t.closest('.fk-pt-card')?.classList.toggle('is-done', t.checked);
      if (t.checked) n++;
    }
    const skip = st.account === 'yes' ? cardsOf(accountStep).length : 0;
    const total = ticks.length - skip;
    const done = Math.min(n, total);
    count.textContent = (root!.dataset.tProgress || '{done} / {total}').replace('{done}', String(done)).replace('{total}', String(total));
    bar.style.width = `${total ? Math.round((done / total) * 100) : 0}%`;
    steps.forEach((s, i) => {
      const own = Array.from(s.querySelectorAll<HTMLInputElement>('[data-pt-tick]'));
      railBtns[i]?.classList.toggle('is-done', own.length > 0 && own.every(t => t.checked));
    });
  }

  // enter: which way the new screen springs in from ('next' slides from the right).
  function show(enter?: 'next' | 'prev'): void {
    const g = guided();
    root!.classList.toggle('is-guided', g);
    rail.hidden = !(st.step >= 0 || st.all);
    goBtn.hidden = st.step >= 0 && !st.all;
    modeBtn.hidden = false;
    modeBtn.textContent = st.all ? (root!.dataset.tGuided || '') : (root!.dataset.tAll || '');
    // The old per step Next/Back row is replaced by the screen controls in guided mode.
    root!.querySelectorAll<HTMLElement>('.fk-pt-nav').forEach(n => { n.hidden = true; });
    steps.forEach((s, i) => {
      const here = i === st.step && !st.done;
      s.hidden = g && !here;
      const flip = s.querySelector<HTMLElement>('.fk-pt-flip');
      if (flip) flip.hidden = !(g && here);
      const cards = cardsOf(i);
      cards.forEach((c, j) => {
        const on = !g || !here || j === st.card;
        c.hidden = g && here && j !== st.card;
        c.classList.remove('is-enter-next', 'is-enter-prev');
        if (g && here && on && enter && !calm) {
          void c.offsetWidth; // restart the animation
          c.classList.add(enter === 'next' ? 'is-enter-next' : 'is-enter-prev');
        }
      });
      if (g && here) {
        const label = s.querySelector<HTMLElement>('[data-pt-screen]');
        if (label) label.textContent = (root!.dataset.tScreen || '{n} / {total}').replace('{n}', String(st.card + 1)).replace('{total}', String(cards.length));
        const prev = s.querySelector<HTMLButtonElement>('[data-pt-cprev]');
        if (prev) prev.disabled = st.card === 0 && st.step === firstStep();
      }
    });
    doneCard.hidden = !st.done;
    railBtns.forEach((b, i) => {
      b.classList.toggle('is-current', g && !st.done && i === st.step);
      b.classList.toggle('is-skipped', st.account === 'yes' && i === accountStep);
    });
    root!.querySelectorAll<HTMLButtonElement>('[data-pt-account]').forEach(b => b.classList.toggle('is-picked', b.dataset.ptAccount === st.account));
    paintTicks();
  }

  function scrollToStep(): void {
    rail.scrollIntoView({ behavior: calm ? 'auto' : 'smooth', block: 'start' });
  }

  function go(i: number, card = 0, enter: 'next' | 'prev' = 'next', scroll = true): void {
    st.step = Math.max(0, Math.min(steps.length - 1, i));
    st.card = Math.max(0, Math.min(cardsOf(st.step).length - 1, card));
    st.all = false;
    st.done = false;
    save(st);
    show(enter);
    if (scroll) scrollToStep();
  }

  // "Done, next": tick this screen, then the next screen, or the next step with a cheer.
  function next(): void {
    const cards = cardsOf(st.step);
    const id = cards[st.card]?.dataset.ptCard;
    if (id) st.ticks[id] = true;
    if (st.card < cards.length - 1) {
      st.card++;
      save(st);
      show('next');
      return;
    }
    celebrate(st.step === steps.length - 1);
    if (st.step < steps.length - 1) {
      go(st.step + 1, 0, 'next');
    } else {
      st.done = true;
      save(st);
      show();
      doneCard.scrollIntoView({ behavior: calm ? 'auto' : 'smooth', block: 'start' });
    }
  }

  function prev(): void {
    if (st.card > 0) {
      st.card--;
      save(st);
      show('prev');
      return;
    }
    let p = st.step - 1;
    if (p === accountStep && st.account === 'yes') p--;
    if (p < 0) return;
    go(p, cardsOf(p).length - 1, 'prev');
  }

  // A burst of confetti and a cheer. The last step gets a bigger burst. Calm mode: just the cheer.
  function celebrate(big: boolean): void {
    const n = st.step + 1;
    const cheer = cheers[Math.floor(Math.random() * cheers.length)];
    toast.textContent = big ? '' : `${(root!.dataset.tStepDone || 'Step {n} done!').replace('{n}', String(n))} ${cheer}`;
    if (!big) {
      toast.hidden = false;
      toast.classList.remove('is-pop');
      void toast.offsetWidth;
      toast.classList.add('is-pop');
      window.setTimeout(() => { toast.hidden = true; }, 2400);
    }
    if (calm) return;
    const colors = ['var(--accent)', 'var(--gold)', '#ff7eb6', '#5ee0a0', '#7cc8ff'];
    const pieces = big ? 90 : 40;
    for (let i = 0; i < pieces; i++) {
      const p = document.createElement('i');
      p.style.setProperty('--x', `${(Math.random() * 2 - 1) * (big ? 46 : 32)}vw`);
      p.style.setProperty('--y', `${-(30 + Math.random() * (big ? 55 : 40))}vh`);
      p.style.setProperty('--r', `${Math.round(Math.random() * 720 - 360)}deg`);
      p.style.setProperty('--d', `${Math.round(Math.random() * 180)}ms`);
      p.style.background = colors[i % colors.length];
      if (i % 3 === 0) p.style.borderRadius = '50%';
      confettiBox.appendChild(p);
      window.setTimeout(() => p.remove(), 1900);
    }
  }

  root.addEventListener('click', (e) => {
    const el = (e.target as HTMLElement).closest<HTMLElement>('button, a');
    if (!el || !root.contains(el)) return;
    if (el.dataset.ptAccount) {
      st.account = el.dataset.ptAccount as 'yes' | 'no';
      save(st);
      show();
    } else if (el.hasAttribute('data-pt-go')) {
      go(st.step >= 0 ? st.step : firstStep(), st.step >= 0 ? st.card : 0);
    } else if (el.hasAttribute('data-pt-mode')) {
      st.all = !st.all;
      if (!st.all && st.step < 0) { st.step = firstStep(); st.card = 0; }
      save(st);
      show();
    } else if (el.dataset.ptGoto !== undefined) {
      go(Number(el.dataset.ptGoto), 0);
    } else if (el.hasAttribute('data-pt-cnext')) {
      next();
    } else if (el.hasAttribute('data-pt-cprev')) {
      prev();
    } else if (el.hasAttribute('data-pt-next')) {
      go(st.step + 1, 0);
    } else if (el.hasAttribute('data-pt-prev')) {
      go(st.step - 1 === accountStep && st.account === 'yes' ? st.step : st.step - 1, 0, 'prev');
    } else if (el.hasAttribute('data-pt-finish')) {
      celebrate(true);
      st.done = true;
      save(st);
      show();
      doneCard.scrollIntoView({ behavior: calm ? 'auto' : 'smooth', block: 'start' });
    } else if (el.hasAttribute('data-pt-reset')) {
      st.step = firstStep(); st.card = 0; st.done = false; st.ticks = {};
      save(st);
      show('next');
      scrollToStep();
    } else if (el.dataset.ptHelp) {
      // "Stuck?" and "Open Help" open the matching answer rather than just scrolling to it.
      const d = document.getElementById(`pt-help-${el.dataset.ptHelp}`) as HTMLDetailsElement | null;
      if (d) { e.preventDefault(); d.open = true; d.scrollIntoView({ behavior: calm ? 'auto' : 'smooth', block: 'center' }); }
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

  // Swipe between screens in guided mode: left for next, right for back.
  let sx = 0, sy = 0, tracking = false;
  root.addEventListener('touchstart', (e) => {
    if (!guided() || !(e.target as HTMLElement).closest('.fk-pt-cards')) return;
    const t = e.touches[0];
    sx = t.clientX; sy = t.clientY; tracking = true;
  }, { passive: true });
  root.addEventListener('touchend', (e) => {
    if (!tracking) return;
    tracking = false;
    const t = e.changedTouches[0];
    const dx = t.clientX - sx, dy = t.clientY - sy;
    if (Math.abs(dx) > 60 && Math.abs(dx) > Math.abs(dy) * 1.5) {
      if (dx < 0) next(); else prev();
    }
  }, { passive: true });

  // Arrow keys on a computer (not while typing, not while the picture is zoomed).
  document.addEventListener('keydown', (e) => {
    if (!guided() || st.done || !zoom.hidden) return;
    const tag = (e.target as HTMLElement).tagName;
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
    if (e.key === 'ArrowRight') { e.preventDefault(); next(); }
    else if (e.key === 'ArrowLeft') { e.preventDefault(); prev(); }
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
