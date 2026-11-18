// Stickers: a Pokemon on a round sticker (white rim, slight tilt, type colors), the way the Field
// Kit shows Pokemon everywhere. Shared by the launcher and every restyled page.
import { TYPE_COLORS } from './typecolors';

const SPARK = '<svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M12 2l1.9 6.1L20 10l-6.1 1.9L12 18l-1.9-6.1L4 10l6.1-1.9z"/></svg>';
const esc = (s: string) => s.replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c] as string));

/** A stable small tilt per name, so a sticker always sits the same way. */
export function tilt(id: string): string {
  let s = 0;
  for (let i = 0; i < id.length; i++) s = (s * 31 + id.charCodeAt(i)) | 0;
  return ((Math.abs(s) % 9) - 4) + 'deg';
}

/** Sticker markup. Call trimStickers() on the container after inserting it. */
export function sticker(name: string, img: string | undefined, types: string[], shiny: boolean, size?: number): string {
  const t1 = TYPE_COLORS[types[0]] || '#9099A1', t2 = TYPE_COLORS[types[1] || types[0]] || t1;
  return '<span class="fk-sticker" style="--t1:' + t1 + ';--t2:' + t2 + ';--tilt:' + tilt(name) + (size ? ';--s:' + size + 'px' : '') + '" aria-hidden="true">' +
    (img ? '<img src="' + esc(img) + '" alt="" decoding="async" crossorigin="anonymous" data-trim>' : esc(name.slice(0, 2))) +
    (shiny ? '<span class="fk-spark">' + SPARK + '</span>' : '') + '</span>';
}

// Icon sets pad each Pokemon to its real-world size, so a Larvitar is a speck in the middle of
// its image. Crop every sprite to its visible pixels, so each one fills its sticker.
const trimmed = new Map<string, string>();

function trimOne(img: HTMLImageElement): void {
  const src = img.currentSrc || img.src;
  const done = trimmed.get(src);
  if (done) { img.src = done; img.classList.add('trimmed'); return; }
  try {
    const w = img.naturalWidth, h = img.naturalHeight;
    if (!w || !h) return;
    const c = document.createElement('canvas');
    c.width = w; c.height = h;
    const g = c.getContext('2d', { willReadFrequently: true });
    if (!g) return;
    g.drawImage(img, 0, 0);
    const px = g.getImageData(0, 0, w, h).data;
    let x0 = w, y0 = h, x1 = -1, y1 = -1;
    for (let y = 0; y < h; y++) for (let x = 0; x < w; x++) {
      if (px[(y * w + x) * 4 + 3] > 24) { if (x < x0) x0 = x; if (x > x1) x1 = x; if (y < y0) y0 = y; if (y > y1) y1 = y; }
    }
    if (x1 < 0) return;
    // Square crop around the visible art with a little breathing room.
    const bw = x1 - x0 + 1, bh = y1 - y0 + 1, side = Math.ceil(Math.max(bw, bh) * 1.08);
    const out = document.createElement('canvas');
    out.width = side; out.height = side;
    out.getContext('2d')!.drawImage(c, x0, y0, bw, bh, Math.floor((side - bw) / 2), Math.floor((side - bh) / 2), bw, bh);
    const url = out.toDataURL('image/png');
    trimmed.set(src, url);
    img.src = url;
    img.classList.add('trimmed');
  } catch {
    // A host without CORS taints the canvas: keep the untrimmed image.
  }
}

/** Trim every sticker image inside root once it has loaded. */
export function trimStickers(root: ParentNode): void {
  root.querySelectorAll<HTMLImageElement>('.fk-sticker img[data-trim]').forEach(img => {
    img.removeAttribute('data-trim');
    img.addEventListener('error', () => {
      // No CORS on this host: load it again without asking for CORS, untrimmed.
      if (img.crossOrigin) { img.removeAttribute('crossorigin'); img.src = img.src; }
    }, { once: true });
    if (img.complete && img.naturalWidth) trimOne(img);
    else img.addEventListener('load', () => trimOne(img), { once: true });
  });
}
