// Short samples for the settings previews.
export const samples: Record<string, string> = {
  php: `<?php
namespace App\\Service;

#[AsService]
final class Invoice extends Document implements Payable
{
    public const RATE = 0.2;

    /** Total including taxes. */
    public function total(array $lines): float
    {
        $sum = array_sum(array_map(fn($l) => $l->price * $l->qty, $lines));
        return round($sum * (1 + self::RATE), 2); // TTC
    }
}
`,
  typescript: `import { createSignal } from 'solid-js'

interface User { id: number; name?: string }

export async function load(id: number): Promise<User | null> {
  const res = await fetch(\`/api/users/\${id}\`)
  if (!res.ok) return null // 404
  return (await res.json()) as User
}
`,
  javascript: `const re = /^[a-z]+$/i
export function greet(name = 'monde') {
  return \`Bonjour \${name}\` + (re.test(name) ? '!' : '?')
}
`,
  python: `@dataclass
class Point:
    """A point."""
    x: float = 0.0

    def norm(self) -> float:
        return math.sqrt(self.x ** 2)  # length
`,
  go: `package main

import "fmt"

type Server struct{ Addr string }

func (s *Server) Start() error {
	fmt.Println("listening on", s.Addr) // log
	return nil
}
`,
  nginx: `server {
    listen 443 ssl http2;
    server_name example.org;

    location ~* \\.php$ {
        fastcgi_pass unix:/run/php/php-fpm.sock;
        include fastcgi_params;
    }
}
`,
  sql: `-- active users
SELECT u.id, u.name, count(o.id) AS orders
FROM users u
LEFT JOIN orders o ON o.user_id = u.id
WHERE u.created_at > '2026-01-01'
GROUP BY u.id
LIMIT 50;
`,
  redis: `HGETALL session:42
SET counter 10 EX 60
SCAN 0 MATCH user:* COUNT 100
`,
  json: `{ "name": "web-ide", "version": 1, "private": true, "tags": ["ide", null] }
`,
  css: `.editor:focus-within { color: var(--fg); margin: 0 4px; }
`,
  html: `<!doctype html>
<a href="/docs" class="link">Docs</a> &amp; <!-- note -->
`,
  markdown: `# Title
- **bold** and *italic*, \`code\`
[link](https://example.org)
`,
  shell: `#!/bin/sh
for f in "$@"; do echo "file: $f"; done
`,
  yaml: `services:
  db:
    image: postgres:17 # version
    ports: ["5432:5432"]
`,
}
