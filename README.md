# Revoke console API (.go)

Control plane for the Revoke client's remote console. Plain Go standard library, deployed as Vercel
Go functions at `https://revoke.wtf/api`.

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
| GET    | `/api/v1/health`  | no   | Liveness probe                                |
| GET    | `/api/v1/whoami`  | yes  | `{"allowed":true,"name":"<DisplayName>"}` or `401` |
| POST   | `/api/v1/command` | yes  | `{"action":"on\|off\|notify\|broadcast","target":"name","payload":"..."}` |
| GET    | `/api/v1/events`  | yes  | Commands addressed to the caller, `?since=<cursor>` |

The older `/v1/...` paths stay aliased to the same functions.

Missing or wrong token, or an unknown name: `401`. Signed in but that account may not use the
command: `403`. Malformed request: `400`. Flooding: `429`. Broadcasts reach every client and carry
`target:"-"`.

## Configuration

| Variable                     | Required  | Meaning                                            |
| ---------------------------- | --------- | -------------------------------------------------- |
| `MONGO_URI`                  | on Vercel | MongoDB connection string, e.g. `mongodb+srv://user:pass@cluster/revoke` |
| `CONSOLE_TOKEN_<USERNAME>`   | optional  | Fallback token for that account (`openssl rand -hex 32`) |

Serverless instances do not share memory, so the shared database is required on Vercel: a command
stored by one instance would otherwise never reach a poll landing on another. Without `MONGO_URI`
the API keeps events in memory, which is fine for local runs.

### MongoDB layout

Two collections in the database named by the URI path (`revoke` when the URI names none):

- `console_events` — one document per command: `id`, `from`, `action`, `target`, `targetKey`,
  `payload`, `time`, `createdAt`. Its `id` comes from a counter document so ids stay ordered across
  instances; a TTL index on `createdAt` drops commands once they expire.
- `console_counters` — the `events` counter that hands out those ids.

Indexes are created once per instance in the background, so the API works on an empty database with
no migration step. Use a database user limited to this database; the API only inserts events,
reads them and increments the counter.

## Run locally

```bash
CONSOLE_TOKEN_POOP656=dev-token go run ./cmd/server
curl http://127.0.0.1:8080/api/v1/health
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

`vercel.json` maps `/api/v1/health`, `/api/v1/whoami`, `/api/v1/command` and `/api/v1/events` onto the
Go functions in `api/`, matching the client's hardcoded `revoke.wtf/api/v1/...` URLs. Point
`revoke.wtf` at the project in Vercel → Settings → Domains; the four Go functions live under
`/api/…`, so no other rewrite is needed.

Client side: nothing to configure when the launcher provides a Minecraft login — the client sends
it automatically. For a launcher that does not, put that account's fallback token in
`run/console.token` (one line, nothing else) or in `REVOKE_CONSOLE_TOKEN`.

`go.mod` and `go.sum` both need to be in the repository: Vercel downloads the MongoDB driver from
them when it builds the functions.
