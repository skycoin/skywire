import {
  Injectable
} from "./chunk-ZNLGPAUZ.js";
import "./chunk-ARXMZVRT.js";

// src/app/wallet/wallet-backend.interceptor.ts
var __decorate = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var defaultBtcBackend = "ssl://electrum.blockstream.info:50002";
function stored(key) {
  try {
    return localStorage.getItem(key) || "";
  } catch (e) {
    return "";
  }
}
function walletBackendTarget(url, page = location.href) {
  let parsed;
  try {
    parsed = new URL(url, page);
  } catch (e) {
    return null;
  }
  const path = parsed.pathname;
  if (/\/api\/v1\/coins$/.test(path)) {
    return { url: "wallet/coins", headers: {} };
  }
  if (/\/api\/v1\/coins\/custom$/.test(path)) {
    return { url: "wallet/coins/custom" + parsed.search, headers: {} };
  }
  const coin = /\/coin\/(\d+)\//.exec(path);
  let target;
  if (coin) {
    target = "wallet" + path.slice(path.indexOf("/coin/")) + parsed.search;
  } else if (/^\/(wallet\/)?api\/v[12]\//.test(path)) {
    target = "wallet/coin/0/" + path.replace(/^\/(wallet\/)?/, "") + parsed.search;
  } else {
    return null;
  }
  const headers = {};
  if (/\/v1\/btc\//.test(path)) {
    let backend = "";
    try {
      backend = (JSON.parse(stored("nodeUrls") || "{}") || {})["-2"] || "";
    } catch (e) {
    }
    headers["X-Skywire-Btc-Backend"] = backend || stored("skywire-btc-backend") || defaultBtcBackend;
    const exit = stored("skywire-btc-proxy");
    if (exit) {
      headers["X-Skywire-Btc-Proxy"] = exit;
    }
  } else {
    const pageHost = new URL(page).host;
    const node = parsed.host && parsed.host !== pageHost ? parsed.protocol + "//" + parsed.host : stored("skywire-coin-node");
    if (node) {
      headers["X-Skywire-Coin-Node"] = node;
    }
  }
  return { url: target, headers };
}
var WalletBackendInterceptor = class WalletBackendInterceptor2 {
  intercept(req, next) {
    const target = walletBackendTarget(req.url);
    return next.handle(target ? req.clone({ url: target.url, setHeaders: target.headers }) : req);
  }
};
WalletBackendInterceptor = __decorate([
  Injectable()
], WalletBackendInterceptor);

// src/app/wallet/wallet-backend.interceptor.spec.ts
describe("walletBackendTarget", () => {
  const page = "https://127.0.0.1:8000/#/wallet";
  const deskPage = "https://mesh.localhost:8443/vnet/8001/#/wallet";
  afterEach(() => {
    localStorage.removeItem("skywire-coin-node");
    localStorage.removeItem("skywire-btc-proxy");
    localStorage.removeItem("nodeUrls");
  });
  it("sends the coin list to the hypervisor registry", () => {
    expect(walletBackendTarget("/api/v1/coins", page)).toEqual({ url: "wallet/coins", headers: {} });
    expect(walletBackendTarget("/api/v1/coins/custom?id=ab12", page)).toEqual({ url: "wallet/coins/custom?id=ab12", headers: {} });
  });
  it("keeps a coin index and query, relative to the page", () => {
    const t = walletBackendTarget("/coin/1/api/v1/balance?addrs=a,b", page);
    expect(t?.url).toBe("wallet/coin/1/api/v1/balance?addrs=a,b");
    expect(walletBackendTarget("/coin/1/api/v1/balance", deskPage)?.url).toBe("wallet/coin/1/api/v1/balance");
  });
  it("sends a bare node API call to coin 0", () => {
    expect(walletBackendTarget("/api/v2/transaction", page)?.url).toBe("wallet/coin/0/api/v2/transaction");
  });
  it("names a node the user entered, else the configured one", () => {
    const custom = walletBackendTarget("https://node.example.com/api/v1/health", page);
    expect(custom?.url).toBe("wallet/coin/0/api/v1/health");
    expect(custom?.headers["X-Skywire-Coin-Node"]).toBe("https://node.example.com");
    localStorage.setItem("skywire-coin-node", "node.skycoin.com.dmsg");
    expect(walletBackendTarget("/coin/0/api/v1/health", page)?.headers["X-Skywire-Coin-Node"]).toBe("node.skycoin.com.dmsg");
  });
  it("gives a bitcoin query its electrum server and exit", () => {
    expect(walletBackendTarget("/coin/2/api/v1/btc/balance", page)?.headers).toEqual({ "X-Skywire-Btc-Backend": "ssl://electrum.blockstream.info:50002" });
    localStorage.setItem("nodeUrls", JSON.stringify({ "-2": "ssl://electrum.example.com:50002" }));
    localStorage.setItem("skywire-btc-proxy", "exitpk");
    expect(walletBackendTarget("/coin/2/api/v1/btc/balance", page)?.headers).toEqual({
      "X-Skywire-Btc-Backend": "ssl://electrum.example.com:50002",
      "X-Skywire-Btc-Proxy": "exitpk"
    });
  });
  it("leaves everything else alone", () => {
    expect(walletBackendTarget("assets/skycoin-wallet/img/logo.png", page)).toBeNull();
    expect(walletBackendTarget("https://api.coinpaprika.com/v1/tickers/sky-skycoin", page)).toBeNull();
  });
});
//# debugId=caab58f8-5944-5d8e-8954-e25a282cef27
//# sourceMappingURL=spec-app-wallet-wallet-backend.interceptor.spec.js.map
