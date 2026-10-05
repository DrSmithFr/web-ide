# Security policy

## Threat model

The pod runs with the rights of the user who starts it: it reads and writes files, runs shell commands, opens SSH connections and database connections on behalf of the web page. Protecting access to the pod is therefore essential.

- The pod listens on `127.0.0.1:4433` by default and rejects connections from other machines.
- Every HTTP request and the WebSocket require the pairing token (stored in an HTTP-only, `SameSite=Strict` cookie after opening the URL printed by the pod). The token is in `~/.web-ide/token`; delete the file and restart the pod to rotate it.
- The WebSocket only accepts same-origin connections, so another website open in the browser cannot reach the pod.
- To reach the pod from other devices, prefer `tailscale serve` (`scripts/install.sh --tailscale`): the pod still listens on the loopback only, Tailscale brings HTTPS and limits access to the devices of your tailnet, and the token is still required.
- With `-allow-remote`, the token is the only protection and traffic is plain HTTP: use it on a trusted network only, or behind a reverse proxy with TLS.
- Database and model server passwords never reach the page; remembered passwords are stored in `~/.web-ide/secrets.json` (mode 0600, not encrypted).
- The AI assistant runs shell commands without confirmation in Build mode. Only connect it to model servers you trust.

## Reporting a vulnerability

Please report vulnerabilities privately through [GitHub security advisories](https://github.com/DrSmithFr/web-ide/security/advisories/new) rather than in a public issue. Include the version (`web-ide-pod -version`), the steps to reproduce and the impact. You should get an answer within a week.
