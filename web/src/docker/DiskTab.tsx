// Disk tab of the Docker tool: space used by images, containers, volumes and the build cache,
// prune buttons (confirmed), and the images and volumes with removal of the unused ones.
import { createResource, createSignal, For, Show } from 'solid-js'
import { request } from '../pod/rpc'
import { contextMenu } from '../ui/overlay'
import { errorToast, toast } from '../ui/toast'
import { Icon } from '../ui/icons'
import { t } from '../i18n'

interface Disk {
  usage: { type: string; total: number; active: number; size: string; reclaimable: string }[]
  images: { id: string; repository: string; tag: string; size: string; created: string; containers: number }[]
  volumes: { name: string; size: string; links: number; anonymous: boolean }[]
}

type Prune = 'containers' | 'danglingImages' | 'images' | 'anonymousVolumes' | 'volumes' | 'buildCache'

/** Prune buttons of each line of docker system df. */
const prunes: Record<string, { what: Prune; label: string; confirm: string }[]> = {
  Containers: [{ what: 'containers', label: 'Remove stopped', confirm: 'Remove every stopped container?' }],
  Images: [
    { what: 'danglingImages', label: 'Remove dangling', confirm: 'Remove the dangling images (untagged layers)?' },
    { what: 'images', label: 'Remove unused', confirm: 'Remove every image no container uses? They will be pulled or built again when needed.' },
  ],
  'Local Volumes': [
    { what: 'anonymousVolumes', label: 'Remove anonymous', confirm: 'Remove the anonymous volumes no container uses?' },
    { what: 'volumes', label: 'Remove unused', confirm: 'Remove EVERY volume no container uses, named ones included? Their data (databases…) is lost for good.' },
  ],
  'Build Cache': [{ what: 'buildCache', label: 'Clear', confirm: 'Clear the whole build cache? The next builds will be slower.' }],
}

/** Labels of the types of docker system df. */
const types: Record<string, string> = { Images: 'Images', Containers: 'Containers', 'Local Volumes': 'Volumes', 'Build Cache': 'Build cache' }

export function DiskTab() {
  const [disk, { refetch }] = createResource(() => request<Disk>('docker.disk'))
  const [running, setRunning] = createSignal('')

  const prune = async (p: { what: Prune; confirm: string }) => {
    if (!confirm(t(p.confirm))) return
    setRunning(p.what)
    try {
      const r = await request<{ reclaimed: string }>('docker.prune', { what: p.what })
      toast(t('Space freed: {size}', { size: r.reclaimed }), 'ok')
    } catch (e) {
      errorToast(e)
    } finally {
      setRunning('')
      refetch()
    }
  }
  const remove = async (kind: 'image' | 'volume', id: string, label: string) => {
    if (!confirm(kind === 'image' ? t('Remove the image {name}?', { name: label }) : t('Remove the volume {name}? Its data is lost.', { name: label }))) return
    try {
      await request('docker.remove', { kind, id })
      toast(t('{name} removed', { name: label }), 'ok', undefined, 2000)
    } catch (e) {
      errorToast(e)
    }
    refetch()
  }

  return (
    <div class="dk-disk" data-testid="docker-disk">
      <div class="dk-bar">
        <span class="muted small">{t('Space used by Docker on this host')}</span>
        <span class="grow" />
        <Show when={disk.loading}>
          <span class="spinner" />
        </Show>
        <button class="icon-btn small" title={t('Refresh')} onClick={() => refetch()}>
          <Icon name="refresh" size={12} />
        </button>
      </div>
      <Show when={!disk.error} fallback={<p class="danger pad mono small dk-error">{(disk.error as Error)?.message}</p>}>
        <Show when={disk.latest} fallback={<p class="muted pad">{t('Loading…')}</p>}>
          <table class="dk-table">
            <thead>
              <tr>
                <th />
                <th>{t('Count')}</th>
                <th>{t('In use')}</th>
                <th>{t('Size')}</th>
                <th>{t('Reclaimable')}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              <For each={disk.latest!.usage}>
                {(u) => (
                  <tr data-testid={`docker-usage-${u.type.replace(' ', '-')}`}>
                    <td>{t(types[u.type] ?? u.type)}</td>
                    <td class="num">{u.total}</td>
                    <td class="num">{u.active}</td>
                    <td class="num">{u.size}</td>
                    <td class="num">{u.reclaimable}</td>
                    <td class="dk-prunes">
                      <For each={prunes[u.type] ?? []}>
                        {(p) => (
                          <button class="btn small" classList={{ danger: p.what === 'volumes' }} disabled={!!running()} onClick={() => prune(p)} data-prune={p.what}>
                            <Show when={running() === p.what}>
                              <span class="spinner" />
                            </Show>
                            {t(p.label)}
                          </button>
                        )}
                      </For>
                    </td>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
          <h4 class="dk-sub">{t('Images')}</h4>
          <For each={disk.latest!.images} fallback={<p class="muted small pad">{t('No image.')}</p>}>
            {(im) => {
              const label = im.repository && im.repository !== '<none>' ? `${im.repository}:${im.tag}` : im.id
              return (
                <div
                  class="dk-obj"
                  data-testid={`docker-image-${label}`}
                  onContextMenu={(e) => contextMenu(e, [{ label: t('docker|Remove'), danger: true, disabled: im.containers > 0, action: () => remove('image', im.id, label) }])}
                >
                  <span class="mono ellipsis">{label}</span>
                  <span class="muted small">{im.created}</span>
                  <span class="grow" />
                  <Show when={im.containers > 0} fallback={<span class="badge">{t('unused')}</span>}>
                    <span class="badge ok">{t('in use')}</span>
                  </Show>
                  <span class="num">{im.size}</span>
                </div>
              )
            }}
          </For>
          <h4 class="dk-sub">{t('Volumes')}</h4>
          <For each={disk.latest!.volumes} fallback={<p class="muted small pad">{t('No volume.')}</p>}>
            {(v) => (
              <div
                class="dk-obj"
                data-testid={`docker-volume-${v.name}`}
                onContextMenu={(e) => contextMenu(e, [{ label: t('docker|Remove'), danger: true, disabled: v.links > 0, action: () => remove('volume', v.name, v.name) }])}
              >
                <span class="mono ellipsis" title={v.name}>
                  {v.anonymous ? v.name.slice(0, 12) : v.name}
                </span>
                <Show when={v.anonymous}>
                  <span class="muted small">{t('anonymous')}</span>
                </Show>
                <span class="grow" />
                <Show when={v.links > 0} fallback={<span class="badge">{t('unused')}</span>}>
                  <span class="badge ok">{t('in use')}</span>
                </Show>
                <span class="num">{v.size}</span>
              </div>
            )}
          </For>
        </Show>
      </Show>
    </div>
  )
}
