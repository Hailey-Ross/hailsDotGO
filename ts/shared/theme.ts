// FlexDex Field Kit theme engine.
//
// A theme is a habitat: a region (Trailhead, Kanto ... Paldea) or a Pokemon type. Every color
// the interface uses is generated in OKLCH from a handful of numbers per preset, so adding or
// tuning a theme never means hand-picking dozens of colors. A custom theme mixes four parts
// taken from any presets: the horizon (banner landscape), the map (background terrain), the
// colors (sky, land and interface tints) and the highlight (accent).
//
// Settings live in localStorage for everyone (key below). The early boot script in
// templates/base.html reads the same key to pick light or dark before the first paint, so keep
// the key and the `mode` values in sync with it.

export const SETTINGS_KEY = 'flexdex-fieldkit';

export type Mode = 'system' | 'light' | 'dark' | 'sun';
export type Phase = 'day' | 'dawn' | 'dusk' | 'night';
export type Layer =
  | 'hills' | 'pines' | 'trees' | 'meadow' | 'peaks' | 'snowpeaks' | 'volcano' | 'sea'
  | 'island' | 'city' | 'town' | 'mesa' | 'dunes';

/** How a habitat's ground looks on the background contour map. */
export interface Topo {
  sc: number;                  // feature scale in px
  levels: number;              // contour levels
  oct?: number[];              // octave weights
  ridged?: boolean;
  terrace?: number;
  sea?: number;                // sea level 0..1 (coastline and depth lines)
  depth?: boolean;
  grid?: boolean;              // street grid
  stretch?: [number, number];  // dunes
  cones?: boolean;             // volcanoes
  rings?: boolean;             // ripples
  valleys?: boolean;
}

export interface Habitat {
  id: string;
  group: 'Regions' | 'Types' | 'Custom';
  n: number;                   // base hue
  c: number;                   // base tint chroma
  a: number;                   // highlight hue
  sky: [number, number];       // [top hue, bottom hue]
  land: [number, number];      // [hue, chroma]
  far: Layer;                  // farthest horizon layer
  layers: Layer[];             // horizon, back to front
  topo: Topo;
  seed?: string;               // custom themes draw the horizon of this preset
}

// Display names and moods are translated strings (theme.<id>.name / theme.<id>.mood) added
// with the Appearance page; only the numbers live here.
const REGIONS: Habitat[] = [
  { id: 'trailhead', group: 'Regions', n: 150, c: 0.012, a: 150, sky: [225, 150], land: [150, 0.09], far: 'hills', layers: ['hills', 'pines', 'hills'], topo: { sc: 300, levels: 10 } },
  { id: 'kanto', group: 'Regions', n: 140, c: 0.01, a: 25, sky: [235, 200], land: [130, 0.09], far: 'hills', layers: ['hills', 'hills', 'meadow'], topo: { sc: 360, oct: [0.72, 0.22, 0.06], levels: 8 } },
  { id: 'johto', group: 'Regions', n: 60, c: 0.012, a: 52, sky: [60, 40], land: [45, 0.1], far: 'peaks', layers: ['hills', 'trees', 'hills'], topo: { sc: 250, oct: [0.55, 0.3, 0.15], levels: 11 } },
  { id: 'hoenn', group: 'Regions', n: 200, c: 0.014, a: 200, sky: [205, 180], land: [175, 0.09], far: 'hills', layers: ['volcano', 'hills', 'sea'], topo: { sc: 240, sea: 0.46, levels: 9 } },
  { id: 'sinnoh', group: 'Regions', n: 240, c: 0.014, a: 250, sky: [250, 230], land: [235, 0.05], far: 'snowpeaks', layers: ['snowpeaks', 'peaks', 'hills'], topo: { sc: 230, ridged: true, levels: 14 } },
  { id: 'unova', group: 'Regions', n: 250, c: 0.008, a: 290, sky: [255, 30], land: [255, 0.04], far: 'city', layers: ['city', 'city', 'sea'], topo: { sc: 460, oct: [0.85, 0.12, 0.03], levels: 6, grid: true } },
  { id: 'kalos', group: 'Regions', n: 310, c: 0.012, a: 340, sky: [300, 340], land: [300, 0.07], far: 'hills', layers: ['hills', 'trees', 'meadow'], topo: { sc: 300, oct: [0.6, 0.3, 0.1], levels: 10 } },
  { id: 'alola', group: 'Regions', n: 190, c: 0.014, a: 32, sky: [40, 20], land: [165, 0.1], far: 'hills', layers: ['island', 'sea', 'sea'], topo: { sc: 210, sea: 0.5, levels: 8 } },
  { id: 'galar', group: 'Regions', n: 120, c: 0.01, a: 305, sky: [230, 210], land: [115, 0.07], far: 'hills', layers: ['hills', 'town', 'meadow'], topo: { sc: 400, oct: [0.72, 0.22, 0.06], levels: 8 } },
  { id: 'paldea', group: 'Regions', n: 50, c: 0.012, a: 78, sky: [60, 30], land: [45, 0.1], far: 'mesa', layers: ['mesa', 'mesa', 'dunes'], topo: { sc: 280, terrace: 5, levels: 13 } },
];

// Type themes: the horizon and the map show the kind of place each type lives in.
// [type, hue (base and highlight), sky hues, land [hue, chroma], far layer, layers, map]
type TypeRow = [string, number, [number, number], [number, number], Layer, Layer[], Topo];
const TYPE_ROWS: TypeRow[] = [
  ['normal', 85, [230, 90], [95, 0.06], 'hills', ['town', 'hills', 'meadow'], { sc: 340, oct: [0.72, 0.22, 0.06], levels: 8 }],
  ['fire', 40, [35, 60], [40, 0.08], 'peaks', ['volcano', 'hills', 'dunes'], { sc: 260, cones: true, levels: 12 }],
  ['water', 250, [220, 195], [190, 0.09], 'island', ['sea', 'island', 'sea'], { sc: 230, sea: 0.56, depth: true, levels: 7 }],
  ['grass', 140, [200, 150], [140, 0.1], 'hills', ['hills', 'trees', 'meadow'], { sc: 200, oct: [0.5, 0.32, 0.18], levels: 10 }],
  ['electric', 95, [240, 80], [100, 0.07], 'city', ['city', 'hills', 'meadow'], { sc: 420, oct: [0.85, 0.12, 0.03], levels: 6, grid: true }],
  ['ice', 200, [220, 200], [210, 0.04], 'snowpeaks', ['snowpeaks', 'hills', 'sea'], { sc: 230, ridged: true, sea: 0.32, depth: true, levels: 12 }],
  ['fighting', 25, [30, 220], [40, 0.06], 'peaks', ['peaks', 'pines', 'meadow'], { sc: 220, ridged: true, levels: 12 }],
  ['poison', 320, [300, 150], [130, 0.06], 'hills', ['trees', 'meadow', 'sea'], { sc: 160, oct: [0.45, 0.35, 0.2], sea: 0.5, depth: true, levels: 7 }],
  ['ground', 80, [70, 50], [75, 0.09], 'mesa', ['dunes', 'dunes', 'dunes'], { sc: 200, stretch: [0.4, 2.2], levels: 11 }],
  ['flying', 285, [250, 210], [240, 0.04], 'snowpeaks', ['peaks', 'hills', 'meadow'], { sc: 420, ridged: true, levels: 7 }],
  ['psychic', 0, [320, 350], [320, 0.06], 'hills', ['hills', 'trees', 'meadow'], { sc: 300, rings: true, levels: 10 }],
  ['bug', 120, [190, 120], [125, 0.1], 'hills', ['trees', 'trees', 'meadow'], { sc: 150, oct: [0.5, 0.3, 0.2], levels: 9 }],
  ['rock', 90, [60, 40], [70, 0.06], 'mesa', ['mesa', 'peaks', 'dunes'], { sc: 260, terrace: 7, levels: 14 }],
  ['ghost', 300, [290, 260], [280, 0.05], 'hills', ['town', 'pines', 'meadow'], { sc: 260, sea: 0.47, depth: true, oct: [0.7, 0.2, 0.1], levels: 6 }],
  ['dragon', 280, [260, 210], [220, 0.06], 'snowpeaks', ['peaks', 'hills', 'sea'], { sc: 240, ridged: true, sea: 0.4, depth: true, levels: 13 }],
  ['dark', 50, [270, 300], [150, 0.05], 'peaks', ['pines', 'pines', 'hills'], { sc: 220, valleys: true, levels: 11 }],
  ['steel', 265, [230, 250], [250, 0.03], 'city', ['town', 'mesa', 'meadow'], { sc: 300, terrace: 6, grid: true, levels: 10 }],
  ['fairy', 345, [340, 300], [340, 0.07], 'hills', ['hills', 'trees', 'meadow'], { sc: 280, rings: true, oct: [0.7, 0.22, 0.08], levels: 9 }],
];

const TYPES: Habitat[] = TYPE_ROWS.map(t => ({
  id: 't-' + t[0], group: 'Types', n: t[1], c: 0.012, a: t[1], sky: t[2], land: t[3], far: t[4], layers: t[5], topo: t[6],
}));

/** All 28 presets: 10 regions then 18 types. Trailhead is the default. */
export const HABITATS: Habitat[] = REGIONS.concat(TYPES);

/** The four parts a custom theme mixes, each taken from a preset id. */
export interface CustomParts { horizon: string; map: string; colors: string; accent: string; }
export const PARTS: (keyof CustomParts)[] = ['horizon', 'map', 'colors', 'accent'];

export interface Settings {
  habitat: string;             // preset id or 'custom'
  custom: CustomParts | null;
  mode: Mode;
  weather: string;             // 'live' or a fixed weather id
  motion: boolean;
  sound: boolean;
  volume: number;              // 0..100
  haptics: boolean;
}

const DEFAULTS: Settings = {
  habitat: 'trailhead', custom: null, mode: 'system', weather: 'live',
  motion: true, sound: true, volume: 60, haptics: true,
};

let S: Settings = loadSettings();
let sunPreview: number | null = null;   // Appearance page slider; never saved

function loadSettings(): Settings {
  const s: Settings = { ...DEFAULTS };
  try {
    const saved = JSON.parse(localStorage.getItem(SETTINGS_KEY) || 'null');
    if (saved && typeof saved === 'object') {
      for (const k of Object.keys(DEFAULTS) as (keyof Settings)[]) {
        if (k in saved) (s as any)[k] = saved[k];
      }
    }
  } catch { /* private window or blocked storage: defaults */ }
  return s;
}

export function settings(): Readonly<Settings> { return S; }

/** Change settings, save them and repaint the theme. */
export function updateSettings(patch: Partial<Settings>): void {
  S = { ...S, ...patch };
  try { localStorage.setItem(SETTINGS_KEY, JSON.stringify(S)); } catch { /* not saved */ }
  applyTheme();
}

/** Preview a time of day on the Appearance page (null returns to the real clock). */
export function setSunPreview(hour: number | null): void {
  sunPreview = hour;
  applyTheme();
}

export function hourNow(): number {
  if (sunPreview != null) return sunPreview;
  const d = new Date();
  return d.getHours() + d.getMinutes() / 60;
}

export function phaseOf(h: number): Phase {
  return h < 5 || h >= 21 ? 'night' : h < 8 ? 'dawn' : h < 17 ? 'day' : 'dusk';
}

// Guarded: the check scripts load modules that import this one under Node, without a window.
const mq: MediaQueryList | null = typeof window !== 'undefined' && window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null;

/** Light or dark interface, and the phase its colors are drawn for. */
export function look(): { dark: boolean; phase: Phase } {
  // Pages not restyled for the Field Kit yet (base.html marks them data-page="legacy") are drawn for
  // dark only: their old light-on-glass text would be unreadable on a light background.
  if (document.documentElement.getAttribute('data-page') === 'legacy') return { dark: true, phase: 'night' };
  if (S.mode === 'sun') {
    const p = phaseOf(hourNow());
    return { dark: p === 'night', phase: p };
  }
  const dark = S.mode === 'dark' ? true : S.mode === 'light' ? false : !!mq?.matches;
  return { dark, phase: dark ? 'night' : 'day' };
}

export function preset(id: string): Habitat {
  return HABITATS.find(h => h.id === id) || HABITATS[0];
}

/** The active habitat, resolving a custom mix into one habitat. */
export function habitat(): Habitat {
  if (S.habitat !== 'custom' || !S.custom) return preset(S.habitat);
  const c = S.custom;
  const col = preset(c.colors), acc = preset(c.accent), hor = preset(c.horizon), map = preset(c.map);
  return {
    id: 'custom', group: 'Custom', seed: hor.id, n: col.n, c: col.c, sky: col.sky, land: col.land,
    a: acc.a, far: hor.far, layers: hor.layers, topo: map.topo,
  };
}

function ok(l: number, c: number, h: number, a?: number): string {
  return 'oklch(' + l.toFixed(3) + ' ' + c.toFixed(3) + ' ' + h + (a != null ? ' / ' + a : '') + ')';
}

/** Every color token for a habitat, light or dark, at a time of day. */
export function tokens(t: Habitat, dark: boolean, phase: Phase): Record<string, string> {
  const n = t.n, c = t.c, a = t.a, lh = t.land[0], lc = t.land[1];
  let s0 = t.sky[0], s1 = t.sky[1], sc = 0.05;
  if (phase === 'dawn') { s0 = 25; s1 = 330; sc = 0.07; }
  if (phase === 'dusk') { s0 = 300; s1 = 35; sc = 0.08; }
  if (dark) return {
    bg: ok(0.19, c, n), surface: ok(0.235, c * 1.2, n), 'surface-2': ok(0.275, c * 1.3, n),
    line: ok(0.92, c, n, 0.1), 'line-hi': ok(0.92, c, n, 0.2),
    fg: ok(0.95, c * 0.5, n), 'fg-2': ok(0.8, c, n), 'fg-3': ok(0.66, c * 1.2, n),
    accent: ok(0.75, 0.15, a), 'accent-ink': ok(0.2, 0.04, a), 'accent-soft': ok(0.75, 0.15, a, 0.16),
    contour: ok(0.82, lc * 0.6, lh, 0.11),
    // Night skies are deep night blue for every habitat.
    sky1: ok(0.27, 0.055, 258), sky2: ok(0.23, 0.035, (s1 + 245 * 3) / 4),
    land0: ok(0.36, lc * 0.3, lh), land1: ok(0.31, lc * 0.4, lh), land2: ok(0.265, lc * 0.45, lh), land3: ok(0.225, lc * 0.45, lh),
    haze: ok(0.45, sc, s1, 0.35), star: ok(0.97, 0.02, 90, 0.9), moon: ok(0.93, 0.03, 95), 'sun-core': ok(0.85, 0.12, 70),
    cloud: ok(0.62, 0.02, n, 0.16), snow: ok(0.86, 0.01, 240),
    sunc: phase === 'night' ? 'oklch(0.92 0.02 90)' : ok(0.8, 0.12, 70),
    'wx-c': ok(0.85, 0.03, 230, 0.5), 'wx-leaf': ok(0.7, 0.12, 60, 0.7), 'wx-fog': ok(0.6, 0.01, n, 0.18),
    'wx-sun': ok(0.75, 0.1, 75, 0.14), 'wx-cloud': ok(0.4, 0.01, n, 0.35),
    scrim: 'rgba(0,0,0,0.45)', good: '#5bd39a', gold: '#f2c14e',
  };
  // Warm highlight hues get a darker accent in light mode so white text stays readable.
  const warm = a >= 45 && a <= 110;
  return {
    bg: ok(0.97, c * 0.8, n), surface: ok(0.995, c * 0.3, n), 'surface-2': ok(0.945, c * 1.1, n),
    line: ok(0.3, c * 2, n, 0.13), 'line-hi': ok(0.3, c * 2, n, 0.24),
    fg: ok(0.24, c * 2, n), 'fg-2': ok(0.42, c * 2, n), 'fg-3': ok(0.52, c * 2, n),
    accent: ok(warm ? 0.6 : 0.58, warm ? 0.13 : 0.15, a), 'accent-ink': ok(0.99, 0.01, a), 'accent-soft': ok(0.6, 0.15, a, 0.13),
    contour: ok(0.5, lc * 0.7, lh, 0.2),
    sky1: ok(phase === 'day' ? 0.88 : 0.86, sc, s0), sky2: ok(0.95, sc * 0.5, s1),
    land0: ok(0.9, lc * 0.3, lh), land1: ok(0.83, lc * 0.5, lh), land2: ok(0.73, lc * 0.8, lh), land3: ok(0.6, lc, lh),
    haze: ok(0.97, 0.04, s1, 0.75), star: 'transparent', moon: ok(0.95, 0.02, 95),
    'sun-core': phase === 'day' ? ok(0.94, 0.15, 92) : ok(0.85, 0.16, 50),
    cloud: ok(1, 0, 0, 0.88), snow: ok(0.995, 0, 0),
    sunc: phase === 'day' ? ok(0.9, 0.13, 85) : ok(0.82, 0.14, 45),
    'wx-c': ok(0.55, 0.05, 240, 0.45), 'wx-leaf': ok(0.62, 0.13, 55, 0.75), 'wx-fog': ok(0.99, 0, 0, 0.75),
    'wx-sun': ok(0.95, 0.12, 85, 0.5), 'wx-cloud': ok(0.99, 0, 0, 0.8),
    scrim: 'rgba(20,30,25,0.28)', good: '#1f8a5b', gold: '#a87700',
  };
}

// Tokens the banner scene owns: they always follow the real clock, whatever the mode.
const SCENE_KEYS = ['sky1', 'sky2', 'haze', 'star', 'moon', 'sun-core', 'cloud', 'snow', 'land0', 'land1', 'land2', 'land3'];

/** Event fired on document after every repaint, for canvases that cache colors. */
export const THEME_EVENT = 'fieldkit:theme';

/** Write the active theme's tokens onto :root and tell the canvases. */
export function applyTheme(): void {
  const root = document.documentElement;
  const h = habitat();
  const L = look();
  const v = tokens(h, L.dark, L.phase);
  const sp = phaseOf(hourNow());
  const sv = tokens(h, sp === 'night', sp);
  for (const k of SCENE_KEYS) v[k] = sv[k];
  v['hero-fg'] = sp === 'night' ? 'oklch(0.96 0.01 250)' : 'oklch(0.24 0.025 ' + h.n + ')';
  v['hero-fg2'] = sp === 'night' ? 'oklch(0.84 0.02 250)' : 'oklch(0.4 0.025 ' + h.n + ')';
  for (const key in v) root.style.setProperty('--' + key, v[key]);
  root.style.setProperty('color-scheme', L.dark ? 'dark' : 'light');
  root.dataset.ink = L.dark ? 'dark' : 'light';
  root.dataset.phase = sp;
  root.removeAttribute('data-boot');
  document.dispatchEvent(new CustomEvent(THEME_EVENT, { detail: { habitat: h, dark: L.dark, phase: L.phase, scenePhase: sp } }));
}

let started = false;

/** Apply the theme now and keep it current: system mode changes, the clock, other tabs. */
export function startTheme(): void {
  if (started) return;
  started = true;
  applyTheme();
  // Let the first paint settle with the real colors before fades come back on.
  requestAnimationFrame(() => requestAnimationFrame(() => document.documentElement.classList.remove('preload')));
  if (mq?.addEventListener) mq.addEventListener('change', applyTheme);
  // The scene and Follow the sun change with the hour; a minute tick is plenty.
  let lastPhase = phaseOf(hourNow());
  window.setInterval(() => {
    const p = phaseOf(hourNow());
    if (p !== lastPhase) { lastPhase = p; applyTheme(); }
  }, 60_000);
  window.addEventListener('storage', e => {
    if (e.key === SETTINGS_KEY) { S = loadSettings(); applyTheme(); }
  });
}
