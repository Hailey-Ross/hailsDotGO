// Topographic map background: scrolls forever, smoothly.
//
// An endless map: value noise traced into contour lines with marching squares, every third line
// drawn a little stronger like the index lines on a real map. The map never changes shape; it
// glides slowly, as if the kit is panning across terrain.
//
// Smoothness: the map is drawn once into an offscreen buffer a little larger than the screen,
// then painted onto the visible canvas every frame at its exact fractional position, so it moves
// a tenth of a pixel at a time with no pixel snapping. The buffer for the NEXT block is prepared
// in idle time ahead of need; at the block boundary the buffers swap in the same frame, and the
// grids line up exactly, so there is never a jump or a hitch.
//
// Budget: one full map trace about every 27 seconds (in idle time), one image copy per frame
// (30 fps on weaker devices), drawn at 1x resolution (the lines are faint and soft anyway); a
// still map with reduced motion, when switched off, when the tab is hidden, or if the device
// struggles (median frame over 28 ms even at 30 fps).
import type { Topo } from './theme';

type IdleWin = Window & {
  requestIdleCallback?: (cb: () => void, o?: { timeout: number }) => number;
  cancelIdleCallback?: (id: number) => void;
};

export interface TopoMap {
  /** Recolor and reshape for the current theme (redraws the visible block). */
  recolor(color: string, cfg: Topo): void;
  /** Start or stop motion (the setting, reduced motion). The map stays drawn either way. */
  setMoving(on: boolean): void;
}

export function startTopo(cv: HTMLCanvasElement, color0: string, cfg0: Topo, moving0: boolean): TopoMap {
  const ctx = cv.getContext('2d')!;
  const win = window as IdleWin;
  let VW = 0, VH = 0, raf = 0, last = 0, color = color0, cfg: Topo = cfg0, moving = moving0;
  let CELL = 24, SC = 280, padX = 192, padY = 72;
  const VX = 7;                             // px per second across; the block's aspect sets the drift down
  let ox = 0, oy = 0, prog = 0;              // map origin of the current buffer; progress through the block (0..1)
  let cur: HTMLCanvasElement, next: HTMLCanvasElement, nextReady = false, idleId = 0;
  const nav = navigator as Navigator & { deviceMemory?: number };
  const weak = (nav.hardwareConcurrency || 8) <= 4 || (nav.deviceMemory || 8) <= 4;
  let minFrame = weak ? 1000 / 30 - 2 : 0, samples: number[] = [], prev = 0, judging = true, still = false, lastPaint = 0;

  function hash(x: number, y: number): number {
    let h = Math.imul(x | 0, 374761393) ^ Math.imul(y | 0, 668265263);
    h = Math.imul(h ^ (h >>> 13), 1274126177);
    return ((h ^ (h >>> 16)) >>> 0) / 4294967295;
  }
  const fade = (t: number) => t * t * (3 - 2 * t);
  function vn(x: number, y: number): number {
    const xi = Math.floor(x), yi = Math.floor(y), xf = fade(x - xi), yf = fade(y - yi);
    const a = hash(xi, yi), b = hash(xi + 1, yi), c = hash(xi, yi + 1), e = hash(xi + 1, yi + 1);
    return a + (b - a) * xf + (c - a) * yf + (a - b - c + e) * xf * yf;
  }
  // The region shapes the ground: ridged mountains, stepped canyon terraces, islands in a sea,
  // flat city blocks with a street grid, or broad rolling land.
  function field(x: number, y: number): number {
    const o = cfg.oct || [0.62, 0.28, 0.1], px = x, py = y;
    if (cfg.stretch) { x *= cfg.stretch[0]; y *= cfg.stretch[1]; }   // long wind-shaped dunes
    let v = vn(x, y) * o[0] + vn(x * 2.1 + 17, y * 2.1 + 5) * o[1] + vn(x * 4.3 + 3, y * 4.3 + 11) * o[2];
    if (cfg.ridged) { v = 1 - Math.abs(v * 2 - 1); v = v * v * 0.9 + 0.05; }
    if (cfg.valleys) { v = Math.abs(v * 2 - 1); v = Math.sqrt(v) * 0.85 + 0.08; }   // a maze of valleys
    if (cfg.cones || cfg.rings) {
      // Scattered points of interest: volcanic cones with a crater, or soft concentric ripples.
      const size = cfg.cones ? 2.4 : 3, gx = Math.floor(px / size), gy = Math.floor(py / size);
      let add = 0;
      for (let ox2 = -1; ox2 <= 1; ox2++) for (let oy2 = -1; oy2 <= 1; oy2++) {
        const cx = gx + ox2, cy = gy + oy2;
        if (hash(cx * 7 + 3, cy * 13 + 1) < 0.45) continue;
        const qx = (cx + 0.2 + 0.6 * hash(cx, cy + 99)) * size, qy = (cy + 0.2 + 0.6 * hash(cx + 55, cy)) * size, dd = Math.hypot(px - qx, py - qy);
        if (cfg.cones) {
          const R = 0.85;
          if (dd < R) { const kk = 1 - dd / R; add = Math.max(add, kk * kk * 0.55 - (dd < R * 0.18 ? (0.18 - dd / R) * 1.2 : 0)); }
        } else if (dd < 1.4) add += Math.sin(dd * 16) * 0.05 * (1 - dd / 1.4);
      }
      v = cfg.cones ? v * 0.55 + add : v + add;
    }
    if (cfg.terrace) {
      const st = v * cfg.terrace, fl = Math.floor(st), fr = st - fl;
      v = (fl + fade(Math.min(1, Math.max(0, (fr - 0.35) / 0.3)))) / cfg.terrace;
    }
    return v;
  }
  function makeBuffer(): HTMLCanvasElement {
    const b = document.createElement('canvas');
    b.width = VW + padX + CELL; b.height = VH + padY + CELL;
    return b;
  }
  function size(): void {
    CELL = innerWidth < 760 ? 20 : 24;
    SC = (cfg.sc || 280) * (innerWidth < 760 ? 0.75 : 1);
    padX = CELL * 8; padY = CELL * 3;
    VW = innerWidth; VH = innerHeight;
    cv.width = VW; cv.height = VH;
    cur = makeBuffer(); next = makeBuffer(); nextReady = false;
  }
  function trace(buf: HTMLCanvasElement, bx: number, by: number): void {
    const g = buf.getContext('2d')!, W = buf.width, H = buf.height;
    const cols = Math.ceil(W / CELL) + 1, rows = Math.ceil(H / CELL) + 1, v = new Float32Array(cols * rows);
    for (let j = 0; j < rows; j++) for (let i = 0; i < cols; i++) v[j * cols + i] = field((bx + i * CELL) / SC, (by + j * CELL) / SC);
    g.clearRect(0, 0, W, H); g.strokeStyle = color; g.lineJoin = 'round';
    const nL = cfg.levels || 11, lo = cfg.sea != null ? cfg.sea : 0.2, hi = 0.82;
    const contour = (th: number) => {
      g.beginPath();
      for (let j = 0; j < rows - 1; j++) for (let i = 0; i < cols - 1; i++) {
        const a = v[j * cols + i], b = v[j * cols + i + 1], c = v[(j + 1) * cols + i + 1], e = v[(j + 1) * cols + i];
        const idx = (a > th ? 8 : 0) | (b > th ? 4 : 0) | (c > th ? 2 : 0) | (e > th ? 1 : 0);
        if (idx === 0 || idx === 15) continue;
        const x = i * CELL, y = j * CELL;
        const T = [x + CELL * (th - a) / (b - a), y], R = [x + CELL, y + CELL * (th - b) / (c - b)],
          B = [x + CELL * (th - e) / (c - e), y + CELL], Lf = [x, y + CELL * (th - a) / (e - a)];
        const segs = ({ 1: [Lf, B], 2: [B, R], 3: [Lf, R], 4: [T, R], 5: [Lf, T, B, R], 6: [T, B], 7: [Lf, T], 8: [Lf, T], 9: [T, B],
          10: [Lf, B, T, R], 11: [T, R], 12: [Lf, R], 13: [B, R], 14: [Lf, B] } as Record<number, number[][]>)[idx];
        for (let s = 0; s < segs.length; s += 2) { g.moveTo(segs[s][0], segs[s][1]); g.lineTo(segs[s + 1][0], segs[s + 1][1]); }
      }
      g.stroke();
    };
    for (let lv = 0; lv < nL; lv++) {
      const th = lo + (hi - lo) * lv / (nL - 1), major = lv % 3 === 0, coast = cfg.sea != null && lv === 0;
      g.lineWidth = coast ? 2.2 : major ? 1.5 : 0.9; g.globalAlpha = major ? 1 : 0.7;
      contour(th);
    }
    if (cfg.sea != null && cfg.depth) {
      // Depth lines out at sea, dashed and faint, like a nautical chart.
      g.setLineDash([3, 5]); g.lineWidth = 0.8; g.globalAlpha = 0.45;
      for (let dk = 1; dk <= 3; dk++) contour(cfg.sea - dk * 0.06);
      g.setLineDash([]);
    }
    if (cfg.grid) {
      // City blocks: a faint dashed street grid tied to the map, so it scrolls with it.
      g.globalAlpha = 0.55; g.lineWidth = 0.8; g.setLineDash([2, 6]); g.beginPath();
      for (let i = 0; i < cols; i++) if (Math.round((bx + i * CELL) / CELL) % 4 === 0) { g.moveTo(i * CELL, 0); g.lineTo(i * CELL, H); }
      for (let j = 0; j < rows; j++) if (Math.round((by + j * CELL) / CELL) % 4 === 0) { g.moveTo(0, j * CELL); g.lineTo(W, j * CELL); }
      g.stroke(); g.setLineDash([]);
    }
    g.globalAlpha = 1;
  }
  function prepareNext(): void {
    nextReady = false;
    const ric = win.requestIdleCallback ? win.requestIdleCallback.bind(win) : (f: () => void) => window.setTimeout(f, 60);
    const cic = win.cancelIdleCallback ? win.cancelIdleCallback.bind(win) : window.clearTimeout;
    cic(idleId);
    idleId = ric(() => { trace(next, ox + padX, oy + padY); nextReady = true; }, { timeout: 4000 });
  }
  function paint(): void {
    ctx.clearRect(0, 0, VW, VH);
    ctx.drawImage(cur, -prog * padX, -prog * padY);   // fractional position: smooth sub-pixel motion
  }
  function loop(ms: number): void {
    raf = requestAnimationFrame(loop);
    if (prev && judging) {
      samples.push(ms - prev);
      if (samples.length >= 120) {
        samples.sort((a, b) => a - b);
        const med = samples[60];
        samples = []; judging = false;
        if (med > 28) {
          if (!minFrame) { minFrame = 1000 / 30 - 2; judging = true; }
          else { still = true; cancelAnimationFrame(raf); return; }
        }
      }
    }
    prev = ms;
    const dt = last ? Math.min(0.1, (ms - last) / 1000) : 0;
    last = ms;
    prog += VX * dt / padX;                     // both axes cross a block at the same moment
    if (prog >= 1) {
      prog -= 1; ox += padX; oy += padY;
      if (!nextReady) trace(next, ox, oy);          // a slow device fell behind: draw it now
      const t = cur; cur = next; next = t;
      prepareNext();
    }
    if (minFrame && ms - lastPaint < minFrame) return;
    lastPaint = ms; paint();
  }
  const running = () => moving && !still && !document.hidden;
  function run(): void {
    cancelAnimationFrame(raf);
    if (running()) { prepareNext(); prev = 0; last = 0; raf = requestAnimationFrame(loop); }
  }

  size(); trace(cur!, ox, oy); paint(); run();
  document.addEventListener('visibilitychange', () => { if (document.hidden) cancelAnimationFrame(raf); else run(); });
  let rt = 0;
  window.addEventListener('resize', () => {
    window.clearTimeout(rt);
    rt = window.setTimeout(() => { size(); trace(cur, ox, oy); paint(); run(); }, 200);
  });

  return {
    recolor(c: string, k: Topo) {
      color = c || color; cfg = k || cfg;
      SC = (cfg.sc || 280) * (innerWidth < 760 ? 0.75 : 1);
      trace(cur, ox, oy); paint(); if (running()) prepareNext();
    },
    setMoving(on: boolean) {
      if (on === moving) return;
      moving = on;
      if (on) run(); else cancelAnimationFrame(raf);
    },
  };
}
