// Folder tree of changed files: folders with a single sub-folder and nothing else are
// joined on one row ("web/src/panels"), folders before files, by name.

export interface TreeNode<T> {
  name: string
  /** Absolute path of the folder or of the file. */
  path: string
  /** Every file under the node (the file itself for a leaf). */
  items: T[]
  children: TreeNode<T>[]
  item?: T
}

export function buildTree<T>(items: T[], base: string, pathOf: (item: T) => string): TreeNode<T> {
  const root: TreeNode<T> = { name: '', path: base, items: [], children: [] }
  for (const item of items) {
    const p = pathOf(item)
    const rel = p.startsWith(base + '/') ? p.slice(base.length + 1) : p
    const parts = rel.split('/')
    let node = root
    node.items.push(item)
    for (const part of parts.slice(0, -1)) {
      let next = node.children.find((c) => !c.item && c.name === part)
      if (!next) {
        next = { name: part, path: node.path + '/' + part, items: [], children: [] }
        node.children.push(next)
      }
      next.items.push(item)
      node = next
    }
    node.children.push({ name: parts[parts.length - 1], path: p, items: [item], children: [], item })
  }
  const finish = (node: TreeNode<T>): TreeNode<T> => {
    node.children = node.children.map(finish).sort((a, b) => Number(!!a.item) - Number(!!b.item) || a.name.localeCompare(b.name))
    // Compact the chains of single folders (never the root).
    while (node !== root && !node.item && node.children.length === 1 && !node.children[0].item) {
      const only = node.children[0]
      node.name += '/' + only.name
      node.path = only.path
      node.children = only.children
    }
    return node
  }
  return finish(root)
}
