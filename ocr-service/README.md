# hailsDotGO OCR microservice

A small FastAPI service that wraps a neural OCR engine (RapidOCR by default) and
returns recognised text plus a bounding box per line. The Go backend
(`internal/handlers/ocr.go`) calls it over loopback to read Pokemon GO
screenshots, the same way the mobile app uses Google ML Kit.

It is **not** part of `deploy.ps1` (which only syncs the Go app). Install it once
on the VPS with the runbook below; after that it runs under systemd and restarts
on boot.

## Contract

`POST /ocr` (multipart form field `image`) ->

```json
{
  "full_text": "CP2717\nKartana\n104 / 104 HP\n4,000\n...",
  "lines": [
    {"text": "CP2717", "x1": 290, "y1": 110, "x2": 540, "y2": 175, "score": 0.99}
  ]
}
```

`GET /health` -> `{"status": "ok", "engine": "..."}`

Bound to `127.0.0.1:18265` only. Never expose it publicly.

## VPS setup (one time)

Run as root on the VPS (Debian, Python 3.11 already present):

```bash
# 1. Code
mkdir -p /opt/hailsdotgo-ocr
# copy server.py + requirements.txt here (scp, git pull, or rsync from the repo's ocr-service/)

# 2. Virtualenv + deps (first install pulls ~15 MB of ONNX models on first request)
cd /opt/hailsdotgo-ocr
python3 -m venv venv
./venv/bin/pip install --upgrade pip
./venv/bin/pip install -r requirements.txt

# 3. systemd unit
cp hailsdotgo-ocr.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now hailsdotgo-ocr.service
systemctl status hailsdotgo-ocr.service --no-pager
```

Expected RSS once warm: ~0.5-1 GB (well within the VPS budget).

## Health / smoke test

```bash
curl -s http://127.0.0.1:18265/health
# scan a known screenshot (CP 2717 Kartana, CP 3102 Snorlax):
curl -s -F image=@/root/kartana.png http://127.0.0.1:18265/ocr | jq '.lines[].text'
# expect a line containing 2717 and one containing 4,000
```

## Logs

```bash
journalctl -u hailsdotgo-ocr.service -n 80 --no-pager
```

## Switching engine to PaddleOCR

If RapidOCR accuracy is insufficient, edit the engine block at the top of
`server.py` (one function, `_raw_recognize`), add `paddleocr` +
`paddlepaddle` to `requirements.txt`, reinstall, and restart the service. The
JSON contract and the Go backend stay unchanged. PaddleOCR peaks ~1.5-2 GB,
still within the VPS budget (raise `MemoryMax` in the unit if needed).
