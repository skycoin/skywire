#!/usr/bin/env python3
"""Writes Assets.xcassets: the Material icons Android draws, the logo, the app icon.

Icons are Google's Material Design Icons (Apache-2.0, npm @material-design-icons/svg), fetched once and committed.
"""
import json
import os
import re
import subprocess
import sys
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CATALOG = os.path.join(ROOT, "Skywire", "Assets.xcassets")
ANDROID_RES = os.path.join(os.path.dirname(ROOT), "android", "app", "src", "main", "res")
CDN = "https://cdn.jsdelivr.net/npm/@material-design-icons/svg@0.14.15"
FOLDER = {"Filled": "filled", "Outlined": "outlined", "Rounded": "round"}

ICONS = """
Filled.ArrowDropDown Filled.Call Filled.CallEnd Filled.Close Filled.ExpandLess
Filled.ExpandMore Filled.Mic Filled.MicOff Filled.Pause Filled.Person
Filled.PlayArrow Filled.Refresh Filled.Share Filled.VolumeDown Filled.VolumeUp
Filled.KeyboardArrowRight Filled.Star
Outlined.AccountBalanceWallet Outlined.Add Outlined.ArrowDownward
Outlined.ArrowUpward Outlined.Chat Outlined.CheckCircle Outlined.Close
Outlined.ContentCopy Outlined.DeleteOutline Outlined.Dns Outlined.Edit
Outlined.ErrorOutline Outlined.HelpOutline Outlined.Home
Outlined.KeyboardArrowDown Outlined.KeyboardArrowRight Outlined.Lock
Outlined.LockOpen Outlined.MoreVert Outlined.OpenInNew Outlined.QrCodeScanner
Outlined.Schedule Outlined.ScreenshotMonitor Outlined.Search Outlined.Settings
Outlined.StarBorder Outlined.Visibility
Rounded.AccountBalanceWallet Rounded.AltRoute Rounded.ArrowBack
Rounded.CandlestickChart Rounded.Chat Rounded.ChevronRight Rounded.Forum
Rounded.HelpOutline Rounded.Home Rounded.Hub Rounded.Route Rounded.Settings
Rounded.Videocam Rounded.VpnLock
""".split()


def snake(name):
    return re.sub(r"(?<!^)(?=[A-Z])", "_", name).lower()


def asset_name(icon):
    variant, name = icon.split(".")
    return f"mi-{variant.lower()}-{snake(name).replace('_', '-')}"


def write_json(path, data):
    with open(path, "w") as f:
        json.dump(data, f, indent=2)
        f.write("\n")


INFO = {"author": "xcode", "version": 1}


def imageset(name, filename, data, template, vector):
    folder = os.path.join(CATALOG, name + ".imageset")
    os.makedirs(folder, exist_ok=True)
    with open(os.path.join(folder, filename), "wb") as f:
        f.write(data)
    contents = {"images": [{"filename": filename, "idiom": "universal"}], "info": INFO}
    props = {}
    if vector:
        props["preserves-vector-representation"] = True
    if template:
        props["template-rendering-intent"] = "template"
    if props:
        contents["properties"] = props
    write_json(os.path.join(folder, "Contents.json"), contents)


def colorset(name, light, dark):
    folder = os.path.join(CATALOG, name + ".colorset")
    os.makedirs(folder, exist_ok=True)

    def comp(hexstr):
        r, g, b = (int(hexstr[i:i + 2], 16) for i in (0, 2, 4))
        return {"color-space": "srgb", "components": {"red": f"0x{r:02X}", "green": f"0x{g:02X}", "blue": f"0x{b:02X}", "alpha": "1.000"}}

    write_json(os.path.join(folder, "Contents.json"), {
        "colors": [
            {"idiom": "universal", "color": comp(light)},
            {"idiom": "universal", "appearances": [{"appearance": "luminosity", "value": "dark"}], "color": comp(dark)},
        ],
        "info": INFO,
    })


def app_icon(logo_path):
    """Android's adaptive icon flattened: the white logo, 44 x 33 dp of 108, on #0F7BF4."""
    folder = os.path.join(CATALOG, "AppIcon.appiconset")
    os.makedirs(folder, exist_ok=True)
    out = os.path.join(folder, "AppIcon.png")
    swift = r'''
import CoreGraphics
import Foundation
import ImageIO
import UniformTypeIdentifiers
let a = CommandLine.arguments
let src = CGImageSourceCreateWithURL(URL(fileURLWithPath: a[1]) as CFURL, nil)!
let logo = CGImageSourceCreateImageAtIndex(src, 0, nil)!
let side = 1024, w = Double(side) * 44 / 108, h = Double(side) * 33 / 108
let rect = CGRect(x: (Double(side) - w) / 2, y: (Double(side) - h) / 2, width: w, height: h)
let rgb = CGColorSpace(name: CGColorSpace.sRGB)!
let layer = CGContext(data: nil, width: side, height: side, bitsPerComponent: 8, bytesPerRow: 0, space: rgb, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
layer.draw(logo, in: rect)
layer.setBlendMode(.sourceIn)
layer.setFillColor(CGColor(srgbRed: 1, green: 1, blue: 1, alpha: 1))
layer.fill(CGRect(x: 0, y: 0, width: side, height: side))
let icon = CGContext(data: nil, width: side, height: side, bitsPerComponent: 8, bytesPerRow: 0, space: rgb, bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue)!
icon.setFillColor(CGColor(srgbRed: 0x0F / 255, green: 0x7B / 255, blue: 0xF4 / 255, alpha: 1))
icon.fill(CGRect(x: 0, y: 0, width: side, height: side))
icon.draw(layer.makeImage()!, in: CGRect(x: 0, y: 0, width: side, height: side))
let dest = CGImageDestinationCreateWithURL(URL(fileURLWithPath: a[2]) as CFURL, UTType.png.identifier as CFString, 1, nil)!
CGImageDestinationAddImage(dest, icon.makeImage()!, nil)
CGImageDestinationFinalize(dest)
'''
    script = os.path.join(os.environ.get("TMPDIR", "/tmp"), "skywire-appicon.swift")
    with open(script, "w") as f:
        f.write(swift)
    subprocess.run(["swift", script, logo_path, out], check=True)
    write_json(os.path.join(folder, "Contents.json"), {
        "images": [{"filename": "AppIcon.png", "idiom": "universal", "platform": "ios", "size": "1024x1024"}],
        "info": INFO,
    })


def main():
    os.makedirs(CATALOG, exist_ok=True)
    write_json(os.path.join(CATALOG, "Contents.json"), {"info": INFO})
    for icon in ICONS:
        variant, name = icon.split(".")
        name_dir = os.path.join(CATALOG, asset_name(icon) + ".imageset")
        if os.path.exists(os.path.join(name_dir, "icon.svg")):
            continue
        url = f"{CDN}/{FOLDER[variant]}/{snake(name)}.svg"
        with urllib.request.urlopen(url, timeout=30) as r:
            svg = r.read()
        if not svg.startswith(b"<svg"):
            sys.exit(f"not an SVG: {url}")
        imageset(asset_name(icon), "icon.svg", svg, template=True, vector=True)
        print("icon", asset_name(icon))
    logo = os.path.join(ANDROID_RES, "drawable-nodpi", "skywire_logo.png")
    with open(logo, "rb") as f:
        # Not a template: the lock and launch screens draw it in its own #0072FF.
        imageset("skywire-logo", "skywire_logo.png", f.read(), template=False, vector=False)
    # Android's window background before the first frame (Theme.Skywire).
    colorset("LaunchBackground", "FFFFFF", "0A101C")
    app_icon(logo)
    print("catalogue written:", CATALOG)


if __name__ == "__main__":
    main()
