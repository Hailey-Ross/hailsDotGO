// The shell's page API (set up by ts/shell.ts, which loads before every page script).
interface FKApi { openSheet(content: Node, name?: string): void; closeSheet(): void; }

/** Open content in the shell's sheet (slides up on phones, in from the right on wider screens). */
export function openSheet(content: Node, name = 'page'): void {
  const fk = (window as unknown as { FK?: FKApi }).FK;
  if (fk) fk.openSheet(content, name);
}

export function closeSheet(): void {
  (window as unknown as { FK?: FKApi }).FK?.closeSheet();
}
