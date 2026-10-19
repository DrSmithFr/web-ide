// Images and files of a prompt in the thread: images loaded from the pod (files of the
// session), opened in a modal on click.
import { createResource, createSignal, For, Show } from 'solid-js'
import { agentImage, type Entry } from './state'
import { Modal } from '../plugin'

type Att = NonNullable<Entry['attachments']>[number]

function Chip(props: { id: string; a: Att; onOpen: (url: string) => void }) {
  const [url] = createResource(
    () => (props.a.kind === 'image' && props.a.file ? props.a.file : null),
    (file) => agentImage(props.id, file).catch(() => ''),
  )
  return (
    <span class="ai-att" classList={{ open: !!url() }} data-kind={props.a.kind} title={props.a.name} onClick={() => url() && props.onOpen(url()!)} data-testid="agent-user-att">
      <Show when={url()} fallback={<span class="ai-att-icon">{props.a.kind === 'image' ? '🖼' : '📃'}</span>}>
        <img src={url()} alt="" />
      </Show>
      <span class="ai-att-name">
        <span class="ellipsis">{props.a.name}</span>
      </span>
    </span>
  )
}

export function UserAttachments(props: { id: string; list: Att[] }) {
  const [shown, setShown] = createSignal<{ url: string; name: string } | null>(null)
  return (
    <div class="ai-atts agent-user-atts">
      <For each={props.list}>{(a) => <Chip id={props.id} a={a} onOpen={(url) => setShown({ url, name: a.name })} />}</For>
      <Show when={shown()}>
        {(s) => (
          <Modal title={s().name} onClose={() => setShown(null)} class="agent-image-modal">
            <img src={s().url} alt={s().name} />
          </Modal>
        )}
      </Show>
    </div>
  )
}
