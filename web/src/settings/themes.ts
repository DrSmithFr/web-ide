// Built-in themes: interface variables and token colors. Token styles are limited to what
// ::highlight() accepts (color, background, decoration, italic; no bold).

export interface Theme {
  id: string
  name: string
  dark: boolean
  ui: Record<string, string>
  tokens: Record<string, string>
}

export const tokenTypes = [
  'keyword', 'string', 'comment', 'number', 'function', 'type', 'variable', 'constant', 'property',
  'operator', 'punctuation', 'tag', 'attribute', 'regexp', 'builtin', 'meta', 'escape', 'heading', 'emphasis', 'link',
] as const

export const tokenLabels: Record<string, string> = {
  keyword: 'Mot-clé', string: 'Chaîne', comment: 'Commentaire', number: 'Nombre', function: 'Fonction', type: 'Type / classe',
  variable: 'Variable', constant: 'Constante', property: 'Propriété', operator: 'Opérateur', punctuation: 'Ponctuation',
  tag: 'Balise', attribute: 'Attribut', regexp: 'Regex', builtin: 'Fonction native', meta: 'Méta / annotation',
  escape: 'Échappement', heading: 'Titre', emphasis: 'Emphase', link: 'Lien',
}

export const themes: Theme[] = [
  {
    id: 'nuit',
    name: 'Nuit',
    dark: true,
    ui: {
      bg: '#1b1d21', 'bg-2': '#212328', 'bg-3': '#2a2d33', 'bg-hover': '#30343b', 'bg-active': '#363b44',
      fg: '#d7dae0', 'fg-muted': '#8b919c', 'fg-faint': '#5e636d', line: '#30333a', accent: '#5b8cff', 'accent-fg': '#ffffff',
      'sel-bg': '#33507f', 'cur-line': '#23262c', danger: '#f0716b', warn: '#e6b450', ok: '#7fc97f', info: '#6cb6ff',
      'match-bg': '#5c4b16', 'match-cur-bg': '#9c7a12', gutter: '#5e636d', 'conflict-bg': '#4a2a2a',
    },
    tokens: {
      keyword: '#c792ea', string: '#a5d6a7', comment: '#6a737d', number: '#f78c6c', function: '#82aaff', type: '#ffcb6b',
      variable: '#f07178', constant: '#ff9e64', property: '#89ddff', operator: '#89ddff', punctuation: '#a0a7b4', tag: '#f07178',
      attribute: '#ffcb6b', regexp: '#e2b86b', builtin: '#7fdbca', meta: '#b4a0d8', escape: '#ff9e64', heading: '#82aaff',
      emphasis: '#c3e88d', link: '#7fdbca',
    },
  },
  {
    id: 'jour',
    name: 'Jour',
    dark: false,
    ui: {
      bg: '#fbfbfa', 'bg-2': '#f2f2f0', 'bg-3': '#e8e8e5', 'bg-hover': '#e2e2de', 'bg-active': '#d7dbe6',
      fg: '#24262b', 'fg-muted': '#62666e', 'fg-faint': '#9a9ea6', line: '#dcdcd8', accent: '#2f6fde', 'accent-fg': '#ffffff',
      'sel-bg': '#bcd3fb', 'cur-line': '#f1f3f7', danger: '#c9362f', warn: '#a86b00', ok: '#2e7d32', info: '#1565c0',
      'match-bg': '#fde68a', 'match-cur-bg': '#f59e0b', gutter: '#a0a4ab', 'conflict-bg': '#fbe1df',
    },
    tokens: {
      keyword: '#8b2fc9', string: '#2e7d32', comment: '#8a8f98', number: '#c2410c', function: '#1d4ed8', type: '#a16207',
      variable: '#be123c', constant: '#c2410c', property: '#0e7490', operator: '#4b5563', punctuation: '#6b7280', tag: '#be123c',
      attribute: '#a16207', regexp: '#b45309', builtin: '#0f766e', meta: '#7c3aed', escape: '#c2410c', heading: '#1d4ed8',
      emphasis: '#15803d', link: '#0f766e',
    },
  },
  {
    id: 'solarized',
    name: 'Solarized sombre',
    dark: true,
    ui: {
      bg: '#002b36', 'bg-2': '#073642', 'bg-3': '#0b3f4c', 'bg-hover': '#104755', 'bg-active': '#165263',
      fg: '#c5ced0', 'fg-muted': '#839496', 'fg-faint': '#586e75', line: '#0e4351', accent: '#268bd2', 'accent-fg': '#ffffff',
      'sel-bg': '#174f63', 'cur-line': '#04313d', danger: '#dc322f', warn: '#b58900', ok: '#859900', info: '#2aa198',
      'match-bg': '#5b4a00', 'match-cur-bg': '#8a7000', gutter: '#586e75', 'conflict-bg': '#3d1f22',
    },
    tokens: {
      keyword: '#859900', string: '#2aa198', comment: '#586e75', number: '#d33682', function: '#268bd2', type: '#b58900',
      variable: '#cb4b16', constant: '#d33682', property: '#93a1a1', operator: '#93a1a1', punctuation: '#839496', tag: '#268bd2',
      attribute: '#b58900', regexp: '#dc322f', builtin: '#6c71c4', meta: '#6c71c4', escape: '#cb4b16', heading: '#268bd2',
      emphasis: '#859900', link: '#2aa198',
    },
  },
  {
    id: 'contraste',
    name: 'Contraste élevé',
    dark: true,
    ui: {
      bg: '#000000', 'bg-2': '#0d0d0d', 'bg-3': '#1a1a1a', 'bg-hover': '#262626', 'bg-active': '#333333',
      fg: '#ffffff', 'fg-muted': '#c8c8c8', 'fg-faint': '#8c8c8c', line: '#4d4d4d', accent: '#ffd400', 'accent-fg': '#000000',
      'sel-bg': '#264f78', 'cur-line': '#141414', danger: '#ff6b6b', warn: '#ffd400', ok: '#7CFC00', info: '#5cc8ff',
      'match-bg': '#665500', 'match-cur-bg': '#b38f00', gutter: '#8c8c8c', 'conflict-bg': '#4d0000',
    },
    tokens: {
      keyword: '#ff9cf7', string: '#9cff9c', comment: '#9e9e9e', number: '#ffcc66', function: '#82d4ff', type: '#ffe066',
      variable: '#ff8f8f', constant: '#ffb366', property: '#8ff0ff', operator: '#ffffff', punctuation: '#d0d0d0', tag: '#ff8f8f',
      attribute: '#ffe066', regexp: '#ffb366', builtin: '#7affd4', meta: '#d2b3ff', escape: '#ffb366', heading: '#82d4ff',
      emphasis: '#9cff9c', link: '#7affd4',
    },
  },
]

export function themeById(id: string) {
  return themes.find((t) => t.id === id) ?? themes[0]
}
