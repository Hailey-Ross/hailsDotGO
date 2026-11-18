// Appearance page (/appearance): light or dark mode with a time-of-day preview, the 28 habitat
// presets plus a custom mix of their four parts, a weather preview and the motion switch. All
// saved in the browser through the theme engine.
import { HABITATS, PARTS, CustomParts, Habitat, Mode, settings, updateSettings, setSunPreview, tokens, phaseOf, hourNow, preset } from './shared/theme';
import { landscapeSVG } from './shared/hero';
import { feedback } from './shared/feel';

interface ApI18n {
  regions: string; types: string; own: string; motionOn: string; motionOff: string; motionReduced: string;
  parts: Record<keyof CustomParts, [string, string]>;
  names: Record<string, [string, string]>;
}
const I = (window as unknown as { AP_I18N: ApI18n }).AP_I18N;
const reduce = window.matchMedia('(prefers-reduced-motion: reduce)');
const lang = document.documentElement.lang || undefined;
const $ = (id: string) => document.getElementById(id);
const esc = (s: string) => s.replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c] as string));
const nameOf = (id: string) => (I.names[id] || [id, ''])[0];
const moodOf = (id: string) => (I.names[id] || ['', ''])[1];

// The custom habitat as it would look, even before one has been made.
function customHabitat(): Habitat {
  const c = settings().custom || { horizon: 'trailhead', map: 'trailhead', colors: 'trailhead', accent: 't-water' };
  const col = preset(c.colors), acc = preset(c.accent), hor = preset(c.horizon), map = preset(c.map);
  return { id: 'custom', group: 'Custom', seed: hor.id, n: col.n, c: col.c, sky: col.sky, land: col.land, a: acc.a, far: hor.far, layers: hor.layers, topo: map.topo };
}

// Previews show each habitat at the real time of day, like the banner.
function card(t: Habitat, id: string): string {
  const sp = phaseOf(hourNow()), v = tokens(t, sp === 'night', sp);
  const svg = landscapeSVG(t, 1200, 160).replace(/var\(--([a-z0-9-]+)\)/g, (_, k) => v[k] || 'currentColor');
  return '<button type="button" class="fk-hab" data-hab="' + id + '" aria-pressed="' + (settings().habitat === id) + '">' +
    '<span class="pv" style="background:linear-gradient(' + v.sky1 + ',' + v.sky2 + ')"><svg viewBox="0 0 1200 160" preserveAspectRatio="xMinYMax slice" aria-hidden="true">' + svg + '</svg>' +
    '<i class="dot" style="background:' + v.accent + '"></i></span><span class="nm">' + esc(nameOf(id)) + '</span><span class="mood">' + esc(moodOf(id)) + '</span></button>';
}

function renderHabitats(): void {
  const box = $('ap-habitats');
  if (!box) return;
  box.innerHTML =
    '<div class="fk-hab-group">' + esc(I.regions) + '</div>' + HABITATS.filter(t => t.group === 'Regions').map(t => card(t, t.id)).join('') +
    '<div class="fk-hab-group">' + esc(I.types) + '</div>' + HABITATS.filter(t => t.group === 'Types').map(t => card(t, t.id)).join('') +
    '<div class="fk-hab-group">' + esc(I.own) + '</div>' + card(customHabitat(), 'custom');
  const b = $('ap-builder');
  if (!b) return;
  const S = settings();
  b.hidden = S.habitat !== 'custom' || !S.custom;
  if (b.hidden || !S.custom) return;
  const sp = phaseOf(hourNow());
  b.innerHTML = PARTS.map(part => '<div class="fk-part"><div><b>' + esc(I.parts[part][0]) + '</b><small>' + esc(I.parts[part][1]) + '</small></div><div class="fk-chips">' +
    HABITATS.map(h => {
      const tv = tokens(h, sp === 'night', sp);
      const sw = part === 'accent' ? tv.accent : part === 'colors' ? 'linear-gradient(135deg,' + tv.sky1 + ' 50%,' + tv.land2 + ' 50%)' : tv.land2;
      return '<button type="button" data-cpart="' + part + '" data-cval="' + h.id + '" aria-pressed="' + (S.custom![part] === h.id) + '"><i style="background:' + sw + '"></i>' + esc(nameOf(h.id)) + '</button>';
    }).join('') + '</div></div>').join('');
}

function syncControls(): void {
  const S = settings();
  document.querySelectorAll<HTMLElement>('[data-mode]').forEach(b => b.setAttribute('aria-pressed', String(b.dataset.mode === S.mode)));
  document.querySelectorAll<HTMLElement>('[data-wx]').forEach(b => b.setAttribute('aria-pressed', String(b.dataset.wx === S.weather)));
  const row = $('ap-sun-row');
  if (row) row.hidden = S.mode !== 'sun';
  const m = $('ap-motion') as HTMLInputElement | null;
  if (m) m.checked = S.motion;
  const note = $('ap-motion-note');
  if (note) note.textContent = !S.motion ? I.motionOff : reduce.matches ? I.motionReduced : I.motionOn;
  const snd = $('ap-sound') as HTMLInputElement | null, vol = $('ap-vol') as HTMLInputElement | null, hap = $('ap-haptics') as HTMLInputElement | null;
  if (snd) snd.checked = S.sound;
  if (vol) { vol.value = String(S.volume); vol.disabled = !S.sound; }
  const volOut = $('ap-vol-out');
  if (volOut) volOut.textContent = S.volume + '%';
  if (hap) hap.checked = S.haptics;
  const sun = $('ap-sun') as HTMLInputElement | null;
  if (sun && document.activeElement !== sun) sun.value = String(hourNow());
  sunLabel();
}

function sunLabel(): void {
  const sun = $('ap-sun') as HTMLInputElement | null, out = $('ap-sun-out');
  if (!sun || !out) return;
  const h = +sun.value, d = new Date();
  d.setHours(Math.floor(h), Math.round((h - Math.floor(h)) * 60));
  try { out.textContent = d.toLocaleTimeString(lang, { hour: 'numeric', minute: '2-digit' }); } catch { out.textContent = d.toLocaleTimeString(); }
}

document.addEventListener('click', e => {
  const t = e.target as Element;
  // The Appearance sheet has its own mode buttons; only handle the ones on this page.
  if (t.closest('#fk-sheet')) return;
  const mode = t.closest('[data-mode]') as HTMLElement | null;
  if (mode) {
    const m = mode.dataset.mode as Mode;
    if (m !== 'sun') setSunPreview(null);
    updateSettings({ mode: m }); syncControls(); return;
  }
  const hab = t.closest('[data-hab]') as HTMLElement | null;
  if (hab) {
    const id = hab.dataset.hab!;
    if (id === 'custom') {
      const S = settings();
      // Start a custom mix from the habitat in use, so nothing jumps.
      const base = S.habitat === 'custom' ? 'trailhead' : S.habitat;
      updateSettings({ habitat: 'custom', custom: S.custom || { horizon: base, map: base, colors: base, accent: base } });
    } else updateSettings({ habitat: id });
    renderHabitats(); return;
  }
  const part = t.closest('[data-cpart]') as HTMLElement | null;
  if (part) {
    const S = settings();
    if (!S.custom) return;
    updateSettings({ custom: { ...S.custom, [part.dataset.cpart as keyof CustomParts]: part.dataset.cval! } });
    renderHabitats(); return;
  }
  const wx = t.closest('[data-wx]') as HTMLElement | null;
  if (wx) { updateSettings({ weather: wx.dataset.wx! }); syncControls(); }
});
($('ap-sound') as HTMLInputElement | null)?.addEventListener('change', e => { updateSettings({ sound: (e.target as HTMLInputElement).checked }); syncControls(); });
($('ap-haptics') as HTMLInputElement | null)?.addEventListener('change', e => { updateSettings({ haptics: (e.target as HTMLInputElement).checked }); syncControls(); feedback('tap'); });
($('ap-vol') as HTMLInputElement | null)?.addEventListener('input', e => {
  updateSettings({ volume: +(e.target as HTMLInputElement).value });
  const out = $('ap-vol-out');
  if (out) out.textContent = settings().volume + '%';
});
// A sample tone when the slider is let go, so the new level can be heard.
($('ap-vol') as HTMLInputElement | null)?.addEventListener('change', () => feedback('tap'));
($('ap-motion') as HTMLInputElement | null)?.addEventListener('change', e => { updateSettings({ motion: (e.target as HTMLInputElement).checked }); syncControls(); });
($('ap-sun') as HTMLInputElement | null)?.addEventListener('input', e => { setSunPreview(+(e.target as HTMLInputElement).value); sunLabel(); });
window.addEventListener('pagehide', () => setSunPreview(null));

renderHabitats();
syncControls();
