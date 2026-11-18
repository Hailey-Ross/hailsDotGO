// The "Logged" stamp: a rectangular rubber date stamp, the kind libraries and passports use:
// double border, a small header, bold LOGGED, the date. It looks inked because ink is one color,
// sits slightly uneven in soft patches and has slightly rough edges (an SVG filter: low frequency
// turbulence as alpha for density, fine displacement for the edges); on light cards it also
// blends into the surface like real ink (mix-blend-mode in fieldkit.css).

let stampN = 0;

/** Stamp SVG markup. `on` shows it already stamped (no thump). */
export function stampSVG(on: boolean, header: string, word: string, lang?: string): string {
  const id = 'fk-ink' + (++stampN);
  let date: string;
  try { date = new Date().toLocaleDateString(lang, { month: 'short', day: 'numeric', year: 'numeric' }); }
  catch { date = new Date().toLocaleDateString(); }
  date = date.toUpperCase().replace(',', '');
  const esc = (s: string) => s.replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c] as string));
  return '<svg class="fk-stamp' + (on ? ' on' : '') + '" viewBox="0 0 120 62" aria-hidden="true"><defs>' +
    '<filter id="' + id + '" x="-6%" y="-12%" width="112%" height="124%" color-interpolation-filters="sRGB">' +
      '<feTurbulence type="fractalNoise" baseFrequency="0.035 0.07" numOctaves="2" seed="' + (stampN % 9 + 1) + '" result="blot"/>' +
      '<feColorMatrix in="blot" type="matrix" values="0 0 0 0 0  0 0 0 0 0  0 0 0 0 0  0 0 0 0.9 0.5" result="density"/>' +
      '<feTurbulence type="fractalNoise" baseFrequency="1.2" numOctaves="1" seed="' + (stampN % 5 + 3) + '" result="grit"/>' +
      '<feDisplacementMap in="SourceGraphic" in2="grit" scale="1.2" xChannelSelector="R" yChannelSelector="G" result="edge"/>' +
      '<feComposite in="edge" in2="density" operator="in"/></filter></defs>' +
    '<g filter="url(#' + id + ')" fill="currentColor">' +
      '<rect x="3" y="3" width="114" height="56" rx="7" fill="none" stroke="currentColor" stroke-width="3.6"/>' +
      '<rect x="8.5" y="8.5" width="103" height="45" rx="4" fill="none" stroke="currentColor" stroke-width="1.2"/>' +
      '<text x="60" y="18.5" text-anchor="middle" font-family="JetBrains Mono, monospace" font-size="5.6" font-weight="700" letter-spacing="1.7">' + esc(header) + '</text>' +
      '<text x="60" y="38" text-anchor="middle" font-family="Bricolage Grotesque, Segoe UI, sans-serif" font-size="21" font-weight="800" letter-spacing="1.6">' + esc(word) + '</text>' +
      '<text x="60" y="49" text-anchor="middle" font-family="JetBrains Mono, monospace" font-size="7.4" font-weight="700" letter-spacing="1.1">' + esc(date) + '</text>' +
    '</g></svg>';
}
