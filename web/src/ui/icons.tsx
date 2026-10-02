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
  table: 'M3 5h18v14H3zM3 10h18M3 15h18M9 5v14',
  key: 'M14 4a6 6 0 1 1-4.5 10L4 19.5V21h3v-2h2v-2h2l1-1A6 6 0 0 1 14 4zM16 8.5v.5',
  edit: 'M4 20h4L19 9l-4-4L4 16zM14 6l4 4',
  copy: 'M8 8h12v12H8zM4 16V4h12',
  home: 'M3 11 12 4l9 7M5 10v10h14V10',
  menu: 'M4 6h16M4 12h16M4 18h16',
  split: 'M3 4h18v16H3zM12 4v16',
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
