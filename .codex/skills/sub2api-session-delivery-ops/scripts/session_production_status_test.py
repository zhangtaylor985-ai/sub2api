#!/usr/bin/env python3

import argparse
import unittest

import session_production_status as target


APP_RAW = """\
disk_percent=80
disk_available_kb=1000
health=ok
admin_ui_http=200
spool_max_bytes=1000
service|sub2api.service|active|running|0|success|0
service|sub2api-session-forwarder.service|active|running|0|success|0
service|sub2api-session-tunnel.service|active|running|0|success|0
spool_json={"used_bytes":100,"max_bytes":1000,"pending_records":5,"quarantined_records":0}
"""

DB_RAW = """\
disk_percent=60
disk_available_kb=1000
disk_reject_percent=75
service|sub2api-sessiond.service|active|running|0|success|0
service|sub2api-session-export.service|inactive|dead|0|success|0
service|sub2api-session-export.timer|active|waiting|0|success|0
timer_enabled=enabled
batch_summary=0|0|82|
recent_batch|2026-08-18T16:00:00Z|purged|203|197|6|1000|197|10|20|30|40|aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
ungranted_locks=0
"""


class StatusTest(unittest.TestCase):
    def setUp(self) -> None:
        self.args = argparse.Namespace(
            app_disk_warning=85,
            db_disk_warning=70,
            spool_critical=85,
            allow_timer_frozen=False,
            pending_target=100,
        )
        self.snapshot = {
            "observed_at_utc": "2026-08-19T00:00:00Z",
            "app": target.parse_snapshot(APP_RAW, "app"),
            "db": target.parse_snapshot(DB_RAW, "db"),
        }

    def test_safe_snapshot_completes(self) -> None:
        critical, warnings = target.evaluate(self.snapshot, self.args)
        self.assertEqual([], critical)
        self.assertEqual([], warnings)
        self.assertTrue(target.is_complete(self.snapshot, self.args))
        self.assertEqual(197, self.snapshot["db"]["recent_batches"][0]["deliveries"])

    def test_quarantine_is_critical(self) -> None:
        self.snapshot["app"]["spool"]["quarantined_records"] = 1
        critical, _ = target.evaluate(self.snapshot, self.args)
        self.assertTrue(any("quarantine=1" in item for item in critical))

    def test_spool_max_is_read_from_forwarder_and_fail_closed(self) -> None:
        self.snapshot["app"]["spool_max_bytes"] = -1
        critical, _ = target.evaluate(self.snapshot, self.args)
        self.assertIn("forwarder spool max bytes is unavailable or invalid", critical)

        self.snapshot["app"]["spool_max_bytes"] = 2000
        critical, _ = target.evaluate(self.snapshot, self.args)
        self.assertTrue(any("spool max mismatch" in item for item in critical))

    def test_frozen_timer_requires_explicit_allowance(self) -> None:
        self.snapshot["db"]["services"]["sub2api-session-export.timer"]["active"] = "inactive"
        critical, _ = target.evaluate(self.snapshot, self.args)
        self.assertIn("export timer is not active and enabled", critical)
        self.args.allow_timer_frozen = True
        critical, warnings = target.evaluate(self.snapshot, self.args)
        self.assertEqual([], critical)
        self.assertTrue(any("allowed to be frozen" in item for item in warnings))

    def test_enabled_timer_may_be_inactive_while_exporting(self) -> None:
        self.snapshot["db"]["services"]["sub2api-session-export.timer"]["active"] = "inactive"
        self.snapshot["db"]["batches"]["exporting"] = 1
        critical, warnings = target.evaluate(self.snapshot, self.args)
        self.assertEqual([], critical)
        self.assertTrue(any("batch is running" in item for item in warnings))

    def test_actual_sessiond_disk_threshold_is_fail_closed(self) -> None:
        self.snapshot["db"]["disk_percent"] = 75
        critical, _ = target.evaluate(self.snapshot, self.args)
        self.assertTrue(any("sessiond rejects at 75%" in item for item in critical))

        self.snapshot["db"]["disk_percent"] = 60
        self.snapshot["db"]["disk_reject_percent"] = -1
        critical, _ = target.evaluate(self.snapshot, self.args)
        self.assertIn("sessiond disk reject threshold is unavailable or invalid", critical)

    def test_cumulative_restart_warns_and_increase_is_detected(self) -> None:
        self.snapshot["app"]["services"]["sub2api.service"]["restarts"] = 2
        critical, warnings = target.evaluate(self.snapshot, self.args)
        self.assertEqual([], critical)
        self.assertTrue(any("cumulative restarts=2" in item for item in warnings))

        previous = target.restart_counts(self.snapshot)
        self.snapshot["app"]["services"]["sub2api.service"]["restarts"] = 3
        increases = target.restart_increases(previous, target.restart_counts(self.snapshot))
        self.assertEqual(["app:sub2api.service restarts increased 2->3"], increases)

    def test_active_required_service_with_failed_result_is_critical(self) -> None:
        service = self.snapshot["app"]["services"]["sub2api.service"]
        service["result"] = "exit-code"
        service["exec_status"] = 1
        critical, _ = target.evaluate(self.snapshot, self.args)
        self.assertIn("sub2api.service result=exit-code", critical)
        self.assertIn("sub2api.service exec status=1", critical)


if __name__ == "__main__":
    unittest.main()
