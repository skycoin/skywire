// Package store pkg/deployment/tpd/store/checkpoints.go c4-net-discovery
//
// Stream checkpoints verify a transport's bandwidth by content. Each edge
// publishes, for what it sent and what it received on its current conn, the
// conn's epoch and recent digests of the byte stream at fixed offsets (see
// pkg/transport/stream_digest.go). When the sender's sent side and the
// receiver's received side name the same epoch, a digest both report at the
// same offset proves every byte before it passed between them.
//
// Per transport, ck:<id> holds each edge's latest record and, per sender and
// epoch, the largest offset verified so far. Growth of that offset is added to
// the day's bw:daily hash as <sender>:vsent, and <sender>:vck marks that the
// two edges reported the same conn that day, which is when the pay rule caps by
// it. The same offset with different digests sets <sender>:vbad once per conn.
package store

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/telemetrywire"
)

// checkpointRecordTTL keeps an edge's checkpoints and the verified offsets
// past any gap in reports, like bandwidthPrevTTL.
const checkpointRecordTTL = 48 * time.Hour

// Daily hash fields, after the sender's key and a colon.
const (
	verifiedSentField  = "vsent"
	verifiedCheckField = "vck"
	verifiedBadField   = "vbad"
)

func (s *redisStore) checkpointKey(id uuid.UUID) string {
	return serviceName + ":ck:" + id.String()
}

// encodeCheckpoints is a direction as the match script reads it:
// "<epoch>;<offset>:<digest>,...", decimal, digests as strings.
func encodeCheckpoints(d telemetrywire.DirCheckpoints) string {
	var b strings.Builder
	b.WriteString(strconv.FormatUint(d.Epoch, 10))
	b.WriteByte(';')
	for i, cp := range d.Points() {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatUint(cp.Offset, 10))
		b.WriteByte(':')
		b.WriteString(strconv.FormatUint(cp.Digest, 10))
	}
	return b.String()
}

// checkpointScript stores the reporter's record and matches it against the
// other edge's, both directions.
//
// KEYS: ck hash, today's bw:daily hash of the transport
// ARGV: reporter hex, sent record, recv record, ck TTL, daily TTL
var checkpointScript = redis.NewScript(`
local function parse(enc)
  if not enc then return nil, nil end
  local epoch, rest = string.match(enc, '^(%d+);(.*)$')
  if not epoch then return nil, nil end
  local pts = {}
  for o, d in string.gmatch(rest, '(%d+):(%d+)') do pts[o] = d end
  return epoch, pts
end
local function match(sender, sEnc, rEnc)
  local se, sp = parse(sEnc)
  local re, rp = parse(rEnc)
  if not se or not re or se == '0' or se ~= re then return end
  redis.call('HSET', KEYS[2], sender .. ':vck', '1')
  local best, bad = 0, false
  for o, d in pairs(sp) do
    local rd = rp[o]
    if rd then
      if rd == d then
        local n = tonumber(o)
        if n > best then best = n end
      else
        bad = true
      end
    end
  end
  if bad and redis.call('HSETNX', KEYS[1], sender .. ':bad:' .. se, '1') == 1 then
    redis.call('HINCRBY', KEYS[2], sender .. ':vbad', 1)
  end
  if best == 0 then return end
  local vf = sender .. ':v:' .. se
  local old = tonumber(redis.call('HGET', KEYS[1], vf) or '0')
  if best > old then
    redis.call('HSET', KEYS[1], vf, string.format('%d', best))
    redis.call('HINCRBY', KEYS[2], sender .. ':vsent', string.format('%d', best - old))
  end
end
redis.call('HSET', KEYS[1], ARGV[1] .. ':s', ARGV[2], ARGV[1] .. ':r', ARGV[3])
redis.call('EXPIRE', KEYS[1], ARGV[4])
local all = redis.call('HGETALL', KEYS[1])
for i = 1, #all, 2 do
  local peer = string.match(all[i], '^(%x+):s$')
  if peer and peer ~= ARGV[1] then
    match(ARGV[1], ARGV[2], redis.call('HGET', KEYS[1], peer .. ':r'))
    match(peer, all[i + 1], ARGV[3])
  end
end
if redis.call('EXISTS', KEYS[2]) == 1 then
  redis.call('EXPIRE', KEYS[2], ARGV[5], 'NX')
end
return 1
`)

// ApplyCheckpoints records a reporter's checkpoints for a transport and
// credits the bytes they verify against the other edge's.
func (s *redisStore) ApplyCheckpoints(ctx context.Context, id uuid.UUID, reporter cipher.PubKey, sent, recv telemetrywire.DirCheckpoints) error {
	now := time.Now().UTC()
	return checkpointScript.Run(ctx, s.client,
		[]string{s.checkpointKey(id), s.bandwidthDailyKey(id.String(), now)},
		reporter.Hex(), encodeCheckpoints(sent), encodeCheckpoints(recv),
		int64(checkpointRecordTTL/time.Second), s.bandwidthDailyTTLFor(),
	).Err()
}

// verifiedFields fills an edge's verified bytes from its day's bw:daily hash.
// sentField is the edge's "<pk>:sent" field, whose prefix the others share.
func verifiedFields(h map[string]string, sentField string, e *EdgeBandwidth) {
	pk := strings.TrimSuffix(sentField, ":sent")
	if v, ok := h[pk+":"+verifiedSentField]; ok {
		e.Verified = parseBWUint(v)
	}
	_, e.Checked = h[pk+":"+verifiedCheckField]
}
