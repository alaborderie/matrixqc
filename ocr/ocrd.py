"""JSON-lines OCR sidecar; gray is base64-encoded bytes. CPU, offline only."""

import base64
import contextlib
import hashlib
import importlib.metadata
import importlib.util
import json
import os
import sys
from pathlib import Path

def decode_request(request):
    try:
        frame_id, w, h = request["frame_id"], request["w"], request["h"]
        if any(type(x) is not int for x in (frame_id, w, h)):
            raise ValueError("frame id and dimensions must be integers")
        if frame_id < 0 or w <= 0 or h <= 0 or w * h > 64 * 1024 * 1024:
            raise ValueError("invalid frame id or dimensions")
        gray = base64.b64decode(request["gray"], validate=True)
        if len(gray) != w * h:
            raise ValueError("gray byte count does not match dimensions")
        return frame_id, w, h, gray
    except (KeyError, TypeError, base64.binascii.Error) as exc:
        raise ValueError("invalid gray frame request") from exc


def models():
    spec = importlib.util.find_spec("rapidocr")
    if spec is None:
        raise ValueError("rapidocr is not installed; use the runner image")
    root = Path(os.environ.get("MATRIXQC_MODEL_DIR", Path(spec.origin).parent / "models"))
    manifest = Path(__file__).with_name("models.sha256")
    paths = []
    for line in manifest.read_text().splitlines():
        expected, name = line.split()
        path = root / name
        if not path.is_file() or hashlib.sha256(path.read_bytes()).hexdigest() != expected:
            raise ValueError(f"missing or hash-mismatched offline model: {name}")
        paths.append(path)
    return paths, hashlib.sha256(manifest.read_bytes()).hexdigest()


def main():
    paths, digest = models()
    if "--metadata" in sys.argv:
        print(json.dumps({"engine": "rapidocr", "engine_version": importlib.metadata.version("rapidocr"), "model_sha256": digest}))
        return
    with contextlib.redirect_stdout(sys.stderr):
        import numpy as np
        from rapidocr import RapidOCR

        engine = RapidOCR(params={
            "Det.model_path": str(paths[0]),
            "Rec.model_path": str(paths[1]),
            "Cls.model_path": str(paths[2]),
            "Global.text_score": 0.0,
            "Global.log_level": "error",
            "EngineConfig.onnxruntime.use_cuda": False,
            "EngineConfig.onnxruntime.use_coreml": False,
            "EngineConfig.onnxruntime.intra_op_num_threads": 2,
            "EngineConfig.onnxruntime.inter_op_num_threads": 1,
        })
    for line in sys.stdin:
        request = {}
        try:
            request = json.loads(line)
            frame_id, w, h, gray = decode_request(request)
            image = np.frombuffer(gray, dtype=np.uint8).reshape((h, w))
            with contextlib.redirect_stdout(sys.stderr):
                result = engine(image, use_cls=False, use_rec=True)
            boxes = []
            if result.boxes is not None and result.txts is not None:
                for quad, text, conf in zip(result.boxes, result.txts, result.scores):
                    boxes.append({"quad": quad.tolist(), "text": text, "conf": float(conf)})
            # Keep detected regions that recognition filtered out (empty strings).
            # This extra detection is conservative, not a silent disappearance.
            with contextlib.redirect_stdout(sys.stderr):
                detected = engine(image, use_cls=False, use_rec=False)
            if detected.boxes is not None:
                for quad in detected.boxes:
                    center = quad.mean(axis=0)
                    if not any(np.linalg.norm(center - np.asarray(b["quad"]).mean(axis=0)) < 5 for b in boxes):
                        boxes.append({"quad": quad.tolist(), "text": "", "conf": 0.0})
            print(json.dumps({"frame_id": frame_id, "boxes": boxes}, allow_nan=False), flush=True)
        except Exception as exc:
            print(json.dumps({"frame_id": request.get("frame_id"), "error": str(exc)}), flush=True)
            return


if __name__ == "__main__":
    main()
