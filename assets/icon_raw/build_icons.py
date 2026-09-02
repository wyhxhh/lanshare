#!/usr/bin/env python3
"""
程序化绘制 LanShare 图标（不再依赖 AI 抠图）：
- 1024 蓝渐变圆角方块（squircle）背景
- 两台对角笔记本（圆角矩形描边+圆角矩形底座）
- 中央顶部 WiFi 信号弧
- 中央底部循环传输箭头（双弧+三角头）

输出：
- assets/icon_raw/app-1024.png    高清源
- internal/gui/icon/app.png       256 PNG 供 Fyne go:embed
- assets/icon/app.ico             16/24/32/48/64/128/256 多尺寸 ICO 供 exe 图标
"""
from PIL import Image, ImageDraw
import math
import os

_HERE = os.path.dirname(os.path.abspath(__file__))
# 仓库根 = icon_raw 的上级上级（assets/icon_raw -> 仓库根）
_ROOT = os.path.abspath(os.path.join(_HERE, "..", ".."))

OUT_SRC  = os.path.join(_ROOT, "assets", "icon_raw", "app-1024.png")
OUT_FYNE = os.path.join(_ROOT, "internal", "gui", "icon", "app.png")
OUT_ICO  = os.path.join(_ROOT, "assets", "icon", "app.ico")

W = 1024
RADIUS = 224  # iOS squircle 圆角 (~21.9%)
WHITE = (255, 255, 255, 255)


def lerp_rgb(c1, c2, t):
    return tuple(int(c1[i] + (c2[i] - c1[i]) * t) for i in range(3))


def make_background():
    """蓝渐变 squircle 背景。"""
    grad = Image.new("RGB", (1, W))
    for y in range(W):
        t = y / (W - 1)
        grad.putpixel(
            (0, y),
            lerp_rgb((0x2F, 0x6B, 0xFF), (0x1D, 0x54, 0xE6), t),
        )
    grad = grad.resize((W, W))

    bg = Image.new("RGBA", (W, W), (0, 0, 0, 0))
    mask = Image.new("L", (W, W), 0)
    ImageDraw.Draw(mask).rounded_rectangle((0, 0, W - 1, W - 1), radius=RADIUS, fill=255)
    bg.paste(grad, (0, 0), mask)
    return bg


def draw_laptop(d: ImageDraw.ImageDraw, x0: int, y0: int, w: int, h: int, stroke: int):
    """一台对角放置的笔记本：圆角矩形屏幕（白描边）+ 圆角矩形底座（白填充）。"""
    r = max(18, h // 10)
    # 屏幕描边
    d.rounded_rectangle((x0, y0, x0 + w - 1, y0 + h - 1), radius=r, outline=WHITE, width=stroke)
    # 底座（屏幕下方略宽一点的白填充矩形）
    base_w = int(w * 1.18)
    base_h = max(20, stroke + 12)
    bx = x0 + (w - base_w) // 2
    by = y0 + h + 18
    d.rounded_rectangle(
        (bx, by, bx + base_w - 1, by + base_h - 1),
        radius=base_h // 2,
        fill=WHITE,
    )


def draw_wifi(d: ImageDraw.ImageDraw, cx: int, cy: int, stroke: int):
    """WiFi 信号弧：3 条同心弧 + 底部小圆点，弧口朝下。"""
    for r in (95, 150, 205):
        d.arc(
            (cx - r, cy - r, cx + r, cy + r),
            start=205,
            end=335,
            fill=WHITE,
            width=stroke,
        )
    dot_r = stroke // 2 + 2
    d.ellipse(
        (cx - dot_r, cy + int(stroke * 1.8), cx + dot_r, cy + int(stroke * 1.8) + dot_r * 2),
        fill=WHITE,
    )


def draw_transfer_arrow(d: ImageDraw.ImageDraw, cx: int, cy: int, length: int, stroke: int):
    """横向双向传输箭头：横线 + 两端指向外侧的三角箭头。
    比循环箭头更易识别，绘制可靠（Pillow arc + 自定义多边形组合易变形）。"""
    half = length // 2
    tip_gap = stroke * 1
    # 中线段（左右各给三角留 space）
    d.line(
        (cx - half + tip_gap, cy, cx + half - tip_gap, cy),
        fill=WHITE, width=stroke,
    )
    # 左三角（指向左）
    L = [
        (cx - half - stroke, cy),
        (cx - half + tip_gap + stroke * 0.4, cy - stroke),
        (cx - half + tip_gap + stroke * 0.4, cy + stroke),
    ]
    d.polygon(L, fill=WHITE)
    # 右三角（指向右）
    R = [
        (cx + half + stroke, cy),
        (cx + half - tip_gap - stroke * 0.4, cy - stroke),
        (cx + half - tip_gap - stroke * 0.4, cy + stroke),
    ]
    d.polygon(R, fill=WHITE)


def compose():
    img = make_background()
    d = ImageDraw.Draw(img)

    STROKE = 28  # 笔记本描边/弧线宽度（缩到 16/24/32 时仍清晰）

    # 上：WiFi 信号弧（画面顶部 ~y=270）
    draw_wifi(d, cx=W // 2, cy=240, stroke=STROKE)

    # 中：双向传输箭头（WiFi 与笔记本之间）
    draw_transfer_arrow(d, cx=W // 2, cy=510, length=240, stroke=STROKE)

    # 下：两台对角笔记本
    draw_laptop(d, x0=80, y0=620, w=370, h=270, stroke=STROKE)
    draw_laptop(d, x0=W - 80 - 370, y0=620, w=370, h=270, stroke=STROKE)

    return img


def main():
    final = compose()
    os.makedirs(os.path.dirname(OUT_SRC), exist_ok=True)
    final.save(OUT_SRC)

    os.makedirs(os.path.dirname(OUT_FYNE), exist_ok=True)
    final.resize((256, 256), Image.LANCZOS).save(OUT_FYNE)

    os.makedirs(os.path.dirname(OUT_ICO), exist_ok=True)
    ico_sizes = [(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)]
    final.save(OUT_ICO, format="ICO", sizes=ico_sizes)

    print("1024:", OUT_SRC)
    print("FYNE:", OUT_FYNE)
    print("ICO :", OUT_ICO)


if __name__ == "__main__":
    main()