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

### Environment variables

| Variable | Description | Default |
|---|---|---|
| `GOCACHEPROG_TARGET` | gRPC address of the REAPI server (required) | - |
| `GOCACHEPROG_INSTANCE` | REAPI instance name | `juls` |
| `GOCACHEPROG_CACHE_DIR` | Local disk cache directory | `~/.cache/gocacheprog` |
| `GOCACHEPROG_CACHE_SIZE_MB` | Local cache size limit in MB | `10240` (10 GB) |
| `GOCACHEPROG_TLS_CERT` | Path to TLS client certificate | - |
| `GOCACHEPROG_TLS_KEY` | Path to TLS client key | - |
| `GOCACHEPROG_TLS_CA` | Path to TLS CA certificate | - |
| `GOCACHEPROG_WORKERS` | Number of concurrent request workers | `GOMAXPROCS * 2` |
| `GOCACHEPROG_CONNECT_TIMEOUT` | gRPC connection timeout | `10s` |
| `GOCACHEPROG_REQUEST_TIMEOUT` | Per-request timeout | `60s` |
| `GOCACHEPROG_LOG_LEVEL` | Log level: `debug`, `info`, `warn`, `error` | `info` |
| `GOCACHEPROG_METRICS_ADDR` | Prometheus metrics listen address (e.g., `:9090`) | - (disabled) |

### Config file

Optional JSON config at `~/.config/gocacheprog/config.json`:

```json
{
  "target": "build-cache.example.com:9092",
  "instance_name": "default",
  "cache_size_mb": 20480,
  "tls_cert": "/path/to/cert.pem",
  "tls_key": "/path/to/key.pem",
  "tls_ca": "/path/to/ca.pem",
  "log_level": "info",
  "metrics_addr": ":9090"
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

Sample results building `./cmd/kubectl/...` from Kubernetes v1.32.0:

| Scenario | Wall Clock | User CPU | Sys CPU | Peak RSS |
|---|---|---|---|---|
| D - cacheprog, both cold | 36.8s | 243.4s | 62.2s | 1,110 MB |
| C - cacheprog, warm remote | **2.0s** | 1.5s | 1.3s | 61 MB |
| A - default go cache, cold | 21.8s | 238.6s | 58.1s | 411 MB |
| B - default go cache, warm | 0.4s | 1.4s | 1.2s | 80 MB |

Key takeaways:
- **C vs A**: remote cache is **10x faster** than a cold compile
- **D vs A**: cold cacheprog adds ~15s overhead from gRPC upload
- **C vs B**: remote fetch is ~5x slower than local warm cache (network vs disk)

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
