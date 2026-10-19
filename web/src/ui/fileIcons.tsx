// Icons of the file types in the explorer: a colored badge for the languages, a colored
// line icon for the other recognized files, the plain file icon otherwise.

type Badge = { label: string; bg: string; fg?: string }
type Line = { path: string; color: string }
type Kind = Badge | Line

const b = (label: string, bg: string, fg?: string): Badge => ({ label, bg, fg })
const l = (path: string, color: string): Line => ({ path, color })

const shapes = {
  file: 'M6 3h8l4 4v14H6zM14 3v4h4',
  text: 'M6 3h8l4 4v14H6zM14 3v4h4M9 12h6M9 16h6',
  braces: 'M9 4C7 4 7 6 7 8s-1 3-3 4c2 1 3 2 3 4s0 4 2 4M15 4c2 0 2 2 2 4s1 3 3 4c-2 1-3 2-3 4s0 4-2 4',
  markdown: 'M3 6h18v12H3zM6 15V9l2.5 3L11 9v6M16 9v6M14 13l2 2 2-2',
  image: 'M3 5h18v14H3zM3 16l5-5 4 4 3-3 6 6M15.5 8.5v.01',
  lock: 'M6 11h12v9H6zM8.5 11V8a3.5 3.5 0 0 1 7 0v3',
  git: 'M6 3v12M6 15a3 3 0 1 0 0 6 3 3 0 0 0 0-6zM18 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM18 9c0 5-6 4-12 6',
  key: 'M14 4a6 6 0 1 1-4.5 10L4 19.5V21h3v-2h2v-2h2l1-1A6 6 0 0 1 14 4zM16 8.5v.5',
  gear: 'M12 9a3 3 0 1 1 0 6 3 3 0 0 1 0-6zM12 3v3M12 18v3M3 12h3M18 12h3M5.6 5.6l2.1 2.1M16.3 16.3l2.1 2.1M5.6 18.4l2.1-2.1M16.3 7.7l2.1-2.1',
  box: 'M12 3l8 4.5v9L12 21l-8-4.5v-9zM4 7.5l8 4.5 8-4.5M12 12v9',
  archive: 'M4 4h16v4H4zM5 8v12h14V8M10 12h4',
  terminal: 'M3 5h18v14H3zM7 9l3 3-3 3M12 15h5',
  info: 'M12 3a9 9 0 1 1 0 18 9 9 0 0 1 0-18zM12 11v6M12 7.5v.5',
  database: 'M4 6c0-1.7 3.6-3 8-3s8 1.3 8 3-3.6 3-8 3-8-1.3-8-3zM4 6v12c0 1.7 3.6 3 8 3s8-1.3 8-3V6M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3',
  font: 'M4 20 10 4h1l6 16M6.5 14h8',
  film: 'M3 5h18v14H3zM7 5v14M17 5v14M3 9.5h4M3 14.5h4M17 9.5h4M17 14.5h4',
  folder: 'M3 6h6l2 2h10v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1z',
  folderOpen: 'M3 18V6h6l2 2h8v3M3 18l3-7h16l-3 7z',
}

const byExt: Record<string, Kind> = {
  go: b('GO', '#00add8'),
  ts: b('TS', '#3178c6'),
  tsx: b('TS', '#3178c6'),
  mts: b('TS', '#3178c6'),
  cts: b('TS', '#3178c6'),
  js: b('JS', '#f1dd35', '#222'),
  jsx: b('JS', '#f1dd35', '#222'),
  mjs: b('JS', '#f1dd35', '#222'),
  cjs: b('JS', '#f1dd35', '#222'),
  py: b('PY', '#3572a5'),
  php: b('PHP', '#777bb4'),
  rs: b('RS', '#c5603b'),
  java: b('J', '#b07219'),
  kt: b('KT', '#a97bff'),
  kts: b('KT', '#a97bff'),
  scala: b('SC', '#c22d40'),
  c: b('C', '#5c6bc0'),
  h: b('H', '#5c6bc0'),
  cpp: b('C++', '#f34b7d'),
  cc: b('C++', '#f34b7d'),
  hpp: b('H++', '#f34b7d'),
  cs: b('C#', '#178600'),
  rb: b('RB', '#cc342d'),
  swift: b('SW', '#f05138'),
  dart: b('DT', '#00b4ab'),
  lua: b('LUA', '#000080'),
  ex: b('EX', '#6e4a7e'),
  exs: b('EX', '#6e4a7e'),
  vue: b('V', '#41b883'),
  svelte: b('S', '#ff3e00'),
  html: b('<>', '#e34c26'),
  htm: b('<>', '#e34c26'),
  css: b('#', '#2965f1'),
  scss: b('#', '#cd6799'),
  sass: b('#', '#cd6799'),
  less: b('#', '#1d365d'),
  sql: l(shapes.database, '#e38c00'),
  sh: l(shapes.terminal, '#4eaa25'),
  bash: l(shapes.terminal, '#4eaa25'),
  zsh: l(shapes.terminal, '#4eaa25'),
  fish: l(shapes.terminal, '#4eaa25'),
  ps1: l(shapes.terminal, '#5391fe'),
  json: l(shapes.braces, '#cbcb41'),
  jsonc: l(shapes.braces, '#cbcb41'),
  json5: l(shapes.braces, '#cbcb41'),
  yaml: l(shapes.gear, '#cb4b6b'),
  yml: l(shapes.gear, '#cb4b6b'),
  toml: l(shapes.gear, '#9c4221'),
  ini: l(shapes.gear, '#8b919c'),
  conf: l(shapes.gear, '#8b919c'),
  xml: b('<>', '#f1662a'),
  md: l(shapes.markdown, '#519aba'),
  mdx: l(shapes.markdown, '#519aba'),
  txt: l(shapes.text, '#8b919c'),
  log: l(shapes.text, '#8b919c'),
  csv: l(shapes.text, '#89e051'),
  pdf: b('PDF', '#d93025'),
  png: l(shapes.image, '#a074c4'),
  jpg: l(shapes.image, '#a074c4'),
  jpeg: l(shapes.image, '#a074c4'),
  gif: l(shapes.image, '#a074c4'),
  webp: l(shapes.image, '#a074c4'),
  ico: l(shapes.image, '#a074c4'),
  svg: l(shapes.image, '#ffb13b'),
  mp4: l(shapes.film, '#a074c4'),
  webm: l(shapes.film, '#a074c4'),
  woff: l(shapes.font, '#8b919c'),
  woff2: l(shapes.font, '#8b919c'),
  ttf: l(shapes.font, '#8b919c'),
  otf: l(shapes.font, '#8b919c'),
  zip: l(shapes.archive, '#c9a227'),
  gz: l(shapes.archive, '#c9a227'),
  tar: l(shapes.archive, '#c9a227'),
  tgz: l(shapes.archive, '#c9a227'),
  lock: l(shapes.lock, '#8b919c'),
  db: l(shapes.database, '#8b919c'),
  sqlite: l(shapes.database, '#8b919c'),
  env: l(shapes.key, '#e6b450'),
  pem: l(shapes.key, '#e6b450'),
  key: l(shapes.key, '#e6b450'),
}

const byName: Record<string, Kind> = {
  'go.mod': b('GO', '#7e57c2'),
  'go.sum': b('GO', '#7e57c2'),
  'package.json': b('npm', '#cb3837'),
  'package-lock.json': l(shapes.lock, '#cb3837'),
  'tsconfig.json': b('TS', '#3178c6'),
  'composer.json': b('PHP', '#885630'),
  'composer.lock': l(shapes.lock, '#885630'),
  dockerfile: l(shapes.box, '#2496ed'),
  'docker-compose.yml': l(shapes.box, '#2496ed'),
  'docker-compose.yaml': l(shapes.box, '#2496ed'),
  'compose.yml': l(shapes.box, '#2496ed'),
  'compose.yaml': l(shapes.box, '#2496ed'),
  makefile: l(shapes.terminal, '#e37933'),
  '.gitignore': l(shapes.git, '#f14e32'),
  '.gitattributes': l(shapes.git, '#f14e32'),
  '.gitmodules': l(shapes.git, '#f14e32'),
  license: l(shapes.text, '#d0bf41'),
  'license.md': l(shapes.text, '#d0bf41'),
  'license.txt': l(shapes.text, '#d0bf41'),
  '.editorconfig': l(shapes.gear, '#8b919c'),
}

/** Kind of a file name: by full name, README* and .env*, then by extension. */
function kindOf(name: string): Kind | undefined {
  const n = name.toLowerCase()
  if (byName[n]) return byName[n]
  if (n.startsWith('readme')) return l(shapes.info, '#519aba')
  if (n === '.env' || n.startsWith('.env.')) return byExt.env
  if (n.startsWith('dockerfile')) return byName.dockerfile
  const dot = n.lastIndexOf('.')
  return dot > 0 ? byExt[n.slice(dot + 1)] : undefined
}

/** Icon of a file or folder; folders take the color of their mark (source, tests, excluded). */
export function FileIcon(props: { name: string; dir?: boolean; open?: boolean; mark?: string | null; size?: number }) {
  const size = () => props.size ?? 16
  const kind = () => (props.dir ? undefined : kindOf(props.name))
  return (
    <svg
      class="file-icon"
      classList={{ [`mark-${props.mark}`]: !!props.mark, folder: props.dir }}
      data-kind={(() => {
        const k = kind()
        return k ? ('label' in k ? k.label : 'line') : props.dir ? 'folder' : 'file'
      })()}
      width={size()}
      height={size()}
      viewBox="0 0 24 24"
      fill="none"
      stroke-width="1.7"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
    >
      {(() => {
        const k = kind()
        if (k && 'label' in k)
          return (
            <>
              <rect x="1.5" y="4.5" width="21" height="15" rx="3" fill={k.bg} />
              <text x="12" y="12.4" fill={k.fg ?? '#fff'} font-size={k.label.length > 2 ? '8.2' : '10'} font-weight="700" text-anchor="middle" dominant-baseline="middle" font-family="system-ui, sans-serif">
                {k.label}
              </text>
            </>
          )
        if (k) return <path d={k.path} stroke={k.color} />
        if (props.dir) return <path d={props.open ? shapes.folderOpen : shapes.folder} stroke="currentColor" />
        return <path d={shapes.file} stroke="currentColor" />
      })()}
    </svg>
  )
}
