#!/usr/bin/env python3
"""Generate Stringwork icon PNGs at 16, 48, 128px from pure Python (no deps)."""

import struct, zlib, math, os

def make_png(width, height, pixels):
    """Create a PNG file from RGBA pixel data."""
    def chunk(ctype, data):
        c = ctype + data
        return struct.pack('>I', len(data)) + c + struct.pack('>I', zlib.crc32(c) & 0xFFFFFFFF)

    raw = b''
    for y in range(height):
        raw += b'\x00'
        for x in range(width):
            raw += bytes(pixels[y * width + x])

    return (b'\x89PNG\r\n\x1a\n' +
            chunk(b'IHDR', struct.pack('>IIBBBBB', width, height, 8, 6, 0, 0, 0)) +
            chunk(b'IDAT', zlib.compress(raw, 9)) +
            chunk(b'IEND', b''))

def draw_circle(pixels, w, h, cx, cy, r, color, aa=True):
    """Draw a filled circle with anti-aliasing."""
    for y in range(max(0, int(cy - r - 2)), min(h, int(cy + r + 2))):
        for x in range(max(0, int(cx - r - 2)), min(w, int(cx + r + 2))):
            d = math.sqrt((x - cx) ** 2 + (y - cy) ** 2)
            if d <= r - 0.5:
                alpha = 1.0
            elif d <= r + 0.5 and aa:
                alpha = r + 0.5 - d
            else:
                continue
            idx = y * w + x
            cr, cg, cb, ca = color
            a = int(ca * alpha)
            old = pixels[idx]
            if old[3] == 0:
                pixels[idx] = (cr, cg, cb, a)
            else:
                t = a / 255
                pixels[idx] = (
                    int(old[0] * (1 - t) + cr * t),
                    int(old[1] * (1 - t) + cg * t),
                    int(old[2] * (1 - t) + cb * t),
                    min(255, old[3] + a)
                )

def draw_line(pixels, w, h, x0, y0, x1, y1, color, thickness=1.5):
    """Draw an anti-aliased line."""
    dx, dy = x1 - x0, y1 - y0
    length = math.sqrt(dx * dx + dy * dy)
    if length == 0:
        return
    steps = int(length * 3)
    for i in range(steps + 1):
        t = i / steps
        px, py = x0 + dx * t, y0 + dy * t
        for oy in range(-2, 3):
            for ox in range(-2, 3):
                ix, iy = int(px) + ox, int(py) + oy
                if 0 <= ix < w and 0 <= iy < h:
                    d = math.sqrt((ix - px) ** 2 + (iy - py) ** 2)
                    if d < thickness:
                        alpha = max(0, min(1, thickness - d))
                        idx = iy * w + ix
                        cr, cg, cb, ca = color
                        a = int(ca * alpha)
                        old = pixels[idx]
                        if old[3] == 0:
                            pixels[idx] = (cr, cg, cb, a)
                        else:
                            blend = a / 255
                            pixels[idx] = (
                                int(old[0] * (1 - blend) + cr * blend),
                                int(old[1] * (1 - blend) + cg * blend),
                                int(old[2] * (1 - blend) + cb * blend),
                                min(255, old[3] + a)
                            )

def generate_icon(size):
    """Render the Stringwork icon at given size."""
    scale = size / 128.0
    pixels = [(0, 0, 0, 0)] * (size * size)

    indigo = (79, 70, 229, 255)
    teal = (20, 184, 166, 255)

    cx, cy = 64 * scale, 64 * scale
    nodes = [
        (24 * scale, 38 * scale),
        (105 * scale, 31 * scale),
        (102 * scale, 103 * scale),
        (29 * scale, 104 * scale),
    ]

    line_w = max(1.0, 2.0 * scale)
    for nx, ny in nodes:
        draw_line(pixels, size, size, cx, cy, nx, ny, indigo, line_w)

    center_r = max(2.0, 8.0 * scale)
    draw_circle(pixels, size, size, cx, cy, center_r, indigo)
    draw_circle(pixels, size, size, cx, cy, max(1.0, 2.0 * scale), (248, 250, 252, 255))

    node_r = max(1.5, 5.0 * scale)
    for nx, ny in nodes:
        draw_circle(pixels, size, size, nx, ny, node_r, teal)

    return make_png(size, size, pixels)

if __name__ == '__main__':
    outdir = os.path.join(os.path.dirname(__file__), 'icons')
    os.makedirs(outdir, exist_ok=True)
    for sz in (16, 48, 128):
        path = os.path.join(outdir, f'icon{sz}.png')
        with open(path, 'wb') as f:
            f.write(generate_icon(sz))
        print(f'wrote {path} ({sz}x{sz})')
