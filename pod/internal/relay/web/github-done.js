// The window GitHub sends back to after a link (github.go): it tells the panel that opened it,
// and closes on its button. A file of its own: the policy of the relay runs no inline script.
const done = document.getElementById('done')
try { localStorage.setItem('webide.github.done', done.dataset.id) } catch {}
if (window.opener) window.opener.postMessage({ type: 'webide.github', login: done.dataset.login }, location.origin)
document.getElementById('close').addEventListener('click', () => window.close())
