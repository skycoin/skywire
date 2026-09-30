#!/usr/bin/env python3
"""Writes the iOS app's string catalogues from the Android ones.

ios/Skywire/Localizable.xcstrings holds exactly the keys the app's Swift
sources use. A key the Android app has takes its English, Simplified Chinese
and Spanish from android/app/src/main/res/values{,-zh-rCN,-es}/strings.xml,
so the two apps say the same thing in the same words; a key only iOS has comes
from ios/scripts/strings-ios.json, as do the Info.plist strings
(InfoPlist.xcstrings). Android's placeholders become iOS's: %1$s -> %1$@,
%1$d -> %1$lld.

The keys are found the way StringCatalogTests (SkywireTests) finds them: a
lower-case, underscore-style literal on a line that calls Text, Label,
Button, Toggle, Section, Picker, TextField, LocalizedStringKey, L10n.text,
L10n.format or L10n.key, outside comments, and not a systemImage/systemName
or a case label.

Run it after adding or removing a string in the app, then review the diff:
  ios/scripts/seed-strings.py
"""
import glob
import json
import os
import re
import sys
import xml.etree.ElementTree as ET

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
APP = os.path.join(ROOT, "ios", "Skywire")
ANDROID_RES = os.path.join(ROOT, "android", "app", "src", "main", "res")
SUPPLEMENT = os.path.join(ROOT, "ios", "scripts", "strings-ios.json")
LANGUAGES = {"en": "values", "zh-Hans": "values-zh-rCN", "es": "values-es"}

TRIGGER = re.compile(
    r"\b(?:Text|Label|Button|Toggle|Section|Picker|TextField|LocalizedStringKey"
    r"|L10n\.(?:text|format|key)|navigationTitle)\("
)
LITERAL = re.compile(r'(?<!systemImage: )(?<!systemName: )"([a-z][a-z0-9_]*)"(?!\s*:)')


def used_keys():
    keys = set()
    for path in glob.glob(os.path.join(APP, "**", "*.swift"), recursive=True):
        with open(path, encoding="utf-8") as source:
            for line in source:
                if line.lstrip().startswith("//") or not TRIGGER.search(line):
                    continue
                keys.update(LITERAL.findall(line))
    return keys


def android_text(element):
    """A <string>'s value as Android resolves it."""
    raw = "".join(element.itertext())
    out, i, quoted = [], 0, False
    while i < len(raw):
        c = raw[i]
        if c == "\\" and i + 1 < len(raw):
            n = raw[i + 1]
            if n == "u" and i + 5 < len(raw):
                out.append(chr(int(raw[i + 2:i + 6], 16)))
                i += 6
                continue
            out.append({"n": "\n", "t": "\t"}.get(n, n))
            i += 2
            continue
        if c == '"':
            quoted = not quoted
            i += 1
            continue
        if c.isspace() and not quoted:
            # Unquoted whitespace runs collapse to one space.
            if not out or out[-1] != " ":
                out.append(" ")
            i += 1
            continue
        out.append(c)
        i += 1
    text = "".join(out)
    return text if raw.startswith('"') else text.strip()


def ios_placeholders(text):
    return re.sub(r"%(\d+\$)?([sd])", lambda m: "%" + (m.group(1) or "") + ("@" if m.group(2) == "s" else "lld"), text)


def android_catalogue(folder):
    tree = ET.parse(os.path.join(ANDROID_RES, folder, "strings.xml"))
    return {e.get("name"): e for e in tree.getroot() if e.tag == "string"}


def entry(values):
    return {
        "extractionState": "manual",
        "localizations": {
            language: {"stringUnit": {"state": "translated", "value": value}}
            for language, value in sorted(values.items())
        },
    }


def write(path, strings):
    catalogue = {"sourceLanguage": "en", "strings": strings, "version": "1.0"}
    with open(path, "w", encoding="utf-8") as out:
        json.dump(catalogue, out, indent=2, ensure_ascii=False, sort_keys=True, separators=(",", " : "))
        out.write("\n")


def main():
    with open(SUPPLEMENT, encoding="utf-8") as f:
        supplement = json.load(f)
    android = {language: android_catalogue(folder) for language, folder in LANGUAGES.items()}
    strings, missing = {}, []
    for key in sorted(used_keys()):
        if key in supplement["Localizable"]:
            strings[key] = entry(supplement["Localizable"][key])
        elif key in android["en"]:
            english = android["en"][key]
            values = {}
            for language in LANGUAGES:
                element = android[language].get(key)
                # translatable="false" strings live in values/ only.
                if element is None and english.get("translatable") == "false":
                    element = english
                if element is None:
                    missing.append(f"{key} ({language})")
                    continue
                values[language] = ios_placeholders(android_text(element))
            strings[key] = entry(values)
        else:
            missing.append(key)
    write(os.path.join(APP, "Localizable.xcstrings"), strings)
    write(os.path.join(APP, "InfoPlist.xcstrings"), {k: entry(v) for k, v in supplement["InfoPlist"].items()})
    print(f"Localizable.xcstrings: {len(strings)} keys; InfoPlist.xcstrings: {len(supplement['InfoPlist'])} keys")
    if missing:
        print("no text for: " + ", ".join(missing), file=sys.stderr)
        print("add them to ios/scripts/strings-ios.json (iOS only) or to the Android catalogues", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
