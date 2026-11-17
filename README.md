# Revoke console API (.go)

Control plane for the Revoke client's remote console. Plain Go standard library, deployed as Vercel
Go functions at `https://api.revoke.wtf`.

Clients never hold a password. Each account proves itself with

```
X-Console-User:  <Minecraft name>
X-Console-Token:  hex(HMAC-SHA256(console secret, lower(Minecraft name)))
```

so the secret stays on the client machine and in the server's environment; nothing usable leaks
from the source, and the account name alone gets you nothing.

## Endpoints

| Method | Path          | Auth | Purpose                                            |
| ------ | ------------- | ---- | -------------------------------------------------- |
| GET    | `/v1/health`  | no   | Liveness probe                                     |
| GET    | `/v1/whoami`  | yes  | `{"allowed":true,"name":"..."}` for whitelisted names |
| POST   | `/v1/command` | yes  | `{"action":"on\|off\|notify\|broadcast","target":"name","payload":"..."}` |
| GET    | `/v1/events`  | yes  | Commands addressed to the caller, `?since=<cursor>` |

Refused requests answer `401` (bad or missing token, or a name that is not whitelisted), malformed
ones `400`, and flooding an account `429`. `broadcast` events reach every client and carry
`target:"-"`.

## Configuration

Set these in Vercel → Project → Settings → Environment Variables (or in a local `.env`, which is
git-ignored):

| Variable              | Required | Meaning                                                        |
| --------------------- | -------- | -------------------------------------------------------------- |
| `CONSOLE_SECRET`      | yes      | Shared secret clients derive their token from (`openssl rand -hex 32`) |
| `CONSOLE_ACCOUNTS`    | yes      | Whitelist: `poop656,lain` or `poop656:Display,lain`             |
| `KV_REST_API_URL`     | on Vercel| Vercel KV / Upstash REST URL                                    |
| `KV_REST_API_TOKEN`   | on Vercel| Matching REST token                                             |

Serverless instances do not share memory, so the shared store is required on Vercel: a command
stored by one instance would otherwise never reach a poll that lands on another. `UPSTASH_*` names
work too. Without the variables the API keeps events in memory, which is fine for local runs.

## Run locally

```bash
CONSOLE_SECRET=dev-secret CONSOLE_ACCOUNTS=poop656,lain go run ./cmd/server
curl http://127.0.0.1:8080/v1/health
go test ./...
```

## Deploy

```bash
vercel            # preview
vercel --prod     # production
```

`vercel.json` maps `/v1/health`, `/v1/whoami`, `/v1/command` and `/v1/events` onto the Go
functions in `api/`, so the client's hardcoded `api.revoke.wtf/v1/...` URLs keep working. Point the
`api.revoke.wtf` domain at the project in Vercel → Settings → Domains.

On the client side the same secret goes in `run/console.secret` (one line, nothing else) or in the
`REVOKE_CONSOLE_SECRET` environment variable. Without it the console commands refuse to run instead
of sending unauthenticated requests.
