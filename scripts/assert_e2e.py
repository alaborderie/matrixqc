"""Independent CI oracle: checks labels, actual findings, report markup and drift."""

import json
import sys
from html.parser import HTMLParser
from pathlib import Path


def assert_cases(labels, results):
    assert results["synthetic"] is True, "unlabelled synthetic batch"
    actual = {d["path"]: d for d in results["deliverables"]}
    assert len(actual) == len(results["deliverables"]) == len(labels), "coverage differs"
    for label in labels:
        assert label["synthetic"] is True
        result = actual[label["deliverable_path"]]
        assert result["outcome"] == label["expected_outcome"], (label, result["outcome"])
        checks = {f["check"] for f in result["findings"]}
        assert set(label["expected_checks"]) <= checks, (label, checks)
        assert set(label["expected_checks"]) <= set(result["checks_run"])
        if "C0" not in checks:
            assert result["frames_decoded"] == result["frame_count"] == 120, "not every frame decoded"
            assert result["frames_ocrd"] > 0
        if label.get("injection_frame") is not None:
            injection = label["injection_frame"]
            assert injection in result["ocr_frame_ids"], "single-frame injection gated out"
            assert any(f["check"] == "C2" and f["first_frame"] <= injection <= f["last_frame"]
                       and f["other_version_row_id"] for f in result["findings"]), "one-frame foreign owner lost"
        for finding in result["findings"]:
            assert finding["id"] and finding["deliverable_id"] == result["id"]
            if finding["check"] != "C0":
                assert finding["evidence_path"] and finding["timecode"], "missing real evidence"


def signature(results):
    # Stable semantic check IDs, not CPU-dependent confidences or generated batch IDs.
    return {d["path"]: (d["outcome"], sorted({f["check"] for f in d["findings"]}))
            for d in results["deliverables"]}


class ReportParser(HTMLParser):
    def __init__(self):
        super().__init__()
        self.outcomes = []
        self.checks = []

    def handle_starttag(self, tag, attributes):
        attrs = dict(attributes)
        if "data-outcome" in attrs:
            self.outcomes.append(attrs["data-outcome"])
        if "data-check" in attrs:
            self.checks.append(attrs["data-check"])
        if tag in ("img", "script", "link"):
            target = attrs.get("src", attrs.get("href", ""))
            assert not target or target.startswith("data:"), "report makes external requests"


def assert_report(path, results):
    text = Path(path).read_text()
    assert "Synthetic" in text and "Not checked" in text and "not a certificate" in text
    parser = ReportParser()
    parser.feed(text)
    assert sorted(parser.outcomes) == sorted(d["outcome"] for d in results["deliverables"])
    assert sorted(parser.checks) == sorted(f["check"] for d in results["deliverables"] for f in d["findings"])


def main():
    labels_path, results_path, report_path, sample_path = sys.argv[1:]
    labels = json.loads(Path(labels_path).read_text())
    results = json.loads(Path(results_path).read_text())
    sample = json.loads(Path(sample_path).read_text())
    assert_cases(labels, results)
    assert signature(results) == signature(sample), "committed sample verdict/check drift"
    assert_report(report_path, results)
    assert_report(Path(sample_path).with_name("report.html"), sample)
    print("Synthetic C0–C4, one-frame gate and sample report assertions passed.")


if __name__ == "__main__":
    main()
