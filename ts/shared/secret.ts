// Friend codes are hidden behind stars so a screen recording or a glance over the shoulder does not
// catch them. The real code shows while hovered (devices with a mouse) and toggles on tap (phones);
// the toggle itself lives in shell.ts. Copy buttons always copy the real code.
//
// Templates build the same markup with the maskCode template func (internal/handlers/handlers.go).

/** maskCode replaces every character but spaces with a star, so the mask is as wide as the code. */
export function maskCode(code: string): string {
  return code.replace(/[^ ]/g, '*');
}

/** secretCode is a friend code as a hidden-until-shown button. extra adds classes to it. */
export function secretCode(code: string, extra = ''): HTMLButtonElement {
  const b = document.createElement('button');
  b.type = 'button';
  b.className = ('fk-secret ' + extra).trim();
  const mask = document.createElement('span');
  mask.className = 'fk-secret-mask';
  mask.setAttribute('aria-hidden', 'true');
  mask.textContent = maskCode(code);
  const val = document.createElement('span');
  val.className = 'fk-secret-val';
  val.textContent = code;
  b.append(mask, val);
  return b;
}
