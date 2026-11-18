// Weather layer behind the page: a handful of elements moved by the compositor (CSS animations
// in fieldkit.css), matching the trainer's live in-game weather. Dimmed away from the launcher.

export type WeatherKind = 'clear' | 'partly' | 'cloudy' | 'rain' | 'windy' | 'snow' | 'fog' | '';

// /api/weather answers with the in-game weather name; map it to how the sky looks.
const FROM_GAME: Record<string, WeatherKind> = {
  'Clear': 'clear', 'Sunny': 'clear', 'Partly Cloudy': 'partly', 'Overcast': 'cloudy', 'Cloudy': 'cloudy',
  'Rainy': 'rain', 'Rain': 'rain', 'Windy': 'windy', 'Snow': 'snow', 'Fog': 'fog',
};

export function kindFromGame(name: string): WeatherKind {
  return FROM_GAME[name] || '';
}

const LEAF = '<svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M4 20C4 11 9 5 20 4c-1 10-6 16-16 16z"/></svg>';

export function renderWeather(box: HTMLElement, k: WeatherKind, motion: boolean): void {
  const phone = innerWidth < 760;
  const rnd = (a: number, b: number) => a + Math.random() * (b - a);
  let html = '';
  if (k === 'rain') for (let i = 0; i < (phone ? 14 : 26); i++) html += '<i class="wx drop" style="--x:' + rnd(0, 120).toFixed(0) + 'vw;animation-duration:' + rnd(0.9, 1.5).toFixed(2) + 's;animation-delay:-' + rnd(0, 1.5).toFixed(2) + 's"></i>';
  if (k === 'snow') for (let i = 0; i < (phone ? 14 : 24); i++) html += '<i class="wx flake" style="--x:' + rnd(0, 100).toFixed(0) + 'vw;width:' + rnd(3, 7).toFixed(1) + 'px;height:' + rnd(3, 7).toFixed(1) + 'px;animation-duration:' + rnd(11, 19).toFixed(1) + 's;animation-delay:-' + rnd(0, 19).toFixed(1) + 's"></i>';
  if (k === 'windy') for (let i = 0; i < (phone ? 4 : 6); i++) html += '<i class="wx leaf" style="--y:' + rnd(5, 80).toFixed(0) + 'vh;animation-duration:' + rnd(13, 22).toFixed(1) + 's;animation-delay:-' + rnd(0, 22).toFixed(1) + 's">' + LEAF + '</i>';
  if (k === 'fog') for (let i = 0; i < 3; i++) html += '<i class="wx fog" style="--y:' + (10 + i * 28) + 'vh;animation-duration:' + (40 + i * 12) + 's;animation-delay:-' + (i * 9) + 's"></i>';
  if (k === 'clear' || k === 'partly') html += '<i class="wx sun"></i>';
  if (k === 'cloudy' || k === 'partly') for (let i = 0; i < (k === 'cloudy' ? 4 : 2); i++) html += '<i class="wx cloud" style="--y:' + rnd(6, 30).toFixed(0) + 'vh;width:' + rnd(120, 210).toFixed(0) + 'px;animation-duration:' + rnd(90, 150).toFixed(0) + 's;animation-delay:-' + rnd(0, 150).toFixed(0) + 's"></i>';
  box.innerHTML = motion ? html : '';
}

export interface LiveWeather { name: string; kind: WeatherKind; boosts: string[]; }

const CACHE_KEY = 'flexdex-weather';
const CACHE_MS = 15 * 60_000;

/** The signed-in trainer's in-game weather, cached for 15 minutes per tab session. */
export async function fetchLiveWeather(): Promise<LiveWeather | null> {
  try {
    const c = JSON.parse(sessionStorage.getItem(CACHE_KEY) || 'null');
    if (c && Date.now() - c.at < CACHE_MS) return c.w;
  } catch { /* no cache */ }
  try {
    const r = await fetch('/api/weather', { credentials: 'same-origin' });
    if (!r.ok) return null;
    const j = await r.json();
    const w: LiveWeather | null = j && j.pogo_weather
      ? { name: String(j.pogo_weather), kind: kindFromGame(String(j.pogo_weather)), boosts: (j.boosted_types || []).map(String) }
      : null;
    try { sessionStorage.setItem(CACHE_KEY, JSON.stringify({ at: Date.now(), w })); } catch { /* not cached */ }
    return w;
  } catch {
    return null;
  }
}
