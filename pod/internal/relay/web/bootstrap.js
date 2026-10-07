// Bootstrap for the relay. It opens the WebRTC tunnel to the pod (chosen by #pod=),
// proves the token INSIDE the tunnel (channel-binding handshake, the token never goes
// to the relay) and then hands the app to the pod, which serves it through the tunnel.
// The Service Worker (sw.js) forwards the app's fetches to the pod as "http:<n>"
// channels; the rpc transport is passed to the app through a MessagePort.
const params = new URLSearchParams();
{ const h = location.hash.slice(1); if (h) new URLSearchParams(h).forEach((v, k) => params.set(k, v)); }
const pod = params.get("pod");
const token = params.get("t") || params.get("token") || "";

const status = (html) => { const el = document.getElementById("msg"); if (el) el.innerHTML = html; };
const fail = (m) => { status('<div class="err">' + m + "</div>"); };

function rand() { const b = new Uint8Array(16); crypto.getRandomValues(b); return [...b].map((x) => x.toString(16).padStart(2, "0")).join(""); }

// MAC(role, token, fpBrowser, fpPod, nonceBrowser, noncePod) -> hex HMAC-SHA1, same as the pod.
async function mac(role, fpB, fpP) {
  const key = await crypto.subtle.importKey("raw", new TextEncoder().encode(token), { name: "HMAC", hash: "SHA-1" }, false, ["sign"]);
  const sig = await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(role + "|" + fpB + "|" + fpP + "|" + nonceB + "|" + nonceP));
  return [...new Uint8Array(sig)].map((x) => x.toString(16).padStart(2, "0")).join("");
}

let nonceB = rand(), nonceP = "", pc;
const wait = (ms) => new Promise((r) => setTimeout(r, ms));

async function main() {
  if (!pod) return fail("No pod given. Open <relay>/?pod=<id>&t=<token>.");
  status("Connecting to <b>" + pod + "</b>…");
  // The Service Worker intercepts the app's fetches; register it first.
  try { await navigator.serviceWorker.register("/sw.js", { scope: "/" }); } catch (e) { fail("Service Worker: " + e.message); }

  // The pod's TURN/STUN ICE servers arrive with the signaling "ice" message, so connect
  // after we read it: open the signaling socket, get the ice, then build the PC.
  const sig = new WebSocket(wsURL("/_relay/signal?pod=" + encodeURIComponent(pod)));
  await openSig(sig);
  let ice = null;
  const sigWait = new Promise((res) => { sig.onmessage = (e) => { const m = JSON.parse(e.data); if (m.type === "ice") { ice = m; res(); } }; });
  await Promise.race([sigWait, wait(5000).then(() => { if (!ice) fail("This pod is not online at the relay."); })]);
  if (!ice) return;

  pc = new RTCPeerConnection({ iceServers: [{ urls: ice.urls, username: ice.username, credential: ice.credential }] });
  const rpc = pc.createDataChannel("rpc", { ordered: true, maxRetransmits: 0 });
  const chan = new MessageChannel();
  navigator.serviceWorker.controller && navigator.serviceWorker.controller.postMessage({ kind: "rpc-port", port: chan.port1 });

  rpc.onopen = () => {
    send(rpc, { protocol: "web-ide-rpc", version: "1", nonce: nonceB, method: "$hello" });
  };
  rpc.onmessage = async (e) => {
    const m = JSON.parse(e.data);
    if (m.nonce !== undefined && !nonceP) {
      nonceP = m.nonce;
      // The pod gave its nonce and proof; bind its fingerprint from the SDP and answer.
      const fpP = fingerprint(pc.remoteDescription);
      if (fpP) { /* binded into mac() */ }
      send(rpc, { method: "$auth", proof: await mac("browser", fingerprint(pc.localDescription), fpP) });
    } else if (m.method === "$ready") {
      status("Connected.");
      status('Loading the IDE…');
      location.replace("/app");
    } else if (m.error === "version") {
      fail("Update this pod: the versions do not match.");
    } else if (m.method === "$hello") {
      // versioned hello we did not expect
    }
  };
  pc.oniceconnectionstatechange = () => { if (pc.iceConnectionState === "failed") fail("The connection could not be established."); };
  // Create the offer toward the pod (the pod answers with its own SDP).
  const offer = await pc.createOffer();
  await pc.setLocalDescription(offer);
  sig.send(JSON.stringify({ type: "offer", sdp: pc.localDescription.sdp }));
  pc.ondatachannel = (dc) => { /* term:<id>, xfer, http channels are routed by the SW/app */ };
  // Route pod-side signaling (answer, candidates) back into the PC.
  sig.onmessage = (e) => {
    const m = JSON.parse(e.data);
    if (m.type === "answer") { pc.setRemoteDescription(new RTCSessionDescription({ type: "answer", sdp: m.sdp })); }
    else if (m.type === "candidate") { try { pc.addIceCandidate(JSON.parse(m.candidate)); } catch (_) {} }
    else if (m.type === "bye") { fail("This pod closed the connection."); }
  };
}

function send(dc, obj) { dc.send(JSON.stringify(obj)); }
function wsURL(p) { return (location.protocol === "https:" ? "wss:" : "ws:") + "//" + location.host + p; }
function fingerprint(desc) {
  if (!desc) return "";
  const m = desc.sdp.match(/a=fingerprint:(?:sha-256:)?([0-9a-fA-F]+)/);
  return m ? m[1].toUpperCase() : "";
}

main();
