import unittest

from scripts.compare_aicost_snapshot import compare, compare_prism


class CompareSnapshotTest(unittest.TestCase):
    def test_presence_and_decimal_prices(self):
        reference = {
            "video": [
                {"model": "same", "price_cny": "0.30"},
                {"model": "different", "price_cny": "1"},
                {"model": "absent", "price_cny": "2"},
            ],
            "image": ["picture"],
        }
        snapshot = {
            "pricing": [
                {"model_name": "same", "model_price": 0.3},
                {"model_name": "different", "model_price": 1.5},
                {"model_name": "picture", "model_price": 4},
            ],
            "models": ["same", "different", "other"],
        }
        self.assertEqual(compare(reference, snapshot), {
            "reference_count": 4,
            "missing_from_pricing": ["absent"],
            "missing_from_user_models": ["absent", "picture"],
            "price_differences": [{"model": "different", "document": "1", "snapshot": "1.5"}],
        })

    def test_incomplete_snapshot_rejected(self):
        with self.assertRaisesRegex(ValueError, "incomplete snapshot"):
            compare({"video": [], "image": []}, {"statuses": {"/api/pricing": 429}, "pricing": [], "models": []})

    def test_duplicate_reference_rejected(self):
        with self.assertRaises(ValueError):
            compare({"video": [{"model": "x", "price_cny": "1"}], "image": ["x"]},
                    {"pricing": [], "models": []})


    def test_prism_catalog_comparison(self):
        reference = {"video": [
            {"model": "same", "price_cny": "0.30", "unit": "second"},
            {"model": "different", "price_cny": "1", "unit": "request"},
            {"model": "missing", "price_cny": "2", "unit": "request"},
        ], "image": ["picture"]}
        prism = {"products": [
            {"id": 1, "vendor_model": "same", "offering_id": 11, "offering_state": "active"},
            {"id": 2, "vendor_model": "different", "offering_id": 12, "offering_state": "disabled"},
            {"id": 3, "vendor_model": "outside", "offering_id": 13, "offering_state": "disabled"},
            {"id": 4, "vendor_model": "picture", "offering_id": 14, "offering_state": "active"},
        ], "costs": [
            {"product_id": 1, "offering_id": 11, "unit_code": "second", "unit_price": "0.3000", "pricing_mode": "flat"},
            {"product_id": 2, "offering_id": 12, "unit_code": "request", "unit_price": "1.5", "pricing_mode": "flat"},
        ]}
        self.assertEqual(compare_prism(reference, prism), {
            "prism_product_count": 4,
            "outside_document_products": [{"model": "outside", "product_id": 3,
                                           "offering_id": 13, "state": "disabled"}],
            "missing_from_prism": ["missing"],
            "disabled_in_prism": ["different"],
            "cost_price_differences": [{"model": "different", "product_id": 2,
                                        "offering_id": 12, "document": "1", "prism": "1.5"}],
            "cost_unit_mismatches": [],
            "missing_costs": [],
            "capability_differences": [],
        })

    def test_explicit_capabilities_compare_both_layers_only(self):
        reference = {"video": [{"model": "x", "price_cny": "1", "unit": "request",
                                "duration_options": [6, 15], "max_images": 9,
                                "duration_min_seconds": 4}], "image": []}
        prism = {"products": [{"id": 3, "vendor_model": "x", "offering_id": 4,
                               "offering_state": "active", "capability_constraints": {
                                   "duration_options": [6, 15], "max_images": 1,
                                   "duration_min": 4, "adapter": {"validation": {"models": {
                                       "x": {"max_images": 9, "duration_min": 5}
                                   }}}}}],
                 "costs": [{"product_id": 3, "offering_id": 4, "unit_code": "request",
                            "unit_price": "1", "pricing_mode": "flat"}]}
        self.assertEqual(compare_prism(reference, prism)["capability_differences"], [
            {"model": "x", "product_id": 3, "source": "public", "field": "max_images",
             "document": 9, "prism": 1},
            {"model": "x", "product_id": 3, "source": "adapter", "field": "duration_options",
             "document": [6, 15], "prism": None},
            {"model": "x", "product_id": 3, "source": "adapter", "field": "duration_min",
             "document": 4, "prism": 5},
            {"model": "x", "product_id": 3, "source": "adapter.request",
             "field": "content_projections.images", "document": "image_url URL array",
             "prism": None},
        ])

    def test_media_url_arrays_require_exact_projections(self):
        reference = {"video": [{"model": "x", "price_cny": "1", "unit": "request",
                                "max_images": 9, "max_videos": 3, "max_audios": 3}], "image": []}
        constraints = {"max_images": 9, "max_videos": 3, "max_audios": 3,
                       "adapter": {"validation": {"models": {"x": {
                           "max_images": 9, "max_videos": 3, "max_audios": 3}}},
                           "request": {"include_content": True,
                                       "content_fields": {"url": "url"},
                                       "content_projections": [
                                           {"source": "url", "output": "array", "target": "images",
                                            "types": ["image_url"]},
                                           {"source": "provider_object", "output": "array", "target": "videos",
                                            "types": ["video_url"]},
                                       ]}}}
        prism = {"products": [{"id": 1, "vendor_model": "x", "offering_state": "active",
                               "capability_constraints": constraints}],
                 "costs": [{"product_id": 1, "offering_id": 1, "unit_code": "request",
                            "unit_price": "1", "pricing_mode": "flat"}]}
        differences = compare_prism(reference, prism)["capability_differences"]
        self.assertEqual([row["field"] for row in differences], [
            "content_projections.videos", "content_projections.audios"])
        constraints["adapter"]["request"]["content_projections"].extend([
            {"source": "url", "output": "array", "target": "videos", "types": ["video_url"]},
            {"source": "url", "output": "array", "target": "audios", "types": ["audio_url"],
             "models": ["x"]},
        ])
        self.assertEqual(compare_prism(reference, prism)["capability_differences"], [])

    def test_unit_mismatch_is_not_reported_as_price_difference(self):
        result = compare_prism(
            {"video": [{"model": "x", "price_cny": "2", "unit": "second"}], "image": []},
            {"products": [{"id": 1, "vendor_model": "x", "offering_state": "active"}],
             "costs": [{"product_id": 1, "offering_id": 1, "unit_code": "request",
                        "unit_price": "3", "pricing_mode": "flat"}]})
        self.assertEqual(result["cost_price_differences"], [])
        self.assertEqual(result["cost_unit_mismatches"][0]["model"], "x")


if __name__ == "__main__":
    unittest.main()
