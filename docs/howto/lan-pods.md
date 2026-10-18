# Pods on the local network

The pods of one account ([account and devices](account-and-devices.md)) find each other and form a **cluster**.

## Other pods on the same network, with no relay

The pods paired with one account find each other on the local network by themselves, with no relay and no internet: each paired pod opens a LAN port (`:4436`, `-lan` or `lan.addr` in `config.json`, `-lan off` to close it) and announces itself by mDNS. The home page then lists the projects of every pod of the account in one list, the last opened first, each with the badge of its pod; a project of another pod opens through a port this pod keeps for it, so its page and its terminals come from the pod that holds it, and the home button always leads back to the home page of the pod you entered through. Only the pods of the account are listed: each one proves its certificate, bound to the TLS certificates of the link, and a revoked pod is refused and dropped. An unpaired pod opens no LAN port at all.

When mDNS does not find a pod (another subnet, multicast blocked), type its address (`192.168.1.20:4436`) in Settings > Remote access > Local network; the reason an address fails is shown next to it.

```sh
web-ide-pod pair join            # no relay: waits on the LAN, approve it in Devices on localhost
web-ide-pod pair join --phrase   # no admin around: the phrase typed, the pod keeps its own key
web-ide-pod pair revoke <key>    # the phrase typed: the list spreads from pod to pod
```

With no relay, the Devices panel (on `localhost`) is answered by the pod itself: it lists the devices it knows, shows the pods waiting to join on the LAN with their fingerprint, and passes a revocation on to the other pods. A list made offline goes to the relay as soon as a pod of the account reaches it, so a later revocation never undoes it. Inviting a phone by QR still needs the relay.

## Several pods, one entry point

With a relay, the pods of one account are listed on the home page of any of them, and the relay can send you to the right one. The pods that see each other on the local network form a **site**; the relay elects one **gateway** per site — the pod you marked *Preferred gateway* (Settings > Remote access > Pods of the account), else the one online the longest — and moves the role when it leaves. Opening the relay link with no `#pod=` goes to that gateway, and the browser remembers the site you used last.

Each pod publishes its list of projects **encrypted** under its own key; the relay caches the ciphertext and never sees a project name, and the pods read the lists themselves. So the home page shows the pods that are offline too, greyed with the time they last published, and a pod with no link of its own is carried to the relay by a peer of its site. A pod that has no sync key yet is listed with *waiting for the sync key*: the Devices panel wraps the key for it.

A project opens with the front end of **the pod that holds it**, and the address never leaves the origin you opened: on the relay link its page lives under `/pod/<device key>/`, on `127.0.0.1` under `/_lan/pod/<device key>/`. A pod registered at the relay is reached directly, by its own tunnel; a pod with no link of its own is carried by the tunnel of its gateway, and a pod of another site, from `127.0.0.1`, is reached pod to pod. Every hop checks the certificates, and the pod at the end rechecks the browser behind a gateway: a revoked device is refused even when its gateway has not heard about it yet (the gateway does see the traffic it relays — end-to-end encryption of that hop is planned). If the gateway leaves, the browser reconnects through the new one within a few seconds, showing *Reconnecting through {name}…*: the page you had open keeps working, its terminals re-attach.

A device proves it holds its key when it registers with the relay (a signature on a nonce the relay gave), so a copied certificate opens nothing.

Each pod also says which **multi-pod protocol** it speaks. A pod of another one is listed with *older version* or *newer version* and is never routed to: its projects are listed without a link, the two would not understand each other. When it is that pod to update, the home page of a pod running a release shows *Update this pod*: your browser signs the request with its device key, the pod it names checks it against the account it pinned and its revocation list, then runs the installer it installed itself (`scripts/install.sh`), outside the process it restarts. A pod that does not run as the service answers with the command to run there by hand.
