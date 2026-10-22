// Package store pkg/deployment/tpd/store/batch_scripts.go c4-net-discovery
//
// The batched forms of bandwidthScript and latencyScript. A batch of rows
// from one reporter on one day shares that reporter's visor totals and the
// day's network hash, so those writes are made once per batch with the
// summed deltas, not once per row. The per-transport writes are the same as
// the single-row scripts'.
package store

import "github.com/go-redis/redis/v8"

// maxScriptRows bounds the rows one batched script call carries, so one
// call does not hold redis for long.
const maxScriptRows = 256

// bandwidthBatchScript applies many transports' counters from one reporter
// on one day (see bandwidthScript for one row). It returns, per row, 1 when
// the row moved its transport's daily hash.
//
// KEYS: visorAll, visorDaily, netDaily, netSince, then prev and daily per row
// ARGV: reporter, now, prevTTL, histTTL, visorAllTTL, date, dailyTTL, then
// sent, recv and type per row
var bandwidthBatchScript = redis.NewScript(`
local function int(v) return string.format('%d', v) end
local n = (#KEYS - 4) / 2
local moved = {}
local vs, vr, grow = 0, 0, 0
local tgrow = {}
for i = 1, n do
  local kp, kd = KEYS[3 + 2 * i], KEYS[4 + 2 * i]
  local cs, cr = tonumber(ARGV[5 + 3 * i]), tonumber(ARGV[6 + 3 * i])
  local ty = ARGV[7 + 3 * i]
  local ds, dr = 0, 0
  local prev = redis.call('HMGET', kp, 'sent', 'recv')
  if prev[1] and prev[2] then
    ds, dr = cs, cr
    local ps, pr = tonumber(prev[1]), tonumber(prev[2])
    if cs >= ps then ds = cs - ps end
    if cr >= pr then dr = cr - pr end
  end
  redis.call('HSET', kp, 'sent', ARGV[5 + 3 * i], 'recv', ARGV[6 + 3 * i])
  redis.call('EXPIRE', kp, ARGV[3])
  moved[i] = 0
  local d = ds + dr
  if d > 0 then
    local rs = redis.call('HINCRBY', kd, ARGV[1] .. ':sent', int(ds))
    local rr = redis.call('HINCRBY', kd, ARGV[1] .. ':recv', int(dr))
    redis.call('HINCRBY', kd, 'bandwidth', int(d))
    redis.call('HSET', kd, 'updated_at', ARGV[2])
    local total = rs + rr
    local max = tonumber(redis.call('HGET', kd, 'max') or '0')
    if total > max then
      local g = total - max
      redis.call('HSET', kd, 'max', int(total))
      grow = grow + g
      tgrow[ty] = (tgrow[ty] or 0) + g
    end
    redis.call('EXPIRE', kd, ARGV[7])
    vs, vr = vs + ds, vr + dr
    moved[i] = 1
  end
end
if grow > 0 then
  redis.call('SETNX', KEYS[4], ARGV[6])
  redis.call('HINCRBY', KEYS[3], 'bandwidth', int(grow))
  for ty, g in pairs(tgrow) do
    redis.call('HINCRBY', KEYS[3], 'type:' .. ty, int(g))
  end
  redis.call('EXPIRE', KEYS[3], ARGV[4])
end
if vs + vr > 0 then
  redis.call('SADD', KEYS[1], ARGV[1])
  redis.call('EXPIRE', KEYS[1], ARGV[5])
  redis.call('HINCRBY', KEYS[2], 'sent', int(vs))
  redis.call('HINCRBY', KEYS[2], 'recv', int(vr))
  redis.call('HINCRBY', KEYS[2], 'bandwidth', int(vs + vr))
  redis.call('HSET', KEYS[2], 'updated_at', ARGV[2])
  redis.call('EXPIRE', KEYS[2], ARGV[4])
end
return moved
`)

// latencyBatchScript records many transports' latest average latency on
// one day (see latencyScript for one row).
//
// KEYS: netLatency, netDaily
// ARGV: histTTL, then id, average and type per row
var latencyBatchScript = redis.NewScript(`
local n = (#ARGV - 1) / 3
local count, sum = 0, 0
local tcount, tsum = {}, {}
for i = 1, n do
  local id, ty = ARGV[3 * i - 1], ARGV[3 * i + 1]
  local v = tonumber(ARGV[3 * i])
  local old = redis.call('HGET', KEYS[1], id)
  redis.call('HSET', KEYS[1], id, ARGV[3 * i])
  local dv = v
  if old then
    dv = v - tonumber(old)
  else
    count = count + 1
    tcount[ty] = (tcount[ty] or 0) + 1
  end
  sum = sum + dv
  tsum[ty] = (tsum[ty] or 0) + dv
end
redis.call('EXPIRE', KEYS[1], ARGV[1])
if count > 0 then
  redis.call('HINCRBY', KEYS[2], 'lat_count', count)
  for ty, c in pairs(tcount) do
    redis.call('HINCRBY', KEYS[2], 'type_lat_count:' .. ty, c)
  end
end
redis.call('HINCRBYFLOAT', KEYS[2], 'lat_sum', sum)
for ty, d in pairs(tsum) do
  redis.call('HINCRBYFLOAT', KEYS[2], 'type_lat_sum:' .. ty, d)
end
redis.call('EXPIRE', KEYS[2], ARGV[1])
return 1
`)
