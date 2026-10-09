FROM golang:1.27-bookworm AS build
ENV TMPDIR=/var/cache/matrixqc
RUN mkdir -p /var/cache/matrixqc
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/matrixqc ./cmd/matrixqc

FROM python:3.11-slim-bookworm AS runner
ENV TMPDIR=/home/matrixqc/.cache/matrixqc \
    XDG_CACHE_HOME=/home/matrixqc/.cache \
    PYTHONDONTWRITEBYTECODE=1 \
    PIP_NO_CACHE_DIR=1 \
    OMP_THREAD_LIMIT=2 \
    MATRIXQC_OCRD=/opt/matrixqc/ocr/ocrd.py
RUN mkdir -p "$TMPDIR" && apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg tesseract-ocr tesseract-ocr-eng fonts-dejavu-core libgl1 libglib2.0-0 \
    && rm -r /var/lib/apt/lists/*
COPY ocr /opt/matrixqc/ocr
RUN pip install -r /opt/matrixqc/ocr/requirements.txt \
    && python /opt/matrixqc/ocr/ocrd.py --metadata
COPY --from=build /out/matrixqc /usr/local/bin/matrixqc
RUN useradd --uid 10001 --create-home matrixqc \
    && chown -R matrixqc:matrixqc /home/matrixqc
USER 10001:10001
WORKDIR /home/matrixqc
ENTRYPOINT ["matrixqc"]

FROM runner AS test
COPY --from=build /usr/local/go /usr/local/go
COPY --from=build --chown=10001:10001 /go/pkg/mod /home/matrixqc/go/pkg/mod
ENV PATH=/usr/local/go/bin:$PATH MATRIXQC_OCR_INTEGRATION=1
COPY --chown=10001:10001 . /src
WORKDIR /src
ENTRYPOINT ["go", "test", "./..."]
