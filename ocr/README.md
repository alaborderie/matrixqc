# Offline OCR engines

RapidOCR 3.9.2 runs on ONNX Runtime CPU (two intra-op threads). Its wheel includes
the three models in `models.sha256`. The runner verifies each full SHA-256 at build
and again at startup, before creating the engine. Explicit local paths prevent
fallback downloads; missing or changed files stop the run. `Global.text_score=0`
retains low-confidence reads for C4 rather than dropping them. Empty detected
regions are retained too. Stdout is exclusively the JSON-lines protocol.

Requests: `{"frame_id":0,"w":2,"h":2,"gray":"AAAAAA=="}` (base64 grayscale).
Responses: `{"frame_id":0,"boxes":[{"quad":[[0,0],[1,0],[1,1],[0,1]],"text":"…","conf":0.9}]}`.
Exceptions, invalid responses, process exit and the 30-second per-frame deadline
are errors, never successful empty responses. No frames are written to disk.

Tesseract reads PGM bytes over stdin and returns TSV. Go joins words by OCR line,
with the minimum word confidence. Its English trained-data SHA-256 and executable
version are recorded in every batch. This is not an assertion that all languages
or all broadcast typography are supported.

Verification: `go test ./...`, `python3 -m unittest discover -s ocr`, and the Docker
`test` target with `--network none` and `MATRIXQC_OCR_INTEGRATION=1`. The last test
runs both real engines on AI-authored synthetic footage; no customer footage or
pilot evidence is used.

Sources checked during BUILD:
- https://pypi.org/pypi/rapidocr/3.9.2/json (wheel SHA-256 `04d6b8d151f823d930bd91910555f57bea897c0c44fa6794267b94cf9c1ef9a0`)
- https://github.com/RapidAI/RapidOCR (local model-path and ONNX CPU configuration)

The model digests were computed from that hash-verified release wheel, not from
an unversioned download. This is a reproducibility boundary, not an accuracy claim.
