# matrixqc

**Version-aware text QC for rendered video deliverables.** Read phone numbers, URLs
and legal text against each version’s delivery-matrix row; flag a variant that used
its sibling’s number, including a single-frame foreign-number injection.

## Synthetic-only M1 — not demand validation

**M1 results are measured on synthetic footage only and say nothing about real broadcast supers.**

Every generated campaign file, matrix, label and sample report is **synthetic**:
AI-authored fictional test data, procedurally rendered with ffmpeg. None is pilot,
customer, shop, payment or testimonial evidence. No paid pilot has been secured.
The founder waived the no-product-code condition **for M1 only** on 2026-10-09;
demand remains unvalidated and the two paid $49 pilots condition remains unmet.
Issues #5 and #6 (hosted checking and self-serve) stay on hold.

See [the measured benchmark](BENCHMARK.md), including the initial hard-typography
failures. The static sample lives in [`internal/qc/assets/site/sample/`](internal/qc/assets/site/sample/).
Open its `report.html` offline. No raw video is committed or embedded in the web image;
the sample report contains synthetic evidence crops only.

## What it checks

| ID | Check |
|---|---|
| C0 | File↔matrix joins: no row, no file, duplicates or ambiguous glob matches. |
| C1 | Each required value read exactly for at least 12 consecutive frames (configurable). |
| C2 | Foreign phone/URL values; name the other matrix row when it owns the value. One frame is enough. |
| C3 | FPO, TBD, XXX, LOREM, PLACEHOLDER and campaign-specific forbidden/placeholder text. |
| C4 | Unreadable, low-confidence, unsupported or near-match text requiring human review. |

### Outcomes

- **Pass:** expected values read exactly at the confidence floor, for the required
  consecutive frames, with no conflicting finding. Gated frames reuse the last read.
- **Fail:** a confident missing, foreign or forbidden value. A sibling’s known phone
  number remains C2 even if it differs from the expected value by just one digit.
- **Needs review:** unreadable/uncertain or near-match text. Fuzzy matching never
  produces pass. An unknown phone one digit off stays needs_review.
- **Error / not checked:** operational failures and C0 joins, **not a fourth QC verdict**.
  Unsupported decode, failed OCR and incomplete input never become pass.

## What it does NOT check

Logos, wrong pictures, grade, mattes, audio content, loudness and delivery specifications.
The report is **not a certificate, delivery sign-off or liability transfer**. It records
the checks, source timecodes, decoded/OCR frame counts and evidence read under the
chosen settings. There is no turnaround or general accuracy promise.

## Quick start (synthetic files only)

Prerequisites: Linux/amd64, Docker and Python 3. The runner bundles ffmpeg/ffprobe,
Tesseract, RapidOCR CPU models and fonts. Building downloads dependencies; the
commands below disable network access at runtime. Run from this repository.

```sh
docker build --target runner -t matrixqc-runner:local .
docker build --target web -t matrixqc-web:local .

# A fresh disk-backed scratch directory, never /tmp. Keep its path to inspect output.
root="${XDG_CACHE_HOME:-$HOME/.cache}/matrixqc"
mkdir -p "$root"
work="$(mktemp -d "$root/quickstart-XXXXXX")"
mkdir -p "$work/tmp" "$work/cache"

run_qc() {
  docker run --rm --network none --user "$(id -u):$(id -g)" \
    -e TMPDIR=/work/tmp -e XDG_CACHE_HOME=/work/cache \
    -v "$work:/work" matrixqc-runner:local "$@"
}

run_qc synth --seed 42 --variants 8 --seconds 5 --width 854 --height 480 \
  --out /work/synthetic-campaign

run_qc check --matrix /work/synthetic-campaign/synthetic-matrix.csv \
  --files /work/synthetic-campaign --engine rapidocr --out /work/rapidocr
run_qc check --matrix /work/synthetic-campaign/synthetic-matrix.csv \
  --files /work/synthetic-campaign --engine tesseract --out /work/tesseract

run_qc bench --labels /work/synthetic-campaign/synthetic-labels.json \
  --results /work/tesseract/results.json,/work/rapidocr/results.json \
  --benchmark /work/BENCHMARK.md
echo "Synthetic reports and benchmark: $work"

# Static demo only; stop it after inspecting http://127.0.0.1:8080.
docker run -d --name matrixqc-demo --read-only --cap-drop ALL \
  --security-opt no-new-privileges -p 127.0.0.1:8080:8080 matrixqc-web:local
python3 scripts/check_web.py http://127.0.0.1:8080
docker stop matrixqc-demo
docker rm matrixqc-demo
```

`synth` requires an empty output directory and labels videos with `SYNTHETIC` plus
embedded provenance. It writes `synthetic-matrix.csv`, `synthetic-labels.json` and
`SYNTHETIC.json`. With the defaults, both measured engines produced 2 pass, 4 fail,
1 needs_review and 1 intentional C0 orphan. CI verifies the verdicts and C0–C4 IDs,
including that injection frame 60 reached OCR. This is a small fixture, not a recall guarantee.

Each `check` writes `results.json`, a self-contained `report.html` and evidence crops.
Effective consecutive-frame, confidence, campaign-token and gate settings are recorded
in both JSON and HTML. Textless leaders do not invalidate later confirmed text;
detected but unreadable text still requires review.
Open `$work/rapidocr/report.html` locally; filtering and printing need no network.
A successfully written report exits 0 even if some deliverables fail or have errors:
**inspect the outcomes**, not just the CLI exit status. CLI/configuration failures exit 1.

`bench` refuses unexpected analysis errors or incomplete frame coverage rather than
publishing misleading accuracy or gate statistics. The deliberately labelled C0
orphan is reported separately as an expected error. An all-synthetic heading requires
both batch and label provenance to agree.

`bench` can also run the checker directly when `--results` is omitted:

```sh
run_qc bench --labels /work/synthetic-campaign/synthetic-labels.json \
  --matrix /work/synthetic-campaign/synthetic-matrix.csv --files /work/synthetic-campaign \
  --engine tesseract --out /work/bench-tesseract --benchmark /work/BENCHMARK-tesseract.md
```

## Matrix CSV format

Strict UTF-8 CSV, header required. `file` is required: an exact basename or glob,
never a path. Optional `version_id` values must be unique when non-empty; `language`
is a language tag (the M1 fixture covers `en-GB` and `fr-FR` only). Any number of
`phone:<label>`, `url:<label>`, `text:<label>` and `forbid:<label>` columns may follow.
Unknown or duplicate headers, empty labels and malformed cells report row and column.

```csv
file,version_id,language,phone:contact,url:offer,text:legal,forbid:draft
synthetic-example.mp4,synthetic-example,en-GB,+1 202 555 0100,https://example.invalid/offer,Terms apply,INTERNAL ONLY
```

The example and downloadable template are **synthetic**, not a customer matrix.
An empty expectation cell means **not expected in this version**, not “missing”.
Each file must join exactly one row and each row exactly one file. A new matrix is a
new immutable SHA-256 recorded in every batch; originals are never edited.

| Kind | Normalisation / matching |
|---|---|
| Phone | ASCII digits, preserving a leading `+`; exact digit string. |
| URL | Lowercase, remove HTTP(S) scheme and trailing `/`; exact value. |
| Text / forbid | Unicode NFKC, casefold, whitespace/punctuation collapsed; exact contiguous normalised phrase in an OCR line. |

## Settings and local development

`check` and direct-run `bench` accept `--min-frames 12`, `--confidence 0.8`,
`--gate-threshold 1`, `--no-gate` (OCR every frame) and repeated `--placeholder TOKEN`.
Every frame is decoded; the 16×16 tile gate compares against the last OCR’d frame.
The gate is heuristic: the seeded injection is tested, **not every possible
single-frame change**. `--no-gate` is the correctness/cost baseline.

For a native build, install Go 1.24+ (CI uses 1.27.2), ffmpeg with `drawtext`/libx264,
ffprobe, Tesseract with English trained data and DejaVu fonts at
`/usr/share/fonts/truetype/dejavu`. Homebrew’s minimal `ffmpeg` lacks `drawtext`;
put `ffmpeg-full/bin` first on PATH if using it. For RapidOCR, use the runner or the
pinned packages in `ocr/requirements.txt`; see [offline OCR details](ocr/README.md).
`MATRIXQC_PYTHON` selects Python, `MATRIXQC_OCRD` the sidecar file, and
`MATRIXQC_MODEL_DIR` an optional directory whose models must match `ocr/models.sha256`.

```sh
go build -o matrixqc ./cmd/matrixqc
./matrixqc serve --addr 127.0.0.1:8080
```

`serve` embeds the site, requires no home/cache directory and writes no files.
`PORT` defaults to 8080. Set public, non-secret `PILOT_URL` to a **founder-issued**
HTTP(S) payment link or form. Otherwise set `CONTACT_EMAIL`; no price button appears.
With neither set, the page says the pilot link is not configured. No accounts,
upload, queue, object storage, billing integration, database or secret exists in M1.
Only the `web` target is for later INFRA deployment; the runner stays local.

## Seeding (founder’s later real-file workflow)

**No agent handles real footage in M1.** Only after permission and paid pilots exist,
the founder or shop independently verifies pristine originals, reshapes the matrix,
and labels the results on the machine that holds the footage.

`seed --matrix <matrix.csv> --out <empty-cache-directory>` leaves originals untouched:
it swaps the first two rows’ phones, requires a known absent legal string in the third
and changes one phone digit in the fourth. At least four complete rows, a phone column
and a text column are required. It writes `seeded-matrix.csv` and `seeded-labels.json`.
Missing legal is **not** seeded by blanking the cell, because blank disables C1.
Selected swap values must be distinct and uniquely owned; ambiguous seed inputs are
rejected instead of producing false critical labels. Add `--files <source-directory>`
to carry explicit `SYNTHETIC.json` provenance into labels. Names beginning with
`synthetic-` alone are never accepted as provenance; without source evidence the
seed's provenance stays unknown/non-synthetic.

`seed --splice --matrix <matrix.csv> --files <directory> --source-a <basename>
--source-b <sibling-basename> --frame <index> --out <empty-cache-directory>` replaces
one frame in A with B’s frame, using lossless FFV1 `.mkv` in scratch and symlinks for
untouched siblings. Dimensions and frame rates must agree; filenames must be exact.
Use a sibling with a genuinely different expected phone/URL. Do not score seed labels
as truth without checking originals: existing errors or ambiguous inputs invalidate
the seed assumptions. Injected files are tests, not original pilot evidence.

## Verification

```sh
go test -short -count=1 ./...
go vet ./...
python3 -m unittest discover -s ocr -v
python3 -m unittest discover -s scripts -v
docker build --target test -t matrixqc-test:local .
docker run --rm --network none matrixqc-test:local
```

The Docker test target runs the full suite with both real engines, decode/gate,
protocol failure/timeout, missing/foreign/placeholder/near-match, report escaping,
metrics and lossless one-frame splice tests. The single self-hosted Jarvis CI job
also generates the 8×5-second corpus, checks both engines, asserts semantic report
parity and builds/tests the read-only non-root web image. It neither publishes images
nor deploys on pull requests or feature-branch pushes. After merge to `main`, the
same job publishes the **already-tested** `web` and `runner` images to
`ghcr.io/alaborderie/matrixqc-web` and `ghcr.io/alaborderie/matrixqc-runner`, tagged
`latest` and the full commit SHA. Only an installed `matrixqc` web Deployment is
restarted over SSH, with a bounded readiness wait; the runner is never deployed.
CI mutation tests demonstrate that removing any C0–C4 finding or gating
out the injected frame breaks the end-to-end oracle.

## Deployment handoff (Gate 2 required)

The multi-stage Dockerfile's `web` target uses `scratch`, UID/GID `10001`, and
port `8080`. It embeds all site files, needs no runtime secrets or writable volume,
and exposes `GET /healthz`. Manifests belong in the separate
[`alaborderie/k3s-ovh-config`](https://github.com/alaborderie/k3s-ovh-config) repository:
`matrixqc.yaml` and `matrixqc-ingressroute.yaml`. Keep image names, port, environment,
probes and rollout target coordinated across the two repositories.

Public origin: **https://matrixqc.antoinelaborderie.com/**. This is the synthetic
M1 demo only. `PILOT_URL` is left unset; `CONTACT_EMAIL` is public non-secret config.
Do not fabricate a payment link or release hosted checking (#5/#6).

After Gate 2, merge the app PR first and wait for successful image publication.
The remote script skips restart only if Kubernetes confirms that the Deployment
does not exist; authentication/API errors still fail. Then merge the infra PR and
wait for its Ansible workflow and pod readiness. Subsequent app main pushes restart
the existing Deployment. No PR/feature-branch run publishes or touches the VPS.

Deployment acceptance requires both PRs merged, a **direct** HTTPS 2xx from
`/healthz`, non-empty HTML with 2xx at `/`, and non-empty `text/css` with 2xx from
every same-origin linked stylesheet. Check `/style.css` explicitly too (the landing
currently embeds its CSS). For landing/assets, reject userinfo, cross-origin or
HTTP redirects; allow at most five hops and one shared 15-second deadline per
resource, including all redirects. Also open the synthetic sample report and CSV
downloads. A healthy pod or health endpoint alone is not deployment acceptance.

Rollback uses a **new reviewed PR**, never `kubectl rollout undo` against mutable
`latest`: revert the faulty app change and let main rebuild/publish/restart. A saved
good SHA tag/digest can be pinned in an emergency infra PR. To stop first deployment,
set replicas to zero via an infra PR (the retained route will return 503). Simply
deleting manifest files does not delete live resources in this Ansible reconciler.

## Limitations and stop point

- Only a small AI-authored synthetic campaign was measured. Larger fonts/shorter
  reserved URLs were needed for the clean fixtures; initial harder URLs raised 50%
  needs_review. Real stylised, small, busy-picture or non-Latin supers are unvalidated.
- Constant-rate ffmpeg-decodable video is the intended local input; container support
  is not tested broadcast compliance. OCR language coverage is limited. Preparation
  and human review time are not measured by the harness.
- Unknown phone-shaped values can be prices/dates/product codes; C2 may false-alarm.
  OCR/gate errors can miss real content. A report cannot prove absence or perfect recall.
- CPU cost includes waited-for child processes; wall time includes startup. Runtime
  network is not needed. Changing library, model, encoder or font versions may change
  OCR results or synthetic video bytes; deterministic synthesis is within one environment.
- No real-file performance, accuracy, willingness-to-pay or turnaround claim. The
  founder must secure two independent paid $49 pilots with permissioned footage and
  actual matrices, measure critical misses, clean false alarms, compute and total
  prep/review savings, and determine whether footage may leave each shop.

**Stop after M1.** Do not release #5/#6 or build hosted checking from synthetic numbers.
