import copy
import unittest
from collections import Counter

from build_aicost_video_api_preview import (
    PROPOSAL, SNAPSHOT, build_preview, load_json,
)


class AICostVideoAPIPreviewTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.proposal = load_json(PROPOSAL)
        cls.snapshot = load_json(SNAPSHOT)

    def test_existing_requests_are_versioned_and_ordered(self):
        result = build_preview(self.proposal, self.snapshot)
        steps = result["existing_change_steps"]
        self.assertEqual(Counter(step["kind"] for step in steps), {
            "product": 23, "cost_rate": 7, "sell_rate": 7, "public_identities": 1,
        })
        self.assertEqual(result["expected_config_version_after_existing_changes"], 148)
        self.assertEqual([step["body"]["expected_config_version"] for step in steps],
                         list(range(110, 148)))
        self.assertEqual([step["config_version_after"] for step in steps],
                         list(range(111, 149)))
        self.assertEqual(steps[-1]["kind"], "public_identities")
        self.assertEqual(len(steps[-1]["body"]["renames"]), 31)
        self.assertTrue(all(step["body"]["expected_active_release_id"] == 2
                            for step in steps))
        self.assertEqual(len(result["activate_after_deployment_and_verification"]), 4)
        self.assertTrue(all(item["body"]["expected_version"] == 2
                            for item in result["activate_after_deployment_and_verification"]))

    def test_flat_prices_keep_existing_meter_and_address_before_rename(self):
        result = build_preview(self.proposal, self.snapshot)
        steps = result["existing_change_steps"]
        by_kind = Counter(step["kind"] for step in steps[:-1])
        self.assertEqual(by_kind["cost_rate"], by_kind["sell_rate"])
        rate_steps = [step for step in steps if step["kind"] in ("cost_rate", "sell_rate")]
        self.assertTrue(all(step["body"]["pricing_mode"] == "flat" for step in rate_steps))
        for step in rate_steps:
            body = step["body"]
            if step["kind"] == "cost_rate":
                self.assertTrue(body["product_code"] and body["plan_code"] and body["pool_code"])
            else:
                self.assertTrue(body["sku_code"])
        target = next(row for row in self.proposal["changes"]
                      if row["model"] == "seedance-2.5-480p-2")
        self.assertEqual([step["body"]["unit_price"] for step in rate_steps
                          if step["body"].get("product_code") ==
                          target["old_product"]["product_code"]], ["0.45"])
        self.assertTrue(all("aicost" not in rename["to_api_name"].lower()
                            for rename in steps[-1]["body"]["renames"]))

    def test_fast_onboard_is_incomplete_and_model_specific(self):
        result = build_preview(self.proposal, self.snapshot)
        template = result["incomplete_onboard_template"]
        body = template["body_template"]
        self.assertFalse(template["ready"])
        self.assertEqual(body["expected_config_version"], 148)
        self.assertEqual(body["sku"]["api_name"], "seedance2.0-900-fast")
        self.assertEqual(body["product"]["vendor_model"], "seedance2.0-900-fast")
        self.assertEqual(body["sell_rate"]["unit_price"], "1.8")
        self.assertEqual(body["cost_rate"]["unit_code"], "request")
        self.assertEqual(list(body["product"]["capability_constraints"]["adapter"]
                              ["validation"]["models"]), ["seedance2.0-900-fast"])
        self.assertTrue(all(body["product"][field] is None for field in (
            "auth_scheme", "transport_timeout_ms", "task_timeout_ms",
            "upstream_scope_kind", "upstream_scope_key", "allowed_hosts", "actions")))
        self.assertEqual(result["not_ready_for_onboarding"], [
            "seedance2.5-2-1080p", "seedance2.5-2-480p", "seedance2.5-2-720p"])

    def test_stale_or_ambiguous_input_is_rejected(self):
        proposal = copy.deepcopy(self.proposal)
        proposal["release"]["config_version"] += 1
        with self.assertRaises(ValueError):
            build_preview(proposal, self.snapshot)
        proposal = copy.deepcopy(self.proposal)
        proposal["changes"][0]["blockers"].append("manual review")
        with self.assertRaises(ValueError):
            build_preview(proposal, self.snapshot)
        proposal = copy.deepcopy(self.proposal)
        proposal["changes"][1]["public_identity"] = proposal["changes"][0]["public_identity"]
        with self.assertRaises(ValueError):
            build_preview(proposal, self.snapshot)
        snapshot = copy.deepcopy(self.snapshot)
        snapshot["entries"].append({"model_code": self.proposal["changes"][0]["model"]})
        with self.assertRaises(ValueError):
            build_preview(self.proposal, snapshot)
        proposal = copy.deepcopy(self.proposal)
        changed_rate = next(row["cost_rates"][0] for row in proposal["changes"]
                            if row["cost_rates"][0]["changed"])
        changed_rate["proposed"]["unit_code"] = "unexpected"
        with self.assertRaises(ValueError):
            build_preview(proposal, self.snapshot)


if __name__ == "__main__":
    unittest.main()
