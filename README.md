<p align="center">
  <b>WordBreak</b> — <a href="https://wordbreak-fe.vercel.app/">Live app</a> ·
  <a href="https://github.com/wordBr/wordbreak-fe">Frontend</a> ·
  <a href="https://github.com/wordBr/wordbreak-contracts">Contracts</a>
</p>

# WordBreak — Backend

**Live:** [wordbreak-backend-production.up.railway.app](https://wordbreak-backend-production.up.railway.app)
(`/health` for a liveness check)

Go service that is the **game engine**, the **referee**, and the **multiplayer room server**.
It validates words, generates racks, scores rounds server-authoritatively, tracks leaderboards,
signs the EIP-712 settlement results `WordBreakPools` verifies on-chain, and runs the poll-based
multiplayer race engine (public/private rooms, winner-takes-all staking).

## Why Go

Fast in-memory dictionary lookups (370k words), trivial concurrency for the API and the room
engine, and `go-ethereum` gives native EIP-712 signing that matches the contract byte-for-byte
(proven by `internal/signer` tests against the contract's own digest) plus the ability to
broadcast transactions (opening/settling staked rooms) directly from the backend.

## Layout

```
cmd/server            entrypoint (env config, graceful shutdown)
internal/dictionary   embedded English word list + validation / word-finding
internal/rack         known-good rack generation (solo random, daily deterministic)
internal/game         server-authoritative scoring (the referee's judgment)
internal/signer       EIP-712 settlement signing (matches WordBreakPools)
internal/chain        read-only entry verification + write-capable tx broadcasting
internal/room         multiplayer race engine (rooms, live standings, auto-settlement)
internal/store        in-memory daily rounds + leaderboards (swap for Postgres later)
internal/api          HTTP handlers + routing
```

## Run

```bash
# Game only (no signing, no staking, no paid daily):
go run ./cmd/server

# With the referee + chain verification (enables settlement signing + paid dailies):
REFEREE_PRIVATE_KEY=0x...   \
POOLS_CONTRACT=0x...        \
CHAIN_ID=42220               \
CHAIN_RPC_URL=https://forno.celo.org \
ADMIN_TOKEN=change-me       \
go run ./cmd/server

# Also enable staked multiplayer rooms (needs a funded operator key):
OPERATOR_PRIVATE_KEY=0x... go run ./cmd/server   # (in addition to the above)
```

### Environment

| Var | Default | Purpose |
|---|---|---|
| `PORT` | `8080` | HTTP port (Railway/most PaaS inject this automatically) |
| `REFEREE_PRIVATE_KEY` | — | Referee signer key. Omit to run the game without signing. |
| `POOLS_CONTRACT` | — | Deployed `WordBreakPools` **proxy** address (EIP-712 `verifyingContract`). Required if signing. |
| `CHAIN_ID` | `42220` | 42220 = Celo mainnet, 11142220 = Celo Sepolia. |
| `CHAIN_RPC_URL` | — | RPC for on-chain entry verification. Required for paid dailies / staked rooms. |
| `OPERATOR_PRIVATE_KEY` | — | Funded key that broadcasts `createRound`/`settle` for **staked multiplayer rooms**. Must be the pool's owner or referee. Omit to keep staked rooms disabled (free rooms still work). |
| `ADMIN_TOKEN` | — | Protects `/api/admin/*`. Omit to disable admin routes. |
| `SOLO_RACK_SIZE` | `6` | Solo rack size (4–8). |
| `DAILY_RACK_SIZE` | `6` | Daily rack size (4–8). |
| `RACE_SECONDS` | `60` | Multiplayer race duration. |

> **Security:** the referee and operator keys control who gets paid and can move real funds.
> Keep them in your host's secrets manager (e.g. Railway's variable store), never in the repo,
> and put `/api/admin/*` behind network controls in production.

## Deploy (Railway)

```bash
railway login          # one-time, opens a browser
railway init            # create/link a project (run from this backend/ folder)
railway up               # deploy the current directory
railway domain           # generate a public URL
```

A `nixpacks.toml` at the repo root tells Railway's builder how to build the Go binary (the
entrypoint is `cmd/server`, not a root-level `main.go`). Set the environment variables above in
the Railway dashboard (or `railway variables set KEY=value`) — none are required for the service
to boot; the game runs with signing/staking disabled until they're set.

**Live config reference** (Celo mainnet): `POOLS_CONTRACT=0x8eF9AA2ccc401A1146eCDa6605A02cc1A72e3F3a`
(the [proxy address](https://github.com/wordBr/wordbreak-contracts) — always use the proxy,
never the implementation), `CHAIN_ID=42220`, `CHAIN_RPC_URL=https://forno.celo.org`.

## API

### Game

| Method | Path | Notes |
|---|---|---|
| GET | `/health` | liveness + dictionary size |
| GET | `/api/referee` | referee address (to configure the contract) |
| GET | `/api/solo/rack?size=6` | fresh solo rack (answers withheld) |
| POST | `/api/solo/score` | `{letters, words[]}` → scored result |
| GET | `/api/daily` | today's shared rack (deterministic per date) |
| POST | `/api/daily/submit` | `{address, words[]}` → scores vs the server's rack. **For a paid round, gated on on-chain `hasEntered` and rejected after `endTime`.** |
| GET | `/api/daily/leaderboard?date=YYYY-MM-DD` | ranked standings |
| POST | `/api/admin/daily/open` | `{roundId, endTime, dateKey?}` → register today's on-chain round so paid submissions are gated (needs `X-Admin-Token`) |
| POST | `/api/admin/sign-settlement` | `{roundId, winners[], amounts[]}` → referee signature (needs `X-Admin-Token`) |

### Multiplayer rooms

| Method | Path | Notes |
|---|---|---|
| POST | `/api/room/create` | `{playerId, name, public, stake}` → new room. `stake` is wei, `"0"` = free. Staked rooms need `playerId` to be a wallet address and `OPERATOR_PRIVATE_KEY` configured. |
| POST | `/api/room/join` | `{code, playerId, name}` → join. Staked rooms require on-chain `hasEntered` first (verified server-side). |
| POST | `/api/room/start` | `{code, playerId}` → host starts the race (deals the shared rack). |
| POST | `/api/room/submit` | `{code, playerId, word}` → scores one word, returns live room state. |
| GET | `/api/room/{code}?you=playerId` | poll for live standings (client polls ~every 1.5s). |
| GET | `/api/room/list` | open public rooms (lobby state, not full) — the "join anyone" browser. |

Staked rooms settle **automatically**: when the race ends, the backend signs and broadcasts a
winner-takes-all payout via the same `WordBreakPools` contract the daily pool uses — no new
contract, no manual step.

### Fund safety

Paid submissions are **gated on on-chain entry**: `/api/daily/submit` refuses any address that
didn't call `enter()`, and refuses submissions after `endTime`. Without this, an unpaid address
could be scored, top the leaderboard, and be signed as a winner — draining the honest pot.
`sign-settlement` re-checks `hasEntered` for every winner as defense in depth. The same principle
gates multiplayer room `join` for staked rooms.

### The operator round lifecycle (daily pool)

1. **Open on-chain** — run the `CreateRound` forge script (`contracts/script/CreateRound.s.sol`)
   with `roundId` = a `YYYYMMDD` date key, an entry fee, and an `endTime`.
2. **Register with the backend** — `POST /api/admin/daily/open` with the same `roundId` + `endTime`.
   Now paid submissions are gated.
3. **Players** enter (approve + `enter`), play, submit scores (gated).
4. **Settle** — after `endTime`: compute winners + amounts from the leaderboard,
   `POST /api/admin/sign-settlement` to get the referee signature, then submit
   `pool.settle(roundId, winners, amounts, signature)` on-chain. Winners `claim()`.

## Anti-cheat status (MVP)

Scoring is server-authoritative (the client can't self-report a win). The **open risk** noted in
the concept doc still stands: a paid mode that rewards raw word count is beatable by an anagram
solver. Before enabling real-money daily pools or staked rooms at scale, add speed/typing-cadence
signals, small capped stakes, and/or account staking. Tracked, not yet built.

## Dictionary note

Uses the full `words_alpha` English list (~370k words), which includes obscure entries. A
frequency-filtered "common words" pass is a good follow-up so players don't lose to words nobody
knows.

## Test

```bash
go test ./...
```

The load-bearing test is `internal/signer.TestDigestMatchesContract`: it asserts the Go referee
produces the **exact** digest the Solidity contract computes for identical inputs.
