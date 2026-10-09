// Small line icons (24×24 viewBox, currentColor).
import type { JSX } from 'solid-js'

const paths: Record<string, string> = {
  files: 'M4 4h6l2 2h8v12a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2zM4 9h16',
  search: 'M11 4a7 7 0 1 1 0 14 7 7 0 0 1 0-14zM16.5 16.5 21 21',
  plug: 'M9 3v5M15 3v5M6 8h12v3a6 6 0 0 1-12 0zM12 17v4',
  database: 'M4 6c0-1.7 3.6-3 8-3s8 1.3 8 3-3.6 3-8 3-8-1.3-8-3zM4 6v12c0 1.7 3.6 3 8 3s8-1.3 8-3V6M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3',
  conflict: 'M12 3 2 20h20zM12 10v4M12 17v.5',
  outline: 'M4 6h3M10 6h10M6 12h3M12 12h8M6 18h3M12 18h8',
  puzzle: 'M10 4a2 2 0 1 1 4 0v2h4v4h-2a2 2 0 1 0 0 4h2v4h-4v-2a2 2 0 1 0-4 0v2H6v-4h2a2 2 0 1 0 0-4H6V6h4z',
  info: 'M12 3a9 9 0 1 1 0 18 9 9 0 0 1 0-18zM12 11v6M12 7.5v.5',
  terminal: 'M3 5h18v14H3zM7 9l3 3-3 3M12 15h5',
  problems: 'M12 3a9 9 0 1 1 0 18 9 9 0 0 1 0-18zM9 9l6 6M15 9l-6 6',
  gear: 'M12 9a3 3 0 1 1 0 6 3 3 0 0 1 0-6zM19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z',
  external: 'M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5',
  plus: 'M12 5v14M5 12h14',
  play: 'M7 4v16l13-8z',
  stop: 'M6 6h12v12H6z',
  refresh: 'M20 11a8 8 0 1 0-2.3 5.7M20 4v7h-7',
  close: 'M6 6l12 12M18 6 6 18',
  folder: 'M3 6h6l2 2h10v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1z',
  file: 'M6 3h8l4 4v14H6zM14 3v4h4',
  chevron: 'M9 6l6 6-6 6',
  locate: 'M12 8a4 4 0 1 1 0 8 4 4 0 0 1 0-8zM12 2v4M12 18v4M2 12h4M18 12h4',
  history: 'M3 12a9 9 0 1 0 3-6.7L3 8M3 3v5h5M12 7v5l3 3',
  check: 'M5 12l5 5 9-10',
  undo: 'M9 14 4 9l5-5M4 9h11a5 5 0 0 1 0 10h-3',
  redo: 'M15 14l5-5-5-5M20 9H9a5 5 0 0 0 0 10h3',
  pen: 'M4 20l1.5-5L16 4.5a2.1 2.1 0 0 1 3 3L8.5 18zM14 6.5l3 3',
  marker: 'M9 14l-4 4v2h4l1-1M9 14l8.5-9.5a2 2 0 0 1 2.9 2.8L11 16zM13 21h8',
  eraser: 'M9 20h11M4.5 15.5l9.6-10.6a2 2 0 0 1 2.8-.1l3 2.8a2 2 0 0 1 .1 2.8L12 18.5a2 2 0 0 1-1.5.5H8a2 2 0 0 1-1.4-.6l-2-2a1 1 0 0 1-.1-1.2zM9 11l5 5',
  grid: 'M4 4h16v16H4zM4 10h16M4 16h16M10 4v16M16 4v16',
  cursor: 'M6 3l13 7.5-5.5 1.5L11 18z',
  rect: 'M4 6h16v12H4z',
  ellipse: 'M12 5c4.4 0 8 3.1 8 7s-3.6 7-8 7-8-3.1-8-7 3.6-7 8-7z',
  line: 'M5 19 19 5',
  arrow: 'M5 19 19 5M10 5h9v9',
  text: 'M5 7V5h14v2M12 5v14M9 19h6',
  image: 'M4 5h16v14H4zM4 16l5-5 4 4 2-2 5 5M15 9.5a1 1 0 1 1 0 .1',
  imageOff: 'M4 5h16v14H4zM4 16l5-5 4 4M3 3l18 18',
  screen: 'M3 4h18v12H3zM8 20h8M12 16v4',
  layout: 'M4 4h16v16H4zM10 4v16M10 10h10',
  fill: 'M4 6h16v12H4zM4 14l6-6M4 18l10-10M8 18l10-10M12 18l8-8M16 18l4-4',
  trash: 'M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3',
  table: 'M3 5h18v14H3zM3 10h18M3 15h18M9 5v14',
  key: 'M14 4a6 6 0 1 1-4.5 10L4 19.5V21h3v-2h2v-2h2l1-1A6 6 0 0 1 14 4zM16 8.5v.5',
  edit: 'M4 20h4L19 9l-4-4L4 16zM14 6l4 4',
  copy: 'M8 8h12v12H8zM4 16V4h12',
  home: 'M3 11 12 4l9 7M5 10v10h14V10',
  menu: 'M4 6h16M4 12h16M4 18h16',
  split: 'M3 4h18v16H3zM12 4v16',
  sparkle: 'M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8zM19 15l.8 2.2L22 18l-2.2.8L19 21l-.8-2.2L16 18l2.2-.8z',
  mic: 'M12 3a3 3 0 0 1 3 3v6a3 3 0 0 1-6 0V6a3 3 0 0 1 3-3zM5 11a7 7 0 0 0 14 0M12 18v3M9 21h6',
  sidebar: 'M3 5a1 1 0 0 1 1-1h16a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1zM9 4v16',
  sliders: 'M4 7h9M17 7h3M4 17h3M11 17h9M15 5v4M9 15v4',
  paperclip: 'M20 11.5l-8 8a5 5 0 0 1-7-7l8.5-8.5a3.3 3.3 0 0 1 4.7 4.7L9.7 17a1.7 1.7 0 0 1-2.4-2.4L15 7',
  arrowUp: 'M12 19V5M6 11l6-6 6 6',
  kanban: 'M4 4h4v16H4zM10 4h4v10h-4zM16 4h4v13h-4z',
  comment: 'M4 5h16v11H10l-6 4z',
  expandAll: 'M7 9l5-5 5 5M7 15l5 5 5-5',
  collapseAll: 'M7 4l5 5 5-5M7 20l5-5 5 5',
  docker: 'M2 12h18c1 0 2-1 2.2-2.2M2 12c0 4.4 3.6 8 9 8 5 0 8.4-3 9.4-8M5 12V9h3v3M8 12V9h3v3M11 12V9h3v3M8 9V6h3v3',
  branch: 'M6 3v12M6 15a3 3 0 1 0 0 6 3 3 0 0 0 0-6zM18 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM18 9c0 5-6 4-12 6',
  lock: 'M6 11h12v10H6zM8 11V7a4 4 0 0 1 8 0v4',
  up: 'M6 15l6-6 6 6',
  down: 'M6 9l6 6 6-6',
  clock: 'M12 3a9 9 0 1 1 0 18 9 9 0 0 1 0-18zM12 7v5l3 3',
  chart: 'M4 4v16h16M8 16v-4M12 16V8M16 16v-7',
  warning: 'M12 3 2 20h20zM12 10v4M12 17v.5',
}

export function Icon(props: { name: keyof typeof paths | string; size?: number; title?: string } & JSX.SvgSVGAttributes<SVGSVGElement>) {
  return (
    <svg
      class="icon"
      width={props.size ?? 16}
      height={props.size ?? 16}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="1.7"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden={props.title ? undefined : 'true'}
      role={props.title ? 'img' : undefined}
    >
      {props.title && <title>{props.title}</title>}
      <path d={paths[props.name] ?? paths.file} />
    </svg>
  )
}
