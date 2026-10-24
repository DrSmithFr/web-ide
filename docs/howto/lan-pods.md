# Pods on the local network

Make the pods of one account find each other on the LAN, with no relay, and open any of their projects from one home page.

The pods of one account ([account and devices](account-and-devices.md)) that see each other on the LAN form a **cluster**. Each paired pod opens a LAN port (`:4436`) and announces itself by mDNS (`_web-ide._tcp`). Only the pods of the account are listed: each one proves its certificate, bound to the TLS certificates of the link, and a revoked pod is refused and dropped. An unpaired pod opens no LAN port at all.

## Prerequisites

- Two or more pods on the same network, each [paired](account-and-devices.md) to the account (`web-ide-pod pair status`).
- Port 4436 reachable between them, and multicast (mDNS, UDP 5353) allowed on the network; otherwise type the addresses, see step 3.
- Not under WSL 2 NAT: see [Windows with WSL](../install/windows-wsl.md), *Networking*.

## Steps

1. Pair the pods. With no relay, on the new machine:

   ```sh
   web-ide-pod pair join            # waits on the LAN: approve it in Members, on localhost of a pod of the account
   web-ide-pod pair join --phrase   # no admin around: the phrase typed, the pod keeps its own key
   ```

2. Open the home page of any pod: it lists the projects of every pod of the account in one list, the last opened first, each with the badge of its pod. A project of another pod opens through a port this pod keeps for it: its page and its terminals come from the pod that holds it, and the home button leads back to the home page of the pod you entered through.
3. When mDNS does not find a pod (another subnet, multicast blocked), type its address (`192.168.1.20:4436`) in Settings > Remote access > Local network. The reason an address fails is shown next to it.
4. To revoke a pod with no relay: `web-ide-pod pair revoke <key>`, the phrase typed. The list spreads from pod to pod, and one made offline goes to the relay as soon as a pod reaches it.

The LAN listen address is `-lan` (or `lan.addr` in `config.json`); `-lan off` closes the port, and `-mdns=false` keeps only the addresses typed in Settings. With no relay, the Members panel (on `localhost`) is answered by the pod itself: it lists the devices it knows, shows the pods waiting to join with their fingerprint, and passes a revocation on to the other pods. Inviting a phone by QR still needs a relay.

## Check

- Home page: the projects of the other pods appear, with their badge, and one opens.
- Settings > Remote access > Local network shows the other pods, or the reason an address fails.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| A pod is not listed | Firewall: allow port 4436 between the pods. Another subnet or blocked multicast: type its address in Settings > Remote access > Local network. |
| A pod listens on no LAN port | It is not paired: `web-ide-pod pair status`; or it was started with `-lan off`. |
| A pod under WSL 2 is not found | NAT hides it from the LAN: use a relay, see [Windows with WSL](../install/windows-wsl.md). |
| A pod is listed *older version* or *newer version* | Its multi-pod protocol differs: its projects are listed without a link. Update it (below). |
| A revoked pod still appears | It is dropped when the revocation list reaches the other pods; check that they are online. |

## Update a pod from the home page

A pod running a release shows *Update this pod* on the home page: your browser signs the request with its device key, the pod checks it against its account and its revocation list, then runs the installer it installed itself (`scripts/install.sh`), outside the process it restarts. A pod that does not run as a unit answers with the command to run there by hand.

## Several pods, one entry point

With a relay, the pods of one account are listed on the home page of any of them, and the relay can send you to the right one.

With a relay, the pods of one account are listed on the home page of any of them, and the relay can send you to the right one. The pods that see each other on the local network form a **cluster**; the relay elects one **gateway** per cluster — the pod you marked *Preferred gateway* (Settings > Remote access > Pods of the account), else the one online the longest — and moves the role when it leaves. Opening the relay link with no `#pod=` goes to that gateway, and the browser remembers the cluster you used last.

Each pod publishes its list of projects **encrypted** under its own key; the relay caches the ciphertext and never sees a project name, and the pods read the lists themselves. So the home page shows the pods that are offline too, greyed with the time they last published, and a pod with no link of its own is carried to the relay by a peer of its cluster. A pod that has no sync key yet is listed with *waiting for the sync key*: the Members panel wraps the key for it.

A project opens with the front end of **the pod that holds it**, and the address never leaves the origin you opened: on the relay link its page lives under `/pod/<device key>/`, on `127.0.0.1` under `/_lan/pod/<device key>/`. A pod registered at the relay is reached directly, by its own tunnel; a pod with no link of its own is carried by the tunnel of its gateway, and a pod of another cluster, from `127.0.0.1`, is reached pod to pod. Every hop checks the certificates, and the pod at the end rechecks the browser behind a gateway: a revoked device is refused even when its gateway has not heard about it yet (the gateway does see the traffic it relays — end-to-end encryption of that hop is planned). If the gateway leaves, the browser reconnects through the new one within a few seconds, showing *Reconnecting through {name}…*: the page you had open keeps working, its terminals re-attach.

A device proves it holds its key when it registers with the relay (a signature on a nonce the relay gave), so a copied certificate opens nothing.

Each pod also says which **multi-pod protocol** it speaks. A pod of another one is listed with *older version* or *newer version* and is never routed to: its projects are listed without a link, the two would not understand each other. Update it as above.

## Share a service with your other pods

An HTTP service of one machine (Ollama, the web UI of a daemon) can be reached by the other pods of your account. On the home page of the pod that runs it, *Shared services* > *Share a service*: a name and the port it listens on, on this machine (`127.0.0.1`). Nothing is shared by default; the switch of a service turns it off without deleting it, and the pods attached to it are listed under it (since when, requests, bytes) with *Cut*, which detaches one. Only the pods of the account reach it, through the LAN or the relay — never a browser, nor a window of the pod.

On the other pods, *Services of your other pods* lists it with a switch: on, it listens on a port of that machine — the port of the service, the next free one when it is taken, or one you type — for this machine only, or for the local network (anyone there reaches it with no authentication). Your tools use `http://127.0.0.1:<port>` as if the service ran there; answers stream, WebSockets work. A badge shows the way it goes: **LAN** when the two pods see each other on the network, else **Relay · direct** or **Relay · TURN** (copied by the relay, counted in your quota), **idle** when nothing is open (the tunnel closes after 10 minutes without a request and opens again on the next one). A link stays on across restarts, with no window open; it says when its pod is offline (502 meanwhile), when the service is no longer shared, and when the other pod cut it (*Cut*: it turns off until you turn it on again).

## See also

[Account and devices](account-and-devices.md) · [Self-host a relay](self-host-relay.md) · [Windows with WSL](../install/windows-wsl.md) · [Relay design](../design/relay.md)
