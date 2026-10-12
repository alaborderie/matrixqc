import importlib.util
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("ocrd", Path(__file__).with_name("ocrd.py"))
ocrd = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ocrd)


class RequestTests(unittest.TestCase):
    def test_invalid_frame_cannot_be_recognized(self):
        for request in [
            {},
            {"frame_id": 1, "w": 2, "h": 2, "gray": ""},
            {"frame_id": 1, "w": 2, "h": 2, "gray": "!!!!"},
            {"frame_id": 1, "w": -2, "h": 2, "gray": "AAAAAA=="},
        ]:
            with self.subTest(request=request), self.assertRaises(ValueError):
                ocrd.decode_request(request)


if __name__ == "__main__":
    unittest.main()
