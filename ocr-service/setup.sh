#!/usr/bin/env bash
# One-shot installer for the hailsDotGO OCR microservice (RapidOCR).
# Idempotent: safe to re-run. Expects the service files to already live in
# /opt/hailsdotgo-ocr (server.py, requirements.txt, hailsdotgo-ocr.service).
# Run as root:  bash /opt/hailsdotgo-ocr/setup.sh
set -euo pipefail

DIR=/opt/hailsdotgo-ocr
cd "$DIR"

echo "==> [1/5] System packages (python venv + OpenCV libs)"
apt-get update -qq
apt-get install -y python3-venv python3-pip libgl1 libglib2.0-0 curl >/dev/null

echo "==> [2/5] Virtualenv + Python deps (takes a few minutes)"
[ -d venv ] || python3 -m venv venv
./venv/bin/pip install --quiet --upgrade pip
./venv/bin/pip install --quiet -r requirements.txt

echo "==> [3/5] Warming up the OCR engine"
./venv/bin/python -c "from rapidocr_onnxruntime import RapidOCR; RapidOCR(); print('    rapidocr import ok')"

echo "==> [4/5] Installing + (re)starting systemd service"
cp hailsdotgo-ocr.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable hailsdotgo-ocr.service >/dev/null 2>&1 || true
systemctl restart hailsdotgo-ocr.service
sleep 2

echo "==> [5/5] Health check"
if [ "$(systemctl is-active hailsdotgo-ocr.service)" != "active" ]; then
  echo "    SERVICE NOT ACTIVE -- recent logs:"
  journalctl -u hailsdotgo-ocr.service -n 20 --no-pager
  exit 1
fi
curl -fsS http://127.0.0.1:18265/health && echo
echo
echo "DONE. OCR service is live on http://127.0.0.1:18265"
echo "Smoke test with a screenshot:"
echo "  curl -s -F image=@/root/scan.png http://127.0.0.1:18265/ocr | python3 -m json.tool"
