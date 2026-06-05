export const TYPE_COLORS: Record<string, string> = {
  Normal:   '#9099A1',
  Fire:     '#FF9741',
  Water:    '#3692DC',
  Electric: '#FAC000',
  Grass:    '#38BF4B',
  Ice:      '#39C6C0',
  Fighting: '#E0306A',
  Poison:   '#B567CE',
  Ground:   '#E87236',
  Flying:   '#89AAE3',
  Psychic:  '#FF6675',
  Bug:      '#83C300',
  Rock:     '#C7B78B',
  Ghost:    '#4C6AB2',
  Dragon:   '#006FC9',
  Dark:     '#5B5466',
  Steel:    '#5A8EA2',
  Fairy:    '#FB89EB',
};

export function typeBadge(type: string): HTMLElement {
  const span = document.createElement('span');
  span.className = 'type-badge';
  span.textContent = type;
  span.style.backgroundColor = TYPE_COLORS[type] ?? '#888';
  return span;
}
