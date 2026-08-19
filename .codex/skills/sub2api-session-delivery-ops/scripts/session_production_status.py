#!/usr/bin/env python3
"""Read-only Sub2API Session production snapshot and bounded catch-up monitor."""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


APP_SCRIPT = r"""
set -u

emit_service() {
  unit=$1
  active=$(systemctl is-active "$unit" 2>/dev/null || true)
  sub=$(systemctl show "$unit" -p SubState --value 2>/dev/null || true)
  restarts=$(systemctl show "$unit" -p NRestarts --value 2>/dev/null || true)
  result=$(systemctl show "$unit" -p Result --value 2>/dev/null || true)
  status=$(systemctl show "$unit" -p ExecMainStatus --value 2>/dev/null || true)
  printf 'service|%s|%s|%s|%s|%s|%s\n' "$unit" "$active" "$sub" "$restarts" "$result" "$status"
}

printf 'disk_percent=%s\n' "$(df -P / | awk 'NR==2 {gsub("%", "", $5); print $5}')"
printf 'disk_available_kb=%s\n' "$(df -Pk / | awk 'NR==2 {print $4}')"
printf 'load=%s\n' "$(uptime | sed 's/.*load average[s]*: *//')"
printf 'memory_available_bytes=%s\n' "$(free -b | awk '/^Mem:/ {print $7}')"

for unit in sub2api.service sub2api-session-forwarder.service sub2api-session-tunnel.service; do
  emit_service "$unit"
done

if curl -fsS --max-time 5 http://127.0.0.1:8080/health >/dev/null 2>&1; then
  printf 'health=ok\n'
else
  printf 'health=failed\n'
fi
ui_code=$(curl -sS -o /dev/null --max-time 8 -w '%{http_code}' http://127.0.0.1:8080/admin/accounts 2>/dev/null || true)
printf 'admin_ui_http=%s\n' "$ui_code"

for binary in /opt/sub2api/sub2api /opt/sub2api/sessionctl; do
  if sudo test -f "$binary"; then
    sha=$(sudo sha256sum "$binary" 2>/dev/null | awk '{print $1}')
    printf 'binary_sha|%s|%s\n' "$binary" "$sha"
  fi
done

spool_max=""
forwarder_pid=$(systemctl show sub2api-session-forwarder.service -p MainPID --value 2>/dev/null || true)
if [[ "$forwarder_pid" =~ ^[1-9][0-9]*$ ]]; then
  process_max=$(sudo sh -c 'tr "\000" "\n" < "/proc/$1/environ"' sh "$forwarder_pid" 2>/dev/null \
    | sed -n 's/^SESSION_DELIVERY_SPOOL_MAX_BYTES=//p' | head -n 1)
  if [[ "$process_max" =~ ^[1-9][0-9]*$ ]]; then
    spool_max=$process_max
  fi
fi
printf 'spool_max_bytes=%s\n' "$spool_max"
if [[ "$spool_max" =~ ^[1-9][0-9]*$ ]] && sudo test -x /opt/sub2api/sessionctl; then
  spool_json=$(sudo /opt/sub2api/sessionctl spool-status \
    -spool-dir /opt/sub2api/data/session-delivery/spool \
    -spool-max-bytes "$spool_max" 2>/dev/null | tr -d '\n' || true)
  printf 'spool_json=%s\n' "$spool_json"
else
  printf 'spool_json=\n'
fi
"""


DB_SCRIPT = r"""
set -u

emit_service() {
  unit=$1
  active=$(systemctl is-active "$unit" 2>/dev/null || true)
  sub=$(systemctl show "$unit" -p SubState --value 2>/dev/null || true)
  restarts=$(systemctl show "$unit" -p NRestarts --value 2>/dev/null || true)
  result=$(systemctl show "$unit" -p Result --value 2>/dev/null || true)
  status=$(systemctl show "$unit" -p ExecMainStatus --value 2>/dev/null || true)
  printf 'service|%s|%s|%s|%s|%s|%s\n' "$unit" "$active" "$sub" "$restarts" "$result" "$status"
}

printf 'disk_percent=%s\n' "$(df -P / | awk 'NR==2 {gsub("%", "", $5); print $5}')"
printf 'disk_available_kb=%s\n' "$(df -Pk / | awk 'NR==2 {print $4}')"
printf 'load=%s\n' "$(uptime | sed 's/.*load average[s]*: *//')"
printf 'memory_available_bytes=%s\n' "$(free -b | awk '/^Mem:/ {print $7}')"

for unit in sub2api-sessiond.service sub2api-session-export.service sub2api-session-export.timer xray-session-egress.service cloudflared-session.service; do
  emit_service "$unit"
done
printf 'timer_enabled=%s\n' "$(systemctl is-enabled sub2api-session-export.timer 2>/dev/null || true)"
printf 'timer_next=%s\n' "$(systemctl show sub2api-session-export.timer -p NextElapseUSecRealtime --value 2>/dev/null || true)"

sessiond_exec=$(systemctl show sub2api-sessiond.service -p ExecStart --value 2>/dev/null || true)
disk_reject=$(printf '%s\n' "$sessiond_exec" \
  | grep -oE -- '-{1,2}reject-disk-usage([=[:space:]]+)[0-9]+' \
  | tail -n 1 \
  | grep -oE '[0-9]+$' || true)
if [[ ! "$disk_reject" =~ ^[1-9][0-9]*$ ]]; then
  sessiond_pid=$(systemctl show sub2api-sessiond.service -p MainPID --value 2>/dev/null || true)
  if [[ "$sessiond_pid" =~ ^[1-9][0-9]*$ ]]; then
    disk_reject=$(sudo sh -c 'tr "\000" "\n" < "/proc/$1/environ"' sh "$sessiond_pid" 2>/dev/null \
      | sed -n 's/^SESSION_DISK_REJECT_PERCENT=//p' | head -n 1)
  fi
fi
printf 'disk_reject_percent=%s\n' "$disk_reject"

for binary in /opt/sub2api/sessionctl /opt/sub2api/sessiond; do
  if [[ -f "$binary" ]]; then
    sha=$(sudo sha256sum "$binary" 2>/dev/null | awk '{print $1}')
    printf 'binary_sha|%s|%s\n' "$binary" "$sha"
  fi
done

exporter_pid=$(systemctl show sub2api-session-export.service -p MainPID --value 2>/dev/null || true)
if [[ "$exporter_pid" =~ ^[1-9][0-9]*$ ]]; then
  process=$(ps -p "$exporter_pid" -o etimes=,%cpu=,rss= 2>/dev/null | xargs || true)
  printf 'exporter_process=%s\n' "$process"
  if [[ -r "/proc/$exporter_pid/io" ]]; then
    io=$(awk '/^(read_bytes|write_bytes):/ {printf "%s=%s ", $1, $2}' "/proc/$exporter_pid/io" 2>/dev/null || true)
    printf 'exporter_io=%s\n' "$io"
  fi
fi

summary=$(sudo -u postgres psql -XAt -v ON_ERROR_STOP=1 -d session_delivery -F '|' -c "
  SELECT
    COUNT(*) FILTER (WHERE status = 'failed'),
    COUNT(*) FILTER (WHERE status = 'exporting'),
    COUNT(*) FILTER (WHERE status = 'purged'),
    COALESCE(to_char(MAX(export_hour) FILTER (WHERE status = 'exporting'), 'YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'), '')
  FROM session_export_batches
" 2>/dev/null || true)
printf 'batch_summary=%s\n' "$summary"

sudo -u postgres psql -XAt -v ON_ERROR_STOP=1 -d session_delivery -F '|' -c "
  SELECT
    to_char(export_hour, 'YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'),
    status,
    record_count,
    delivery_count,
    rejected_count,
    archive_size,
    delivery_tokens_counted,
    delivery_input_tokens,
    delivery_cache_creation_input_tokens,
    delivery_cache_read_input_tokens,
    delivery_output_tokens,
    archive_sha256
  FROM session_export_batches
  ORDER BY export_hour DESC
  LIMIT 6
" 2>/dev/null | while IFS= read -r row; do
  printf 'recent_batch|%s\n' "$row"
done

printf 'ungranted_locks=%s\n' "$(sudo -u postgres psql -XAt -v ON_ERROR_STOP=1 -d session_delivery -c 'SELECT COUNT(*) FROM pg_locks WHERE NOT granted' 2>/dev/null || true)"
"""


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")


def as_int(value: Any, default: int = -1) -> int:
    try:
        return int(str(value).strip())
    except (TypeError, ValueError):
        return default


def run_ssh(
    ssh_bin: str,
    host: str,
    key: Path,
    port: int,
    script: str,
    timeout: int,
) -> str:
    command = [
        ssh_bin,
        "-o",
        "BatchMode=yes",
        "-o",
        "ConnectTimeout=12",
        "-o",
        "ServerAliveInterval=15",
        "-o",
        "ServerAliveCountMax=2",
        "-o",
        "IPQoS=none",
        "-i",
        str(key),
        "-p",
        str(port),
        host,
        "bash -s",
    ]
    result = subprocess.run(
        command,
        input=script,
        text=True,
        capture_output=True,
        timeout=timeout,
        check=False,
    )
    if result.returncode != 0:
        detail = result.stderr.strip().splitlines()
        last = detail[-1] if detail else f"exit {result.returncode}"
        raise RuntimeError(f"SSH snapshot failed for {host}: {last}")
    return result.stdout


def parse_snapshot(raw: str, role: str) -> dict[str, Any]:
    snapshot: dict[str, Any] = {
        "role": role,
        "services": {},
        "binary_sha256": {},
        "recent_batches": [],
    }
    for raw_line in raw.splitlines():
        line = raw_line.strip()
        if not line:
            continue
        if line.startswith("service|"):
            parts = line.split("|", 6)
            if len(parts) == 7:
                snapshot["services"][parts[1]] = {
                    "active": parts[2],
                    "sub": parts[3],
                    "restarts": as_int(parts[4], 0),
                    "result": parts[5],
                    "exec_status": as_int(parts[6], 0),
                }
            continue
        if line.startswith("binary_sha|"):
            parts = line.split("|", 2)
            if len(parts) == 3:
                snapshot["binary_sha256"][parts[1]] = parts[2]
            continue
        if line.startswith("recent_batch|"):
            parts = line.split("|")
            if len(parts) == 13:
                snapshot["recent_batches"].append(
                    {
                        "hour": parts[1],
                        "status": parts[2],
                        "records": as_int(parts[3]),
                        "deliveries": as_int(parts[4]),
                        "rejected": as_int(parts[5]),
                        "archive_bytes": as_int(parts[6]),
                        "tokens_counted": as_int(parts[7]),
                        "input_tokens": as_int(parts[8]),
                        "cache_creation_input_tokens": as_int(parts[9]),
                        "cache_read_input_tokens": as_int(parts[10]),
                        "output_tokens": as_int(parts[11]),
                        "archive_sha256": parts[12],
                    }
                )
            continue
        if "=" not in line:
            continue
        key, value = line.split("=", 1)
        if key == "spool_json":
            try:
                snapshot["spool"] = json.loads(value) if value else {}
            except json.JSONDecodeError:
                snapshot["spool"] = {}
            continue
        if key == "batch_summary":
            parts = value.split("|", 3)
            if len(parts) == 4:
                snapshot["batches"] = {
                    "failed": as_int(parts[0]),
                    "exporting": as_int(parts[1]),
                    "purged": as_int(parts[2]),
                    "current_hour": parts[3],
                }
            continue
        if key in {
            "disk_percent",
            "disk_available_kb",
            "memory_available_bytes",
            "ungranted_locks",
            "disk_reject_percent",
            "spool_max_bytes",
        }:
            snapshot[key] = as_int(value)
        else:
            snapshot[key] = value
    return snapshot


def spool_percent(spool: dict[str, Any]) -> float:
    used = as_int(spool.get("used_bytes"), 0)
    maximum = as_int(spool.get("max_bytes"), 0)
    return (used * 100.0 / maximum) if maximum > 0 else -1.0


def evaluate(snapshot: dict[str, Any], args: argparse.Namespace) -> tuple[list[str], list[str]]:
    critical: list[str] = []
    warnings: list[str] = []
    app = snapshot["app"]
    db = snapshot["db"]

    required = [
        (app, "sub2api.service"),
        (app, "sub2api-session-forwarder.service"),
        (app, "sub2api-session-tunnel.service"),
        (db, "sub2api-sessiond.service"),
    ]
    for endpoint, unit in required:
        service = endpoint.get("services", {}).get(unit, {})
        if service.get("active") != "active":
            critical.append(f"{unit} is not active")
        if service.get("result") not in {"", "success"}:
            critical.append(f"{unit} result={service.get('result')}")
        if as_int(service.get("exec_status"), 0) != 0:
            critical.append(f"{unit} exec status={service.get('exec_status')}")

    for role in ("app", "db"):
        for unit, service in snapshot[role].get("services", {}).items():
            restarts = as_int(service.get("restarts"), 0)
            if restarts > 0:
                warnings.append(f"{unit} cumulative restarts={restarts}")

    if app.get("health") != "ok":
        critical.append("app health check failed")
    if app.get("admin_ui_http") != "200":
        critical.append(f"admin UI returned {app.get('admin_ui_http', 'unknown')}")

    app_disk = as_int(app.get("disk_percent"))
    db_disk = as_int(db.get("disk_percent"))
    disk_reject = as_int(db.get("disk_reject_percent"))
    if app_disk < 0:
        critical.append("app disk usage is unavailable")
    elif app_disk >= args.app_disk_warning:
        warnings.append(f"app disk is {app_disk}%")
    if not 1 <= disk_reject <= 100:
        critical.append("sessiond disk reject threshold is unavailable or invalid")
    elif args.db_disk_warning >= disk_reject:
        critical.append(
            f"DB disk warning threshold {args.db_disk_warning}% must be below sessiond reject threshold {disk_reject}%"
        )
    if db_disk < 0:
        critical.append("DB disk usage is unavailable")
    elif 1 <= disk_reject <= 100 and db_disk >= disk_reject:
        critical.append(f"DB disk is {db_disk}% (sessiond rejects at {disk_reject}%)")
    elif db_disk >= args.db_disk_warning:
        warnings.append(f"DB disk is {db_disk}%")

    spool = app.get("spool", {})
    configured_spool_max = as_int(app.get("spool_max_bytes"))
    if configured_spool_max <= 0:
        critical.append("forwarder spool max bytes is unavailable or invalid")
    if not spool:
        critical.append("spool status is unavailable")
    else:
        reported_spool_max = as_int(spool.get("max_bytes"))
        if configured_spool_max > 0 and reported_spool_max != configured_spool_max:
            critical.append(
                f"spool max mismatch configured={configured_spool_max} reported={reported_spool_max}"
            )
        percent = spool_percent(spool)
        if percent >= args.spool_critical:
            critical.append(f"spool is {percent:.1f}% full")
        if as_int(spool.get("quarantined_records"), 0) > 0:
            critical.append(f"spool quarantine={spool.get('quarantined_records')}")

    batches = db.get("batches")
    if not isinstance(batches, dict):
        critical.append("batch summary is unavailable")
    else:
        if as_int(batches.get("failed"), 0) > 0:
            critical.append(f"failed batches={batches.get('failed')}")
        if as_int(batches.get("exporting"), 0) > 0:
            warnings.append(f"exporting batches={batches.get('exporting')}")

    exporter = db.get("services", {}).get("sub2api-session-export.service", {})
    if exporter.get("active") == "failed" or exporter.get("result") == "failed":
        critical.append("exporter service failed")

    timer = db.get("services", {}).get("sub2api-session-export.timer", {})
    timer_active = timer.get("active") == "active"
    timer_enabled = db.get("timer_enabled") == "enabled"
    timer_unavailable = not timer_active or not timer_enabled
    exporting = as_int(batches.get("exporting"), 0) if isinstance(batches, dict) else 0
    if timer_unavailable:
        if args.allow_timer_frozen:
            warnings.append("export timer is intentionally allowed to be frozen")
        elif timer_enabled and exporting > 0:
            warnings.append("export timer is inactive while an enabled exporter batch is running")
        else:
            critical.append("export timer is not active and enabled")

    if as_int(db.get("ungranted_locks"), 0) > 0:
        warnings.append(f"ungranted DB locks={db.get('ungranted_locks')}")
    return critical, warnings


def restart_counts(snapshot: dict[str, Any]) -> dict[str, int]:
    counts: dict[str, int] = {}
    for role in ("app", "db"):
        for unit, service in snapshot[role].get("services", {}).items():
            counts[f"{role}:{unit}"] = as_int(service.get("restarts"), 0)
    return counts


def restart_increases(previous: dict[str, int], current: dict[str, int]) -> list[str]:
    increases: list[str] = []
    for identity, count in current.items():
        if identity in previous and count > previous[identity]:
            increases.append(f"{identity} restarts increased {previous[identity]}->{count}")
    return increases


def is_complete(snapshot: dict[str, Any], args: argparse.Namespace) -> bool:
    batches = snapshot["db"].get("batches", {})
    spool = snapshot["app"].get("spool", {})
    return (
        as_int(batches.get("failed"), 1) == 0
        and as_int(batches.get("exporting"), 1) == 0
        and as_int(spool.get("pending_records"), args.pending_target + 1) <= args.pending_target
        and as_int(spool.get("quarantined_records"), 1) == 0
    )


def compact_line(snapshot: dict[str, Any], critical: list[str], warnings: list[str]) -> str:
    app = snapshot["app"]
    db = snapshot["db"]
    spool = app.get("spool", {})
    batches = db.get("batches", {})
    return (
        f"[{snapshot['observed_at_utc']}] "
        f"app_disk={app.get('disk_percent', '?')}% db_disk={db.get('disk_percent', '?')}% "
        f"spool={spool_percent(spool):.1f}% pending={spool.get('pending_records', '?')} "
        f"quarantine={spool.get('quarantined_records', '?')} "
        f"batches(f/e/p)={batches.get('failed', '?')}/{batches.get('exporting', '?')}/{batches.get('purged', '?')} "
        f"critical={len(critical)} warnings={len(warnings)}"
    )


def collect(args: argparse.Namespace) -> dict[str, Any]:
    app_raw = run_ssh(
        args.ssh_bin,
        args.app_host,
        args.app_key,
        args.app_port,
        APP_SCRIPT,
        args.ssh_timeout,
    )
    db_raw = run_ssh(
        args.ssh_bin,
        args.db_host,
        args.db_key,
        args.db_port,
        DB_SCRIPT,
        args.ssh_timeout,
    )
    return {
        "observed_at_utc": utc_now(),
        "app": parse_snapshot(app_raw, "app"),
        "db": parse_snapshot(db_raw, "db"),
    }


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Read-only Sub2API Session production snapshot and catch-up monitor."
    )
    parser.add_argument("--app-host", default="opc@161.153.91.242")
    parser.add_argument("--app-key", type=Path, default=Path("/Users/taylor/.ssh/ssh-key-oracle.key"))
    parser.add_argument("--app-port", type=int, default=22)
    parser.add_argument("--db-host", default="ubuntu@110.40.157.171")
    parser.add_argument("--db-key", type=Path, default=Path("/Users/taylor/.ssh/tencent-db-trail.pem"))
    parser.add_argument("--db-port", type=int, default=22)
    parser.add_argument("--ssh-bin", default="ssh")
    parser.add_argument("--ssh-timeout", type=int, default=45)
    parser.add_argument("--json", action="store_true", help="Print full JSON snapshots.")
    parser.add_argument("--watch", action="store_true", help="Poll until catch-up is safe or a limit is reached.")
    parser.add_argument("--interval", type=int, default=60)
    parser.add_argument("--timeout", type=int, default=14400)
    parser.add_argument("--pending-target", type=int, default=100)
    parser.add_argument("--allow-timer-frozen", action="store_true")
    parser.add_argument("--app-disk-warning", type=int, default=85)
    parser.add_argument("--db-disk-warning", type=int, default=70)
    parser.add_argument("--spool-critical", type=int, default=85)
    return parser


def validate_args(args: argparse.Namespace) -> None:
    for key in (args.app_key, args.db_key):
        if not key.is_file():
            raise ValueError(f"SSH key does not exist: {key}")
    if args.interval <= 0 or args.timeout <= 0 or args.ssh_timeout <= 0:
        raise ValueError("interval and timeout values must be positive")
    if args.pending_target < 0:
        raise ValueError("pending target must not be negative")
    for name in ("app_disk_warning", "db_disk_warning", "spool_critical"):
        value = getattr(args, name)
        if not 1 <= value <= 100:
            raise ValueError(f"{name} must be between 1 and 100")


def main() -> int:
    args = build_parser().parse_args()
    try:
        validate_args(args)
    except ValueError as exc:
        print(str(exc), file=sys.stderr)
        return 2

    started = time.monotonic()
    previous_restarts: dict[str, int] | None = None
    while True:
        try:
            snapshot = collect(args)
        except (RuntimeError, subprocess.TimeoutExpired) as exc:
            print(str(exc), file=sys.stderr)
            return 1
        critical, warnings = evaluate(snapshot, args)
        current_restarts = restart_counts(snapshot)
        if previous_restarts is not None:
            critical.extend(restart_increases(previous_restarts, current_restarts))
        previous_restarts = current_restarts
        snapshot["evaluation"] = {"critical": critical, "warnings": warnings}
        if args.json:
            print(json.dumps(snapshot, ensure_ascii=False, indent=2, sort_keys=True))
        else:
            print(compact_line(snapshot, critical, warnings), flush=True)
            for item in critical:
                print(f"CRITICAL: {item}", file=sys.stderr)
            for item in warnings:
                print(f"WARNING: {item}", file=sys.stderr)

        if critical:
            return 1
        if not args.watch:
            return 0
        if is_complete(snapshot, args):
            print("Catch-up completion gate passed.")
            return 0
        if time.monotonic() - started >= args.timeout:
            print("Monitoring timed out before the completion gate passed.", file=sys.stderr)
            return 124
        time.sleep(args.interval)


if __name__ == "__main__":
    raise SystemExit(main())
