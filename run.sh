#!/usr/bin/env bash
#
# run.sh - manage the long-running hailo-ollama-cli apps as background jobs.
#
# Apps:
#   webui   Browser chat UI + live NPU panel        (default addr :8080)
#   serve   Mock Hailo-Ollama API server            (needs MOCK=1, default :8000;
#           the real API server is a system service, see 'sudo systemctl')
#
# Usage:
#   ./run.sh <start|stop|restart|status> [webui|serve|all]
#   ./run.sh logs [-f] [webui|serve]
#   ./run.sh build
#
# Without an app argument the command applies to all (serve is skipped for
# 'start'/'restart' unless MOCK=1).
#
# Configuration (environment variables):
#   WEBUI_ADDR      Listen address for the chat UI        (default ":8888")
#   WEBUI_METRICS   Optional metrics.jsonl path for webui (default: backend default)
#   SERVE_ADDR      Bind address for the mock server      (default "localhost:8000")
#   MOCK            Set to 1 to pass --mock to the apps   (default off)
#   NO_BUILD        Set to 1 to skip the automatic rebuild (default off)
#
# PID files and logs live in .run/ next to this script.

set -u

APP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$APP_DIR/bin"
BIN="$BIN_DIR/hailo-ollama-cli"
RUN_DIR="$APP_DIR/.run"

WEBUI_ADDR="${WEBUI_ADDR:-:8888}"
WEBUI_METRICS="${WEBUI_METRICS:-}"
SERVE_ADDR="${SERVE_ADDR:-localhost:8000}"
MOCK="${MOCK:-0}"
NO_BUILD="${NO_BUILD:-0}"

pidfile() { printf '%s/%s.pid' "$RUN_DIR" "$1"; }
logfile() { printf '%s/%s.log' "$RUN_DIR" "$1"; }

# True when $1 has a live process behind its pidfile. The /proc cmdline check
# avoids treating a recycled PID as our app (Linux-only; falls back silently).
is_running() {
    local pf pid
    pf="$(pidfile "$1")"
    [ -f "$pf" ] || return 1
    pid="$(cat "$pf" 2>/dev/null)"
    [ -n "$pid" ] || return 1
    kill -0 "$pid" 2>/dev/null || return 1
    if [ -r "/proc/$pid/cmdline" ]; then
        tr '\0' ' ' < "/proc/$pid/cmdline" | grep -q hailo || return 1
    fi
    return 0
}

build() {
    echo "Building $(basename "$BIN") ..."
    mkdir -p "$BIN_DIR"
    (cd "$APP_DIR" && go build -ldflags "-s -w" -o "$BIN" .) || {
        echo "ERROR: build failed" >&2
        return 1
    }
}

# Rebuild when the binary is missing or any source/static asset is newer.
need_build() {
    [ "$NO_BUILD" = "1" ] && return 1
    [ ! -x "$BIN" ] && return 0
    find "$APP_DIR" \
        \( -path "$APP_DIR/dist" -o -path "$APP_DIR/.git" \
           -o -path "$APP_DIR/bin" -o -path "$APP_DIR/.run" \) -prune -o \
        \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' \
           -o -path "$APP_DIR/cli/static/*" \) \
        -newer "$BIN" -print -quit 2>/dev/null | grep -q . && return 0
    return 1
}

start_app() {
    local app="$1" log pf
    if is_running "$app"; then
        echo "$app: already running (pid $(cat "$(pidfile "$app")"))"
        return 0
    fi
    mkdir -p "$RUN_DIR"

    local args=()
    [ "$MOCK" = "1" ] && args+=(--mock)

    local addr
    case "$app" in
        webui)
            addr="$WEBUI_ADDR"
            args+=(webui "$WEBUI_ADDR")
            [ -n "$WEBUI_METRICS" ] && args+=("$WEBUI_METRICS")
            ;;
        serve)
            if [ "$MOCK" != "1" ]; then
                echo "serve: the real Hailo-Ollama API server is managed by systemd," >&2
                echo "       so this script only manages the mock server. Start it with:" >&2
                echo "         MOCK=1 ./run.sh start serve" >&2
                return 1
            fi
            addr="$SERVE_ADDR"
            args+=(-H "$SERVE_ADDR" serve)
            ;;
        *)
            echo "ERROR: unknown app '$app' (expected webui or serve)" >&2
            return 1
            ;;
    esac

    if need_build; then
        build || return 1
    fi

    # Fail fast with a readable message when another process owns the port.
    local port="${addr##*:}"
    if [ -n "$port" ] && ss -ltn 2>/dev/null | awk '{print $4}' | grep -q ":$port\$"; then
        echo "$app: port $port is already in use by another process."
        echo "       Stop it first, or choose a different address:"
        case "$app" in
            webui)  echo "         WEBUI_ADDR=:9090 ./run.sh start webui" ;;
            serve)  echo "         SERVE_ADDR=localhost:9000 MOCK=1 ./run.sh start serve" ;;
        esac
        return 1
    fi

    log="$(logfile "$app")"
    pf="$(pidfile "$app")"
    nohup "$BIN" "${args[@]}" >>"$log" 2>&1 &
    local pid=$!
    echo "$pid" >"$pf"

    # Give the process a moment to die on a bad port/config before claiming success.
    sleep 0.7
    if ! kill -0 "$pid" 2>/dev/null; then
        echo "$app: failed to start (see $log):" >&2
        tail -n 5 "$log" >&2
        rm -f "$pf"
        return 1
    fi
    echo "$app: started (pid $pid, log $log)"
}

stop_app() {
    local app="$1" pf pid i
    pf="$(pidfile "$app")"
    if ! is_running "$app"; then
        [ -f "$pf" ] && rm -f "$pf" && echo "$app: removed stale pidfile"
        echo "$app: not running"
        return 0
    fi
    pid="$(cat "$pf")"
    kill "$pid" 2>/dev/null
    for i in $(seq 1 25); do
        kill -0 "$pid" 2>/dev/null || break
        sleep 0.2
    done
    if kill -0 "$pid" 2>/dev/null; then
        echo "$app: did not exit after SIGTERM, sending SIGKILL"
        kill -9 "$pid" 2>/dev/null
        sleep 0.3
    fi
    rm -f "$pf"
    echo "$app: stopped"
}

status_app() {
    local app="$1" pf pid
    if is_running "$app"; then
        pid="$(cat "$(pidfile "$app")")"
        printf '%-8s running  (pid %s, up %s)\n' "$app" "$pid" \
            "$(ps -o etime= -p "$pid" 2>/dev/null | tr -d ' ')"
    else
        printf '%-8s stopped\n' "$app"
    fi
}

usage() {
    awk 'NR >= 3 { if (! /^#/) exit; sub(/^# ?/, ""); print }' "$0"
}

cmd="${1:-}"
[ $# -gt 0 ] && shift
case "$cmd" in
    start|stop|restart|status)
        target="${1:-all}"
        case "$target" in
            all) apps=(webui serve) ;;
            webui|serve) apps=("$target") ;;
            *) echo "ERROR: unknown app '$target'" >&2; exit 1 ;;
        esac
        rc=0
        for app in "${apps[@]}"; do
            # Plain './run.sh start' should just work: don't try the mock
            # server unless it was asked for explicitly.
            if [ "$target" = "all" ] && [ "$app" = "serve" ] && [ "$MOCK" != "1" ]; then
                if [ "$cmd" != "stop" ]; then
                    echo "serve: skipped (set MOCK=1 to include the mock server)"
                    continue
                fi
            fi
            case "$cmd" in
                start)   start_app "$app" || rc=1 ;;
                stop)    stop_app "$app" ;;
                restart) stop_app "$app"; start_app "$app" || rc=1 ;;
                status)  status_app "$app" ;;
            esac
        done
        exit "$rc"
        ;;
    logs)
        follow=""
        if [ "${1:-}" = "-f" ] || [ "${1:-}" = "--follow" ]; then
            follow="-f"
            shift
        fi
        app="${1:-webui}"
        case "$app" in
            webui|serve) ;;
            *) echo "ERROR: unknown app '$app'" >&2; exit 1 ;;
        esac
        tail $follow -n "${TAIL_LINES:-50}" "$(logfile "$app")"
        ;;
    build)
        build
        ;;
    ""|-h|--help|help)
        usage
        ;;
    *)
        echo "ERROR: unknown command '$cmd'" >&2
        usage >&2
        exit 1
        ;;
esac
