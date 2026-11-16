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
import xml.etree.ElementTree as ET

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CATALOG = os.path.join(ROOT, "Skywire", "Assets.xcassets")
ANDROID_RES = os.path.join(os.path.dirname(ROOT), "android", "app", "src", "main", "res")
CDN = "https://cdn.jsdelivr.net/npm/@material-design-icons/svg@0.14.15"
FOLDER = {"Filled": "filled", "Outlined": "outlined", "Rounded": "round"}

ICONS = """
Filled.ArrowDropDown Filled.Call Filled.Check Filled.CallEnd Filled.Close Filled.ExpandLess
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
Outlined.FileDownload Outlined.Key Outlined.KeyboardArrowUp Outlined.Sync Outlined.WarningAmber
Rounded.AccountBalanceWallet Rounded.AltRoute Rounded.ArrowBack
Rounded.CandlestickChart Rounded.Chat Rounded.ChevronRight Rounded.Dns Rounded.Forum
Rounded.HelpOutline Rounded.Home Rounded.Hub Rounded.Route Rounded.Settings
Rounded.Videocam Rounded.VpnLock
Rounded.AttachFile Rounded.CheckCircle Rounded.Close Rounded.Contacts
Rounded.ContentCopy Rounded.Delete Rounded.Download Rounded.Edit Rounded.Email
Rounded.Reply Rounded.Send Rounded.Share Rounded.Warning
Rounded.AlternateEmail Rounded.PersonAdd
Rounded.Add Rounded.Block Rounded.Bookmark Rounded.Call Rounded.CallMade Rounded.CallReceived
Rounded.CameraAlt Rounded.Campaign Rounded.Check Rounded.CropSquare Rounded.DeleteForever Rounded.Done
Rounded.DoneAll Rounded.FlipCameraAndroid Rounded.Fullscreen Rounded.GroupAdd Rounded.Groups Rounded.Image
Rounded.InsertDriveFile Rounded.Link Rounded.LinkOff Rounded.Lock Rounded.Logout Rounded.Mic Rounded.MoreVert
Rounded.Notifications Rounded.NotificationsOff Rounded.Pause Rounded.PlayArrow Rounded.PushPin Rounded.QrCode2
Rounded.QrCodeScanner Rounded.RadioButtonUnchecked Rounded.Refresh Rounded.Schedule Rounded.Search Rounded.Shortcut
Rounded.SystemUpdate
""".split()


def snake(name):
    return re.sub(r"(?<!^)(?=[A-Z])|(?<=[a-z])(?=\d)", "_", name).lower()


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


def path_ops(data):
    """Swift CGMutablePath calls for a VectorDrawable pathData of absolute M, L, C and Z."""
    tokens = re.findall(r"[A-Za-z]|-?(?:\d+\.?\d*|\.\d+)(?:[eE][-+]?\d+)?", data)
    ops, i = [], 0
    while i < len(tokens):
        cmd = tokens[i]
        n = {"M": 2, "L": 2, "C": 6, "Z": 0}.get(cmd)
        if n is None:
            sys.exit(f"path command {cmd!r} not supported")
        v = tokens[i + 1:i + 1 + n]
        i += 1 + n
        pts = [f"CGPoint(x: {v[k]}, y: {v[k + 1]})" for k in range(0, n, 2)]
        if cmd == "M":
            ops.append(f"p.move(to: {pts[0]})")
        elif cmd == "L":
            ops.append(f"p.addLine(to: {pts[0]})")
        elif cmd == "C":
            ops.append(f"p.addCurve(to: {pts[2]}, control1: {pts[0]}, control2: {pts[1]})")
        else:
            ops.append("p.closeSubpath()")
    return "\n".join(ops)


def app_icon():
    """Android's adaptive icon: ic_launcher_foreground's 108 dp layer, its middle 72 dp
    (the part every launcher mask shows), on ic_launcher_background."""
    folder = os.path.join(CATALOG, "AppIcon.appiconset")
    os.makedirs(folder, exist_ok=True)
    out = os.path.join(folder, "AppIcon.png")
    ns = "{http://schemas.android.com/apk/res/android}"
    vector = ET.parse(os.path.join(ANDROID_RES, "drawable", "ic_launcher_foreground.xml")).getroot()
    group, path = vector.find("group"), vector.find("group/path")
    colors = ET.parse(os.path.join(ANDROID_RES, "values", "colors.xml")).getroot()
    bg = colors.find("color[@name='ic_launcher_background']").text.strip().lstrip("#")
    fill = path.get(ns + "fillColor").lstrip("#")
    g = {k: group.get(ns + k, d) for k, d in
         (("scaleX", "1"), ("scaleY", "1"), ("translateX", "0"), ("translateY", "0"))}
    swift = r'''
import CoreGraphics
import Foundation
import ImageIO
import UniformTypeIdentifiers
func srgb(_ hex: UInt32) -> CGColor {
    CGColor(srgbRed: CGFloat(hex >> 16 & 0xFF) / 255, green: CGFloat(hex >> 8 & 0xFF) / 255, blue: CGFloat(hex & 0xFF) / 255, alpha: 1)
}
let side = 1024
let icon = CGContext(data: nil, width: side, height: side, bitsPerComponent: 8, bytesPerRow: 0, space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue)!
icon.setFillColor(srgb(0x%(bg)s))
icon.fill(CGRect(x: 0, y: 0, width: side, height: side))
icon.translateBy(x: 0, y: CGFloat(side))
icon.scaleBy(x: CGFloat(side) / 72, y: -CGFloat(side) / 72)
icon.translateBy(x: -18 + %(translateX)s, y: -18 + %(translateY)s)
icon.scaleBy(x: %(scaleX)s, y: %(scaleY)s)
let p = CGMutablePath()
%(ops)s
icon.addPath(p)
icon.setFillColor(srgb(0x%(fill)s))
icon.fillPath(using: %(rule)s)
let dest = CGImageDestinationCreateWithURL(URL(fileURLWithPath: CommandLine.arguments[1]) as CFURL, UTType.png.identifier as CFString, 1, nil)!
CGImageDestinationAddImage(dest, icon.makeImage()!, nil)
CGImageDestinationFinalize(dest)
''' % dict(g, bg=bg, fill=fill, ops=path_ops(path.get(ns + "pathData")),
           rule=".evenOdd" if path.get(ns + "fillType") == "evenOdd" else ".winding")
    script = os.path.join(os.environ.get("TMPDIR", "/tmp"), "skywire-appicon.swift")
    with open(script, "w") as f:
        f.write(swift)
    subprocess.run(["swift", script, out], check=True)
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
        # Not a template: the lock and launch screens draw it in its own #1677FF.
        imageset("skywire-logo", "skywire_logo.png", f.read(), template=False, vector=False)
    # Android's window background before the first frame (Theme.Skywire).
    colorset("LaunchBackground", "FFFFFF", "0A101C")
    app_icon()
    print("catalogue written:", CATALOG)


if __name__ == "__main__":
    main()
