// The routing decisions of the bootstrap page (pod/internal/relay/web/routes.js), run outside a
// browser: which pod opens the page of another one, and under which label. Run by make test.
import test from 'node:test'
import assert from 'node:assert/strict'
import { keyOf, nextGateway, pickGateway, pickSite, routeOf } from '../pod/internal/relay/web/routes.js'

// Site 1: A (preferred) and B, both registered at the relay. Site 2: C. D is a pod of the LAN of
// A that the relay never sees (it has no link of its own).
const pods = [
  { key: 'ka', podId: 'pa', site: 's1', gateway: true, preferred: true },
  { key: 'kb', podId: 'pb', site: 's1', gateway: false },
  { key: 'kc', podId: 'pc', site: 's2', gateway: true },
]

test('the gateway of a site is its preferred pod, else the elected one', () => {
  assert.equal(pickGateway(pods, 's1').key, 'ka')
  assert.equal(pickGateway(pods, 's2').key, 'kc')
  assert.equal(pickGateway(pods, 'nope'), null)
})

test('a window enters the site it used last, else the one with a preferred gateway', () => {
  assert.equal(pickSite(pods, 's2').site, 's2')
  assert.equal(pickSite(pods, 'gone').site, 's1')
  assert.equal(pickSite(pods, 's2').pod.key, 'kc')
})

test('a pod registered at the relay gets its own tunnel', () => {
  assert.deepEqual(routeOf(pods, 'kb', 'ka'), { via: 'kb', prefix: '' })
  assert.deepEqual(routeOf(pods, 'kc', 'ka'), { via: 'kc', prefix: '' })
})

test('a pod the relay does not see rides the gateway, with its labels prefixed', () => {
  assert.deepEqual(routeOf(pods, 'kd', 'ka'), { via: 'ka', prefix: '@kd/' })
})

test('a pod of another multi-pod protocol is never routed to', () => {
  const mixed = pods.map((p) => ({ ...p, protocol: 1 }))
  mixed[1] = { ...mixed[1], protocol: 0, mismatch: 'outdated' }
  // Its own tunnel is refused: the channels would not understand each other.
  assert.deepEqual(routeOf(mixed, 'kb', 'ka'), { via: 'ka', prefix: '@kb/' })
  assert.deepEqual(routeOf(mixed, 'kc', 'ka'), { via: 'kc', prefix: '' })
})

test('the page of the gateway itself is not routed through it', () => {
  assert.deepEqual(routeOf(pods, 'ka', 'ka'), { via: 'ka', prefix: '' })
  assert.deepEqual(routeOf(pods, '', 'ka'), { via: 'ka', prefix: '' })
})

test('failover: when the gateway leaves, the same pod rides the new one', () => {
  const after = pods.filter((p) => p.key !== 'ka')
  after[0].gateway = true
  // The page of D keeps its /pod/kd/ URL: only the tunnel it rides on changes.
  assert.deepEqual(routeOf(after, 'kd', 'kb'), { via: 'kb', prefix: '@kd/' })
  assert.deepEqual(routeOf(after, 'kc', 'kb'), { via: 'kc', prefix: '' })
})

test('failover: the window follows a pod of the site it entered, not another site', () => {
  const prev = pods.map((p) => ({ ...p }))
  // A left. B is alone on its own site now (a site is named after the pods of the local network),
  // C is the gateway of its own site: the window that entered through A goes to B.
  const after = [
    { key: 'kb', podId: 'pb', site: 'kb', gateway: true },
    { key: 'kc', podId: 'pc', site: 's2', gateway: true },
  ]
  assert.equal(nextGateway(after, prev, 's1', 'ka').key, 'kb')
  // The site id did not move: the elected gateway of that site is taken.
  assert.equal(nextGateway([{ key: 'kb', podId: 'pb', site: 's1', gateway: true }], prev, 's1', 'ka').key, 'kb')
  // Nobody of that site is online: another gateway carries the window.
  assert.equal(nextGateway([{ key: 'kc', podId: 'pc', site: 's2', gateway: true }], prev, 's1', 'ka').key, 'kc')
  assert.equal(nextGateway([], prev, 's1', 'ka'), null)
})

test('the base of a page names the pod it belongs to', () => {
  assert.equal(keyOf('/pod/kb/project/1'), 'kb')
  assert.equal(keyOf('/_lan/pod/kd'), 'kd')
  assert.equal(keyOf('/pod/kb'), 'kb')
  assert.equal(keyOf(''), '')
  assert.equal(keyOf('/app'), '')
})
