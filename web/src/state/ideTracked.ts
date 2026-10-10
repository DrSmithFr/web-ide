// The .ide folder is ignored by git, but a project may have committed some of its files
// before: offered once (the pod remembers the answer) to take them out of the index.
import { request } from '../pod/rpc'
import { t } from '../i18n'
import { errorToast, toast } from '../ui/toast'

export function offerIdeUntrack(files: string[] | null | undefined) {
  if (!files?.length) return
  // After the first rendering of the window.
  setTimeout(async () => {
    const list = files.slice(0, 8).join('\n') + (files.length > 8 ? '\n…' : '')
    const untrack = confirm(
      t('The settings of the IDE (.ide) stay out of git, but {n} of its files are committed:\n\n{files}\n\nTake them out of git (git rm --cached; the files stay on disk, you commit the change)?', {
        n: files.length,
        files: list,
      }),
    )
    try {
      await request('project.ideUntrack', { untrack })
      if (untrack) toast(t('The files of .ide are no longer tracked: commit the change.'), 'ok')
    } catch (e) {
      errorToast(e)
    }
  }, 300)
}
