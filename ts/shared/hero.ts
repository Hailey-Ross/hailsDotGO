// Hero banner (launcher and sign in): sky gradient, sun or moon on its arc for the hour, stars at
// night, soft clouds and four landscape layers with atmospheric perspective that slide sideways
// forever at different speeds.
//
// Every layer is built to repeat exactly over one period of 1200 units (integer frequencies,
// objects placed per period) and drawn as ONE path two periods wide. Each layer (and the clouds)
// is rendered once into an image, then painted onto the hero canvas every frame at its exact
// fractional position with ONE drawImage, so even very slow drift glides instead of stepping a
// pixel at a time. Never tile with overlap: overlap flickers. Images are rebuilt only when the
// habitat, colors or size change (a cache key), and all layers swap in at once.
// Budget: a handful of image copies per frame over a small area, 30 fps on weaker devices,
// paused when no hero is on screen or the tab is hidden, a still picture with reduced motion.
import { habitat, hourNow, phaseOf, settings, Habitat, Layer } from './theme';

function rng(seed: string): () => number {
  let s = 0;
  for (let i = 0; i < seed.length; i++) s = Math.imul(s ^ seed.charCodeAt(i), 2654435761);
  return () => {
    s = Math.imul(s ^ (s >>> 15), 2246822507);
    s = Math.imul(s ^ (s >>> 13), 3266489909);
    s ^= s >>> 16;
    return (s >>> 0) / 4294967296;
  };
}
const f1 = (n: number) => n.toFixed(1);

// A periodic ridge height (0..1 of the layer height) at x, repeating every w.
function ridgeFn(r: () => number, w: number, base: number, amp: number): (x: number) => number {
  const ks = [1, 2, 3, 5, 8], ph = ks.map(() => r() * 6.283), wt = [0.45, 0.28, 0.16, 0.08, 0.03];
  return x => { let s = 0; for (let i = 0; i < ks.length; i++) s += wt[i] * Math.sin(6.283 * ks[i] * x / w + ph[i]); return base - amp * s; };
}
function ridgePath(f: (x: number) => number, w: number, h: number, n: number): string {
  const pts: number[][] = [];
  for (let i = 0; i <= n * 2; i++) { const x = (w * 2) * i / (n * 2); pts.push([x, f(x) * h]); }
  let d = 'M0 ' + h + ' L0 ' + f1(pts[0][1]);
  for (let i = 1; i < pts.length; i++) {
    const a = pts[i - 1], b = pts[i];
    d += ' Q' + f1(a[0]) + ' ' + f1(a[1]) + ' ' + f1((a[0] + b[0]) / 2) + ' ' + f1((a[1] + b[1]) / 2);
  }
  return d + ' L' + (w * 2) + ' ' + f1(pts[pts.length - 1][1]) + ' L' + (w * 2) + ' ' + h + ' Z';
}
function pine(x: number, y: number, th: number): string {
  const w1 = th * 0.42, w2 = th * 0.3, tr = th * 0.07;
  return 'M' + f1(x - tr) + ' ' + f1(y + 2) + ' L' + f1(x - tr) + ' ' + f1(y - th * 0.18) +
    ' L' + f1(x - w1 / 2) + ' ' + f1(y - th * 0.18) + ' L' + f1(x - w2 / 2.6) + ' ' + f1(y - th * 0.52) +
    ' L' + f1(x - w2 / 2) + ' ' + f1(y - th * 0.52) + ' L' + f1(x) + ' ' + f1(y - th) +
    ' L' + f1(x + w2 / 2) + ' ' + f1(y - th * 0.52) + ' L' + f1(x + w2 / 2.6) + ' ' + f1(y - th * 0.52) +
    ' L' + f1(x + w1 / 2) + ' ' + f1(y - th * 0.18) + ' L' + f1(x + tr) + ' ' + f1(y - th * 0.18) + ' L' + f1(x + tr) + ' ' + f1(y + 2) + ' Z';
}
function bush(x: number, y: number, th: number): string {
  const rr = th * 0.34;
  let d = 'M' + f1(x - th * 0.04) + ' ' + (y + 2) + ' L' + f1(x - th * 0.04) + ' ' + f1(y - th * 0.3) + ' L' + f1(x + th * 0.04) + ' ' + f1(y - th * 0.3) + ' L' + f1(x + th * 0.04) + ' ' + (y + 2) + ' Z';
  for (const c of [[0, -0.62, 1], [-0.28, -0.45, 0.8], [0.28, -0.45, 0.8]]) {
    const cx = x + c[0] * th, cy = y + c[1] * th, R = rr * c[2];
    d += ' M' + f1(cx - R) + ' ' + f1(cy) + ' a' + f1(R) + ' ' + f1(R) + ' 0 1 0 ' + f1(2 * R) + ' 0 a' + f1(R) + ' ' + f1(R) + ' 0 1 0 ' + f1(-2 * R) + ' 0 Z';
  }
  return d;
}

// depth: 0 far .. 3 near. Returns the inner SVG for one layer (two periods wide).
function layer(kind: Layer, r: () => number, w: number, h: number, depth: number, fill: string): string {
  const base = [0.44, 0.58, 0.72, 0.86][depth], amp = [0.16, 0.13, 0.1, 0.06][depth];
  const both = (fn: (off: number) => string) => fn(0) + ' ' + fn(w);
  let d = '', f: (x: number) => number;
  if (kind === 'hills' || kind === 'meadow' || kind === 'dunes') {
    f = ridgeFn(r, w, base, kind === 'meadow' ? amp * 0.5 : amp); d = ridgePath(f, w, h, 40);
  } else if (kind === 'pines' || kind === 'trees') {
    f = ridgeFn(r, w, base + 0.03, amp * 0.6); d = ridgePath(f, w, h, 40);
    const trees: number[][] = [], clusters = 7 + Math.floor(r() * 3);
    for (let k = 0; k < clusters; k++) {
      const cx = (k + r() * 0.6) / clusters * w, cnt = 2 + Math.floor(r() * 5);
      for (let i = 0; i < cnt; i++) trees.push([cx + (i - cnt / 2) * (10 + r() * 12), (0.12 + r() * 0.12) * h * (depth >= 2 ? 1.2 : 1)]);
    }
    const ff = f;
    d += ' ' + both(off => trees.map(t => { const x = ((t[0] % w) + w) % w; return (kind === 'pines' ? pine : bush)(x + off, ff(x) * h, t[1]); }).join(' '));
  } else if (kind === 'peaks' || kind === 'snowpeaks') {
    const n = 5 + Math.floor(r() * 3), P: number[][] = [];
    for (let i = 0; i < n; i++) P.push([i * w / n + (r() - 0.5) * w / n * 0.4, (0.1 + r() * 0.26 + depth * 0.06) * h]);
    const valley = h * (0.5 + depth * 0.05);
    d = both(off => {
      let s = 'M' + off + ' ' + h + ' L' + off + ' ' + f1(valley);
      P.forEach((p, j) => { const nx = j + 1 < n ? P[j + 1][0] : w; s += ' L' + f1(p[0] + off) + ' ' + f1(p[1]) + ' L' + f1((p[0] + nx) / 2 + off) + ' ' + f1(valley); });
      return s + ' L' + (w + off) + ' ' + f1(valley) + ' L' + (w + off) + ' ' + h + ' Z';
    });
    if (kind === 'snowpeaks') {
      const caps = both(off => P.map(p => {
        const x = p[0] + off, y = p[1], c = 18 + depth * 3;
        return 'M' + f1(x - c) + ' ' + f1(y + c * 0.9) + ' L' + f1(x) + ' ' + f1(y) + ' L' + f1(x + c) + ' ' + f1(y + c * 0.9) + ' L' + f1(x + c * 0.4) + ' ' + f1(y + c * 0.65) + ' L' + f1(x) + ' ' + (y + c) + ' L' + f1(x - c * 0.4) + ' ' + f1(y + c * 0.65) + ' Z';
      }).join(' '));
      return '<path fill="' + fill + '" d="' + d + '"/><path fill="var(--snow)" d="' + caps + '"/>';
    }
  } else if (kind === 'volcano' || kind === 'island') {
    f = ridgeFn(r, w, base + 0.06, amp * 0.3); d = ridgePath(f, w, h, 24);
    const vx = w * (0.3 + r() * 0.4);
    d += ' ' + both(off => {
      if (kind === 'volcano') return 'M' + (vx - w * 0.22 + off) + ' ' + h + ' Q' + (vx - 40 + off) + ' ' + (h * 0.3) + ' ' + (vx - 22 + off) + ' ' + (h * 0.12) + ' L' + (vx + 22 + off) + ' ' + (h * 0.12) + ' Q' + (vx + 40 + off) + ' ' + (h * 0.3) + ' ' + (vx + w * 0.22 + off) + ' ' + h + ' Z';
      return 'M' + (vx - 230 + off) + ' ' + h + ' Q' + (vx - 110 + off) + ' ' + (h * 0.28) + ' ' + (vx + off) + ' ' + (h * 0.3) + ' Q' + (vx + 130 + off) + ' ' + (h * 0.34) + ' ' + (vx + 260 + off) + ' ' + h + ' Z' +
        ' M' + (vx + 420 + off) + ' ' + h + ' Q' + (vx + 520 + off) + ' ' + (h * 0.55) + ' ' + (vx + 620 + off) + ' ' + h + ' Z';
    });
  } else if (kind === 'sea') {
    const y0 = h * base, waves = 24;
    let s2 = 'M0 ' + h + ' L0 ' + f1(y0);
    for (let i = 0; i < waves * 2; i++) { const x0 = i * w / waves; s2 += ' Q' + f1(x0 + w / waves / 2) + ' ' + f1(y0 + (i % 2 ? 4 : -4)) + ' ' + f1(x0 + w / waves) + ' ' + f1(y0); }
    d = s2 + ' L' + (w * 2) + ' ' + h + ' Z';
  } else if (kind === 'city' || kind === 'town') {
    const bs: [number, number, boolean][] = [];
    let tot = 0;
    while (tot < w) {
      const bw = (kind === 'town' ? 34 : 44) + r() * 46;
      bs.push([bw, (kind === 'town' ? 0.14 + r() * 0.16 : 0.18 + r() * (0.5 - depth * 0.1)) * h, kind === 'town' && r() > 0.45]);
      tot += bw + 4;
    }
    const scale = w / tot;
    d = both(off => {
      let x = off, s = 'M' + off + ' ' + h;
      for (const b of bs) {
        const bw = b[0] * scale, by = h - b[1];
        s += ' L' + f1(x) + ' ' + f1(by);
        if (b[2]) s += ' L' + f1(x + bw / 2) + ' ' + f1(by - 16);
        s += ' L' + f1(x + bw) + ' ' + f1(by) + ' L' + f1(x + bw) + ' ' + f1(h - b[1] * 0.4);
        x += bw + 4 * scale;
        s += ' L' + f1(x) + ' ' + f1(h - b[1] * 0.4);
      }
      return s + ' L' + (w + off) + ' ' + h + ' Z';
    });
  } else if (kind === 'mesa') {
    f = ridgeFn(r, w, base + 0.08, amp * 0.3); d = ridgePath(f, w, h, 24);
    const M: number[][] = [];
    for (let i = 0; i < 3; i++) M.push([(i + r() * 0.5) / 3 * w, 140 + r() * 140, (0.22 + r() * 0.2 + depth * 0.05) * h]);
    d += ' ' + both(off => M.map(m => { const x = m[0] + off; return 'M' + (x - 24) + ' ' + h + ' L' + x + ' ' + f1(m[2]) + ' L' + (x + m[1]) + ' ' + f1(m[2]) + ' L' + (x + m[1] + 24) + ' ' + h + ' Z'; }).join(' '));
  }
  return '<path fill="' + fill + '" d="' + d + '"/>';
}

/** One layer as inline SVG markup, for small previews (the Appearance page). */
export function landscapeSVG(t: Habitat, w: number, h: number): string {
  const r = rng(t.seed || t.id), kinds: Layer[] = [t.far || 'hills', ...t.layers];
  const fills = ['var(--land0)', 'var(--land1)', 'var(--land2)', 'var(--land3)'];
  return kinds.map((k, i) => layer(k, r, w, h, i, fills[i])).join('');
}

const CLOUD = 'M20 34 a14 14 0 0 1 4 -27 a18 18 0 0 1 34 -3 a13 13 0 0 1 22 10 a11 11 0 0 1 2 20 Z';
const reduce = window.matchMedia('(prefers-reduced-motion: reduce)');
const nav = navigator as Navigator & { deviceMemory?: number };
const weak = (nav.hardwareConcurrency || 8) <= 4 || (nav.deviceMemory || 8) <= 4;

interface Drawn { img: HTMLImageElement; period: number; w: number; h: number; y: number; secs: number; }
interface Hero { box: HTMLElement; cv: HTMLCanvasElement; ctx?: CanvasRenderingContext2D; W: number; H: number; dpr: number; layers: Drawn[]; key?: string; gen: number; skyHTML?: string; visible: boolean; }

const heroes: Hero[] = [];
let raf = 0, lastFrame = 0;

function svgImage(inner: string, vw: number, vh: number, pw: number, ph: number, done: (img: HTMLImageElement) => void): void {
  const img = new Image();
  img.onload = () => done(img);
  img.src = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg" width="' + Math.ceil(pw) + '" height="' + Math.ceil(ph) + '" viewBox="0 0 ' + vw + ' ' + vh + '" preserveAspectRatio="none">' + inner + '</svg>');
}

// Sun or moon on its arc for the hour, stars at night.
function renderSky(h: Hero): void {
  const fx = h.box.querySelector('.fk-sky-fx') as HTMLElement | null;
  if (!fx) return;
  const hr = hourNow(), night = phaseOf(hr) === 'night';
  let p: number;
  if (!night) p = Math.max(0, Math.min(1, (hr - 6) / 12));
  else { const hh = hr < 12 ? hr + 24 : hr; p = hh >= 18 && hh <= 30 ? (hh - 18) / 12 : 0.35; }
  // Keep the sun or moon clear of the greeting: on phones it rides low on the right, between the
  // text and the hills; on wider screens its arc stays in the right half of the sky.
  const narrow = h.box.clientWidth < 560, arc = Math.sin(p * Math.PI);
  const x = narrow ? 72 + p * 18 : 50 + p * 42, y = narrow ? 58 - arc * 6 : 60 - arc * 40;
  let html = '';
  if (night) {
    const r = rng('stars');
    for (let i = 0; i < 26; i++) html += '<i class="fk-star" style="left:' + f1(r() * 100) + '%;top:' + f1(r() * 55) + '%;animation-delay:-' + f1(r() * 6) + 's;width:' + f1(1 + r() * 2) + 'px"></i>';
  }
  html += '<i class="fk-orb ' + (night ? 'moon' : 'sun') + '" style="left:' + f1(x) + '%;top:' + f1(y) + '%"></i>';
  // Only touch the sky when it actually changed, so stars do not restart their twinkle.
  if (h.skyHTML !== html) { fx.innerHTML = html; h.skyHTML = html; }
}

function buildLand(h: Hero): void {
  const W = h.box.clientWidth, H = h.box.clientHeight;
  if (!W || !H) return;
  const dpr = Math.min(window.devicePixelRatio || 1, 2), cs = getComputedStyle(document.documentElement);
  const t = habitat(), r = rng(t.seed || t.id), kinds: Layer[] = [t.far || 'hills', ...t.layers];
  const fills = ['var(--land0)', 'var(--land1)', 'var(--land2)', 'var(--land3)'], secs = [520, 300, 190, 120];
  const resolve = (s: string) => s.replace(/var\(--([a-z0-9-]+)\)/g, (_, k) => cs.getPropertyValue('--' + k).trim() || '#888');
  // Nothing changed (same habitat, colors and size): keep the scenery, no rebuild.
  const key = [t.id, t.seed, t.layers.join(','), W, H, dpr].concat(['land0', 'land1', 'land2', 'land3', 'cloud', 'snow'].map(k => cs.getPropertyValue('--' + k).trim())).join('|');
  if (h.key === key && h.layers.length) { start(); return; }
  h.key = key;
  const gen = ++h.gen;
  // Each layer is one image two widths wide; its width is at least the banner's, so a single
  // draw always covers the banner and nothing is ever painted twice (no pulsing edges, no seams).
  const landH = H * 0.68, sc = Math.max(W / 1200, landH / 160), csc = Math.max((H * 0.42) / 120, W / 1200);
  const next: Drawn[] = [];
  let pending = kinds.length + 1;
  const ready = () => {
    if (h.gen !== gen || --pending) return;
    // Everything is loaded: swap in the new scenery all at once, resizing the canvas only now.
    if (h.cv.width !== Math.round(W * dpr) || h.cv.height !== Math.round(H * dpr)) { h.cv.width = Math.round(W * dpr); h.cv.height = Math.round(H * dpr); }
    h.ctx = h.cv.getContext('2d') || undefined; h.W = W; h.H = H; h.dpr = dpr; h.layers = next;
    paint(h, performance.now());
  };
  kinds.forEach((k, i) => {
    const inner = resolve(layer(k, r, 1200, 160, i, fills[i]));
    svgImage(inner, 2400, 160, 2400 * sc * dpr, 160 * sc * dpr, img => { next[i] = { img, period: 1200 * sc, w: 2400 * sc, h: 160 * sc, y: H - 160 * sc, secs: secs[i] }; ready(); });
  });
  const clouds = [[140, 26, 1.1], [560, 52, 0.8], [900, 18, 0.95], [1340, 26, 1.1], [1760, 52, 0.8], [2100, 18, 0.95]]
    .map(c => '<path transform="translate(' + c[0] + ' ' + c[1] + ') scale(' + c[2] + ')" d="' + CLOUD + '"/>').join('');
  svgImage('<g fill="' + (cs.getPropertyValue('--cloud').trim() || 'white') + '">' + clouds + '</g>', 2400, 120, 2400 * csc * dpr, 120 * csc * dpr,
    img => { next[4] = { img, period: 1200 * csc, w: 2400 * csc, h: 120 * csc, y: H * 0.05, secs: 240 }; ready(); });
  start();
}

const moving = () => !reduce.matches && settings().motion;

function paint(h: Hero, ms: number): void {
  if (!h.ctx) return;
  const ctx = h.ctx, s = moving() ? ms / 1000 : 0;
  ctx.setTransform(h.dpr, 0, 0, h.dpr, 0, 0);
  ctx.clearRect(0, 0, h.W, h.H);
  for (const i of [4, 0, 1, 2, 3]) {
    const L = h.layers[i];
    if (!L) continue;
    // One draw per layer: the image is periodic, so jumping back one period is invisible.
    const off = (s / L.secs * L.period) % L.period;
    ctx.drawImage(L.img, -off, L.y, L.w, L.h);
  }
}

function loop(ms: number): void {
  raf = requestAnimationFrame(loop);
  if (weak && ms - lastFrame < 1000 / 30 - 2) return;
  lastFrame = ms;
  for (const h of heroes) if (h.visible) paint(h, ms);
}

function start(): void {
  cancelAnimationFrame(raf);
  raf = 0;
  if (moving() && !document.hidden && heroes.some(h => h.visible)) raf = requestAnimationFrame(loop);
}

let io: IntersectionObserver | null = null;

/** Turn an element with .fk-sky-fx and canvas.fk-land children into a living hero banner. */
export function mountHero(box: HTMLElement): void {
  const cv = box.querySelector('canvas.fk-land') as HTMLCanvasElement | null;
  if (!cv) return;
  const h: Hero = { box, cv, W: 0, H: 0, dpr: 1, layers: [], gen: 0, visible: true };
  heroes.push(h);
  if (!io && 'IntersectionObserver' in window) {
    io = new IntersectionObserver(entries => {
      for (const e of entries) { const x = heroes.find(hh => hh.box === e.target); if (x) x.visible = e.isIntersecting; }
      start();
    });
  }
  io?.observe(box);
  renderSky(h);
  buildLand(h);
}

/** Repaint every hero for the current theme and hour (called on theme changes). */
export function refreshHeroes(): void {
  for (const h of heroes) { renderSky(h); buildLand(h); }
  if (!moving()) for (const h of heroes) paint(h, 0);
}

document.addEventListener('visibilitychange', () => { if (document.hidden) cancelAnimationFrame(raf); else start(); });
reduce.addEventListener?.('change', refreshHeroes);
let rt = 0;
window.addEventListener('resize', () => { window.clearTimeout(rt); rt = window.setTimeout(refreshHeroes, 200); });
