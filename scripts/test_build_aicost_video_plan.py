import copy
import unittest
from decimal import Decimal

from build_aicost_video_plan import ROOT, build_plan, desired_duration, load_json


def fixture():
    target = {"models": [{"model": "seedance2.5-9图", "group": "即梦900分组",
                          "images": 9, "videos": 0, "audios": 0, "seconds": [30],
                          "price": "2.4", "unit": "request"}]}
    constraints = {
        "adapter": {
            "request": {"fixed_body": {"vendor_option": True},
                        "content_projections": [
                            {"types": ["image_url"], "source": "url",
                             "output": "array", "target": "images"},
                            {"types": ["video_url"], "source": "url",
                             "output": "array", "target": "videos"}]},
            "validation": {"models": {"seedance2.5-9图": {
                "duration_min": 4, "duration_max": 30,
                "max_images": 30, "max_videos": 1,
                "require_visual_media_with_audio": True}}}},
        "max_images": 30, "max_videos": 1,
        "duration_min": 4, "duration_max": 30,
        "allow_generated_audio": False,
    }
    product = {"id": 1, "offering_id": 2, "offering_state": "disabled",
               "channel_id": 74, "protocol": "video_generation", "cost_plan_id": 3,
               "vendor_model": "seedance2.5-9图", "capability_constraints": constraints}
    rate = {"id": 4, "parent_id": 3, "pricing_mode": "flat",
            "unit_code": "request", "unit_price": "2.000000", "extra": "keep"}
    sell = {**rate, "id": 5, "parent_id": 6}
    entry = {"model_code": "seedance2.5-9图", "description": "old",
             "products": [{"id": 1, "pool_name": "即梦900分组",
                           "pool_code": "jm900", "sku_ids": [6]}],
             "skus": [{"id": 6, "api_name": "seedance2.5-9图"}]}
    prism = {"captured_at": "old", "release": {"id": 2},
             "products": [product], "costs": [rate], "sells": [sell], "entries": [entry]}
    inventory = {"captured_at": "today",
                 "statuses": {"/api/pricing": 200, "/api/user/models": 200},
                 "models": ["seedance2.5-9图"],
                 "pricing": [{"model_name": "seedance2.5-9图", "model_price": 2.4,
                              "enable_groups": "即梦900分组"}]}
    return target, prism, inventory


class BuildAICostVideoPlanTests(unittest.TestCase):
    def test_existing_product_keeps_other_fields_and_sets_explicit_zero(self):
        target, prism, inventory = fixture()
        original = copy.deepcopy(prism)
        row = build_plan(target, prism, inventory)["changes"][0]
        self.assertEqual(prism, original)
        self.assertEqual(row["old_product"], original["products"][0])
        self.assertEqual(row["proposed_product"]["offering_state"], "active")
        updated = row["proposed_product"]["capability_constraints"]
        rule = updated["adapter"]["validation"]["models"]["seedance2.5-9图"]
        self.assertEqual(updated["task_types"], ["text", "multimodal"])
        self.assertEqual(rule["task_modes"], ["text", "references"])
        for configured in (updated, rule):
            self.assertEqual([configured[f"max_{kind}"] for kind in
                              ("images", "videos", "audios")], [9, 0, 0])
            self.assertEqual(configured["duration_options"], [30])
            self.assertEqual(configured["duration_min"], 30)
        self.assertTrue(rule["require_visual_media_with_audio"])
        self.assertFalse(updated["allow_generated_audio"])
        self.assertEqual(updated["adapter"]["request"]["fixed_body"],
                         {"vendor_option": True})
        self.assertEqual([item["target"] for item in updated["adapter"]["request"]
                          ["content_projections"]], ["images"])
        self.assertEqual(row["cost_rates"][0]["proposed"]["unit_price"], "2.4")
        self.assertEqual(row["sell_rates"][0]["proposed"]["unit_price"], "2.4")
        self.assertIn("9 图、0 视频、0 音频", row["proposed_public_description"])

    def test_snapshot_price_overrides_table_and_marks_conflict(self):
        target, prism, inventory = fixture()
        target["models"][0]["price"] = "2.3"
        row = build_plan(target, prism, inventory)["changes"][0]
        self.assertEqual(row["cost_rates"][0]["proposed"]["unit_price"], "2.4")
        self.assertTrue(any("target price differs" in item for item in row["blockers"]))

    def test_scalar_audio_projection_blocks_unsafe_mapping(self):
        target, prism, inventory = fixture()
        target["models"][0]["audios"] = 3
        request = prism["products"][0]["capability_constraints"]["adapter"]["request"]
        request["content_projections"].append({
            "types": ["audio_url"], "source": "url", "output": "scalar",
            "target": "audio_reference"})
        row = build_plan(target, prism, inventory)["changes"][0]
        self.assertTrue(any("audio_url projection is not a URL array" in item
                            for item in row["blockers"]))
        projections = row["proposed_product"]["capability_constraints"]["adapter"]
        projections = projections["request"]["content_projections"]
        self.assertFalse(any(item["target"] == "audios" for item in projections))

    def test_range_removes_stale_discrete_options_and_adds_url_arrays(self):
        target, prism, inventory = fixture()
        model = target["models"][0]
        model.pop("seconds")
        model.update(seconds_min=4, seconds_max=30, videos=3, audios=3)
        old = prism["products"][0]["capability_constraints"]
        old["duration_options"] = [30]
        old["adapter"]["validation"]["models"]["seedance2.5-9图"]["duration_options"] = [30]
        row = build_plan(target, prism, inventory)["changes"][0]
        updated = row["proposed_product"]["capability_constraints"]
        self.assertNotIn("duration_options", updated)
        self.assertNotIn("duration_options", updated["adapter"]["validation"]
                         ["models"]["seedance2.5-9图"])
        self.assertEqual({item["target"] for item in updated["adapter"]["request"]
                          ["content_projections"]}, {"images", "videos", "audios"})

    def test_missing_model_has_no_inferred_configuration(self):
        target, prism, inventory = fixture()
        target["models"][0]["group"] = "即梦特价"
        inventory["pricing"][0]["enable_groups"] = "即梦特价"
        prism["products"] = []
        prism["entries"] = []
        result = build_plan(target, prism, inventory)
        self.assertEqual(result["changes"], [])
        self.assertEqual(len(result["not_created"]), 1)
        self.assertTrue(any("credential pool mapping" in item
                            for item in result["not_created"][0]["blockers"]))

    def test_incomplete_snapshot_is_rejected(self):
        target, prism, inventory = fixture()
        inventory["statuses"]["/api/pricing"] = 429
        with self.assertRaisesRegex(ValueError, "incomplete AICost snapshot"):
            build_plan(target, prism, inventory)

    def test_real_snapshots_have_expected_partition_and_user_corrections(self):
        temporary = ROOT / ".temp"
        target_data = load_json(temporary / "aicost_video_target_20260924.json")
        plan = build_plan(
            target_data,
            load_json(temporary / "prism_live_video_catalog_snapshot_20260923.json"),
            load_json(temporary / "aicost_inventory_snapshot_20260924.json"),
        )
        self.assertEqual(plan["summary"]["target_models"], 35)
        self.assertEqual(plan["summary"]["existing_products"], 31)
        self.assertEqual(plan["summary"]["not_created"], 4)
        rows = {row["model"]: row for row in plan["changes"]}
        nine = rows["seedance2.5-9图"]["proposed_product"]["capability_constraints"]
        self.assertEqual(nine["max_images"], 9)
        doubao = rows["seedance2.5-doubao"]["proposed_product"]["capability_constraints"]
        self.assertEqual([doubao[f"max_{kind}"] for kind in
                          ("images", "videos", "audios")], [9, 3, 3])
        minimax = rows["minimax-h3-768p-930-8-22"]
        self.assertEqual(minimax["blockers"], [])
        projections = minimax["proposed_product"]["capability_constraints"]
        projections = projections["adapter"]["request"]["content_projections"]
        self.assertEqual([item["target"] for item in projections],
                         ["reference_images", "audios"])
        self.assertEqual(projections[1]["output"], "array")
        self.assertEqual(plan["summary"]["blocked_products"], 0)
        self.assertEqual(sum(bool(row["credential_pool_candidates"])
                             for row in plan["not_created"]), 1)
        targets = {row["model"]: row for row in target_data["models"]}
        for row in plan["changes"]:
            with self.subTest(model=row["model"]):
                target = targets[row["model"]]
                constraints = row["proposed_product"]["capability_constraints"]
                rule = constraints["adapter"]["validation"]["models"][row["model"]]
                minimum, maximum, options = desired_duration(target)
                for configured in (constraints, rule):
                    for kind in ("images", "videos", "audios"):
                        self.assertEqual(configured[f"max_{kind}"], target[kind])
                    self.assertEqual((configured["duration_min"], configured["duration_max"]),
                                     (minimum, maximum))
                    self.assertEqual(configured.get("duration_options"), options)
                self.assertEqual(row["proposed_public_identity"],
                                 {"model_code": row["model"], "sku_api_name": row["model"]})
                description = row["proposed_public_description"]
                self.assertIn(target["group"], description)
                self.assertIn(f"{target['images']} 图", description)
                self.assertIn(f"{target['videos']} 视频", description)
                self.assertIn(f"{target['audios']} 音频", description)
                for kind in ("cost_rates", "sell_rates"):
                    for rate in row[kind]:
                        self.assertEqual(Decimal(rate["proposed"]["unit_price"]),
                                         Decimal(target["price"]))


if __name__ == "__main__":
    unittest.main()
