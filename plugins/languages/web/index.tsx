// The Language servers plugin (pod/plugins/languages): code navigation, completion,
// diagnostics, the Problems and Structure tools, the Symbols tab of Search Everywhere. The
// editor of the core asks it through the language capability.
import type { PluginModule } from '@ide/types'
import { api as theApi, bindApi, desk, pick, t, toast } from './plugin'
import * as lspc from './client'
import { formatDocument, renameSymbol } from './refactor'
import { complete, resolveItem, triggerCharacters } from './completion'
import { resync, watchBuffers } from './sync'
import { diagnostics, problemCount, ProblemsTool, watchServers } from './problems'
import { StructureTool } from './structure'
import { symbolsProvider } from './symbols'

/** Runs an action on the text editor of the active tab (none: the key goes on). */
const onEditor = (f: (v: NonNullable<ReturnType<typeof theApi.editor.active>>['view'], d: NonNullable<ReturnType<typeof theApi.editor.active>>['doc']) => unknown) => () => {
  const ed = theApi.editor.active()
  if (!ed) return false
  void f(ed.view, ed.doc)
}

export const languages: PluginModule = {
  activate(api) {
    bindApi(api)
    watchServers()
    watchBuffers()
    api.provide('language', {
      diagnostics: (path) => diagnostics[path] ?? [],
      complete,
      resolve: resolveItem,
      triggerCharacters,
      gotoDeclaration: (doc, offset) => void lspc.gotoDeclaration(doc, offset),
      showProblems: () => {
        api.showTool('problems')
        desk.set((s) => (s.tab = 'problems'))
      },
    })
    const at = (v: { getSelection(): { head: number } }) => v.getSelection().head
    return {
      tools: { problems: { component: ProblemsTool, badge: problemCount }, structure: { component: StructureTool } },
      searchProviders: [symbolsProvider],
      opened: resync,
      commands: {
        'lsp.definition': onEditor((v, d) => lspc.gotoDeclaration(d, at(v))),
        'lsp.implementation': onEditor((v, d) => lspc.gotoImplementation(d, at(v))),
        'lsp.typeDefinition': onEditor((v, d) => lspc.gotoTypeDefinition(d, at(v))),
        'lsp.superMethod': onEditor((v, d) => lspc.gotoSuperMethod(d, at(v))),
        'lsp.references': onEditor((v, d) => lspc.findReferences(d, at(v))),
        'lsp.rename': onEditor((v, d) => renameSymbol(v, d)),
        'lsp.format': onEditor((v, d) => formatDocument(v, d)),
        'lsp.hover': onEditor(async (v, d) => {
          const off = at(v)
          try {
            const text = await lspc.hoverText(d, off)
            const c = v.coordsAt(off)
            if (text) v.showTooltip(c.left, c.bottom - 8, text, 'doc')
            else toast(t('No documentation here'), 'info')
          } catch (e) {
            toast((e as Error).message, 'info')
          }
        }),
        'nav.fileStructure': onEditor(async (v, d) => {
          try {
            const symbols = lspc.flatten(await lspc.documentSymbols(d.path))
            const chosen = await pick({
              placeholder: t('Structure of {name}', { name: api.project.basename(d.path) }),
              items: symbols.map(({ s, depth }) => ({ label: '  '.repeat(depth) + s.name, detail: s.detail, icon: lspc.symbolKinds[s.kind]?.[1], value: s })),
            })
            if (chosen) {
              const off = d.offset(chosen.selectionRange.start.line, chosen.selectionRange.start.character)
              v.setSelection(off)
              v.scrollToOffset(off, true)
            }
          } catch (e) {
            toast((e as Error).message, 'info')
          }
        }),
      },
    }
  },
}
