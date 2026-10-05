import { expect, test } from 'claude-code/testing'

import { parseProject, parseTarget, parseTicket, touched } from '../hooks/register'

test('reads the ticket of a kanban_get answer', async () => {
  const get = '# Ticket #12 · CSV export\nStatus: In progress · priority: High · branch: ticket/12-csv\n\n## Plan\n…\n\nTicket in the IDE: https://ide.ts.net/project/web-ide?ticket=12\nProject in the IDE: https://ide.ts.net/project/web-ide-t12'
  expect(parseTicket(get)).toEqual({ id: 12, title: 'CSV export', status: 'In progress', url: 'https://ide.ts.net/project/web-ide?ticket=12' })
  expect(parseProject(get)).toBe('https://ide.ts.net/project/web-ide-t12')
  expect(parseTicket('Kanban of web-ide (/p)\nProject in the IDE: https://ide.ts.net/project/web-ide\nNo ticket.')).toBe(null)
  expect(parseProject('Error: no project of the IDE holds /tmp')).toBe(null)
})

test('resolves /ide arguments against the working directory', async () => {
  expect(parseTarget('src/a.go:12', '/p/')).toEqual({ path: '/p/src/a.go', line: 12 })
  expect(parseTarget('/abs/b.ts', '/p')).toEqual({ path: '/abs/b.ts', line: 0 })
})

test('keeps the last files changed, newest first', async () => {
  expect(touched(['/a', '/b', '/c', '/d'], '/c')).toEqual(['/c', '/a', '/b', '/d'])
  expect(touched(['/a', '/b', '/c', '/d'], '/e')).toEqual(['/e', '/a', '/b', '/c'])
})

test('an edit of Claude adds its file to the band', async ($, on) => {
  on('tool.call', () => ({ result: 'edited' }))
  await $.tool.call({ tool: 'Edit', file_path: '/p/src/a.go', old_string: 'a', new_string: 'b' })
  await $.tool.call({ tool: 'Read', file_path: '/p/src/b.go' })
  const props = { hasSurvey: false, isWorking: false, maxRows: 3, bodyColumns: 100, scroll: { offset: 0, bodyRows: 3 }, view: {} }
  for (const surface of ['terminal', 'desktop'] as const) {
    const band = await $.ui.mount({ plugin: 'web-ide', surface, component: 'AbovePrompt', props })
    expect((await band.find({ key: '/p/src/a.go' }))?.props.label).toBe('↗ a.go')
    expect(await band.find({ key: '/p/src/b.go' })).toBe(undefined)
  }
})
