#!/usr/bin/env bash
set -euo pipefail

BUILD_TARGET="${K8S_BUILD_TARGET:-./cmd/kubectl/...}"
RESULTS_DIR="/results"
K8S_DIR="/src/k8s"
LOCAL_CACHE_DIR="${GOCACHEPROG_CACHE_DIR:-/tmp/gocacheprog-cache}"

mkdir -p "$RESULTS_DIR"

# Wait for bazel-remote to be ready
REMOTE_HOST="${GOCACHEPROG_TARGET:-bazel-remote:9092}"
REMOTE_HTTP_HOST="${REMOTE_HOST%%:*}:8080"
echo "Waiting for bazel-remote at $REMOTE_HTTP_HOST..."
for i in $(seq 1 30); do
    if curl -sf "http://$REMOTE_HTTP_HOST/status" >/dev/null 2>&1; then
        echo "bazel-remote is ready."
        break
    fi
    if [ "$i" -eq 30 ]; then
        echo "ERROR: bazel-remote not ready after 30 attempts"
        exit 1
    fi
    sleep 1
done

# Create a wrapper script that redirects gocacheprog stderr to a log file
# while preserving stdin/stdout for the go tool protocol.
create_wrapper() {
    local logfile="$1"
    local wrapper="/tmp/gocacheprog-wrapper.sh"
    cat > "$wrapper" <<WRAPPER
#!/usr/bin/env bash
exec /usr/local/bin/gocacheprog 2>>"$logfile"
WRAPPER
    chmod +x "$wrapper"
    echo "$wrapper"
}

# Parse GNU time -v output and extract key metrics.
parse_time() {
    local timefile="$1"
    local wall user sys rss
    wall=$(grep "Elapsed (wall clock)" "$timefile" | sed 's/.*: //')
    user=$(grep "User time" "$timefile" | sed 's/.*: //')
    sys=$(grep "System time" "$timefile" | sed 's/.*: //')
    rss=$(grep "Maximum resident" "$timefile" | sed 's/.*: //')
    echo "$wall|$user|$sys|$rss"
}

# Parse gocacheprog JSON logs for hit/miss counts.
parse_cacheprog_stats() {
    local logfile="$1"
    if [ ! -f "$logfile" ] || [ ! -s "$logfile" ]; then
        echo "n/a|n/a|n/a"
        return
    fi
    local gets puts misses
    gets=$(jq -s '[.[] | select(.msg == "get")] | length' "$logfile" 2>/dev/null || echo "n/a")
    puts=$(jq -s '[.[] | select(.msg == "put")] | length' "$logfile" 2>/dev/null || echo "n/a")
    misses=$(jq -s '[.[] | select(.msg == "get" and .miss == true)] | length' "$logfile" 2>/dev/null || echo "n/a")
    echo "$gets|$puts|$misses"
}

run_build() {
    local scenario="$1"
    local use_cacheprog="$2"
    local timefile="$RESULTS_DIR/${scenario}.time"

    echo "=== Scenario $scenario ==="

    if [ "$use_cacheprog" = "yes" ]; then
        local logfile="$RESULTS_DIR/${scenario}.gocacheprog.log"
        > "$logfile"
        local wrapper
        wrapper=$(create_wrapper "$logfile")
        export GOCACHEPROG="$wrapper"
        echo "  Using gocacheprog (logs -> $logfile)"
    else
        unset GOCACHEPROG 2>/dev/null || true
        echo "  Using default go cache"
    fi

    cd "$K8S_DIR"
    /usr/bin/time -v go build $BUILD_TARGET 2>"$timefile" || {
        echo "  Build failed! See $timefile for details:"
        cat "$timefile"
        return 1
    }
    echo "  Done."
}

clean_go_cache() {
    echo "  Cleaning go build cache..."
    go clean -cache
}

clean_local_cacheprog() {
    echo "  Cleaning local gocacheprog cache..."
    rm -rf "$LOCAL_CACHE_DIR"
    mkdir -p "$LOCAL_CACHE_DIR"
}

# ---------- Scenario D: gocacheprog, both caches cold ----------
echo ""
echo "============================================"
echo "  Scenario D: gocacheprog, both caches cold"
echo "============================================"
clean_go_cache
clean_local_cacheprog
run_build "D" "yes"

# ---------- Populate remote cache ----------
echo ""
echo "============================================"
echo "  Populating remote cache (not timed)"
echo "============================================"
clean_go_cache
clean_local_cacheprog
wrapper=$(create_wrapper "/dev/null")
export GOCACHEPROG="$wrapper"
cd "$K8S_DIR"
go build $BUILD_TARGET
echo "  Remote cache populated."

# ---------- Scenario C: gocacheprog, cold local, warm remote ----------
echo ""
echo "============================================"
echo "  Scenario C: gocacheprog, warm remote"
echo "============================================"
clean_go_cache
clean_local_cacheprog
run_build "C" "yes"

# ---------- Scenario A: default go cache, cold ----------
echo ""
echo "============================================"
echo "  Scenario A: default go cache, cold"
echo "============================================"
clean_go_cache
run_build "A" "no"

# ---------- Scenario B: default go cache, warm ----------
echo ""
echo "============================================"
echo "  Scenario B: default go cache, warm"
echo "============================================"
# No cleaning - reuse warm cache from A
run_build "B" "no"

# ---------- Summary ----------
echo ""
echo "============================================"
echo "  Results Summary"
echo "============================================"
echo ""

printf "%-12s | %-20s | %-10s | %-10s | %-15s\n" \
    "Scenario" "Wall Clock" "User CPU" "Sys CPU" "Peak RSS (KB)"
printf "%-12s-+-%-20s-+-%-10s-+-%-10s-+-%-15s\n" \
    "------------" "--------------------" "----------" "----------" "---------------"

for scenario in D C A B; do
    timefile="$RESULTS_DIR/${scenario}.time"
    if [ -f "$timefile" ]; then
        IFS='|' read -r wall user sys rss <<< "$(parse_time "$timefile")"
        printf "%-12s | %-20s | %-10s | %-10s | %-15s\n" \
            "$scenario" "$wall" "$user" "$sys" "$rss"
    else
        printf "%-12s | %-20s\n" "$scenario" "NO DATA"
    fi
done

echo ""

# Cacheprog stats for scenarios that used it
echo "Cacheprog Stats:"
printf "%-12s | %-10s | %-10s | %-10s\n" "Scenario" "GETs" "PUTs" "Misses"
printf "%-12s-+-%-10s-+-%-10s-+-%-10s\n" \
    "------------" "----------" "----------" "----------"

for scenario in D C; do
    logfile="$RESULTS_DIR/${scenario}.gocacheprog.log"
    IFS='|' read -r gets puts misses <<< "$(parse_cacheprog_stats "$logfile")"
    printf "%-12s | %-10s | %-10s | %-10s\n" "$scenario" "$gets" "$puts" "$misses"
done

echo ""
echo "Legend:"
echo "  A = Default go cache, cold (baseline full compile)"
echo "  B = Default go cache, warm (best-case local)"
echo "  C = gocacheprog, cold local + warm remote (remote fetch)"
echo "  D = gocacheprog, both cold (full compile + remote upload)"
echo ""
echo "Key comparisons:"
echo "  C < A  => remote cache provides speedup"
echo "  D ~ A  => upload overhead is small (async)"
echo "  B << A => local cache baseline"

# Write summary to file
{
    echo "Benchmark Results - $(date -u '+%Y-%m-%d %H:%M:%S UTC')"
    echo "Build target: $BUILD_TARGET"
    echo ""
    printf "%-12s | %-20s | %-10s | %-10s | %-15s\n" \
        "Scenario" "Wall Clock" "User CPU" "Sys CPU" "Peak RSS (KB)"
    printf "%-12s-+-%-20s-+-%-10s-+-%-10s-+-%-15s\n" \
        "------------" "--------------------" "----------" "----------" "---------------"
    for scenario in D C A B; do
        timefile="$RESULTS_DIR/${scenario}.time"
        if [ -f "$timefile" ]; then
            IFS='|' read -r wall user sys rss <<< "$(parse_time "$timefile")"
            printf "%-12s | %-20s | %-10s | %-10s | %-15s\n" \
                "$scenario" "$wall" "$user" "$sys" "$rss"
        fi
    done
} > "$RESULTS_DIR/summary.txt"

echo ""
echo "Results written to $RESULTS_DIR/summary.txt"
