import {
  ApiService,
  RequestOptions,
  ResponseTypes
} from "./chunk-ZMZW6HXB.js";
import {
  StorageService
} from "./chunk-XOUXCPTJ.js";
import {
  Injectable,
  map
} from "./chunk-ZNLGPAUZ.js";

// node_modules/bignumber.js/dist/bignumber.mjs
var BigNumber = clone();
var isNumeric = /^-?(?:\d+(?:\.\d*)?|\.\d+)(?:e[+-]?\d+)?$/i;
var mathceil = Math.ceil;
var mathfloor = Math.floor;
var bignumberError = "[BigNumber Error] ";
var BASE = 1e14;
var LOG_BASE = 14;
var MAX_SAFE_INTEGER = 9007199254740991;
var POWS_TEN = [1, 10, 100, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9, 1e10, 1e11, 1e12, 1e13];
var SQRT_BASE = 1e7;
var MAX = 1e9;
function clone(configObject) {
  var div, convertBase, basePrefix = /^(-?)0([xbo])(?=[^.])/i, isInfinityOrNaN = /^-?(Infinity|NaN)$/, whitespaceOrPlus = /^\s*\+(?!-)|^\s+|\s+$/g, P = BigNumber2.prototype = { constructor: BigNumber2, toString: null, valueOf: null }, ONE = new BigNumber2(1), DECIMAL_PLACES = 20, ROUNDING_MODE = 4, TO_EXP_NEG = -7, TO_EXP_POS = 21, MIN_EXP = -1e7, MAX_EXP = 1e7, CRYPTO = false, STRICT = true, MODULO_MODE = 1, POW_PRECISION = 0, FORMAT = {
    prefix: "",
    negativeSign: "-",
    positiveSign: "",
    groupSeparator: ",",
    groupSize: 3,
    secondaryGroupSize: 0,
    decimalSeparator: ".",
    fractionGroupSeparator: "",
    fractionGroupSize: 0,
    suffix: ""
  }, ALPHABET = "0123456789abcdefghijklmnopqrstuvwxyz";
  function BigNumber2(v, b) {
    var e, i, str, t, x = this;
    if (!(x instanceof BigNumber2)) return new BigNumber2(v, b);
    t = typeof v;
    if (b == null) {
      if (isBigNumber(v)) {
        x.s = v.s;
        if (!v.c || v.e > MAX_EXP) {
          x.c = x.e = null;
        } else if (v.e < MIN_EXP) {
          x.c = [x.e = 0];
        } else {
          x.e = v.e;
          x.c = v.c.slice();
        }
        return;
      }
      if (t == "number") {
        if (v * 0 != 0) {
          x.s = isNaN(v) ? null : v < 0 ? -1 : 1;
          x.c = x.e = null;
          return;
        }
        x.s = 1 / v < 0 ? (v = -v, -1) : 1;
        if (v === ~~v) {
          for (e = 0, i = v; i >= 10; i /= 10, e++) ;
          if (e > MAX_EXP) {
            x.c = x.e = null;
          } else {
            x.e = e;
            x.c = [v];
          }
          return;
        }
        return parseValidString(x, String(v));
      }
      if (t == "bigint") {
        x.s = v < 0 ? (v = -v, -1) : 1;
        return parseValidString(x, String(v));
      }
      if (t == "string") {
        str = v;
      } else {
        if (STRICT) {
          throw Error(bignumberError + "BigNumber, string, number, or BigInt expected: " + v);
        }
        str = String(v);
      }
      if (isNumeric.test(str)) {
        x.s = str.charCodeAt(0) == 45 ? (str = str.slice(1), -1) : 1;
        return parseValidString(x, str);
      }
      str = str.replace(whitespaceOrPlus, "");
      if (isInfinityOrNaN.test(str)) {
        x.s = isNaN(str) ? null : str < 0 ? -1 : 1;
        x.c = x.e = null;
        return;
      }
      str = str.replace(basePrefix, function(m, p1, p2) {
        b = (p2 = p2.toLowerCase()) == "x" ? 16 : p2 == "b" ? 2 : 8;
        return p1;
      });
      if (b) {
        return parseBaseString(x, str, b, v);
      }
      str = str.replace(/(\d)_(?=\d)/g, "$1");
      if (isNumeric.test(str)) {
        x.s = str.charCodeAt(0) == 45 ? (str = str.slice(1), -1) : 1;
        return parseValidString(x, str);
      }
      if (STRICT) {
        throw Error(bignumberError + "Not a number: " + v);
      }
      x.s = x.c = x.e = null;
    } else {
      if (t != "string") {
        if (STRICT) {
          throw Error(bignumberError + "String expected: " + v);
        }
        v = String(v);
      }
      intCheck(b, 2, ALPHABET.length, "Base");
      parseBaseString(x, v.replace(whitespaceOrPlus, ""), b, v);
    }
  }
  BigNumber2.clone = clone;
  BigNumber2.ROUND_UP = 0;
  BigNumber2.ROUND_DOWN = 1;
  BigNumber2.ROUND_CEIL = 2;
  BigNumber2.ROUND_FLOOR = 3;
  BigNumber2.ROUND_HALF_UP = 4;
  BigNumber2.ROUND_HALF_DOWN = 5;
  BigNumber2.ROUND_HALF_EVEN = 6;
  BigNumber2.ROUND_HALF_CEIL = 7;
  BigNumber2.ROUND_HALF_FLOOR = 8;
  BigNumber2.EUCLID = 9;
  BigNumber2.config = BigNumber2.set = function(obj) {
    var p, v;
    if (obj != null) {
      if (typeof obj == "object") {
        if (obj.hasOwnProperty(p = "DECIMAL_PLACES")) {
          DECIMAL_PLACES = intCheck(obj[p], 0, MAX, p);
        }
        if (obj.hasOwnProperty(p = "ROUNDING_MODE")) {
          ROUNDING_MODE = intCheck(obj[p], 0, 8, p);
        }
        if (obj.hasOwnProperty(p = "EXPONENTIAL_AT")) {
          v = obj[p];
          if (isArray(v)) {
            intCheck(v[0], -MAX, 0, p);
            intCheck(v[1], 0, MAX, p);
            TO_EXP_NEG = v[0];
            TO_EXP_POS = v[1];
          } else {
            TO_EXP_NEG = -(TO_EXP_POS = intCheck(v, -MAX, MAX, p) < 0 ? -v : v);
          }
        }
        if (obj.hasOwnProperty(p = "RANGE")) {
          v = obj[p];
          if (v) {
            if (isArray(v)) {
              intCheck(v[0], -MAX, -1, p);
              intCheck(v[1], 1, MAX, p);
              MIN_EXP = v[0];
              MAX_EXP = v[1];
            } else {
              MIN_EXP = -(MAX_EXP = intCheck(v, -MAX, MAX, p) < 0 ? -v : v);
            }
          } else {
            throw Error(bignumberError + p + " cannot be zero: " + v);
          }
        }
        if (obj.hasOwnProperty(p = "CRYPTO")) {
          v = obj[p];
          if (v === !!v) {
            if (v) {
              if (typeof crypto != "undefined" && crypto && (crypto.getRandomValues || crypto.randomBytes)) {
                CRYPTO = v;
              } else {
                CRYPTO = !v;
                throw Error(bignumberError + "crypto unavailable");
              }
            } else {
              CRYPTO = v;
            }
          } else {
            throw Error(bignumberError + p + " not true or false: " + v);
          }
        }
        if (obj.hasOwnProperty(p = "STRICT")) {
          v = obj[p];
          if (v === !!v) {
            STRICT = v;
          } else {
            throw Error(bignumberError + p + " not true or false: " + v);
          }
        }
        if (obj.hasOwnProperty(p = "MODULO_MODE")) {
          MODULO_MODE = intCheck(obj[p], 0, 9, p);
        }
        if (obj.hasOwnProperty(p = "POW_PRECISION")) {
          POW_PRECISION = intCheck(obj[p], 0, MAX, p);
        }
        if (obj.hasOwnProperty(p = "FORMAT")) {
          v = obj[p];
          if (typeof v == "object") {
            for (p in v) {
              if (v.hasOwnProperty(p) && FORMAT.hasOwnProperty(p)) {
                FORMAT[p] = v[p];
              }
            }
          } else {
            throw Error(bignumberError + p + " not an object: " + v);
          }
        }
        if (obj.hasOwnProperty(p = "ALPHABET")) {
          v = obj[p];
          if (typeof v == "string" && !/^.?$|[+\-.\s]|(.).*\1/.test(v)) {
            ALPHABET = v;
          } else {
            throw Error(bignumberError + p + " invalid: " + v);
          }
        }
      } else {
        throw Error(bignumberError + "Object expected: " + obj);
      }
    }
    return {
      DECIMAL_PLACES,
      ROUNDING_MODE,
      EXPONENTIAL_AT: [TO_EXP_NEG, TO_EXP_POS],
      RANGE: [MIN_EXP, MAX_EXP],
      CRYPTO,
      STRICT,
      MODULO_MODE,
      POW_PRECISION,
      FORMAT,
      ALPHABET
    };
  };
  BigNumber2.fromFormat = function(str, options) {
    if (typeof str !== "string") {
      throw Error(bignumberError + "Not a string: " + str);
    }
    if (options == null) {
      options = FORMAT;
    } else if (typeof options != "object") {
      throw Error(bignumberError + "Argument not an object: " + options);
    } else {
      options = resolveFormatOptions(options);
    }
    var i, isNeg, integerPart, fractionPart, negativeSign = options.negativeSign || "-", positiveSign = options.positiveSign || "", prefix = options.prefix || "", suffix = options.suffix || "", groupSeparator = options.groupSeparator || "", decimalSeparator = options.decimalSeparator || ".", fractionGroupSeparator = options.fractionGroupSeparator || "";
    if (prefix && str.indexOf(prefix) === 0) str = str.slice(prefix.length);
    if (suffix && str.lastIndexOf(suffix) === str.length - suffix.length) {
      str = str.slice(0, -suffix.length);
    }
    if (negativeSign && str.indexOf(negativeSign) === 0) {
      str = str.slice(negativeSign.length);
      isNeg = true;
    } else if (positiveSign && str.indexOf(positiveSign) === 0) {
      str = str.slice(positiveSign.length);
    }
    i = str.indexOf(decimalSeparator);
    if (i < 0) {
      if (groupSeparator) {
        while (str.indexOf(groupSeparator) > -1) {
          str = str.replace(groupSeparator, "");
        }
      }
    } else {
      integerPart = str.slice(0, i);
      fractionPart = str.slice(i + decimalSeparator.length);
      if (groupSeparator) {
        while (integerPart.indexOf(groupSeparator) > -1) {
          integerPart = integerPart.replace(groupSeparator, "");
        }
      }
      if (fractionGroupSeparator) {
        while (fractionPart.indexOf(fractionGroupSeparator) > -1) {
          fractionPart = fractionPart.replace(fractionGroupSeparator, "");
        }
      }
      str = integerPart + "." + fractionPart;
    }
    return new BigNumber2(isNeg ? "-" + str : str);
  };
  BigNumber2.isBigNumber = function(v) {
    if (!isBigNumber(v)) return false;
    var i, n, c = v.c, e = v.e, s = v.s;
    if (!isArray(c)) {
      return c === null && e === null && (s === null || s === 1 || s === -1);
    }
    if (s !== 1 && s !== -1 || e < -MAX || e > MAX || e !== mathfloor(e)) {
      return false;
    }
    if (c[0] === 0) {
      return e === 0 && c.length === 1;
    }
    i = (e + 1) % LOG_BASE;
    if (i < 1) i += LOG_BASE;
    if (String(c[0]).length !== i) {
      return false;
    }
    for (i = 0; i < c.length; i++) {
      n = c[i];
      if (n < 0 || n >= BASE || n !== mathfloor(n)) return false;
    }
    return n !== 0;
  };
  BigNumber2.maximum = BigNumber2.max = function() {
    return maxOrMin(arguments, -1);
  };
  BigNumber2.minimum = BigNumber2.min = function() {
    return maxOrMin(arguments, 1);
  };
  BigNumber2.random = (function() {
    var pow2_53 = 9007199254740992;
    var random53bitInt = Math.random() * pow2_53 & 2097151 ? function() {
      return mathfloor(Math.random() * pow2_53);
    } : function() {
      return (Math.random() * 1073741824 | 0) * 8388608 + (Math.random() * 8388608 | 0);
    };
    return function(dp) {
      var a, b, e, k, v, i = 0, c = [], rand = new BigNumber2(ONE);
      dp = dp == null ? DECIMAL_PLACES : intCheck(dp, 0, MAX);
      k = mathceil(dp / LOG_BASE);
      if (CRYPTO) {
        if (crypto.getRandomValues) {
          a = crypto.getRandomValues(new Uint32Array(k *= 2));
          for (; i < k; ) {
            v = a[i] * 131072 + (a[i + 1] >>> 11);
            if (v >= 9e15) {
              b = crypto.getRandomValues(new Uint32Array(2));
              a[i] = b[0];
              a[i + 1] = b[1];
            } else {
              c.push(v % 1e14);
              i += 2;
            }
          }
          i = k / 2;
        } else if (crypto.randomBytes) {
          a = crypto.randomBytes(k *= 7);
          for (; i < k; ) {
            v = (a[i] & 31) * 281474976710656 + a[i + 1] * 1099511627776 + a[i + 2] * 4294967296 + a[i + 3] * 16777216 + (a[i + 4] << 16) + (a[i + 5] << 8) + a[i + 6];
            if (v >= 9e15) {
              crypto.randomBytes(7).copy(a, i);
            } else {
              c.push(v % 1e14);
              i += 7;
            }
          }
          i = k / 7;
        } else {
          CRYPTO = false;
          throw Error(bignumberError + "crypto unavailable");
        }
      }
      if (!CRYPTO) {
        for (; i < k; ) {
          v = random53bitInt();
          if (v < 9e15) c[i++] = v % 1e14;
        }
      }
      k = c[--i];
      dp %= LOG_BASE;
      if (k && dp) {
        v = POWS_TEN[LOG_BASE - dp];
        c[i] = mathfloor(k / v) * v;
      }
      for (; c[i] === 0; c.pop(), i--) ;
      if (i < 0) {
        c = [e = 0];
      } else {
        for (e = -1; c[0] === 0; c.splice(0, 1), e -= LOG_BASE) ;
        for (i = 1, v = c[0]; v >= 10; v /= 10, i++) ;
        if (i < LOG_BASE) e -= LOG_BASE - i;
      }
      rand.e = e;
      rand.c = c;
      return rand;
    };
  })();
  BigNumber2.sum = function() {
    var i = 0, sum = new BigNumber2(0);
    for (; i < arguments.length; ) sum = sum.plus(arguments[i++]);
    return sum;
  };
  function parseValidString(x, str) {
    var e, i, len;
    if ((e = str.indexOf(".")) > -1) str = str.replace(".", "");
    if ((i = str.search(/e/i)) > 0) {
      if (e < 0) e = i;
      e += +str.slice(i + 1);
      str = str.substring(0, i);
    } else if (e < 0) {
      e = str.length;
    }
    for (i = 0; str.charCodeAt(i) === 48; i++) ;
    for (len = str.length; str.charCodeAt(--len) === 48; ) ;
    if (str = str.slice(i, ++len)) {
      len -= i;
      e = e - i - 1;
      if (e > MAX_EXP) {
        x.c = x.e = null;
      } else if (e < MIN_EXP) {
        x.c = [x.e = 0];
      } else {
        x.e = e;
        x.c = [];
        i = (e + 1) % LOG_BASE;
        if (e < 0) i += LOG_BASE;
        if (i < len) {
          if (i) x.c.push(+str.slice(0, i));
          for (len -= LOG_BASE; i < len; ) {
            x.c.push(+str.slice(i, i += LOG_BASE));
          }
          i = LOG_BASE - (str = str.slice(i)).length;
        } else {
          i -= len;
        }
        for (; i--; str += "0") ;
        x.c.push(+str);
      }
    } else {
      x.c = [x.e = 0];
    }
  }
  function parseBaseString(x, str, b, v) {
    var c, len, alphabet = ALPHABET.slice(0, b), i = 0, clean = "", hasDot = false, prevIsNumeral = false, caseChanged = false;
    x.s = str.charCodeAt(0) === 45 ? (str = str.slice(1), -1) : 1;
    for (len = str.length; i < len; i++) {
      c = str.charAt(i);
      if (alphabet.indexOf(c) >= 0) {
        clean += c;
        prevIsNumeral = true;
        continue;
      }
      if (c == "_") {
        if (prevIsNumeral && i + 1 < len) {
          prevIsNumeral = false;
          continue;
        }
      } else if (c == ".") {
        if (i == 0 || !hasDot && prevIsNumeral) {
          if (i + 1 == len) break;
          if (i == 0) clean = "0";
          clean += c;
          hasDot = true;
          prevIsNumeral = false;
          continue;
        }
      } else if (!caseChanged) {
        if (str == str.toUpperCase() && alphabet == alphabet.toLowerCase() && (str = str.toLowerCase()) || str == str.toLowerCase() && alphabet == alphabet.toUpperCase() && (str = str.toUpperCase())) {
          i = -1;
          clean = "";
          caseChanged = true;
          hasDot = prevIsNumeral = false;
          continue;
        }
      }
      if (STRICT) {
        throw Error(bignumberError + "Not a base " + b + " number: " + v);
      }
      x.s = x.c = x.e = null;
      return;
    }
    parseValidString(x, convertBase(clean, b, 10, x.s));
  }
  convertBase = /* @__PURE__ */ (function() {
    var decimal = "0123456789";
    function toBaseOut(str, baseIn, baseOut, alphabet) {
      var j, arr = [0], arrL, i = 0, len = str.length;
      for (; i < len; ) {
        for (arrL = arr.length; arrL--; arr[arrL] *= baseIn) ;
        arr[0] += alphabet.indexOf(str.charAt(i++));
        for (j = 0; j < arr.length; j++) {
          if (arr[j] > baseOut - 1) {
            if (arr[j + 1] == null) arr[j + 1] = 0;
            arr[j + 1] += arr[j] / baseOut | 0;
            arr[j] %= baseOut;
          }
        }
      }
      return arr.reverse();
    }
    return function(str, baseIn, baseOut, sign, callerIsToString) {
      var alphabet, d, e, k, r, x, xc, y, i = str.indexOf("."), dp = DECIMAL_PLACES, rm = ROUNDING_MODE;
      if (i >= 0) {
        k = POW_PRECISION;
        POW_PRECISION = 0;
        str = str.replace(".", "");
        y = new BigNumber2(baseIn);
        x = y.pow(str.length - i);
        POW_PRECISION = k;
        y.c = toBaseOut(
          toFixedPoint(coeffToString(x.c), x.e, "0"),
          10,
          baseOut,
          decimal
        );
        y.e = y.c.length;
      }
      xc = toBaseOut(str, baseIn, baseOut, callerIsToString ? (alphabet = ALPHABET, decimal) : (alphabet = decimal, ALPHABET));
      e = k = xc.length;
      for (; xc[--k] == 0; xc.pop()) ;
      if (!xc[0]) return alphabet.charAt(0);
      if (i < 0) {
        --e;
      } else {
        x.c = xc;
        x.e = e;
        x.s = sign;
        x = div(x, y, dp, rm, baseOut);
        xc = x.c;
        r = x.r;
        e = x.e;
      }
      d = e + dp + 1;
      i = xc[d];
      k = baseOut / 2;
      r = r || d < 0 || xc[d + 1] != null;
      r = rm < 4 ? (i != null || r) && (rm == 0 || rm == (x.s < 0 ? 3 : 2)) : i > k || i == k && (rm == 4 || r || rm == 6 && xc[d - 1] & 1 || rm == (x.s < 0 ? 8 : 7));
      if (d < 1 || !xc[0]) {
        str = r ? toFixedPoint(alphabet.charAt(1), -dp, alphabet.charAt(0)) : alphabet.charAt(0);
      } else {
        if (d < xc.length) xc.length = d;
        if (r) {
          for (--baseOut; ++xc[--d] > baseOut; ) {
            xc[d] = 0;
            if (!d) {
              ++e;
              xc = [1].concat(xc);
            }
          }
        }
        for (k = xc.length; !xc[--k]; ) ;
        for (i = 0, str = ""; i <= k; str += alphabet.charAt(xc[i++])) ;
        str = toFixedPoint(str, e, alphabet.charAt(0));
      }
      return str;
    };
  })();
  div = /* @__PURE__ */ (function() {
    function multiply(x, k, base) {
      var m, temp, xlo, xhi, carry = 0, i = x.length, klo = k % SQRT_BASE, khi = k / SQRT_BASE | 0;
      for (x = x.slice(); i--; ) {
        xlo = x[i] % SQRT_BASE;
        xhi = x[i] / SQRT_BASE | 0;
        m = khi * xlo + xhi * klo;
        temp = klo * xlo + m % SQRT_BASE * SQRT_BASE + carry;
        carry = (temp / base | 0) + (m / SQRT_BASE | 0) + khi * xhi;
        x[i] = temp % base;
      }
      if (carry) x = [carry].concat(x);
      return x;
    }
    function compare2(a, b, aL, bL) {
      var i, cmp;
      if (aL != bL) {
        cmp = aL > bL ? 1 : -1;
      } else {
        for (i = cmp = 0; i < aL; i++) {
          if (a[i] != b[i]) {
            cmp = a[i] > b[i] ? 1 : -1;
            break;
          }
        }
      }
      return cmp;
    }
    function subtract(a, b, aL, base) {
      var i = 0;
      for (; aL--; ) {
        a[aL] -= i;
        i = a[aL] < b[aL] ? 1 : 0;
        a[aL] = i * base + a[aL] - b[aL];
      }
      for (; !a[0] && a.length > 1; a.splice(0, 1)) ;
    }
    return function(x, y, dp, rm, base) {
      var cmp, e, i, more, n, prod, prodL, q, qc, rem, remL, rem0, xi, xL, yc0, yL, yz, s = x.s == y.s ? 1 : -1, xc = x.c, yc = y.c;
      if (!xc || !xc[0] || !yc || !yc[0]) {
        return new BigNumber2(
          // Return NaN if either NaN, or both Infinity or 0.
          !x.s || !y.s || (xc ? yc && xc[0] == yc[0] : !yc) ? NaN : (
            // Return ±0 if x is ±0 or y is ±Infinity, or return ±Infinity as y is ±0.
            xc && xc[0] == 0 || !yc ? s * 0 : s / 0
          )
        );
      }
      q = new BigNumber2(s);
      qc = q.c = [];
      e = x.e - y.e;
      s = dp + e + 1;
      if (!base) {
        base = BASE;
        e = bitFloor(x.e / LOG_BASE) - bitFloor(y.e / LOG_BASE);
        s = s / LOG_BASE | 0;
      }
      for (i = 0; yc[i] == (xc[i] || 0); i++) ;
      if (yc[i] > (xc[i] || 0)) e--;
      if (s < 0) {
        qc.push(1);
        more = true;
      } else {
        xL = xc.length;
        yL = yc.length;
        i = 0;
        s += 2;
        n = mathfloor(base / (yc[0] + 1));
        if (n > 1) {
          yc = multiply(yc, n, base);
          xc = multiply(xc, n, base);
          yL = yc.length;
          xL = xc.length;
        }
        xi = yL;
        rem = xc.slice(0, yL);
        remL = rem.length;
        for (; remL < yL; rem[remL++] = 0) ;
        yz = yc.slice();
        yz = [0].concat(yz);
        yc0 = yc[0];
        if (yc[1] >= base / 2) yc0++;
        do {
          n = 0;
          cmp = compare2(yc, rem, yL, remL);
          if (cmp < 0) {
            rem0 = rem[0];
            if (yL != remL) rem0 = rem0 * base + (rem[1] || 0);
            n = mathfloor(rem0 / yc0);
            if (n > 1) {
              if (n >= base) n = base - 1;
              prod = multiply(yc, n, base);
              prodL = prod.length;
              remL = rem.length;
              while (compare2(prod, rem, prodL, remL) == 1) {
                n--;
                subtract(prod, yL < prodL ? yz : yc, prodL, base);
                prodL = prod.length;
                cmp = 1;
              }
            } else {
              if (n == 0) {
                cmp = n = 1;
              }
              prod = yc.slice();
              prodL = prod.length;
            }
            if (prodL < remL) prod = [0].concat(prod);
            subtract(rem, prod, remL, base);
            remL = rem.length;
            if (cmp == -1) {
              while (compare2(yc, rem, yL, remL) < 1) {
                n++;
                subtract(rem, yL < remL ? yz : yc, remL, base);
                remL = rem.length;
              }
            }
          } else if (cmp === 0) {
            n++;
            rem = [0];
          }
          qc[i++] = n;
          if (rem[0]) {
            rem[remL++] = xc[xi] || 0;
          } else {
            rem = [xc[xi]];
            remL = 1;
          }
        } while ((xi++ < xL || rem[0] != null) && s--);
        more = rem[0] != null;
        if (!qc[0]) qc.splice(0, 1);
      }
      if (base == BASE) {
        for (i = 1, s = qc[0]; s >= 10; s /= 10, i++) ;
        round(q, dp + (q.e = i + e * LOG_BASE - 1) + 1, rm, more);
      } else {
        q.e = e;
        q.r = +more;
      }
      return q;
    };
  })();
  function format(n, i, rm, id) {
    var c0, e, ne, len, str;
    rm = rm == null ? ROUNDING_MODE : intCheck(rm, 0, 8);
    if (!n.c) return n.toString();
    c0 = n.c[0];
    ne = n.e;
    if (i == null) {
      str = coeffToString(n.c);
      str = id == 1 || id == 2 && (ne <= TO_EXP_NEG || ne >= TO_EXP_POS) ? toExponential(str, ne) : toFixedPoint(str, ne, "0");
    } else {
      n = round(new BigNumber2(n), i, rm);
      e = n.e;
      str = coeffToString(n.c);
      len = str.length;
      if (id == 1 || id == 2 && (i <= e || e <= TO_EXP_NEG)) {
        for (; len < i; str += "0", len++) ;
        str = toExponential(str, e);
      } else {
        i -= ne + (id === 2 && e > ne);
        str = toFixedPoint(str, e, "0");
        if (e + 1 > len) {
          if (--i > 0) for (str += "."; i--; str += "0") ;
        } else {
          i += e - len;
          if (i > 0) {
            if (e + 1 == len) str += ".";
            for (; i--; str += "0") ;
          }
        }
      }
    }
    return n.s < 0 && c0 ? "-" + str : str;
  }
  function isBigNumber(v) {
    return v instanceof BigNumber2 || !!v && v._isBigNumber === true;
  }
  function maxOrMin(args, n) {
    var k, y, i = 1, x = new BigNumber2(args[0]);
    for (; i < args.length; i++) {
      y = new BigNumber2(args[i]);
      if (!y.s || (k = compare(x, y)) === n || k === 0 && x.s === n) {
        x = y;
      }
    }
    return x;
  }
  function normalise(n, c, e) {
    var i = 1, j = c.length;
    for (; !c[--j]; c.pop()) ;
    for (j = c[0]; j >= 10; j /= 10, i++) ;
    if ((e = i + e * LOG_BASE - 1) > MAX_EXP) {
      n.c = n.e = null;
    } else if (e < MIN_EXP) {
      n.c = [n.e = 0];
    } else {
      n.e = e;
      n.c = c;
    }
    return n;
  }
  function resolveFormatOptions(options) {
    var key, resolved = {};
    for (key in FORMAT) {
      if (FORMAT.hasOwnProperty(key)) {
        resolved[key] = options.hasOwnProperty(key) ? options[key] : FORMAT[key];
      }
    }
    return resolved;
  }
  function round(x, sd, rm, r) {
    var d, i, j, k, n, ni, rd, xc = x.c, pows10 = POWS_TEN;
    if (xc) {
      out: {
        for (d = 1, k = xc[0]; k >= 10; k /= 10, d++) ;
        i = sd - d;
        if (i < 0) {
          i += LOG_BASE;
          j = sd;
          n = xc[ni = 0];
          rd = mathfloor(n / pows10[d - j - 1] % 10);
        } else {
          ni = mathceil((i + 1) / LOG_BASE);
          if (ni >= xc.length) {
            if (r) {
              for (; xc.length <= ni; xc.push(0)) ;
              n = rd = 0;
              d = 1;
              i %= LOG_BASE;
              j = i - LOG_BASE + 1;
            } else {
              break out;
            }
          } else {
            n = k = xc[ni];
            for (d = 1; k >= 10; k /= 10, d++) ;
            i %= LOG_BASE;
            j = i - LOG_BASE + d;
            rd = j < 0 ? 0 : mathfloor(n / pows10[d - j - 1] % 10);
          }
        }
        r = r || sd < 0 || // Are there any non-zero digits after the rounding digit?
        // The expression  n % pows10[d - j - 1]  returns all digits of n to the right
        // of the digit at j, e.g. if n is 908714 and j is 2, the expression gives 714.
        xc[ni + 1] != null || (j < 0 ? n : n % pows10[d - j - 1]);
        r = rm < 4 ? (rd || r) && (rm == 0 || rm == (x.s < 0 ? 3 : 2)) : rd > 5 || rd == 5 && (rm == 4 || r || rm == 6 && // Check whether the digit to the left of the rounding digit is odd.
        (i > 0 ? j > 0 ? n / pows10[d - j] : 0 : xc[ni - 1]) % 10 & 1 || rm == (x.s < 0 ? 8 : 7));
        if (sd < 1 || !xc[0]) {
          xc.length = 0;
          if (r) {
            sd -= x.e + 1;
            xc[0] = pows10[(LOG_BASE - sd % LOG_BASE) % LOG_BASE];
            x.e = -sd || 0;
          } else {
            xc[0] = x.e = 0;
          }
          return x;
        }
        if (i == 0) {
          xc.length = ni;
          k = 1;
          ni--;
        } else {
          xc.length = ni + 1;
          k = pows10[LOG_BASE - i];
          xc[ni] = j > 0 ? mathfloor(n / pows10[d - j] % pows10[j]) * k : 0;
        }
        if (r) {
          for (; ; ) {
            if (ni == 0) {
              for (i = 1, j = xc[0]; j >= 10; j /= 10, i++) ;
              j = xc[0] += k;
              for (k = 1; j >= 10; j /= 10, k++) ;
              if (i != k) {
                x.e++;
                if (xc[0] == BASE) xc[0] = 1;
              }
              break;
            } else {
              xc[ni] += k;
              if (xc[ni] != BASE) break;
              xc[ni--] = 0;
              k = 1;
            }
          }
        }
        for (i = xc.length; xc[--i] === 0; xc.pop()) ;
      }
      if (x.e > MAX_EXP) {
        x.c = x.e = null;
      } else if (x.e < MIN_EXP) {
        x.c = [x.e = 0];
      }
    }
    return x;
  }
  function valueOf(n) {
    var str, e = n.e;
    if (e === null) return n.toString();
    str = coeffToString(n.c);
    str = e <= TO_EXP_NEG || e >= TO_EXP_POS ? toExponential(str, e) : toFixedPoint(str, e, "0");
    return n.s < 0 ? "-" + str : str;
  }
  P.absoluteValue = P.abs = function() {
    var x = new BigNumber2(this);
    if (x.s < 0) x.s = 1;
    return x;
  };
  P.comparedTo = function(y, b) {
    return compare(this, new BigNumber2(y, b));
  };
  P.decimalPlaces = P.dp = function(dp, rm) {
    var c, n, v, x = this;
    if (dp != null) {
      return round(
        new BigNumber2(x),
        intCheck(dp, -MAX, MAX) + x.e + 1,
        rm == null ? ROUNDING_MODE : intCheck(rm, 0, 8)
      );
    }
    if (!(c = x.c)) return null;
    n = ((v = c.length - 1) - bitFloor(this.e / LOG_BASE)) * LOG_BASE;
    if (v = c[v]) for (; v % 10 == 0; v /= 10, n--) ;
    if (n < 0) n = 0;
    return n;
  };
  P.dividedBy = P.div = function(y, b) {
    return div(this, new BigNumber2(y, b), DECIMAL_PLACES, ROUNDING_MODE);
  };
  P.dividedToIntegerBy = P.idiv = function(y, b) {
    return div(this, new BigNumber2(y, b), 0, 1);
  };
  P.exponentiatedBy = P.pow = function(n, m) {
    var half, isModExp, i, k, more, nIsBig, nIsNeg, nIsOdd, y, x = this;
    n = new BigNumber2(n);
    if (n.c && !n.isInteger()) {
      throw Error(bignumberError + "Exponent not an integer: " + valueOf(n));
    }
    if (m != null) m = new BigNumber2(m);
    nIsBig = n.e > 14;
    if (!x.c || !x.c[0] || x.c[0] == 1 && !x.e && x.c.length == 1 || !n.c || !n.c[0]) {
      y = new BigNumber2(Math.pow(+valueOf(x), nIsBig ? n.s * (2 - isOdd(n)) : +valueOf(n)));
      return m ? y.mod(m) : y;
    }
    nIsNeg = n.s < 0;
    if (m) {
      if (m.c ? !m.c[0] : !m.s) return new BigNumber2(NaN);
      isModExp = !nIsNeg && x.isInteger() && m.isInteger();
      if (isModExp) x = x.mod(m);
    } else if (n.e > 9 && (x.e > 0 || x.e < -1 || (x.e == 0 ? x.c[0] > 1 || nIsBig && x.c[1] >= 24e7 : x.c[0] < 8e13 || nIsBig && x.c[0] <= 9999975e7))) {
      k = x.s < 0 && isOdd(n) ? -0 : 0;
      if (x.e > -1) k = 1 / k;
      return new BigNumber2(nIsNeg ? 1 / k : k);
    } else if (POW_PRECISION) {
      k = mathceil(POW_PRECISION / LOG_BASE + 2);
    }
    if (nIsBig) {
      half = new BigNumber2(0.5);
      if (nIsNeg) n.s = 1;
      nIsOdd = isOdd(n);
    } else {
      i = Math.abs(+valueOf(n));
      nIsOdd = i % 2;
    }
    y = new BigNumber2(ONE);
    for (; ; ) {
      if (nIsOdd) {
        y = y.times(x);
        if (!y.c) break;
        if (k) {
          if (y.c.length > k) y.c.length = k;
        } else if (isModExp) {
          y = y.mod(m);
        }
      }
      if (i) {
        i = mathfloor(i / 2);
        if (i === 0) break;
        nIsOdd = i % 2;
      } else {
        n = n.times(half);
        round(n, n.e + 1, 1);
        if (n.e > 14) {
          nIsOdd = isOdd(n);
        } else {
          i = +valueOf(n);
          if (i === 0) break;
          nIsOdd = i % 2;
        }
      }
      x = x.times(x);
      if (k) {
        if (x.c && x.c.length > k) x.c.length = k;
      } else if (isModExp) {
        x = x.mod(m);
      }
    }
    if (isModExp) return y;
    if (nIsNeg) y = ONE.div(y);
    return m ? y.mod(m) : k ? round(y, POW_PRECISION, ROUNDING_MODE, more) : y;
  };
  P.integerValue = function(rm) {
    var n = new BigNumber2(this);
    return round(n, n.e + 1, rm == null ? ROUNDING_MODE : intCheck(rm, 0, 8));
  };
  P.isEqualTo = P.eq = function(y, b) {
    return compare(this, new BigNumber2(y, b)) === 0;
  };
  P.isFinite = function() {
    return !!this.c;
  };
  P.isGreaterThan = P.gt = function(y, b) {
    return compare(this, new BigNumber2(y, b)) > 0;
  };
  P.isGreaterThanOrEqualTo = P.gte = function(y, b) {
    return (b = compare(this, new BigNumber2(y, b))) === 1 || b === 0;
  };
  P.isInteger = function() {
    return !!this.c && bitFloor(this.e / LOG_BASE) > this.c.length - 2;
  };
  P.isLessThan = P.lt = function(y, b) {
    return compare(this, new BigNumber2(y, b)) < 0;
  };
  P.isLessThanOrEqualTo = P.lte = function(y, b) {
    return (b = compare(this, new BigNumber2(y, b))) === -1 || b === 0;
  };
  P.isNaN = function() {
    return !this.s;
  };
  P.isNegative = function() {
    return this.s < 0;
  };
  P.isPositive = function() {
    return this.s > 0;
  };
  P.isZero = function() {
    return !!this.c && this.c[0] == 0;
  };
  P.minus = function(y, b) {
    var i, j, t, xLTy, x = this, a = x.s;
    y = new BigNumber2(y, b);
    b = y.s;
    if (!a || !b) return new BigNumber2(NaN);
    if (a != b) {
      y.s = -b;
      return x.plus(y);
    }
    var xe = x.e / LOG_BASE, ye = y.e / LOG_BASE, xc = x.c, yc = y.c;
    if (!xe || !ye) {
      if (!xc || !yc) return xc ? (y.s = -b, y) : new BigNumber2(yc ? x : NaN);
      if (!xc[0] || !yc[0]) {
        return yc[0] ? (y.s = -b, y) : new BigNumber2(xc[0] ? x : (
          // IEEE 754 (2008) 6.3: n - n = -0 when rounding to -Infinity
          ROUNDING_MODE == 3 ? -0 : 0
        ));
      }
    }
    xe = bitFloor(xe);
    ye = bitFloor(ye);
    xc = xc.slice();
    if (a = xe - ye) {
      if (xLTy = a < 0) {
        a = -a;
        t = xc;
      } else {
        ye = xe;
        t = yc;
      }
      t.reverse();
      for (b = a; b--; t.push(0)) ;
      t.reverse();
    } else {
      j = (xLTy = (a = xc.length) < (b = yc.length)) ? a : b;
      for (a = b = 0; b < j; b++) {
        if (xc[b] != yc[b]) {
          xLTy = xc[b] < yc[b];
          break;
        }
      }
    }
    if (xLTy) {
      t = xc;
      xc = yc;
      yc = t;
      y.s = -y.s;
    }
    b = (j = yc.length) - (i = xc.length);
    if (b > 0) for (; b--; xc[i++] = 0) ;
    b = BASE - 1;
    for (; j > a; ) {
      if (xc[--j] < yc[j]) {
        for (i = j; i && !xc[--i]; xc[i] = b) ;
        --xc[i];
        xc[j] += BASE;
      }
      xc[j] -= yc[j];
    }
    for (; xc[0] == 0; xc.splice(0, 1), --ye) ;
    if (!xc[0]) {
      y.s = ROUNDING_MODE == 3 ? -1 : 1;
      y.c = [y.e = 0];
      return y;
    }
    return normalise(y, xc, ye);
  };
  P.modulo = P.mod = function(y, b) {
    var q, s, x = this;
    y = new BigNumber2(y, b);
    if (!x.c || !y.s || y.c && !y.c[0]) {
      return new BigNumber2(NaN);
    } else if (!y.c || x.c && !x.c[0]) {
      return new BigNumber2(x);
    }
    if (MODULO_MODE == 9) {
      s = y.s;
      y.s = 1;
      q = div(x, y, 0, 3);
      y.s = s;
      q.s *= s;
    } else {
      q = div(x, y, 0, MODULO_MODE);
    }
    y = x.minus(q.times(y));
    if (!y.c[0] && MODULO_MODE == 1) y.s = x.s;
    return y;
  };
  P.multipliedBy = P.times = function(y, b) {
    var c, e, i, j, k, m, xcL, xlo, xhi, ycL, ylo, yhi, zc, base, sqrtBase, x = this, xc = x.c, yc = (y = new BigNumber2(y, b)).c;
    if (!xc || !yc || !xc[0] || !yc[0]) {
      if (!x.s || !y.s || xc && !xc[0] && !yc || yc && !yc[0] && !xc) {
        y.c = y.e = y.s = null;
      } else {
        y.s *= x.s;
        if (!xc || !yc) {
          y.c = y.e = null;
        } else {
          y.c = [0];
          y.e = 0;
        }
      }
      return y;
    }
    e = bitFloor(x.e / LOG_BASE) + bitFloor(y.e / LOG_BASE);
    y.s *= x.s;
    xcL = xc.length;
    ycL = yc.length;
    if (xcL < ycL) {
      zc = xc;
      xc = yc;
      yc = zc;
      i = xcL;
      xcL = ycL;
      ycL = i;
    }
    for (i = xcL + ycL, zc = []; i--; zc.push(0)) ;
    base = BASE;
    sqrtBase = SQRT_BASE;
    for (i = ycL; --i >= 0; ) {
      c = 0;
      ylo = yc[i] % sqrtBase;
      yhi = yc[i] / sqrtBase | 0;
      for (k = xcL, j = i + k; j > i; ) {
        xlo = xc[--k] % sqrtBase;
        xhi = xc[k] / sqrtBase | 0;
        m = yhi * xlo + xhi * ylo;
        xlo = ylo * xlo + m % sqrtBase * sqrtBase + zc[j] + c;
        c = (xlo / base | 0) + (m / sqrtBase | 0) + yhi * xhi;
        zc[j--] = xlo % base;
      }
      zc[j] = c;
    }
    if (c) {
      ++e;
    } else {
      zc.splice(0, 1);
    }
    return normalise(y, zc, e);
  };
  P.negated = function() {
    var x = new BigNumber2(this);
    x.s = -x.s || null;
    return x;
  };
  P.plus = function(y, b) {
    var t, x = this, a = x.s;
    y = new BigNumber2(y, b);
    b = y.s;
    if (!a || !b) return new BigNumber2(NaN);
    if (a != b) {
      y.s = -b;
      return x.minus(y);
    }
    var xe = x.e / LOG_BASE, ye = y.e / LOG_BASE, xc = x.c, yc = y.c;
    if (!xe || !ye) {
      if (!xc || !yc) return new BigNumber2(a / 0);
      if (!xc[0] || !yc[0]) return yc[0] ? y : new BigNumber2(xc[0] ? x : a * 0);
    }
    xe = bitFloor(xe);
    ye = bitFloor(ye);
    xc = xc.slice();
    if (a = xe - ye) {
      if (a > 0) {
        ye = xe;
        t = yc;
      } else {
        a = -a;
        t = xc;
      }
      t.reverse();
      for (; a--; t.push(0)) ;
      t.reverse();
    }
    a = xc.length;
    b = yc.length;
    if (a - b < 0) {
      t = yc;
      yc = xc;
      xc = t;
      b = a;
    }
    for (a = 0; b; ) {
      a = (xc[--b] = xc[b] + yc[b] + a) / BASE | 0;
      xc[b] = BASE === xc[b] ? 0 : xc[b] % BASE;
    }
    if (a) {
      xc = [a].concat(xc);
      ++ye;
    }
    return normalise(y, xc, ye);
  };
  P.precision = P.sd = function(sd, rm) {
    var c, n, v, x = this;
    if (sd != null && sd !== !!sd) {
      return round(
        new BigNumber2(x),
        intCheck(sd, 1, MAX),
        rm == null ? ROUNDING_MODE : intCheck(rm, 0, 8)
      );
    }
    if (!(c = x.c)) return null;
    v = c.length - 1;
    n = v * LOG_BASE + 1;
    if (v = c[v]) {
      for (; v % 10 == 0; v /= 10, n--) ;
      for (v = c[0]; v >= 10; v /= 10, n++) ;
    }
    if (sd && x.e + 1 > n) n = x.e + 1;
    return n;
  };
  P.shiftedBy = function(k) {
    return this.times("1e" + intCheck(k, -MAX_SAFE_INTEGER, MAX_SAFE_INTEGER));
  };
  P.squareRoot = P.sqrt = function() {
    var m, n, r, rep, t, x = this, c = x.c, s = x.s, e = x.e, dp = DECIMAL_PLACES + 4, half = new BigNumber2("0.5");
    if (s !== 1 || !c || !c[0]) {
      return new BigNumber2(!s || s < 0 && (!c || c[0]) ? NaN : c ? x : 1 / 0);
    }
    s = Math.sqrt(+valueOf(x));
    if (s == 0 || s == 1 / 0) {
      n = coeffToString(c);
      if ((n.length + e) % 2 == 0) n += "0";
      s = Math.sqrt(+n);
      e = bitFloor((e + 1) / 2) - (e < 0 || e % 2);
      if (s == 1 / 0) {
        n = "5e" + e;
      } else {
        n = s.toExponential();
        n = n.slice(0, n.indexOf("e") + 1) + e;
      }
      r = new BigNumber2(n);
    } else {
      r = new BigNumber2(s + "");
    }
    if (r.c[0]) {
      e = r.e;
      s = e + dp;
      if (s < 3) s = 0;
      for (; ; ) {
        t = r;
        r = half.times(t.plus(div(x, t, dp, 1)));
        if (coeffToString(t.c).slice(0, s) === (n = coeffToString(r.c)).slice(0, s)) {
          if (r.e < e) --s;
          n = n.slice(s - 3, s + 1);
          if (n == "9999" || !rep && n == "4999") {
            if (!rep) {
              round(t, t.e + DECIMAL_PLACES + 2, 0);
              if (t.times(t).eq(x)) {
                r = t;
                break;
              }
            }
            dp += 4;
            s += 4;
            rep = 1;
          } else {
            if (!+n || !+n.slice(1) && n.charAt(0) == "5") {
              round(r, r.e + DECIMAL_PLACES + 2, 1);
              m = !r.times(r).eq(x);
            }
            break;
          }
        }
      }
    }
    return round(r, r.e + DECIMAL_PLACES + 1, ROUNDING_MODE, m);
  };
  if (typeof BigInt == "function") {
    P.toBigInt = function(rm) {
      var x = this;
      if (!x.c) return null;
      return BigInt(format(x, x.e + 1, rm));
    };
  }
  P.toExponential = function(dp, rm) {
    return format(this, dp == null ? dp : intCheck(dp, 0, MAX) + 1, rm, 1);
  };
  P.toFixed = function(dp, rm) {
    return format(this, dp == null ? dp : intCheck(dp, -MAX, MAX) + this.e + 1, rm);
  };
  P.toFormat = function(dp, rm, options) {
    var isNeg, min, max, str, x = this;
    if (options == null) {
      options = FORMAT;
      if (dp != null) {
        if (rm != null) {
          if (typeof rm == "object") {
            options = resolveFormatOptions(rm);
            rm = null;
          }
        } else if (typeof dp == "object" && !isArray(dp)) {
          options = resolveFormatOptions(dp);
          dp = rm = null;
        }
      }
    } else if (typeof options != "object") {
      throw Error(bignumberError + "Argument not an object: " + options);
    } else {
      options = resolveFormatOptions(options);
    }
    if (dp != null) {
      if (isArray(dp) && dp.length <= 2) {
        min = dp[0];
        max = dp[1];
        dp = x.dp();
        if (max != null && dp > intCheck(max, 0, MAX)) dp = max;
        if (min != null && intCheck(min, 0, MAX) !== 0) {
          if (max != null && min > max) {
            throw Error(bignumberError + "Minimum must not exceed maximum");
          }
          if (dp < min) dp = min;
        }
      } else {
        intCheck(dp, -MAX, MAX);
      }
    }
    str = x.toFixed(dp, rm);
    isNeg = str.charCodeAt(0) === 45;
    if (isNeg) str = str.slice(1);
    if (x.c) {
      var i, arr = str.split("."), g1 = +options.groupSize, g2 = +options.secondaryGroupSize, groupSeparator = options.groupSeparator || "", intPart = arr[0], fractionPart = arr[1], len = intPart.length;
      if (g2) {
        i = g1;
        g1 = g2;
        g2 = i;
        len -= i;
      }
      if (g1 > 0 && len > 0) {
        i = len % g1 || g1;
        str = intPart.substr(0, i);
        for (; i < len; i += g1) {
          str += groupSeparator + intPart.substr(i, g1);
        }
        if (g2 > 0) str += groupSeparator + intPart.slice(i);
      } else {
        str = intPart;
      }
      if (fractionPart) {
        i = +options.fractionGroupSize;
        if (i) {
          fractionPart = fractionPart.replace(
            new RegExp("\\d{" + i + "}\\B", "g"),
            "$&" + (options.fractionGroupSeparator || "")
          );
        }
        str += (options.decimalSeparator || "") + fractionPart;
      }
    }
    return (options.prefix || "") + (isNeg ? options.negativeSign || "" : x.s > 0 ? options.positiveSign || "" : "") + str + (options.suffix || "");
  };
  P.toFraction = function(md) {
    var d, d0, d1, d2, e, exp, n, n0, n1, q, r, s, xd, xn, x = this, xc = x.c;
    if (md != null) {
      n = new BigNumber2(md);
      if (!n.isInteger() && (n.c || n.s !== 1) || n.lt(ONE)) {
        throw Error(bignumberError + "Argument " + (n.isInteger() ? "out of range: " : "not an integer: ") + valueOf(n));
      }
    }
    if (!xc) {
      return [new BigNumber2(x.s || 0), new BigNumber2(0)];
    }
    d = new BigNumber2(ONE);
    n1 = d0 = new BigNumber2(ONE);
    d1 = n0 = new BigNumber2(ONE);
    s = coeffToString(xc);
    e = d.e = s.length - x.e - 1;
    d.c[0] = POWS_TEN[(exp = e % LOG_BASE) < 0 ? LOG_BASE + exp : exp];
    md = !md || n.comparedTo(d) > 0 ? e > 0 ? d : n1 : n;
    exp = MAX_EXP;
    MAX_EXP = 1 / 0;
    n = new BigNumber2(s);
    xn = n;
    xd = d;
    n0.c[0] = 0;
    for (; ; ) {
      q = div(n, d, 0, 1);
      d2 = d0.plus(q.times(d1));
      if (d2.comparedTo(md) == 1) break;
      d0 = d1;
      d1 = d2;
      n1 = n0.plus(q.times(d2 = n1));
      n0 = d2;
      d = n.minus(q.times(d2 = d));
      n = d2;
    }
    d2 = div(md.minus(d0), d1, 0, 1);
    n0 = n0.plus(d2.times(n1));
    d0 = d0.plus(d2.times(d1));
    r = n1.times(xd).minus(xn.times(d1)).abs().times(d0).comparedTo(
      n0.times(xd).minus(xn.times(d0)).abs().times(d1)
    ) < 1 ? [n1, d1] : [n0, d0];
    r[0].s = x.s;
    MAX_EXP = exp;
    return r;
  };
  P.toNumber = function() {
    return +valueOf(this);
  };
  P.toObject = function() {
    var x = this;
    return {
      c: x.c ? x.c.slice() : null,
      e: x.e,
      s: x.s
    };
  };
  P.toPrecision = function(sd, rm) {
    return format(this, sd == null ? sd : intCheck(sd, 1, MAX), rm, 2);
  };
  P.toString = function(b) {
    var str, n = this, s = n.s, e = n.e;
    if (e === null) {
      if (s) {
        str = "Infinity";
        if (s < 0) str = "-" + str;
      } else {
        str = "NaN";
      }
    } else {
      if (b == null) {
        str = e <= TO_EXP_NEG || e >= TO_EXP_POS ? toExponential(coeffToString(n.c), e) : toFixedPoint(coeffToString(n.c), e, "0");
      } else {
        intCheck(b, 2, ALPHABET.length, "Base");
        str = convertBase(toFixedPoint(coeffToString(n.c), e, "0"), 10, b, s, true);
      }
      if (s < 0 && n.c[0]) str = "-" + str;
    }
    return str;
  };
  P.valueOf = P.toJSON = function() {
    return valueOf(this);
  };
  P._isBigNumber = true;
  if (configObject != null) BigNumber2.set(configObject);
  return BigNumber2;
}
function bitFloor(n) {
  var i = n | 0;
  return n > 0 || n === i ? i : i - 1;
}
function coeffToString(a) {
  var s, z, i = 1, j = a.length, r = a[0] + "";
  for (; i < j; ) {
    s = a[i++] + "";
    z = LOG_BASE - s.length;
    for (; z--; s = "0" + s) ;
    r += s;
  }
  for (j = r.length; r.charCodeAt(--j) === 48; ) ;
  return r.slice(0, j + 1 || 1);
}
function compare(x, y) {
  var a, b, xc = x.c, yc = y.c, i = x.s, j = y.s, k = x.e, l = y.e;
  if (!i || !j) return null;
  a = xc && !xc[0];
  b = yc && !yc[0];
  if (a || b) return a ? b ? 0 : -j : i;
  if (i != j) return i;
  a = i < 0;
  b = k == l;
  if (!xc || !yc) return b ? 0 : !xc ^ a ? 1 : -1;
  if (!b) return k > l ^ a ? 1 : -1;
  j = (k = xc.length) < (l = yc.length) ? k : l;
  for (i = 0; i < j; i++) if (xc[i] != yc[i]) return xc[i] > yc[i] ^ a ? 1 : -1;
  return k == l ? 0 : k > l ^ a ? 1 : -1;
}
function intCheck(n, min, max, name) {
  if (n < min || n > max || n !== mathfloor(n)) {
    throw Error(bignumberError + (name || "Argument") + (typeof n == "number" ? n < min || n > max ? " out of range: " : " not an integer: " : " not a primitive number: ") + String(n));
  }
  return n;
}
function isArray(obj) {
  return {}.toString.call(obj) == "[object Array]";
}
function isOdd(n) {
  var k = n.c.length - 1;
  return bitFloor(n.e / LOG_BASE) == k && n.c[k] % 2 != 0;
}
function toExponential(str, e) {
  return (str.length > 1 ? str.charAt(0) + "." + str.slice(1) : str) + (e < 0 ? "e" : "e+") + e;
}
function toFixedPoint(str, e, z) {
  var len, zs;
  if (e < 0) {
    for (zs = z + "."; ++e; zs += z) ;
    str = zs + str;
  } else {
    len = str.length;
    if (++e > len) {
      for (zs = z, e -= len; --e; zs += z) ;
      str += zs;
    } else if (e < len) {
      str = str.slice(0, e) + "." + str.slice(e);
    }
  }
  return str;
}
var bignumber_default = BigNumber;

// src/app/app.datatypes.ts
var Node = class {
};
var ProxyDiscoveryEntry = class {
};

// src/app/services/node.service.ts
var __decorate = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var KnownHealthStatuses;
(function(KnownHealthStatuses2) {
  KnownHealthStatuses2["Connecting"] = "connecting";
  KnownHealthStatuses2["Unhealthy"] = "unhealthy";
  KnownHealthStatuses2["Healthy"] = "healthy";
})(KnownHealthStatuses || (KnownHealthStatuses = {}));
var UpdaterStorageKeys;
(function(UpdaterStorageKeys2) {
  UpdaterStorageKeys2["UseCustomSettings"] = "updaterUseCustomSettings";
  UpdaterStorageKeys2["Channel"] = "updaterChannel";
  UpdaterStorageKeys2["Version"] = "updaterVersion";
  UpdaterStorageKeys2["ArchiveURL"] = "updaterArchiveURL";
  UpdaterStorageKeys2["ChecksumsURL"] = "updaterChecksumsURL";
})(UpdaterStorageKeys || (UpdaterStorageKeys = {}));
var NodeService = class NodeService2 {
  constructor(apiService, storageService) {
    this.apiService = apiService;
    this.storageService = storageService;
  }
  /**
   * Gets the list of the nodes connected to the hypervisor (flat —
   * does not preserve sub-hypervisor grouping). Backed by
   * /api/visors-summary. Use getNodesTree() if you need the
   * structural information for rendering per-sub-hypervisor tables.
   */
  getNodes() {
    return this.apiService.get("visors-summary").pipe(map((result) => this.nodesFromSummaries(result)));
  }
  /** Parses a /api/visors-summary body, merging in the saved local nodes, for getNodes() and pushed bodies. */
  nodesFromSummaries(result) {
    let nodes = [];
    if (result) {
      result.forEach((response) => {
        const node = new Node();
        node.online = response.online;
        node.offlineSince = response.offline_since;
        node.lastSeenAt = response.last_seen_at;
        node.isStale = !!response.offline_since;
        node.localPk = response.overview.local_pk;
        node.hostname = response.overview.hostname;
        node.version = response.overview.build_info.version;
        node.configVersion = response.config_version;
        node.os = response.overview.build_info.os;
        node.arch = response.overview.build_info.arch;
        node.autoconnectTransports = response.public_autoconnect;
        node.isPublic = response.is_public;
        node.buildTag = response.build_tag ? response.build_tag : "";
        node.rewardsAddress = response.reward_address;
        node.rewardEligible = response.reward_eligible;
        node.rewardIneligibleReason = response.reward_ineligible_reason;
        if (response.overview && response.overview.local_ip && response.overview.local_ip.trim()) {
          node.ip = response.overview.local_ip;
        } else if (response.overview && response.overview.public_ip && response.overview.public_ip.trim()) {
          node.ip = response.overview.public_ip;
        } else {
          node.ip = null;
        }
        if (response.overview && response.overview.public_ip && response.overview.public_ip.trim()) {
          node.publicIp = response.overview.public_ip;
        } else {
          node.publicIp = null;
        }
        node.isSymmeticNat = response.overview.is_symmetic_nat;
        if (response.overview.country_code) {
          node.countryCode = response.overview.country_code;
        }
        if (response.overview.region_name) {
          node.regionName = response.overview.region_name;
        }
        if (response.overview.city_name) {
          node.cityName = response.overview.city_name;
        }
        if (response.overview.latitude) {
          node.latitude = response.overview.latitude;
        }
        if (response.overview.longitude) {
          node.longitude = response.overview.longitude;
        }
        const labelInfo = this.storageService.getLabelInfo(node.localPk);
        node.label = labelInfo && labelInfo.label ? labelInfo.label : this.storageService.getDefaultLabel(node);
        if (response.health) {
          node.health = {
            servicesHealth: response.health.services_health,
            uptimeTrackerHealth: response.health.uptime_tracker_health,
            autoconnectHealth: response.health.autoconnect_health,
            transportabilityHealth: response.health.transportability_health
          };
        }
        node.dmsgServerPk = response.dmsg_stats ? response.dmsg_stats.server_public_key : "";
        node.connectedDmsgServers = response.connected_dmsg_servers || [];
        node.roundTripPing = response.dmsg_stats ? this.nsToMs(response.dmsg_stats.round_trip) : "";
        if (response.dmsg_servers && Array.isArray(response.dmsg_servers)) {
          node.dmsgServers = response.dmsg_servers.map((s) => {
            return {
              pk: s.pk,
              latency: s.latency || 0,
              protocol: s.protocol || ""
            };
          });
        }
        node.transports = [];
        if (response.overview && response.overview.transports) {
          response.overview.transports.forEach((transport) => {
            node.transports.push({
              id: transport.id,
              localPk: transport.local_pk,
              remotePk: transport.remote_pk,
              type: transport.type,
              recv: transport.log ? transport.log.recv : 0,
              sent: transport.log ? transport.log.sent : 0,
              latencyMs: transport.latency_ms || 0,
              initiator: transport.initiator
            });
          });
        }
        node.apps = [];
        if (response.overview && response.overview.apps) {
          response.overview.apps.forEach((app) => {
            node.apps.push({
              name: app.name,
              autostart: app.auto_start,
              port: app.port,
              status: app.status,
              detailedStatus: app.detailed_status,
              args: app.args,
              otp: app.otp
            });
          });
        }
        node.isHypervisor = response.is_hypervisor;
        nodes.push(node);
      });
    }
    const obtainedNodes = /* @__PURE__ */ new Map();
    const nodesToRegisterInLocalStorageAsOnline = [];
    const ipsToRegisterInLocalStorageAsOnline = [];
    nodes.forEach((node) => {
      obtainedNodes.set(node.localPk, node);
      if (node.online) {
        nodesToRegisterInLocalStorageAsOnline.push(node.localPk);
        if (node.ip) {
          ipsToRegisterInLocalStorageAsOnline.push(node.ip);
        }
      }
    });
    this.storageService.includeVisibleLocalNodes(nodesToRegisterInLocalStorageAsOnline, ipsToRegisterInLocalStorageAsOnline);
    const missingSavedNodes = [];
    this.storageService.getSavedLocalNodes().forEach((node) => {
      if (!obtainedNodes.has(node.publicKey) && !node.hidden) {
        const newNode = new Node();
        newNode.localPk = node.publicKey;
        const labelInfo = this.storageService.getLabelInfo(node.publicKey);
        newNode.label = labelInfo && labelInfo.label ? labelInfo.label : this.storageService.getDefaultLabel(newNode);
        newNode.online = false;
        newNode.dmsgServerPk = "";
        newNode.roundTripPing = "";
        missingSavedNodes.push(newNode);
      }
      if (obtainedNodes.has(node.publicKey) && !obtainedNodes.get(node.publicKey).online && node.hidden) {
        obtainedNodes.delete(node.publicKey);
      }
    });
    nodes = [];
    obtainedNodes.forEach((value) => nodes.push(value));
    nodes = nodes.concat(missingSavedNodes);
    return nodes;
  }
  /**
   * Gets the tree of nodes per hypervisor. Returns one
   * NodeSection per hypervisor reachable from the local one — local
   * hypervisor first, then each sub-hypervisor that the local one is
   * directly connected to.
   *
   * Powers the main node list UI's per-sub-hypervisor table rendering
   * (#2633 / #2640). Backed by /api/visors-tree-summary.
   *
   * Visor entries replicate across sections by design: a visor
   * connected to multiple hypervisors appears in every relevant
   * section. Section dedup is on the backend (mutual hypervisor
   * pairs render the shared sub-hypervisor once).
   *
   * Reuses getNodes()'s mapping: each section's `visors` array has
   * the same shape as the /visors-summary array, so the per-row
   * parsing is identical. This wrapper just groups the rows into
   * sections.
   */
  getNodesTree() {
    return this.apiService.get("visors-tree-summary").pipe(map((result) => this.nodesTreeFromResponse(result)));
  }
  /** Parses a /api/visors-tree-summary body into sections, for getNodesTree() and pushed bodies. */
  nodesTreeFromResponse(result) {
    const sections = [];
    if (!result || !result.sections) {
      return sections;
    }
    for (const section of result.sections) {
      const nodeSection = {
        hypervisorPk: section.hypervisor_pk,
        viaChain: section.via_chain || [],
        nodes: section.visors ? this.parseVisorsResponse(section.visors) : []
      };
      if (section.sub_error) {
        nodeSection.subError = section.sub_error;
      }
      sections.push(nodeSection);
    }
    return sections;
  }
  /**
   * Parses a /visors-summary-shaped array into Node[]. Same logic as
   * getNodes()'s pipe but exposed as a private helper so
   * getNodesTree() can run it per section without duplicating the
   * ~170 lines of field mapping.
   *
   * The savedLocalNodes / hidden-node merge that getNodes() does
   * AFTER parsing isn't repeated here — sections are per-hypervisor
   * and the saved-nodes invariant applies to the hypervisor-wide
   * view, not the per-section one. Callers that need that merge
   * should use getNodes() directly.
   */
  parseVisorsResponse(result) {
    const out = [];
    if (!result) {
      return out;
    }
    result.forEach((response) => {
      const node = new Node();
      node.online = response.online;
      node.offlineSince = response.offline_since;
      node.lastSeenAt = response.last_seen_at;
      node.isStale = !!response.offline_since;
      node.localPk = response.overview ? response.overview.local_pk : "";
      node.hostname = response.overview ? response.overview.hostname : void 0;
      node.version = response.overview && response.overview.build_info ? response.overview.build_info.version : "";
      node.configVersion = response.config_version;
      node.os = response.overview && response.overview.build_info ? response.overview.build_info.os : "";
      node.arch = response.overview && response.overview.build_info ? response.overview.build_info.arch : "";
      node.autoconnectTransports = response.public_autoconnect;
      node.isPublic = response.is_public;
      node.buildTag = response.build_tag ? response.build_tag : "";
      node.rewardsAddress = response.reward_address;
      node.rewardEligible = response.reward_eligible;
      node.rewardIneligibleReason = response.reward_ineligible_reason;
      if (response.overview && response.overview.local_ip && response.overview.local_ip.trim()) {
        node.ip = response.overview.local_ip;
      } else if (response.overview && response.overview.public_ip && response.overview.public_ip.trim()) {
        node.ip = response.overview.public_ip;
      } else {
        node.ip = null;
      }
      if (response.overview && response.overview.public_ip && response.overview.public_ip.trim()) {
        node.publicIp = response.overview.public_ip;
      } else {
        node.publicIp = null;
      }
      if (response.overview) {
        node.isSymmeticNat = response.overview.is_symmetic_nat;
        if (response.overview.country_code) {
          node.countryCode = response.overview.country_code;
        }
        if (response.overview.region_name) {
          node.regionName = response.overview.region_name;
        }
        if (response.overview.city_name) {
          node.cityName = response.overview.city_name;
        }
        if (response.overview.latitude) {
          node.latitude = response.overview.latitude;
        }
        if (response.overview.longitude) {
          node.longitude = response.overview.longitude;
        }
      }
      const labelInfo = this.storageService.getLabelInfo(node.localPk);
      node.label = labelInfo && labelInfo.label ? labelInfo.label : this.storageService.getDefaultLabel(node);
      if (response.health) {
        node.health = {
          servicesHealth: response.health.services_health,
          uptimeTrackerHealth: response.health.uptime_tracker_health,
          autoconnectHealth: response.health.autoconnect_health,
          transportabilityHealth: response.health.transportability_health
        };
      }
      node.isHypervisor = response.is_hypervisor;
      node.transports = [];
      if (response.overview && response.overview.transports) {
        response.overview.transports.forEach((transport) => {
          node.transports.push({
            id: transport.id,
            localPk: transport.local_pk,
            remotePk: transport.remote_pk,
            type: transport.type,
            recv: transport.log ? transport.log.recv : 0,
            sent: transport.log ? transport.log.sent : 0,
            latencyMs: transport.latency_ms || 0,
            initiator: transport.initiator
          });
        });
      }
      node.dmsgServerPk = response.dmsg_stats ? response.dmsg_stats.server_public_key : "";
      node.connectedDmsgServers = response.connected_dmsg_servers || [];
      node.roundTripPing = response.dmsg_stats ? this.nsToMs(response.dmsg_stats.round_trip) : "";
      if (response.dmsg_servers && Array.isArray(response.dmsg_servers)) {
        node.dmsgServers = response.dmsg_servers.map((s) => {
          return {
            pk: s.pk,
            latency: s.latency || 0,
            protocol: s.protocol || ""
          };
        });
      }
      node.apps = (response.overview && response.overview.apps || []).map((app) => {
        return {
          name: app.name,
          autostart: app.auto_start,
          port: app.port,
          status: app.status,
          detailedStatus: app.detailed_status,
          args: app.args,
          otp: app.otp
        };
      });
      out.push(node);
    });
    return out;
  }
  /**
   * Converts a ns value to a ms string. It includes 2 decimals is the final value is less than 10.
   * @param time Value to convert.
   */
  nsToMs(time) {
    let value = new bignumber_default(time).dividedBy(1e6);
    if (value.isLessThan(10)) {
      value = value.decimalPlaces(2);
    } else {
      value = value.decimalPlaces(0);
    }
    return value.toString(10);
  }
  /**
   * Gets the details of a specific node.
   */
  getNode(nodeKey) {
    return this.apiService.get(`visors/${nodeKey}/summary`).pipe(map((response) => this.nodeFromSummary(response)));
  }
  /**
   * Turns a /visors/{pk}/summary response into a Node. Shared by the request
   * above and the live subscription in SingleNodeDataService.
   */
  nodeFromSummary(response) {
    const node = new Node();
    node.localPk = response.overview.local_pk;
    node.hostname = response.overview.hostname;
    node.version = response.overview.build_info.version;
    node.configVersion = response.config_version;
    node.os = response.overview.build_info.os;
    node.arch = response.overview.build_info.arch;
    node.secondsOnline = Math.floor(Number.parseFloat(response.uptime));
    node.minHops = response.min_hops;
    node.buildTag = response.build_tag;
    node.connectedDmsgServers = response.connected_dmsg_servers || [];
    node.isSymmeticNat = response.overview.is_symmetic_nat;
    node.publicIp = response.overview.public_ip;
    node.autoconnectTransports = response.public_autoconnect;
    node.isPublic = response.is_public;
    node.rewardsAddress = response.reward_address;
    node.rewardEligible = response.reward_eligible;
    node.rewardIneligibleReason = response.reward_ineligible_reason;
    node.connectivity = response.connectivity?.state;
    node.hypervisors = Array.isArray(response.overview.hypervisors) ? response.overview.hypervisors : [];
    node.connectedHypervisors = Array.isArray(response.overview.connected_hypervisor) ? response.overview.connected_hypervisor : [];
    if (response.overview.country_code) {
      node.countryCode = response.overview.country_code;
    }
    if (response.overview.region_name) {
      node.regionName = response.overview.region_name;
    }
    if (response.overview.city_name) {
      node.cityName = response.overview.city_name;
    }
    if (response.overview.latitude) {
      node.latitude = response.overview.latitude;
    }
    if (response.overview.longitude) {
      node.longitude = response.overview.longitude;
    }
    if (response.overview.local_ip && response.overview.local_ip.trim()) {
      node.ip = response.overview.local_ip;
    } else {
      node.ip = null;
    }
    const labelInfo = this.storageService.getLabelInfo(node.localPk);
    node.label = labelInfo && labelInfo.label ? labelInfo.label : this.storageService.getDefaultLabel(node);
    node.health = {
      servicesHealth: response.health.services_health,
      uptimeTrackerHealth: response.health.uptime_tracker_health,
      autoconnectHealth: response.health.autoconnect_health,
      transportabilityHealth: response.health.transportability_health
    };
    node.transports = [];
    if (response.overview.transports) {
      response.overview.transports.forEach((transport) => {
        node.transports.push({
          id: transport.id,
          localPk: transport.local_pk,
          remotePk: transport.remote_pk,
          type: transport.type,
          recv: transport.log ? transport.log.recv : 0,
          sent: transport.log ? transport.log.sent : 0,
          latencyMs: transport.latency_ms || 0,
          initiator: transport.initiator
        });
      });
    }
    node.persistentTransports = [];
    if (response.persistent_transports) {
      response.persistent_transports.forEach((persistentTransport) => {
        node.persistentTransports.push({
          pk: persistentTransport.pk,
          type: persistentTransport.type
        });
      });
    }
    const routes = [];
    node.routes = routes;
    if (response.routes) {
      response.routes.forEach((route) => {
        routes.push({
          key: route.key,
          rule: route.rule
        });
        if (route.rule_summary) {
          routes[routes.length - 1].ruleSummary = {
            keepAlive: route.rule_summary.keep_alive,
            ruleType: route.rule_summary.rule_type,
            keyRouteId: route.rule_summary.key_route_id
          };
          if (route.rule_summary.app_fields && route.rule_summary.app_fields.route_descriptor) {
            routes[routes.length - 1].appFields = {
              routeDescriptor: {
                dstPk: route.rule_summary.app_fields.route_descriptor.dst_pk,
                dstPort: route.rule_summary.app_fields.route_descriptor.dst_port,
                srcPk: route.rule_summary.app_fields.route_descriptor.src_pk,
                srcPort: route.rule_summary.app_fields.route_descriptor.src_port
              }
            };
          }
          if (route.rule_summary.forward_fields) {
            const forwardFields = {
              nextRid: route.rule_summary.forward_fields.next_rid,
              nextTid: route.rule_summary.forward_fields.next_tid
            };
            routes[routes.length - 1].forwardFields = forwardFields;
            if (route.rule_summary.forward_fields.route_descriptor) {
              forwardFields.routeDescriptor = {
                dstPk: route.rule_summary.forward_fields.route_descriptor.dst_pk,
                dstPort: route.rule_summary.forward_fields.route_descriptor.dst_port,
                srcPk: route.rule_summary.forward_fields.route_descriptor.src_pk,
                srcPort: route.rule_summary.forward_fields.route_descriptor.src_port
              };
            }
          }
          if (route.rule_summary.intermediary_forward_fields) {
            routes[routes.length - 1].intermediaryForwardFields = {
              nextRid: route.rule_summary.intermediary_forward_fields.next_rid,
              nextTid: route.rule_summary.intermediary_forward_fields.next_tid
            };
          }
        }
      });
    }
    node.apps = [];
    if (response.overview.apps) {
      response.overview.apps.forEach((app) => {
        node.apps.push({
          name: app.name,
          status: app.status,
          port: app.port,
          autostart: app.auto_start,
          detailedStatus: app.detailed_status,
          args: app.args,
          otp: app.otp
        });
      });
    }
    let dmsgServerFound = false;
    if (response.dmsg_stats) {
      node.dmsgServerPk = response.dmsg_stats.server_public_key;
      node.roundTripPing = this.nsToMs(response.dmsg_stats.round_trip);
      dmsgServerFound = true;
    }
    if (!dmsgServerFound) {
      node.dmsgServerPk = "-";
      node.roundTripPing = "-1";
    }
    if (response.dmsg_servers && Array.isArray(response.dmsg_servers)) {
      node.dmsgServers = response.dmsg_servers.map((s) => {
        return {
          pk: s.pk,
          latency: s.latency || 0,
          protocol: s.protocol || ""
        };
      });
    }
    node.isHypervisor = response.is_hypervisor;
    return node;
  }
  /**
   * Sets the rewards address of the node. acceptRisk opts in to a single address
   * instead of an account xpub.
   */
  setRewardsAddress(nodeKey, address, acceptRisk = false) {
    const data = { reward_address: address };
    if (acceptRisk) {
      data.accept_risk = true;
    }
    return this.apiService.put(`visors/${nodeKey}/reward`, data);
  }
  /**
   * Gets the rewards address of the node.
   */
  getRewardsAddress(nodeKey) {
    return this.apiService.get(`visors/${nodeKey}/reward`);
  }
  /**
   * Checks whether a reward address has ever spent, which put its public key on chain.
   */
  getRewardsAddressExposure(nodeKey, address) {
    return this.apiService.get(`visors/${nodeKey}/reward/exposure?address=${encodeURIComponent(address)}`);
  }
  /**
   * Gets the runtime logs of the node.
   */
  getRuntimeLogs(nodeKey) {
    return this.apiService.get(`visors/${nodeKey}/runtime-logs`);
  }
  /**
   * Diff-streaming variant of getRuntimeLogs. Pass the previous
   * response's `latest` as `since` to receive only newly-arrived
   * entries; pass 0 for the full buffer. Returns the visor's
   * RuntimeLogsDelta shape: { entries, latest, dropped }.
   */
  getRuntimeLogsSince(nodeKey, since) {
    return this.apiService.get(`visors/${nodeKey}/runtime-logs?since=${since}`);
  }
  /** Visor process Go-runtime stats (heap, GC, goroutines). */
  getRuntimeStats(nodeKey) {
    return this.apiService.get(`visors/${nodeKey}/runtime-stats`);
  }
  /** Host system + visor process resource snapshot (CPU%, mem, disk, net). */
  getHostStats(nodeKey) {
    return this.apiService.get(`visors/${nodeKey}/host-stats`);
  }
  /**
   * Aggregated SD/TPD/UT network view — the same combined table
   * `skywire cli sd` prints. Hypervisor-scope (no nodeKey arg);
   * cached on the visor for 5min by default. Pass refresh=true to
   * force the visor to re-aggregate immediately.
   */
  getNetworkView(refresh = false) {
    const q = refresh ? "?refresh=true" : "";
    return this.apiService.get(`network-view${q}`);
  }
  /**
   * Embedded mainnet reward rules — same content `skywire cli
   * reward rules` prints. Returned as raw markdown text.
   */
  getRewardRules() {
    return this.apiService.get(`reward-rules`, new RequestOptions({ responseType: ResponseTypes.Text }));
  }
  /**
   * Gets the first addresses an account xpub derives, which are where its rewards go.
   */
  getRewardXpubAddresses(xpub) {
    return this.apiService.get(`reward-xpub-addresses?xpub=${encodeURIComponent(xpub)}`);
  }
  /**
   * Removes the rewards address of the node.
   */
  deleteRewardsAddress(nodeKey) {
    return this.apiService.delete(`visors/${nodeKey}/reward`);
  }
  /**
   * Gets the resolving proxy status for a visor.
   */
  getProxies(nodeKey) {
    return this.apiService.get(`visors/${nodeKey}/proxies`);
  }
  /**
   * Enables or disables a resolving proxy.
   */
  setProxyEnabled(nodeKey, kind, enable) {
    return this.apiService.post(`visors/${nodeKey}/proxies/set`, { kind, enable });
  }
  /**
   * Sets the upstream SOCKS5 address for a resolving proxy.
   */
  setProxyUpstream(nodeKey, kind, addr) {
    return this.apiService.post(`visors/${nodeKey}/proxies/upstream`, { kind, addr });
  }
  /**
   * Gets the list of skynet-forwarded ports.
   */
  getSkynetPorts(nodeKey) {
    return this.apiService.get(`visors/${nodeKey}/skynet-ports`);
  }
  /**
   * Registers a local port for skynet forwarding.
   */
  registerSkynetPort(nodeKey, port) {
    return this.apiService.post(`visors/${nodeKey}/skynet-ports/register`, { port });
  }
  /**
   * Deregisters a local port from skynet forwarding.
   */
  deregisterSkynetPort(nodeKey, port) {
    return this.apiService.post(`visors/${nodeKey}/skynet-ports/deregister`, { port });
  }
  /**
   * Gets all forwarded ports with rich metadata.
   */
  getForwardedPorts(nodeKey) {
    return this.apiService.get(`visors/${nodeKey}/forwarded-ports`);
  }
  /**
   * Registers a forwarded port with metadata (label, description, skynet/dmsg, whitelist).
   */
  registerForwardedPort(nodeKey, port) {
    return this.apiService.post(`visors/${nodeKey}/forwarded-ports/register`, port);
  }
  /**
   * Updates a forwarded port's metadata.
   */
  updateForwardedPort(nodeKey, port) {
    return this.apiService.post(`visors/${nodeKey}/forwarded-ports/update`, port);
  }
  /**
   * Gets active skynet forward connections (reverse proxies).
   */
  getSkynetForwards(nodeKey) {
    return this.apiService.get(`visors/${nodeKey}/skynet-forwards`);
  }
  /**
   * Creates a skynet/dmsg forward: maps remote_pk:remote_port (over the
   * named network) to localhost:local_port. Network is "skynet" (default;
   * empty string treated as skynet) or "dmsg".
   */
  skynetConnect(nodeKey, network, remotePK, remotePort, localPort) {
    return this.apiService.post(`visors/${nodeKey}/skynet-forwards/connect`, {
      network,
      remote_pk: remotePK,
      remote_port: remotePort,
      local_port: localPort
    });
  }
  /**
   * Disconnects a skynet forward by UUID.
   */
  skynetDisconnect(nodeKey, id) {
    return this.apiService.post(`visors/${nodeKey}/skynet-forwards/disconnect`, { id });
  }
  /**
   * Turns off a node.
   */
  shutdown(nodeKey) {
    return this.apiService.post(`visors/${nodeKey}/shutdown`);
  }
  /**
   * Asks a node to restart itself.
   */
  restart(nodeKey) {
    return this.apiService.post(`visors/${nodeKey}/restart`);
  }
  /**
   * Checks if a node is currently being updated.
   */
  checkIfUpdating(nodeKey) {
    return this.apiService.get(`visors/${nodeKey}/update/ws/running`);
  }
  /**
   * Checks if there are updates available for a node.
   */
  checkUpdate(nodeKey) {
    let channel = "stable";
    const savedChannel = localStorage.getItem(UpdaterStorageKeys.Channel);
    channel = savedChannel ? savedChannel : channel;
    return this.apiService.get(`visors/${nodeKey}/update/available/${channel}`);
  }
  /**
   * Updates a node.
   */
  update(nodeKey) {
    const body = {
      channel: "stable"
      // channel: 'testing' // for debugging updater
    };
    const useCustomSettings = localStorage.getItem(UpdaterStorageKeys.UseCustomSettings);
    if (useCustomSettings) {
      const channel = localStorage.getItem(UpdaterStorageKeys.Channel);
      if (channel) {
        body["channel"] = channel;
      }
      const version = localStorage.getItem(UpdaterStorageKeys.Version);
      if (version) {
        body["version"] = version;
      }
      const archiveURL = localStorage.getItem(UpdaterStorageKeys.ArchiveURL);
      if (archiveURL) {
        body["archive_url"] = archiveURL;
      }
      const checksumsURL = localStorage.getItem(UpdaterStorageKeys.ChecksumsURL);
      if (checksumsURL) {
        body["checksums_url"] = checksumsURL;
      }
    }
    return this.apiService.ws(`visors/${nodeKey}/update/ws`, body);
  }
  static {
    this.ctorParameters = () => [
      { type: ApiService },
      { type: StorageService }
    ];
  }
};
NodeService = __decorate([
  Injectable({
    providedIn: "root"
  })
], NodeService);

export {
  bignumber_default,
  ProxyDiscoveryEntry,
  KnownHealthStatuses,
  UpdaterStorageKeys,
  NodeService
};
//# debugId=b68ff3cb-3e2d-5d83-a631-07f3a2738c52
//# sourceMappingURL=chunk-XBRWDJ2O.js.map
