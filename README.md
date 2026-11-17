# Revoke console API (.go)

Control plane for the Revoke client's remote console. Plain Go standard library, deployed as Vercel
Go functions at `https://api.revoke.wtf`.

## Where things are configured

`ServerData.go` in this folder is the file you edit:

```go
var Accounts = []Account{
    {DisplayName: "TLine", Username: "poop656", Commands: []string{"on", "off", "notify", "broadcast"}},
    {DisplayName: "Lain", Username: "lain", Commands: []string{"on", "off", "notify"}},
}

var EnabledCommands = []string{"on", "off", "notify", "broadcast"}
```

- **DisplayName** is what other players see on broadcasts and notifications.
- **Username** is the Minecraft account the entry belongs to; the client sends it and the API only
  accepts it together with that account's token.
- **Commands** limits one account to a subset of `EnabledCommands`; leave it empty for all of them.
  Comment a command out of `EnabledCommands` to switch it off for everybody.

### How a request is authorized

Listing a Minecraft account is all it takes to let that person use the console — no per-account
setup. The client sends `X-Console-User` with the name it is playing on and `X-Console-Session` with
its live Minecraft login; the API asks Minecraft's profile service who that login belongs to and
compares the answer with the name. Only the real owner of the account can pass, so a public
whitelist, or somebody who has the source, still cannot post a command as `lain`.

Successful checks are cached for ten minutes, so polling every two seconds does not hit Minecraft's
service on every request. The session token itself is never stored, logged or written to the event
store — only a fingerprint is kept as the cache key.

A per-account token (`CONSOLE_TOKEN_<USERNAME>`, or whatever `TokenEnv` names) is accepted as a
fallback for launchers that hand the game no live login. It is optional and lives only in the
environment, never in this repository.

The `user` field in a command body is ignored entirely: the authenticated account is used for
permissions and as the broadcast signature.

## Endpoints

| Method | Path          | Auth | Purpose                                            |
| ------ | ------------- | ---- | -------------------------------------------------- |
| GET    | `/v1/health`  | no   | Liveness probe                                     |
| GET    | `/v1/whoami`  | yes  | `{"allowed":true,"name":"<DisplayName>"}` or `401` |
| POST   | `/v1/command` | yes  | `{"action":"on\|off\|notify\|broadcast","target":"name","payload":"..."}` |
| GET    | `/v1/events`  | yes  | Commands addressed to the caller, `?since=<cursor>` |

Missing or wrong token, or an unknown name: `401`. Signed in but that account may not use the
command: `403`. Malformed request: `400`. Flooding: `429`. Broadcasts reach every client and carry
`target:"-"`.

## Configuration

| Variable                     | Required  | Meaning                                            |
| ---------------------------- | --------- | -------------------------------------------------- |
| `CONSOLE_TOKEN_<USERNAME>`   | optional  | Fallback token for that account (`openssl rand -hex 32`) |
| `KV_REST_API_URL`            | on Vercel | Vercel KV / Upstash REST URL                       |
| `KV_REST_API_TOKEN`          | on Vercel | Matching REST token                                |

Serverless instances do not share memory, so the shared store is required on Vercel: a command
stored by one instance would otherwise never reach a poll landing on another. `UPSTASH_*` names work
too. Without the variables the API keeps events in memory, which is fine for local runs.

## Run locally

```bash
CONSOLE_TOKEN_POOP656=dev-token go run ./cmd/server
curl http://127.0.0.1:8080/v1/health
go test ./...
```

The server prints a warning for every account whose token is missing.

## Deploy

The repository is [`github.com/BenjYahu/Console`](https://github.com/BenjYahu/Console) and this
module sits at its root, so import that repository in Vercel with the default root directory and add
the token (and KV) variables — nothing else to configure.

```bash
vercel            # preview
vercel --prod     # production
```

`vercel.json` maps `/v1/health`, `/v1/whoami`, `/v1/command` and `/v1/events` onto the Go functions
in `api/`, so the client's hardcoded `api.revoke.wtf/v1/...` URLs keep working. Point the
`api.revoke.wtf` domain at the project in Vercel → Settings → Domains.

Client side: nothing to configure when the launcher provides a Minecraft login — the client sends
it automatically. For a launcher that does not, put that account's fallback token in
`run/console.token` (one line, nothing else) or in `REVOKE_CONSOLE_TOKEN`.
