import {
  settingError
} from "./chunk-BGNEFYT6.js";

// src/app/components/pages/node/apps/app-flags-form/app-flags.ts
function splitArgs(s) {
  const out = [];
  let cur = "";
  let inDouble = false;
  let inSingle = false;
  let inToken = false;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (inDouble) {
      if (c === "\\" && i + 1 < s.length) {
        cur += s[++i];
      } else if (c === '"') {
        inDouble = false;
      } else {
        cur += c;
      }
    } else if (inSingle) {
      if (c === "'") {
        inSingle = false;
      } else {
        cur += c;
      }
    } else if (c === " " || c === "	" || c === "\n") {
      if (inToken) {
        out.push(cur);
        cur = "";
        inToken = false;
      }
    } else {
      if (c === '"') {
        inDouble = true;
      } else if (c === "'") {
        inSingle = true;
      } else {
        cur += c;
      }
      inToken = true;
    }
  }
  if (inToken) {
    out.push(cur);
  }
  return out;
}
function joinArgs(tokens) {
  return tokens.map((t) => {
    if (t === "") {
      return '""';
    }
    return /[\s"'\\]/.test(t) ? `"${t.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"` : t;
  }).join(" ");
}
function lookup(flags, key) {
  return flags.find((f) => f.name === key || !!f.shorthand && f.shorthand === key);
}
function parseFlagArgs(args, flags) {
  const tokens = splitArgs(args);
  const parsed = { positional: [], other: [], state: {} };
  flags.forEach((f) => parsed.state[f.name] = { value: f.default, set: false });
  let i = 0;
  while (i < tokens.length && !tokens[i].startsWith("-")) {
    parsed.positional.push(tokens[i++]);
  }
  for (; i < tokens.length; i++) {
    const t = tokens[i];
    const eq = t.indexOf("=");
    const key = t.replace(/^-+/, "").split("=")[0];
    const f = t.startsWith("-") ? lookup(flags, key) : void 0;
    if (!f) {
      parsed.other.push(t);
      continue;
    }
    let value;
    if (eq >= 0) {
      value = t.slice(eq + 1);
    } else if (f.type === "bool") {
      value = "true";
    } else {
      value = i + 1 < tokens.length ? tokens[++i] : "";
    }
    const st = parsed.state[f.name];
    st.value = f.type === "stringList" && st.set ? `${st.value},${value}` : value;
    st.set = true;
  }
  return parsed;
}
function buildFlagArgs(args, flags, state) {
  const parsed = parseFlagArgs(args, flags);
  const out = [...parsed.positional, ...parsed.other];
  for (const f of flags) {
    const st = state[f.name];
    if (!st || !st.set) {
      continue;
    }
    if (f.type === "stringList") {
      const items = st.value.split(",").map((s) => s.trim()).filter((s) => s);
      items.forEach((item) => out.push(`--${f.name}=${item}`));
      if (items.length === 0) {
        out.push(`--${f.name}=`);
      }
    } else if (f.type === "bool") {
      out.push(st.value === "true" ? `--${f.name}` : `--${f.name}=false`);
    } else {
      out.push(`--${f.name}=${st.value}`);
    }
  }
  return joinArgs(out);
}
function flagError(f, value) {
  if (f.type === "bool") {
    return "";
  }
  const check = f.type === "duration" ? "duration" : f.check || "";
  const row = { format: f.type === "int" ? "int" : "string", check, secret: false };
  return settingError(row, value);
}
var clientServerTypes = {
  "skysocks-client": "proxy",
  "vpn-client": "vpn"
};
function serverTypeFor(...names) {
  for (const name of names) {
    for (const app of Object.keys(clientServerTypes)) {
      if (name && (name === app || name.startsWith(app + "-"))) {
        return clientServerTypes[app];
      }
    }
  }
  return "";
}
function isServerFlag(f) {
  return f.name === "srv" && f.check === "pk";
}

export {
  splitArgs,
  parseFlagArgs,
  buildFlagArgs,
  flagError,
  serverTypeFor,
  isServerFlag
};
//# debugId=999a4dcb-a6cf-5fe7-9103-6a3816c04919
//# sourceMappingURL=chunk-Z3QUGWOD.js.map
