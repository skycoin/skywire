// Package store pkg/deployment/tpd/store/bw_daily_fields.go c3-net-discovery
//
// A bw:daily hash keeps per-reporter fields "<reporter>:<suffix>". The
// reporter was its 66-character hex key, which put every field past redis's
// hash-max-listpack-value of 64 bytes, so each hash was a hashtable at about
// 830 bytes. It is now the key's 53-character DNS label (cipher.DNSLabel),
// which keeps the hash a listpack at about half that. The scripts move a
// reporter's hex counters to the label form when they next write the hash,
// and the readers accept both forms, so a hash written before the change
// reads the same.
package store

import (
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
)

// reporterFieldNames is an edge's field names in a bw:daily hash, each in the
// label form writes use and the hex form of older hashes.
type reporterFieldNames struct {
	hex                    string
	sent, recv, vsent, vck [2]string
}

func reporterFieldNamesOf(pk cipher.PubKey) reporterFieldNames {
	label, hex := pk.DNSLabel(), pk.Hex()
	both := func(suffix string) [2]string { return [2]string{label + ":" + suffix, hex + ":" + suffix} }
	return reporterFieldNames{
		hex:   hex,
		sent:  both("sent"),
		recv:  both("recv"),
		vsent: both(verifiedSentField),
		vck:   both(verifiedCheckField),
	}
}

// reporterFieldSum sums a counter over both forms of its field name, and
// reports whether either exists.
func reporterFieldSum(h map[string]string, names [2]string) (uint64, bool) {
	var total uint64
	found := false
	for _, name := range names {
		if v, ok := h[name]; ok {
			total += parseBWUint(v)
			found = true
		}
	}
	return total, found
}

// reporterFieldSplit splits a field into its reporter key and suffix, for a
// reporter in either form. False for the hash's own fields.
func reporterFieldSplit(field string) (cipher.PubKey, string, bool) {
	i := strings.IndexByte(field, ':')
	if i != cipher.PubKeyDNSLabelLen && i != 2*len(cipher.PubKey{}) {
		return cipher.PubKey{}, "", false
	}
	var pk cipher.PubKey
	if err := pk.UnmarshalText([]byte(field[:i])); err != nil {
		return cipher.PubKey{}, "", false
	}
	return pk, field[i+1:], true
}

// luaReporterLabel defines label(hex) in a script: the reporter's
// cipher.DNSLabel, from the hex the script is given, and fold(key, hex, lab),
// which moves the reporter's hex sent and recv counters to the label fields.
const luaReporterLabel = `
local function label(hex)
  local a = 'abcdefghijklmnopqrstuvwxyz234567'
  local out, acc, bits = {}, 0, 0
  for i = 1, #hex do
    acc = acc * 16 + tonumber(string.sub(hex, i, i), 16)
    bits = bits + 4
    if bits >= 5 then
      bits = bits - 5
      local v = math.floor(acc / 2 ^ bits)
      acc = acc - v * 2 ^ bits
      out[#out + 1] = string.sub(a, v + 1, v + 1)
    end
  end
  if bits > 0 then
    local v = acc * 2 ^ (5 - bits)
    out[#out + 1] = string.sub(a, v + 1, v + 1)
  end
  return table.concat(out)
end
local function fold(key, hex, lab)
  local old = redis.call('HMGET', key, hex .. ':sent', hex .. ':recv')
  if old[1] then
    redis.call('HINCRBY', key, lab .. ':sent', old[1])
    redis.call('HDEL', key, hex .. ':sent')
  end
  if old[2] then
    redis.call('HINCRBY', key, lab .. ':recv', old[2])
    redis.call('HDEL', key, hex .. ':recv')
  end
end
`
