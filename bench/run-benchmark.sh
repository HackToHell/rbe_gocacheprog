#!/usr/bin/env bash
set -euo pipefail

# Support space-separated list of build targets; default to kubectl + kubeadm.
BUILD_TARGETS="${K8S_BUILD_TARGETS:-./cmd/kubectl/... ./cmd/kubeadm/...}"
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

# target_slug converts a build target like ./cmd/kubectl/... to "kubectl"
target_slug() {
    local t="$1"
    # extract the last non-... path component
    echo "$t" | sed 's|/\.\.\.$||' | sed 's|.*/||'
}

run_build() {
    local scenario="$1"
    local use_cacheprog="$2"
    local target="$3"
    local slug
    slug=$(target_slug "$target")
    local timefile="$RESULTS_DIR/${scenario}.${slug}.time"

    echo "=== Scenario $scenario ($slug) ==="

    if [ "$use_cacheprog" = "yes" ]; then
        local logfile="$RESULTS_DIR/${scenario}.${slug}.gocacheprog.log"
        local profilefile="$RESULTS_DIR/${scenario}.${slug}.cpu.pprof"
        > "$logfile"
        local wrapper
        wrapper=$(create_wrapper "$logfile")
        export GOCACHEPROG="$wrapper"
        export GOCACHEPROG_CPUPROFILE="$profilefile"
        echo "  Using gocacheprog (logs -> $logfile, profile -> $profilefile)"
    else
        unset GOCACHEPROG 2>/dev/null || true
        unset GOCACHEPROG_CPUPROFILE 2>/dev/null || true
        echo "  Using default go cache"
    fi

    cd "$K8S_DIR"
    /usr/bin/time -v go build "$target" 2>"$timefile" || {
        echo "  Build failed! See $timefile for details:"
        cat "$timefile"
        return 1
    }
    unset GOCACHEPROG_CPUPROFILE 2>/dev/null || true
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

# Populate remote cache for a given target (not timed).
populate_remote() {
    local target="$1"
    local slug
    slug=$(target_slug "$target")
    echo ""
    echo "============================================"
    echo "  Populating remote cache for $slug (not timed)"
    echo "============================================"
    clean_go_cache
    clean_local_cacheprog
    local wrapper
    wrapper=$(create_wrapper "/dev/null")
    export GOCACHEPROG="$wrapper"
    unset GOCACHEPROG_CPUPROFILE 2>/dev/null || true
    cd "$K8S_DIR"
    go build "$target"
    echo "  Remote cache populated for $slug."
}

# ============================================================
# Main benchmark loop over each build target
# ============================================================
for BUILD_TARGET in $BUILD_TARGETS; do
    SLUG=$(target_slug "$BUILD_TARGET")
    echo ""
    echo "############################################"
    echo "  Target: $BUILD_TARGET  (slug: $SLUG)"
    echo "############################################"

    # ---------- Scenario D: gocacheprog, both caches cold ----------
    echo ""
    echo "============================================"
    echo "  Scenario D ($SLUG): gocacheprog, both caches cold"
    echo "============================================"
    clean_go_cache
    clean_local_cacheprog
    run_build "D" "yes" "$BUILD_TARGET"

    # ---------- Populate remote cache ----------
    populate_remote "$BUILD_TARGET"

    # ---------- Scenario C: gocacheprog, cold local, warm remote ----------
    echo ""
    echo "============================================"
    echo "  Scenario C ($SLUG): gocacheprog, warm remote"
    echo "============================================"
    clean_go_cache
    clean_local_cacheprog
    run_build "C" "yes" "$BUILD_TARGET"

    # ---------- Scenario A: default go cache, cold ----------
    echo ""
    echo "============================================"
    echo "  Scenario A ($SLUG): default go cache, cold"
    echo "============================================"
    clean_go_cache
    run_build "A" "no" "$BUILD_TARGET"

    # ---------- Scenario B: default go cache, warm ----------
    echo ""
    echo "============================================"
    echo "  Scenario B ($SLUG): default go cache, warm"
    echo "============================================"
    # No cleaning - reuse warm cache from A
    run_build "B" "no" "$BUILD_TARGET"
done

# ---------- Summary ----------
echo ""
echo "============================================"
echo "  Results Summary"
echo "============================================"
echo ""

{
    echo "Benchmark Results - $(date -u '+%Y-%m-%d %H:%M:%S UTC')"
    echo "Build targets: $BUILD_TARGETS"
    echo ""
} | tee "$RESULTS_DIR/summary.txt"

for BUILD_TARGET in $BUILD_TARGETS; do
    SLUG=$(target_slug "$BUILD_TARGET")
    {
        echo "--- Target: $BUILD_TARGET ---"
        echo ""
        printf "%-12s | %-20s | %-10s | %-10s | %-15s\n" \
            "Scenario" "Wall Clock" "User CPU" "Sys CPU" "Peak RSS (KB)"
        printf "%-12s-+-%-20s-+-%-10s-+-%-10s-+-%-15s\n" \
            "------------" "--------------------" "----------" "----------" "---------------"

        for scenario in D C A B; do
            timefile="$RESULTS_DIR/${scenario}.${SLUG}.time"
            if [ -f "$timefile" ]; then
                IFS='|' read -r wall user sys rss <<< "$(parse_time "$timefile")"
                printf "%-12s | %-20s | %-10s | %-10s | %-15s\n" \
                    "$scenario" "$wall" "$user" "$sys" "$rss"
            else
                printf "%-12s | %-20s\n" "$scenario" "NO DATA"
            fi
        done

        echo ""
        echo "Cacheprog Stats:"
        printf "%-12s | %-10s | %-10s | %-10s\n" "Scenario" "GETs" "PUTs" "Misses"
        printf "%-12s-+-%-10s-+-%-10s-+-%-10s\n" \
            "------------" "----------" "----------" "----------"

        for scenario in D C; do
            logfile="$RESULTS_DIR/${scenario}.${SLUG}.gocacheprog.log"
            IFS='|' read -r gets puts misses <<< "$(parse_cacheprog_stats "$logfile")"
            printf "%-12s | %-10s | %-10s | %-10s\n" "$scenario" "$gets" "$puts" "$misses"
        done
        echo ""
    } | tee -a "$RESULTS_DIR/summary.txt"
done

echo "" | tee -a "$RESULTS_DIR/summary.txt"
{
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
} | tee -a "$RESULTS_DIR/summary.txt"

# ---------- CPU Profile Analysis ----------
echo ""
echo "============================================"
echo "  CPU Profile Analysis (go tool pprof -top)"
echo "============================================"
echo "" | tee -a "$RESULTS_DIR/summary.txt"
echo "=== CPU Profile Analysis ===" | tee -a "$RESULTS_DIR/summary.txt"
echo "" | tee -a "$RESULTS_DIR/summary.txt"

for pprof_file in "$RESULTS_DIR"/*.cpu.pprof; do
    [ -f "$pprof_file" ] || continue
    label=$(basename "$pprof_file" .cpu.pprof)
    echo "--- Profile: $label ---" | tee -a "$RESULTS_DIR/summary.txt"
    go tool pprof -top -nodecount=20 "$pprof_file" 2>/dev/null | tee -a "$RESULTS_DIR/summary.txt" || \
        echo "  (profile empty or gocacheprog exited before sampling)" | tee -a "$RESULTS_DIR/summary.txt"
    echo "" | tee -a "$RESULTS_DIR/summary.txt"
done

echo ""
echo "Results written to $RESULTS_DIR/summary.txt"
echo "Raw pprof files available in $RESULTS_DIR/*.cpu.pprof"
