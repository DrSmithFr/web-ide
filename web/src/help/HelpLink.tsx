// The "?" next to a place of the interface: opens the page of the documentation that explains it,
// at its heading. The page of each place is in topics.json (checked by scripts/check-links.mjs).
import { Show } from 'solid-js'
import { t } from '../i18n'
import { project } from '../state/project'
import { docTarget, openDoc, type HelpTopic } from '../state/docs'

/** `onBefore` runs before the page opens (a modal closing). Hidden when no project is open: the docs open in its window. */
export function HelpLink(props: { topic: HelpTopic; onBefore?: () => void }) {
  return (
    <Show when={project()}>
      <button
        class="help-link"
        title={t('Help: open the documentation page')}
        aria-label={t('Help')}
        data-testid={`help-${props.topic}`}
        onClick={() => {
          props.onBefore?.()
          openDoc(docTarget(props.topic))
        }}
      >
        ?
      </button>
    </Show>
  )
}
