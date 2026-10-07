export const ICON_PATHS = {
  'chevron-down': ['M4 6l4 4 4-4'],
  'chevron-right': ['M6 4l4 4-4 4'],
  check: ['M3.5 8.5l3 3 6-7'],
  alert: ['M8 1.75a6.25 6.25 0 1 0 0 12.5 6.25 6.25 0 0 0 0-12.5z', 'M8 4.75v4', 'M8 11.25h.01'],
  bolt: ['M9 1.5L3.5 9H8l-1 5.5L12.5 7H8l1-5.5z'],
  sparkle: ['M8 1.5l1.6 4.9 4.9 1.6-4.9 1.6L8 14.5l-1.6-4.9L1.5 8l4.9-1.6z'],
  'arrow-up': ['M8 13V3', 'M4 7l4-4 4 4'],
  x: ['M4 4l8 8', 'M12 4l-8 8'],
  sun: [
    'M8 5a3 3 0 1 0 0 6 3 3 0 0 0 0-6z',
    'M8 1v1.5M8 13.5V15M1 8h1.5M13.5 8H15M3.05 3.05l1.06 1.06M11.89 11.89l1.06 1.06M3.05 12.95l1.06-1.06M11.89 4.11l1.06-1.06',
  ],
  moon: ['M13.5 9.5A5.5 5.5 0 0 1 6.5 2.5a5.5 5.5 0 1 0 7 7z'],
  monitor: ['M2 3h12v8H2z', 'M5.5 14h5M8 11v3'],
  tree: ['M2 3.5h5M2 8h3M7 8h7M7 12.5h7M2 3.5v9'],
  comment: ['M2.5 3h11a.5.5 0 0 1 .5.5v7a.5.5 0 0 1-.5.5H7l-3 2.5V11H2.5a.5.5 0 0 1-.5-.5v-7a.5.5 0 0 1 .5-.5z'],
  activity: ['M1.5 8h3l2-5 3 10 2-5h3'],
  refresh: ['M13.5 8a5.5 5.5 0 1 1-1.6-3.9', 'M13.5 2.5v3h-3'],
  external: ['M9 2.5h4.5V7', 'M13.5 2.5L7 9', 'M11.5 9.5v3a1 1 0 0 1-1 1h-7a1 1 0 0 1-1-1v-7a1 1 0 0 1 1-1h3'],
  conversation: ['M2 3.5A1.5 1.5 0 0 1 3.5 2h9A1.5 1.5 0 0 1 14 3.5v6a1.5 1.5 0 0 1-1.5 1.5H7l-3 3v-3h-.5A1.5 1.5 0 0 1 2 9.5z'],
  sidebar: ['M2 3h12v10H2z', 'M6 3v10'],
  split: ['M2 3h12v10H2z', 'M8 3v10'],
  unified: ['M2 3h12v10H2z', 'M2 8h12'],
  more: ['M3.5 8h.01', 'M8 8h.01', 'M12.5 8h.01'],
  'check-circle': ['M8 1.75a6.25 6.25 0 1 0 0 12.5 6.25 6.25 0 0 0 0-12.5z', 'M5.25 8.25l1.9 1.9 3.6-4'],
  'x-circle': ['M8 1.75a6.25 6.25 0 1 0 0 12.5 6.25 6.25 0 0 0 0-12.5z', 'M5.75 5.75l4.5 4.5', 'M10.25 5.75l-4.5 4.5'],
  'pending-circle': ['M8 1.75a6.25 6.25 0 1 0 0 12.5 6.25 6.25 0 0 0 0-12.5z', 'M8 4.75V8l2 1.5'],
  circle: ['M8 1.75a6.25 6.25 0 1 0 0 12.5 6.25 6.25 0 0 0 0-12.5z'],
  'request-changes': ['M8 1.75a6.25 6.25 0 1 0 0 12.5 6.25 6.25 0 0 0 0-12.5z', 'M5.5 8h5'],
  eye: ['M1.5 8s2.5-4.5 6.5-4.5S14.5 8 14.5 8 12 12.5 8 12.5 1.5 8 1.5 8z', 'M8 6.25a1.75 1.75 0 1 0 0 3.5 1.75 1.75 0 0 0 0-3.5z'],
  branch: ['M5 2.5v11', 'M11 5.5a1.75 1.75 0 1 0 0-3.5 1.75 1.75 0 0 0 0 3.5z', 'M11 5.5c0 3-6 2.5-6 6'],
  bot: ['M3 6h10v6.5H3z', 'M8 3.5V6', 'M8 2.75h.01', 'M6 9h.01', 'M10 9h.01', 'M1.5 8.5v2M14.5 8.5v2'],
} as const satisfies Record<string, readonly string[]>;

export type IconName = keyof typeof ICON_PATHS;

export function iconSymbolId(name: IconName): string {
  return `cc-icon-${name}`;
}

export function iconSprite(names: readonly IconName[]): string {
  const symbols = names.map(
    (name) =>
      `<symbol id="${iconSymbolId(name)}" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">${ICON_PATHS[name]
        .map((d) => `<path d="${d}"/>`)
        .join('')}</symbol>`,
  );
  return `<svg xmlns="http://www.w3.org/2000/svg" style="display:none">${symbols.join('')}</svg>`;
}
