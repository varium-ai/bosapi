import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("collector", Path(__file__).with_name("collect.py"))
collector = importlib.util.module_from_spec(spec)
spec.loader.exec_module(collector)


class CollectorTests(unittest.TestCase):
    def test_long_running_history_is_bounded_and_reboot_resets_it(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "node.json"
            tick = [0]
            def source(path, limit=65536):
                return output.read_text() if path == output else str(1000 + tick[0] * 30)
            with patch.object(collector, "memory", return_value=({"used_percent": 10}, {})), \
                 patch.object(collector, "cpu", side_effect=lambda: {"total": 1000 + tick[0] * 100, "idle": 800 + tick[0] * 80}), \
                 patch.object(collector, "network", side_effect=lambda: {"rx_bytes": tick[0] * 300, "tx_bytes": tick[0] * 600}), \
                 patch.object(collector, "disks", return_value=[]), \
                 patch.object(collector, "processes", return_value=([], 0, False)), \
                 patch.object(collector, "read", side_effect=source), \
                 patch.object(collector.time, "time", side_effect=lambda: 100000 + tick[0] * 30), \
                 patch.object(collector.os, "getloadavg", return_value=(0, 0, 0)):
                for i in range(130):
                    tick[0] = i
                    collector.collect(output, "test-node", ["/"])
                result = json.loads(output.read_text())
                self.assertEqual(len(result["history"]), 120)
                self.assertAlmostEqual(result["cpu_percent"], 20)
                self.assertEqual(result["network"]["rx_bps"], 10)
                self.assertLess(output.stat().st_size, 256 * 1024)
                tick[0] += 1
                collector.collect(output, "replacement-node", ["/"])
                reset = json.loads(output.read_text())
                self.assertEqual(len(reset["history"]), 1)
                self.assertNotIn("cpu_percent", reset)

    def test_memory_uses_available_not_free(self):
        with patch.object(collector, "read", return_value="MemTotal: 100 kB\nMemAvailable: 40 kB\nSwapTotal: 10 kB\nSwapFree: 8 kB\n"):
            memory, swap = collector.memory()
        self.assertEqual(memory["used_percent"], 60)
        self.assertEqual(swap["used_bytes"], 2048)

    def test_network_omits_virtual_duplicates(self):
        line = lambda name, rx, tx: f"{name}: {rx} 0 0 0 0 0 0 0 {tx} 0 0 0 0 0 0 0\n"
        data = "header\nheader\n" + line("eth0", 100, 200) + line("lo", 90, 90) + line("veth123", 40, 40) + line("br-abc", 20, 20)
        with patch.object(collector, "read", return_value=data):
            self.assertEqual(collector.network(), {"rx_bytes": 100, "tx_bytes": 200})

    def test_snapshot_is_bounded_and_atomic(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "node.json"
            with patch.object(collector, "memory", return_value=({"used_percent": 10}, {})), \
                 patch.object(collector, "cpu", return_value={"total": 100, "idle": 80}), \
                 patch.object(collector, "network", return_value={"rx_bytes": 10, "tx_bytes": 20}), \
                 patch.object(collector, "disks", return_value=[]), \
                 patch.object(collector, "processes", return_value=([], 0, False)), \
                 patch.object(collector, "read", side_effect=lambda path, limit=65536: output.read_text() if path == output else "1000 0"), \
                 patch.object(collector.os, "getloadavg", return_value=(0, 0, 0)):
                collector.collect(output, "test-node", ["/"])
                result = json.loads(output.read_text())
                self.assertEqual(result["id"], "test-node")
                self.assertEqual(len(result["history"]), 1)
                self.assertNotIn("cpu_percent", result)
                self.assertFalse(Path(str(output) + ".tmp").exists())
                self.assertLess(output.stat().st_size, 256 * 1024)


if __name__ == "__main__":
    unittest.main()
