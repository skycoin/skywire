// Package wasm pkg/router/policy/wasm/host.go — host-side
// function bindings. The WASM guest imports these from the
// "skywire" module; wazero provides them via
// HostModuleBuilder.NewFunctionBuilder + WithGoModuleFunction.
//
// ABI shape: every host function takes its arguments as
// (offset, length) tuples pointing into the guest's linear
// memory and returns its result the same way. The guest is
// responsible for allocating the input buffer via its own
// malloc-equivalent; the host reads the bytes, computes, and
// writes the result into a guest-allocated output buffer.
//
// Stdlib surface mirrors the Starlark-side modules:
//   - geo_country(pk_ptr, pk_len) → packed_ptr_len
//   - transports_latency(pk_ptr, pk_len) → ms
//   - transports_kind(pk_ptr, pk_len) → packed_ptr_len
//   - peers_is_trusted(pk_ptr, pk_len) → 0 or 1
//   - peers_is_hypervisor(pk_ptr, pk_len) → 0 or 1
//   - log_info(msg_ptr, msg_len)
//   - log_warn(msg_ptr, msg_len)
package wasm

import (
	"context"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"

	"github.com/skycoin/skywire/pkg/router/policy"
)

// hostEnv is the per-module-instance state the host functions
// close over. Provides access to the Provider (geo/transports/
// peers state) and a logger.
type hostEnv struct {
	provider policy.Provider
	logger   func(format string, args ...interface{})
	source   string
}

// readString reads a UTF-8 string from the guest's linear
// memory at offset for length bytes. Bounds-checked via the
// memory.Read API — wazero returns ok=false on out-of-range.
func readString(mem api.Memory, offset, length uint32) string {
	buf, ok := mem.Read(offset, length)
	if !ok {
		return ""
	}
	return string(buf)
}

// writeOutput allocates a guest-side buffer via the module's
// `alloc` export, writes data into it, and returns a packed
// (offset | length << 32) value the guest can decode.
func writeOutput(ctx context.Context, mod api.Module, data []byte) uint64 {
	if len(data) == 0 {
		return 0
	}
	alloc := mod.ExportedFunction("alloc")
	if alloc == nil {
		return 0
	}
	res, err := alloc.Call(ctx, uint64(len(data)))
	if err != nil || len(res) == 0 {
		return 0
	}
	offset := uint32(res[0])
	if ok := mod.Memory().Write(offset, data); !ok {
		return 0
	}
	return uint64(offset) | (uint64(len(data)) << 32)
}

// installHostFunctions registers the skywire stdlib host
// functions on the supplied HostModuleBuilder. Each function
// uses the "raw" GoModuleFunc signature
// (ctx, mod, stack []uint64) so we can return packed
// (ptr|len<<32) uint64 values without wazero rejecting the
// signature.
func installHostFunctions(_ context.Context, env *hostEnv, builder wazero.HostModuleBuilder) {
	twoU32 := []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}
	oneU64 := []api.ValueType{api.ValueTypeI64}

	builder.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, mod api.Module, stack []uint64) {
			pkOff := api.DecodeU32(stack[0])
			pkLen := api.DecodeU32(stack[1])
			pk := readString(mod.Memory(), pkOff, pkLen)
			cc := "??"
			if env.provider != nil {
				cc = env.provider.Geo(pk)
			}
			stack[0] = writeOutput(ctx, mod, []byte(cc))
		}), twoU32, oneU64).
		Export("geo_country")

	builder.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, mod api.Module, stack []uint64) {
			pkOff := api.DecodeU32(stack[0])
			pkLen := api.DecodeU32(stack[1])
			pk := readString(mod.Memory(), pkOff, pkLen)
			ms := int64(0)
			if env.provider != nil {
				ms = int64(env.provider.Latency(pk))
			}
			stack[0] = uint64(ms) //nolint:gosec
		}), twoU32, oneU64).
		Export("transports_latency")

	builder.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, mod api.Module, stack []uint64) {
			pkOff := api.DecodeU32(stack[0])
			pkLen := api.DecodeU32(stack[1])
			pk := readString(mod.Memory(), pkOff, pkLen)
			kind := ""
			if env.provider != nil {
				kind = env.provider.Kind(pk)
			}
			stack[0] = writeOutput(ctx, mod, []byte(kind))
		}), twoU32, oneU64).
		Export("transports_kind")

	builder.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, mod api.Module, stack []uint64) {
			pkOff := api.DecodeU32(stack[0])
			pkLen := api.DecodeU32(stack[1])
			pk := readString(mod.Memory(), pkOff, pkLen)
			b := uint64(0)
			if env.provider != nil && env.provider.IsTrusted(pk) {
				b = 1
			}
			stack[0] = b
		}), twoU32, oneU64).
		Export("peers_is_trusted")

	builder.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, mod api.Module, stack []uint64) {
			pkOff := api.DecodeU32(stack[0])
			pkLen := api.DecodeU32(stack[1])
			pk := readString(mod.Memory(), pkOff, pkLen)
			b := uint64(0)
			if env.provider != nil && env.provider.IsHypervisor(pk) {
				b = 1
			}
			stack[0] = b
		}), twoU32, oneU64).
		Export("peers_is_hypervisor")

	builder.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, mod api.Module, stack []uint64) {
			off := api.DecodeU32(stack[0])
			ln := api.DecodeU32(stack[1])
			msg := readString(mod.Memory(), off, ln)
			env.logger("policy %s: info: %s", env.source, msg)
		}), twoU32, nil).
		Export("log_info")

	builder.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, mod api.Module, stack []uint64) {
			off := api.DecodeU32(stack[0])
			ln := api.DecodeU32(stack[1])
			msg := readString(mod.Memory(), off, ln)
			env.logger("policy %s: warn: %s", env.source, msg)
		}), twoU32, nil).
		Export("log_warn")
}

// packPtrLen packs an (offset, length) tuple into a single
// uint64 the guest can decode (offset in the low 32 bits,
// length in the high 32 bits).
func packPtrLen(offset, length uint32) uint64 {
	return uint64(offset) | (uint64(length) << 32)
}

// unpackPtrLen reverses packPtrLen.
func unpackPtrLen(packed uint64) (offset, length uint32) {
	return uint32(packed & 0xFFFFFFFF), uint32(packed >> 32)
}

var _ = packPtrLen
