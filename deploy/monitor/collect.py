#!/usr/bin/env python3
"""Unprivileged Linux sampler. No network, subprocesses, command lines or credentials."""
import argparse
import datetime
import json
import os
from pathlib import Path
import re
import socket
import time


def read(path, limit=65536):
    with open(path, encoding="utf-8") as source:
        return source.read(limit)


def memory():
    values = {}
    for line in read("/proc/meminfo").splitlines():
        name, value = line.split(":", 1)
        values[name] = int(value.strip().split()[0]) * 1024
    total, available = values["MemTotal"], values["MemAvailable"]
    return ({"total_bytes": total, "available_bytes": available,
             "used_percent": (total - available) * 100 / total},
            {"total_bytes": values["SwapTotal"], "used_bytes": values["SwapTotal"] - values["SwapFree"]})


def cpu():
    ticks = [int(v) for v in read("/proc/stat").splitlines()[0].split()[1:9]]
    return {"total": sum(ticks), "idle": ticks[3] + ticks[4]}


def network():
    rx, tx = 0, 0
    for line in read("/proc/net/dev").splitlines()[2:]:
        name, counters = line.split(":", 1)
        if name.strip() == "lo" or name.strip().startswith(("veth", "docker", "br-")):
            continue
        values = counters.split()
        rx += int(values[0])
        tx += int(values[8])
    return {"rx_bytes": rx, "tx_bytes": tx}


def disks(paths):
    result, devices = [], set()
    for path in paths[:16]:
        device = os.stat(path).st_dev
        if device in devices:
            continue
        devices.add(device)
        stat = os.statvfs(path)
        total, available = stat.f_blocks * stat.f_frsize, stat.f_bavail * stat.f_frsize
        result.append({"path": path, "total_bytes": total, "available_bytes": available,
                       "used_percent": (1 - available / total) * 100 if total else None,
                       "inodes_percent": (1 - stat.f_favail / stat.f_files) * 100 if stat.f_files else None})
    return result


def processes(deadline):
    items, count, truncated = [], 0, False
    page_size = os.sysconf("SC_PAGE_SIZE")
    with os.scandir("/proc") as entries:
        for entry in entries:
            if not entry.name.isdigit():
                continue
            count += 1
            if count > 4096 or time.monotonic() > deadline:
                truncated = True
                break
            try:
                raw = read(entry.path + "/stat", 4096)
                left, right = raw.index("("), raw.rindex(")")
                fields = raw[right + 2:].split()
                items.append({"pid": int(entry.name), "name": raw[left + 1:right][:64],
                              "state": fields[0], "rss_bytes": max(0, int(fields[21])) * page_size,
                              "threads": int(fields[17])})
            except (OSError, ValueError, IndexError):
                continue
    return sorted(items, key=lambda item: item["rss_bytes"], reverse=True)[:10], count, truncated


def collect(output, node_id, paths):
    start, wall = time.monotonic(), time.time()
    previous = {}
    try:
        previous = json.loads(read(output, 256 * 1024))
    except (OSError, ValueError):
        pass
    now = datetime.datetime.now(datetime.timezone.utc).isoformat()
    result = {"id": node_id, "sampled_at": now, "status": "ok", "errors": [],
              "cpu_count": os.cpu_count(), "history": [], "processes": [], "disks": []}
    for key, action in [("memory_pair", memory), ("cpu_ticks", cpu), ("network", network),
                        ("disks", lambda: disks(paths)), ("load", lambda: list(os.getloadavg())),
                        ("uptime_seconds", lambda: float(read("/proc/uptime").split()[0]))]:
        try:
            result[key] = action()
        except (OSError, ValueError, KeyError, IndexError):
            result["errors"].append(key)
    if "memory_pair" in result:
        result["memory"], result["swap"] = result.pop("memory_pair")
    result["processes"], result["process_count"], result["processes_truncated"] = processes(start + 2)
    if result["errors"] or result["processes_truncated"]:
        result["status"] = "partial"
    elapsed = wall - previous.get("wall", wall)
    continuity = previous.get("id") == node_id and 0 < elapsed < 120 and result.get("uptime_seconds", 0) > previous.get("uptime_seconds", 0)
    if continuity:
        ticks, old = result.get("cpu_ticks", {}), previous.get("cpu_ticks", {})
        delta = ticks.get("total", 0) - old.get("total", 0)
        idle = ticks.get("idle", 0) - old.get("idle", 0)
        if delta > 0 and idle >= 0:
            result["cpu_percent"] = max(0, min(100, (1 - idle / delta) * 100))
        for counter, rate in [("rx_bytes", "rx_bps"), ("tx_bytes", "tx_bps")]:
            value, old_value = result.get("network", {}).get(counter), previous.get("network", {}).get(counter)
            if value is not None and old_value is not None and value >= old_value:
                result["network"][rate] = (value - old_value) / elapsed
    history = previous.get("history", [])[-119:] if continuity else []
    history.append({"at": now, "cpu_percent": result.get("cpu_percent"),
                    "memory_percent": result.get("memory", {}).get("used_percent"),
                    "rx_bps": result.get("network", {}).get("rx_bps"),
                    "tx_bps": result.get("network", {}).get("tx_bps")})
    result.update(history=history, wall=wall, collection_ms=(time.monotonic() - start) * 1000)
    temporary = str(output) + ".tmp"
    with open(temporary, "w", encoding="utf-8") as target:
        json.dump(result, target, separators=(",", ":"), allow_nan=False)
    os.chmod(temporary, 0o644)
    os.replace(temporary, output)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", default="/var/lib/bosapi-monitor/local.json")
    parser.add_argument("--node-id", default=socket.gethostname())
    parser.add_argument("--disk", action="append", default=["/"])
    args = parser.parse_args()
    if not re.fullmatch(r"[a-zA-Z0-9_.-]{1,128}", args.node_id):
        parser.error("node-id must use letters, numbers, dots, underscores or hyphens")
    collect(Path(args.output), args.node_id, args.disk)
