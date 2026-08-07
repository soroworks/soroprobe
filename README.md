# SoroProbe

A health and simulation checker for Stellar/Soroban smart contracts.

Before you invoke a Soroban contract for real, you want to know three things:
will this call succeed, what will it cost, and is the contract's state actually
healthy — or is it about to expire and start failing? Today that means ad-hoc
CLI simulation and manual reasoning about TTLs.

SoroProbe packages it. It dry-runs contract calls, shows you the decoded result
and the resource cost, and reports how close a contract's on-ledger state is to
expiring, in terms you can act on.

SoroProbe is **read-only**. It simulates and inspects. It never submits a
transaction, and it never needs a secret key.

```
$ soroprobe check CCLV77FYLRJMBGTILTKVIM76SI6JY56H7KTT47HE26UF4F265UYRDSR4

contract CCLV77FYLRJMBGTILTKVIM76SI6JY56H7KTT47HE26UF4F265UYRDSR4

  PASS  deployed               instance found, executable is wasm
  WARN  instance_ttl           live for 58912 more ledgers (~3.4 days, until ledger 4073187)
  WARN  code_ttl               live for 24268 more ledgers (~1.4 days, until ledger 4038543)
  SKIP  simulate               no function given; pass one to probe a read-only call

result PASS
checked at ledger 4014275
```

That contract's Wasm code expires **two days before** its instance does. Reading
only one of the two would have looked fine.

## Contents

- [Versions this targets](#versions-this-targets)
- [Install](#install)
- [Quickstart](#quickstart)
- [The state expiration model](#the-state-expiration-model)
- [CLI reference](#cli-reference)
- [Argument encoding](#argument-encoding)
- [HTTP API](#http-api)
- [Configuration](#configuration)
- [Why no secret key](#why-no-secret-key)
- [Design](#design)
- [What SoroProbe deliberately does not do](#what-soroprobe-deliberately-does-not-do)

## Versions this targets

Soroban's simulation and state model change between protocol versions, so the
specifics below were verified against a live network rather than taken from
memory. Verified **2026-08-07**:

| Component | Version | Notes |
| --- | --- | --- |
| Go SDK | `github.com/stellar/go-stellar-sdk` v0.7.1 | The former `github.com/stellar/go` is **deprecated and archived**; the Go tool itself reports `module github.com/stellar/go is deprecated: Use github.com/stellar/go-stellar-sdk instead`. |
| Go | 1.25 | Required by the SDK. `GOTOOLCHAIN=auto` lets an older local `go` fetch it. |
| Stellar RPC | `simulateTransaction`, `getLedgerEntries`, `getLatestLedger` | Accessed through the SDK's `clients/rpcclient`. |
| Network | Testnet, **protocol 27** | Ledger close time measured at 5.00s/ledger over 19 consecutive ledgers. |

Two details that commonly trip up code written against older docs:

- **`simulateTransaction` no longer returns a top-level `cost` object.** The old
  `cost.cpuInsns` / `cost.memBytes` pair is gone. Resource figures now come from
  decoding the returned `transactionData` into a `SorobanTransactionData`.
- **`readBytes` was renamed `diskReadBytes`** in protocol 23. SoroProbe reports
  it under the current name.

To re-verify against the live network after a protocol upgrade, run
`make fixtures` and re-run the suite.

## Install

```bash
go install github.com/soroworks/soroprobe/cmd/soroprobe@latest
```

Or build from source:

```bash
git clone https://github.com/soroworks/soroprobe
cd soroprobe
make build      # produces ./bin/soroprobe
```

Or with Docker:

```bash
docker build -t soroprobe .
docker run --rm soroprobe inspect CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC
```

If your local Go is older than 1.25, nothing needs to change: `GOTOOLCHAIN=auto`
is Go's default and will fetch the right toolchain on first build.

## Quickstart

Everything below works against the public testnet with no configuration. The
contract used is the testnet native-asset contract.

**Simulate a call:**

```bash
soroprobe simulate CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC decimals
```

```
contract CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC
function decimals

SUCCESS  the call would succeed

return value
  7

resource cost
  cpu instructions   127569
  disk read bytes    0
  write bytes        0
  resource fee       12279 stroops (0.0012279 XLM)
  min resource fee   12279 stroops (0.0012279 XLM)

footprint
  read  contract instance CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC (persistent)

simulated against ledger 4014077
```

**Inspect its state health:**

```bash
soroprobe inspect CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC
```

**Run a CI-friendly check:**

```bash
soroprobe check CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC --fn decimals
echo $?    # 0 pass, 1 unhealthy, 2 SoroProbe could not run
```

**Get JSON for scripting:**

```bash
soroprobe simulate CDLZ... decimals --json | jq '.cost.instructions'
```

## The state expiration model

This is the part SoroProbe exists to make legible, so it is worth stating
precisely.

Every Soroban contract instance, code entry and data entry carries a **TTL**: a
ledger sequence number recorded in an associated TTL ledger entry. Stellar RPC
surfaces it as `liveUntilLedgerSeq` on each `getLedgerEntries` result.

An entry is live while:

```
liveUntilLedgerSeq >= currentLedgerSeq
```

so the remaining lifetime, in ledgers, is `liveUntilLedgerSeq - currentLedgerSeq`.
Zero means it expires with the current ledger. Negative means it already has.

**What expiry costs depends on durability, and the difference matters:**

| Storage | On expiry | Recoverable |
| --- | --- | --- |
| **Temporary** | Deleted outright | **No — gone permanently** |
| **Persistent** | Archived | Yes, via restoration |
| **Instance** | Archived | Yes, via restoration |
| **Code** (Wasm) | Archived | Yes, via restoration |

SoroProbe says which of these applies in every report, because "expiring in two
days" means something very different for a temporary entry than a persistent one.

**How SoroProbe classifies a TTL:**

| Status | Meaning |
| --- | --- |
| `healthy` | More remaining ledgers than the warning threshold |
| `warning` | At or below the warning threshold (default 120,960 ledgers ≈ 7 days) |
| `critical` | At or below the critical threshold (default 17,280 ledgers ≈ 1 day) |
| `expired` | TTL has already lapsed; archived, or gone if temporary |
| `missing` | No such entry exists on the ledger |
| `unknown` | Entry exists but reported no TTL |

Thresholds are configurable with `--warn-ledgers` and `--critical-ledgers`.

**A note on the time estimates.** SoroProbe converts ledgers to wall-clock time
at the network's five-second target close time, which measured exactly 5.00s
when verified. Real close times drift slightly, so treat the **ledger count as
authoritative** and the duration as a convenience.

**Three things worth knowing:**

1. **A contract's instance and its code expire independently.** A Wasm contract
   can have a comfortable instance TTL and code that expires days sooner, or the
   reverse. `inspect` and `check` always report both, because checking only one
   gives you false confidence.
2. **Instance storage shares the instance's single TTL.** Every value in it
   lives and dies with the instance entry, and one `extend_ttl` call covers all
   of them.
3. **Stellar Asset Contracts have no code entry.** They are implemented by the
   host itself, so there is nothing to expire. SoroProbe reports that explicitly
   rather than leaving a blank.

**Archived entries and simulation.** When a simulated call touches archived
entries, RPC returns a `restorePreamble`. SoroProbe surfaces this as
`RESTORE REQUIRED`, along with the fee a restoration would cost. It reports the
restoration; it does not perform one.

## CLI reference

### `soroprobe simulate <contract-id> <function> [args...]`

Builds an invocation, simulates it, and reports the outcome, decoded return
value, resource cost and ledger footprint.

A call the contract rejects is **not** a tool error — it exits 0 and reports
`FAILED` with the host's error text. Use `check` if you want a non-zero exit.

### `soroprobe inspect <contract-id>`

Reads the contract's instance and code entries and reports expiration health.

| Flag | Description |
| --- | --- |
| `--key <spec>` | A contract data key to inspect, in `type:value` form. Repeatable. |
| `--durability <d>` | Durability for `--key` lookups: `persistent` (default) or `temporary`. |

Stellar RPC reads ledger entries **by key** and offers no way to enumerate the
data entries a contract owns. Listing them all would require an indexer, which a
stateless tool has no business depending on — so data entries you care about are
named explicitly with `--key`.

### `soroprobe check <contract-id>`

Combined health check, designed for CI.

| Flag | Description |
| --- | --- |
| `--fn <name>` | A read-only function to simulate as a liveness probe. |
| `--arg <spec>` | Argument for `--fn`, in `type:value` form. Repeatable. |
| `--key <spec>` | A contract data key to include in the TTL check. Repeatable. |
| `--durability <d>` | Durability for `--key` lookups. |

Checks run in order: `deployed`, `instance_ttl`, `code_ttl`, any `data_ttl`, then
`simulate`.

**Exit codes:**

| Code | Meaning |
| --- | --- |
| 0 | All checks passed |
| 1 | A check failed — the contract has a problem |
| 2 | SoroProbe could not run — bad input, or RPC unreachable |

1 and 2 are kept distinct so a pipeline can tell "your contract is unhealthy"
from "the tool broke".

A **TTL warning does not fail the check.** It is a signal to extend the entry,
not a reason to break a build. Only `critical`, `expired` and `missing` fail.

`--fn` is optional by design. SoroProbe cannot discover a contract's exported
functions from the ledger, and guessing a name would produce a failure that
looks like a broken contract but is really a broken guess. Without `--fn` the
simulation step is honestly reported as `SKIP`.

### `soroprobe serve`

Runs the HTTP API. `--addr` overrides `HTTP_ADDR`.

### Global flags

`--json`, `--config`, `--rpc-url`, `--network-passphrase`, `--source-account`,
`--log-level`, `--timeout`, `--warn-ledgers`, `--critical-ledgers`.

## Argument encoding

Arguments use a `type:value` form:

```bash
soroprobe simulate CDLZ... transfer \
  addr:GABC...  addr:CDEF...  i128:10000000
```

| Type | Example |
| --- | --- |
| `bool` | `bool:true` |
| `void`, `null` | `void` |
| `u32`, `i32` | `u32:5`, `i32:-5` |
| `u64`, `i64` | `u64:100` |
| `u128`, `i128` | `i128:-10000000` |
| `u256`, `i256` | `u256:12345` |
| `timepoint`, `duration` | `timepoint:1700000000` |
| `sym`, `symbol` | `sym:transfer` |
| `str`, `string` | `str:"hello world"` |
| `bytes` | `bytes:deadbeef`, `bytes:0xdeadbeef` |
| `addr`, `address` | `addr:GABC...`, `addr:CDEF...` |

**Inference.** A bare value with no prefix is inferred: `true`/`false` become
bool, `void`/`null` become void, a valid `G...`/`C...` address becomes an
address, a run of digits becomes **i128**, and anything else becomes a symbol if
it fits Soroban's symbol rules (≤32 chars of `[a-zA-Z0-9_]`) or a string
otherwise.

Prefer explicit prefixes. Bare integers infer `i128` because it is the widest
common integer type in Soroban token interfaces, but a contract expecting `u32`
will reject it with a confusing host error.

**Results** are decoded to JSON-friendly values. Integers wider than 32 bits
become decimal **strings**, not JSON numbers — a `u64` or `i128` cannot survive
a round trip through a float64, and silently mangling a token balance would be
worse than a quoted string. Bytes become hex; addresses become strkey.

## HTTP API

`soroprobe serve` exposes the CLI over HTTP for CI systems and other services.
It is read-only; there is no route that submits anything.

### `GET /healthz`

Liveness. Deliberately does **not** call out to the network, so the probe does
not fail because an upstream RPC endpoint blipped.

```json
{"status": "ok"}
```

### `POST /v1/simulate`

```bash
curl -s localhost:8080/v1/simulate -d '{
  "contract_id": "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC",
  "function": "decimals",
  "args": []
}' | jq
```

### `GET /v1/inspect/{contract}`

Query parameters: `key` (repeatable), `durability`.

### `GET /v1/check/{contract}`

Query parameters: `fn`, `arg` (repeatable), `key` (repeatable), `durability`.

### Status codes

| Code | Meaning |
| --- | --- |
| 200 | The probe ran. **Read `success` / `ok` in the body for the verdict.** |
| 400 | Bad input — malformed contract id, bad argument, unknown durability |
| 404 | No such route |
| 502 | Upstream RPC failed |

A contract that fails its check still returns **200**: the request succeeded, it
just carries bad news. Returning 5xx would conflate an unhealthy contract with a
broken service, and monitoring cannot tell those apart after the fact.

## Configuration

Precedence, lowest to highest: **defaults → config file → environment → flags**.

| Environment variable | Flag | Default |
| --- | --- | --- |
| `RPC_URL` | `--rpc-url` | `https://soroban-testnet.stellar.org` |
| `NETWORK_PASSPHRASE` | `--network-passphrase` | `Test SDF Network ; September 2015` |
| `SOURCE_ACCOUNT` | `--source-account` | all-zero placeholder |
| `HTTP_ADDR` | `--addr` (on `serve`) | `:8080` |
| `LOG_LEVEL` | `--log-level` | `info` |
| `RPC_TIMEOUT` | `--timeout` | `30s` |
| `WARN_LEDGERS` | `--warn-ledgers` | `120960` (≈7 days) |
| `CRITICAL_LEDGERS` | `--critical-ledgers` | `17280` (≈1 day) |
| `SOROPROBE_CONFIG` | `--config` | `./soroprobe.json` if present |

[.env.example](.env.example) documents every variable. SoroProbe reads the
**environment**, not `.env` itself, so load it yourself:

```bash
set -a && . ./.env && set +a
docker run --env-file .env soroprobe check <contract>
```

A config file, by contrast, is read directly. It is JSON with snake_case keys:

```json
{
  "rpc_url": "https://soroban-testnet.stellar.org",
  "warn_ledgers": 120960,
  "critical_ledgers": 17280
}
```

## Why no secret key

Simulation does not require a signature. The RPC server does not verify one, and
it never broadcasts the transaction.

Building a transaction does require a **source account**, so SoroProbe asks for
a public key — and only a public key. `Validate` rejects anything that is not a
valid `G...` ed25519 public key, so pasting a secret key in fails immediately
rather than quietly working.

The account does not need to exist or hold a balance. The default is an all-zero
placeholder, which is why the quickstart runs with no setup at all. Set
`SOURCE_ACCOUNT` to a real account only when a contract authorizes on the
invoker's address and you want the simulation to reflect that.

The transaction SoroProbe builds is unsigned, uses sequence number 0, and is
never submitted. There is a test asserting the envelope carries no signatures.

## Design

```
cmd/soroprobe     cobra CLI and output rendering
internal/config   defaults, config file and environment resolution
internal/stellar  Stellar RPC client behind an interface, ledger keys, tx building
  └── stellartest fixture-backed fake + the recorder that captures fixtures
internal/scval    ScVal encode/decode behind an interface, with a type registry
internal/health   TTL interpretation: thresholds, statuses, durability semantics
internal/probe    simulate / inspect / check orchestration
internal/api      chi HTTP handlers mirroring the CLI
```

Two seams exist so the core is testable and replaceable:

- **`stellar.Client`** is an interface over the RPC methods SoroProbe uses. The
  live implementation wraps the SDK client; tests use a fixture-backed fake.
- **`scval.Codec`** is an interface over argument encoding and result decoding.
  The default `Registry` maps type names to `EncodeFunc`s, so supporting a new
  type is one function and one `Register` call.

The CLI and the HTTP API both render the same result structs from `probe`, so
the two interfaces cannot drift apart.

**Tests never touch the network.** They replay responses recorded from the live
testnet into `internal/stellar/stellartest/testdata`. The fake resolves ledger
entries by key and simulations by decoding the transaction envelope it is
handed, so a test exercising the fake also exercises the real ledger-key and
transaction-building code rather than trusting it. Refresh fixtures with
`make fixtures`.

## What SoroProbe deliberately does not do

- **Submit transactions.** SoroProbe is simulation and inspection only. There is
  no code path that broadcasts, and no route in the API that could.
- **Deploy contracts.**
- **Restore archived entries.** It reports that a restore is required, and the
  fee, and leaves the decision to you.
- **Serve a web UI.**
- **Persist anything.** SoroProbe is stateless and queries the chain live. No
  database, by design.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Adding an ScVal type or an RPC method is
a small, well-bounded change; both are documented there.

## License

Apache-2.0. See [LICENSE](LICENSE).
