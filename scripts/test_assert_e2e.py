import copy
import importlib.util
import json
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("assert_e2e", Path(__file__).with_name("assert_e2e.py"))
checks = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checks)


class AssertionTests(unittest.TestCase):
    def test_disabling_each_check_fails_e2e_assertion(self):
        sample = Path(__file__).parents[1] / "internal/qc/assets/site/sample"
        labels = json.loads((sample / "synthetic-labels.json").read_text())
        results = json.loads((sample / "results.json").read_text())
        for disabled in ("C0", "C1", "C2", "C3", "C4"):
            mutated = copy.deepcopy(results)
            for result in mutated["deliverables"]:
                result["findings"] = [f for f in result["findings"] if f["check"] != disabled]
            with self.subTest(check=disabled), self.assertRaises(AssertionError):
                checks.assert_cases(labels, mutated)

    def test_gate_miss_fails_e2e_assertion(self):
        sample = Path(__file__).parents[1] / "internal/qc/assets/site/sample"
        labels = json.loads((sample / "synthetic-labels.json").read_text())
        results = json.loads((sample / "results.json").read_text())
        for result in results["deliverables"]:
            result["ocr_frame_ids"] = [i for i in (result["ocr_frame_ids"] or []) if i != 60]
        with self.assertRaises(AssertionError):
            checks.assert_cases(labels, results)


if __name__ == "__main__":
    unittest.main()
