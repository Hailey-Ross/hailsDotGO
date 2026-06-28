"""
hailsDotGO OCR microservice.

A tiny FastAPI app that wraps a neural OCR engine (RapidOCR by default) and
exposes a single endpoint the Go backend calls over localhost. It returns the
recognised text plus a bounding box per text line, so the Go parser can filter
by position exactly like the mobile app does with Google ML Kit.

Swapping engines (e.g. RapidOCR -> PaddleOCR) only touches the `recognize`
function below; the JSON contract and the Go side stay unchanged.
"""

import io
import os

import numpy as np
from fastapi import FastAPI, File, UploadFile
from PIL import Image

# ---- Engine (RapidOCR by default; swap this block for PaddleOCR if needed) ----
#
# PaddleOCR alternative:
#     from paddleocr import PaddleOCR
#     _engine = PaddleOCR(use_angle_cls=True, lang="en", show_log=False)
#     def _raw_recognize(np_img):
#         out = _engine.ocr(np_img, cls=True)
#         # out: [[ [box4pts], (text, score) ], ...]  (wrapped one level deep)
#         rows = out[0] if out and out[0] else []
#         return [(row[0], row[1][0], float(row[1][1])) for row in rows]

from rapidocr_onnxruntime import RapidOCR

_engine = RapidOCR()


def _raw_recognize(np_img):
    """Return a list of (box_4pts, text, score) for the given RGB numpy image."""
    result, _ = _engine(np_img)
    if not result:
        return []
    return [(box, text, float(score)) for box, text, score in result]


# ---- Normalisation: 4-point box -> axis-aligned x1/y1/x2/y2 ----

def recognize(np_img):
    """Run the engine and normalise its output to a list of line dicts."""
    lines = []
    for box, text, score in _raw_recognize(np_img):
        xs = [p[0] for p in box]
        ys = [p[1] for p in box]
        lines.append(
            {
                "text": text,
                "x1": int(min(xs)),
                "y1": int(min(ys)),
                "x2": int(max(xs)),
                "y2": int(max(ys)),
                "score": round(score, 4),
            }
        )
    return lines


app = FastAPI(title="hailsDotGO OCR", version="1.0")


@app.get("/health")
def health():
    return {"status": "ok", "engine": _engine.__class__.__module__}


@app.post("/ocr")
async def ocr(image: UploadFile = File(...)):
    raw = await image.read()
    img = Image.open(io.BytesIO(raw)).convert("RGB")
    np_img = np.asarray(img)
    lines = recognize(np_img)
    full_text = "\n".join(line["text"] for line in lines)
    return {"full_text": full_text, "lines": lines}


if __name__ == "__main__":
    import uvicorn

    port = int(os.getenv("OCR_PORT", "18265"))
    uvicorn.run(app, host="127.0.0.1", port=port, workers=1)
