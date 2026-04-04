# rbe_gocacheprog

A [GOCACHEPROG](https://go.dev/doc/go1.24#go-command) helper that stores Go build cache artifacts in a remote [REAPI v2](https://github.com/bazelbuild/remote-apis) Content Addressable Storage (CAS) and Action Cache.

This lets teams share compiled artifacts across machines via a remote cache server like [bazel-remote](https://github.com/buchgr/bazel-remote), [BuildBarn](https://github.com/buildbarn), or any REAPI v2 compatible server.

## How it works

```
cmd/go <-- JSON over stdin/stdout --> gocacheprog <-- gRPC --> REAPI v2 server
                                          |
                                     local disk cache
```

- **GET**: checks local disk cache, then remote CAS/AC. Downloads and caches on remote hit.
- **PUT**: writes to local disk cache, then asynchronously uploads to remote CAS/AC.
- **Graceful degradation**: if the remote is unavailable, operates in local-only mode.

## Requirements

- Go 1.24+ (uses the `GOCACHEPROG` protocol)
- A REAPI v2 compatible remote cache server

## Install

```bash
go install github.com/hacktohell/rbe_gocacheprog/cmd/gocacheprog@latest
```

Or build from source:

```bash
git clone https://github.com/hacktohell/rbe_gocacheprog.git
cd rbe_gocacheprog
make build
# binary at bin/gocacheprog
```

## Usage

Set the `GOCACHEPROG` environment variable to the binary path:

```bash
export GOCACHEPROG_TARGET="localhost:9092"
export GOCACHEPROG="$(which gocacheprog)"
go build ./...
```

That's it. All `go build`, `go test`, and `go install` commands will use gocacheprog automatically.

## Configuration

Configuration is loaded with precedence: **environment variables > config file > defaults**.

### Target format

The `target` field (and `GOCACHEPROG_TARGET` env var) accepts three forms:

| Format | TLS | Default port |
|---|---|---|
| `grpcs://host[:port]` | yes (system CAs) | 443 |
| `grpc://host[:port]` | no | 80 |
| `host:port` | from `tls` field | — |

### Environment variables

| Variable | Description | Default |
|---|---|---|
| `GOCACHEPROG_TARGET` | gRPC address of the REAPI server — see target format above (required) | - |
| `GOCACHEPROG_INSTANCE` | REAPI instance name | `""` |
| `GOCACHEPROG_CACHE_DIR` | Local disk cache directory | `~/.cache/gocacheprog` |
| `GOCACHEPROG_CACHE_SIZE_MB` | Local cache size limit in MB | `10240` (10 GB) |
| `GOCACHEPROG_TLS` | Enable TLS with system CAs for bare `host:port` targets (`true`/`1`) | - |
| `GOCACHEPROG_TLS_CERT` | Path to TLS client certificate (mTLS) | - |
| `GOCACHEPROG_TLS_KEY` | Path to TLS client key (mTLS) | - |
| `GOCACHEPROG_TLS_CA` | Path to custom TLS CA certificate | - |
| `GOCACHEPROG_AUTH_HEADER` | gRPC metadata key to send on every request (e.g. `x-buildbuddy-api-key`) | - |
| `GOCACHEPROG_AUTH_TOKEN` | Value for the auth header | - |
| `GOCACHEPROG_WORKERS` | Number of concurrent request workers | `GOMAXPROCS * 2` |
| `GOCACHEPROG_CONNECT_TIMEOUT` | gRPC connection timeout | `10s` |
| `GOCACHEPROG_REQUEST_TIMEOUT` | Per-request timeout | `60s` |
| `GOCACHEPROG_LOG_LEVEL` | Log level: `debug`, `info`, `warn`, `error` | `info` |
| `GOCACHEPROG_METRICS_ADDR` | Prometheus metrics listen address (e.g., `:9090`) | - (disabled) |

### Config file

Optional JSON config at `~/.config/gocacheprog/config.json`:

```json
{
  "target": "grpcs://build-cache.example.com",
  "instance_name": "default",
  "cache_size_mb": 20480,
  "auth_header": "authorization",
  "auth_token": "Bearer <token>",
  "tls_cert": "/path/to/cert.pem",
  "tls_key": "/path/to/key.pem",
  "tls_ca": "/path/to/ca.pem",
  "log_level": "info"
}
```

## Quick start with bazel-remote

Start a local bazel-remote instance:

```bash
docker run -d --name bazel-remote \
  -p 9092:9092 -p 8080:8080 \
  quay.io/bazel-remote/bazel-remote \
  --dir=/data --max_size=20 --storage_mode=uncompressed
```

Build with gocacheprog:

```bash
export GOCACHEPROG_TARGET="localhost:9092"
export GOCACHEPROG_INSTANCE=""
export GOCACHEPROG="$(which gocacheprog)"
go build ./...
```

## BuildBuddy

[BuildBuddy](https://www.buildbuddy.io/) is a hosted REAPI v2 remote cache that works out of the box with gocacheprog.

**1. Get your API key** from the [BuildBuddy settings page](https://app.buildbuddy.io/settings/).

**2. Create `~/.config/gocacheprog/config.json`:**

```json
{
  "target": "grpcs://remote.buildbuddy.io",
  "auth_header": "x-buildbuddy-api-key",
  "auth_token": "<YOUR_API_KEY>"
}
```

The `grpcs://` scheme automatically enables TLS with system CAs and sets port 443 — no extra TLS config needed.

**3. Enable gocacheprog:**

```bash
export GOCACHEPROG="$(which gocacheprog)"
go build ./...
```

Or via environment variables without a config file:

```bash
export GOCACHEPROG_TARGET="grpcs://remote.buildbuddy.io"
export GOCACHEPROG_AUTH_HEADER="x-buildbuddy-api-key"
export GOCACHEPROG_AUTH_TOKEN="<YOUR_API_KEY>"
export GOCACHEPROG="$(which gocacheprog)"
go build ./...
```

Build results and cache stats are visible in the [BuildBuddy UI](https://app.buildbuddy.io/invocation/).

## TLS

For mTLS connections to the remote cache:

```bash
export GOCACHEPROG_TARGET="build-cache.example.com:9092"
export GOCACHEPROG_TLS_CERT="/path/to/client-cert.pem"
export GOCACHEPROG_TLS_KEY="/path/to/client-key.pem"
export GOCACHEPROG_TLS_CA="/path/to/ca.pem"
```

## Metrics

Enable Prometheus metrics:

```bash
export GOCACHEPROG_METRICS_ADDR=":9090"
```

Metrics are served at `http://localhost:9090/metrics`.

## Benchmarks

Run the integration benchmark against Kubernetes source using docker-compose:

```bash
make bench-run
```

This builds kubectl with 4 cache scenarios and produces a comparison table:

| Scenario | Description |
|---|---|
| D | gocacheprog, both caches cold |
| C | gocacheprog, cold local + warm remote |
| A | default go cache, cold |
| B | default go cache, warm |

Sample results building `./cmd/kubectl/...` from Kubernetes v1.32.0 (AMD EPYC 7B13, 32 vCPU):

| Scenario | Wall Clock | User CPU | Sys CPU | Peak RSS |
|---|---|---|---|---|
| D - cacheprog, both cold | 34.8s | 248.3s | 61.6s | 1,081 MB |
| C - cacheprog, warm remote | **2.6s** | 1.7s | 1.3s | 67 MB |
| A - default go cache, cold | 21.3s | 242.7s | 58.0s | 460 MB |
| B - default go cache, warm | 0.4s | 1.4s | 1.3s | 76 MB |

Key takeaways:
- **C vs A**: remote cache is **~8x faster** than a cold compile
- **D vs A**: cold cacheprog adds ~13s overhead from gRPC upload
- **C vs B**: remote fetch is ~6x slower than local warm cache (network vs disk)

### Unit benchmarks

Run micro-benchmarks for individual components:

```bash
make bench
```

Sample results (AMD EPYC 7B13, 32 vCPU):

| Benchmark | ns/op | MB/s | allocs/op |
|---|---|---|---|
| ReaderReadGet | 1,347 | 60.9 | 7 |
| ReaderReadPut/1KiB | 11,849 | 128.8 | 11 |
| WriterWriteHit | 963 | 224.2 | 2 |
| DigestBytes/1MiB | 662,746 | 1,582.2 | 2 |
| DigestFile/1MiB | 771,037 | 1,360.0 | 8 |
| ComputeSyntheticDigests | 1,476 | - | 12 |
| CircuitBreakerAllowClosed | 6 | - | 0 |
| CircuitBreakerAllowParallel | 51 | - | 0 |
| ReadMetadata | 11,631 | - | 10 |
| DiskCacheInstall/1KiB | 1,390,534 | - | 33 |
| DiskCacheLookupHit | 14,414 | - | 14 |
| DiskCacheLookupMiss | 2,556 | - | 4 |
| DiskCacheLookupHitParallel | 4,841 | - | 14 |
| HandleGetLocalHit | 15,438 | - | 18 |
| HandleGetLocalMiss | 2,850 | - | 7 |
| PutThenGetRoundTrip | 1,413,824 | - | 60 |

Results are written to `bench/results/summary.txt`. Customize the build target:

```bash
K8S_BUILD_TARGET=./cmd/kubeadm/... make bench-run
```

Clean up:

```bash
make bench-clean
```

## Testing

```bash
go test ./...
```

## License

See [LICENSE](LICENSE) for details.
