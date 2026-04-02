FROM golang:1.26-bookworm

ARG K8S_VERSION=v1.32.0

RUN apt-get update && apt-get install -y --no-install-recommends \
    jq time curl git && \
    rm -rf /var/lib/apt/lists/*

# Build gocacheprog
WORKDIR /src/gocacheprog
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /usr/local/bin/gocacheprog ./cmd/gocacheprog

# Clone k8s at pinned tag and pre-download modules
WORKDIR /src
RUN git clone --depth 1 --branch ${K8S_VERSION} \
    https://github.com/kubernetes/kubernetes.git k8s
WORKDIR /src/k8s
RUN go mod download

COPY bench/run-benchmark.sh /usr/local/bin/run-benchmark.sh
RUN chmod +x /usr/local/bin/run-benchmark.sh

ENTRYPOINT ["run-benchmark.sh"]
