// Package router pkg/router/fec_mux.go — wiring the systematic Cauchy-RS erasure
// core (fec.go, #4270) into the packet-mux striping / reorder data plane.
//
// The aggregation wall is head-of-line blocking: the no-skip reorder frontier
// (reorder.go) cannot advance past a sequence number striped onto a slow leg,
// so one ordered stream over heterogeneous-RTT legs is capped at the SLOW leg's
// rate — retransmit recovery is likewise bound to that leg's RTT. Erasure coding
// removes the wait: the sender groups K consecutive data frames into a block and
// emits R repair frames; if a data frame is late/lost on a slow leg but ANY K of
// the block's K+R symbols have arrived (repair frames ride the FAST legs), the
// receiver RECONSTRUCTS the missing frame and the frontier advances at the fast
// leg's rate instead of the slow one.
//
// Layering: FEC operates in the WIRE (post-seal) domain. On send the striper is
// fed the on-wire frame payload AFTER per-frame seal (route_mux.wrapPayload); on
// receive the reassembler is fed the on-wire payload BEFORE open
// (route_mux.deliverData), and a reconstructed on-wire frame is passed through
// the normal open() path before entering the reorder buffer. Because repair
// symbols are linear combinations of already-sealed frames they reveal nothing
// beyond the ciphertext, so no separate repair-frame nonce is needed and the code
// works uniformly whether per-frame noise is on or off. This file is
// transport-agnostic and driven entirely by (seq, wireBytes) and
// (blockID, idx, symbol) tuples, so it is exhaustively unit-testable without the
// live mesh (see fec_mux_test.go).
package router

import (
	"encoding/binary"
	"sync"
)

// FEC block geometry. K data frames + R repair frames per block; recovers all K
// from any K of the K+R. K=8/R=2 tolerates a full slow/dead leg's share of a
// 4–8 leg group (~1 frame in 8 striped onto it) at a 25% repair overhead. These
// are the defaults; the negotiated values could later be carried in the handshake.
const (
	fecDefaultK = 8
	fecDefaultR = 2
	// fecSymLen is the fixed erasure-symbol size. Every data payload is
	// length-prefixed and zero-padded to this; a payload larger than
	// fecSymLen-fecLenPrefix cannot be coded and is passed through un-FEC'd
	// (rare — mux frames are MTU-bounded well under this). 16 KiB covers the
	// largest mux frame with headroom.
	fecSymLen    = 16 * 1024
	fecLenPrefix = 2 // uint16 BE plaintext length inside each symbol
)

// fecRepairFrame is one repair symbol ready to be scheduled onto a leg. blockID
// and idx let the receiver place it into the right block slot (K+idx).
type fecRepairFrame struct {
	blockID uint32
	idx     uint8
	symbol  []byte // exactly fecSymLen bytes
}

// symbolize length-prefixes and zero-pads a plaintext payload to fecSymLen.
// Returns (symbol, true); (nil, false) if the payload is too large to code.
func symbolize(payload []byte, symLen int) ([]byte, bool) {
	if len(payload) > symLen-fecLenPrefix {
		return nil, false
	}
	sym := make([]byte, symLen)
	binary.BigEndian.PutUint16(sym, uint16(len(payload)))
	copy(sym[fecLenPrefix:], payload)
	return sym, true
}

// desymbolize recovers the original plaintext from a decoded symbol by reading
// its length prefix. Returns nil on a corrupt/over-long prefix.
func desymbolize(sym []byte) []byte {
	if len(sym) < fecLenPrefix {
		return nil
	}
	n := int(binary.BigEndian.Uint16(sym))
	if n > len(sym)-fecLenPrefix {
		return nil
	}
	out := make([]byte, n)
	copy(out, sym[fecLenPrefix:fecLenPrefix+n])
	return out
}

// --- sender: fecStriper ---

// fecStriper accumulates K consecutive data-frame plaintexts (keyed by their mux
// seq) into a block and, on block completion, emits R repair frames. It assumes
// the seqs it is fed are contiguous and monotonic (as writeSeq produces); block
// B owns seqs [B*K, B*K+K-1] with in-block index seq%K. Safe for concurrent Add.
type fecStriper struct {
	mu      sync.Mutex
	coder   *fecBlockCoder
	k, r    int
	symLen  int
	symbols [][]byte // K slots for the block currently filling
	block   uint32   // blockID currently filling
	filled  int      // count of non-nil slots in the current block
	inited  bool
}

func newFECStriper(k, r, symLen int) *fecStriper {
	coder := newFECBlockCoder(k, r, symLen)
	if coder == nil {
		return nil
	}
	return &fecStriper{
		coder:   coder,
		k:       k,
		r:       r,
		symLen:  symLen,
		symbols: make([][]byte, k),
	}
}

// Add feeds one data frame's plaintext (identified by its mux seq). When the
// frame completes a block it returns that block's R repair frames to schedule;
// otherwise it returns nil. A payload too large to symbolize is skipped for
// coding (the block will simply be short a data symbol; if the frame later needs
// reconstruction it cannot be — an accepted, rare degradation) — but to keep the
// MDS invariant we instead treat an un-codable frame as a zero symbol carrying a
// length prefix of 0 so the block still encodes; the receiver never needs to
// reconstruct a frame it actually received, and an un-received over-long frame
// is beyond FEC anyway.
func (s *fecStriper) Add(seq uint32, plaintext []byte) []fecRepairFrame {
	s.mu.Lock()
	defer s.mu.Unlock()

	blk := seq / uint32(s.k)
	idx := int(seq % uint32(s.k))

	if !s.inited {
		s.block = blk
		s.inited = true
	}
	// A seq jump (should not happen with contiguous writeSeq) resets to the new
	// block to avoid mixing symbols across blocks.
	if blk != s.block {
		s.reset(blk)
	}

	sym, ok := symbolize(plaintext, s.symLen)
	if !ok {
		// Over-long: stand in a zero-length symbol so the block still codes.
		sym, _ = symbolize(nil, s.symLen)
	}
	if s.symbols[idx] == nil {
		s.filled++
	}
	s.symbols[idx] = sym

	if s.filled < s.k {
		return nil
	}
	// Block complete → encode R repair symbols.
	repair, err := s.coder.Encode(s.symbols)
	if err != nil {
		s.reset(blk + 1)
		return nil
	}
	frames := make([]fecRepairFrame, s.r)
	for i := 0; i < s.r; i++ {
		frames[i] = fecRepairFrame{blockID: blk, idx: uint8(i), symbol: repair[i]}
	}
	s.reset(blk + 1)
	return frames
}

// reset clears the fill state for the next block. Caller holds s.mu.
func (s *fecStriper) reset(nextBlock uint32) {
	for i := range s.symbols {
		s.symbols[i] = nil
	}
	s.filled = 0
	s.block = nextBlock
}

// --- receiver: fecReassembler ---

// fecBlockState holds the symbols received for one block until it is fully
// delivered or evicted. slots[0..K-1] are data symbols, slots[K..K+R-1] repair.
type fecBlockState struct {
	slots   [][]byte // len K+R; nil = not yet arrived
	present []bool   // len K+R
	got     int      // count of present slots
	dataGot int      // count of present DATA slots (0..K-1)
}

// fecReassembler retains recently-received symbols per block so that when the
// reorder frontier stalls on a missing seq it can reconstruct it from ≥K of the
// block's K+R symbols. It keeps a bounded window of blocks (fecWindowBlocks);
// blocks older than the window are evicted (their data was either delivered or
// is permanently lost, past FEC's help). Safe for concurrent use.
type fecReassembler struct {
	mu     sync.Mutex
	coder  *fecBlockCoder
	k, r   int
	symLen int
	blocks map[uint32]*fecBlockState
	// lowBlock is the oldest block still retained; blocks below it are evicted.
	lowBlock uint32
	haveLow  bool
}

// fecWindowBlocks bounds retained blocks (memory = window*(K+R)*symLen). 64
// blocks * 10 * 16KiB ≈ 10 MiB worst case — ample for reorder depths in practice.
const fecWindowBlocks = 64

func newFECReassembler(k, r, symLen int) *fecReassembler {
	coder := newFECBlockCoder(k, r, symLen)
	if coder == nil {
		return nil
	}
	return &fecReassembler{
		coder:  coder,
		k:      k,
		r:      r,
		symLen: symLen,
		blocks: make(map[uint32]*fecBlockState),
	}
}

func (f *fecReassembler) blockFor(blk uint32) *fecBlockState {
	bs := f.blocks[blk]
	if bs == nil {
		bs = &fecBlockState{
			slots:   make([][]byte, f.k+f.r),
			present: make([]bool, f.k+f.r),
		}
		f.blocks[blk] = bs
		if !f.haveLow || blk < f.lowBlock {
			f.lowBlock = blk
			f.haveLow = true
		}
		f.evict()
	}
	return bs
}

// evict drops blocks older than the retention window. Caller holds f.mu.
func (f *fecReassembler) evict() {
	if len(f.blocks) <= fecWindowBlocks {
		return
	}
	// find current max block to anchor the window
	var maxBlk uint32
	for b := range f.blocks {
		if b > maxBlk {
			maxBlk = b
		}
	}
	if maxBlk < fecWindowBlocks {
		return
	}
	cutoff := maxBlk - fecWindowBlocks + 1
	for b := range f.blocks {
		if b < cutoff {
			delete(f.blocks, b)
		}
	}
}

// RecordData notes a received data frame's plaintext at its seq. It stores a
// symbolized copy so the block can be decoded later if a sibling is missing.
func (f *fecReassembler) RecordData(seq uint32, plaintext []byte) {
	sym, ok := symbolize(plaintext, f.symLen)
	if !ok {
		sym, _ = symbolize(nil, f.symLen)
	}
	blk := seq / uint32(f.k)
	idx := int(seq % uint32(f.k))
	f.mu.Lock()
	defer f.mu.Unlock()
	bs := f.blockFor(blk)
	if !bs.present[idx] {
		bs.present[idx] = true
		bs.got++
		bs.dataGot++
		bs.slots[idx] = sym
	}
}

// RecordRepair notes a received repair symbol for a block.
func (f *fecReassembler) RecordRepair(blk uint32, idx uint8, symbol []byte) {
	if int(idx) >= f.r || len(symbol) != f.symLen {
		return
	}
	slot := f.k + int(idx)
	f.mu.Lock()
	defer f.mu.Unlock()
	bs := f.blockFor(blk)
	if !bs.present[slot] {
		bs.present[slot] = true
		bs.got++
		cp := make([]byte, f.symLen)
		copy(cp, symbol)
		bs.slots[slot] = cp
	}
}

// Reconstruct attempts to recover the plaintext for a missing seq. It succeeds
// only when the seq's block already holds ≥K of its K+R symbols AND the seq's
// own data slot is not already present. Returns (plaintext, true) on success.
// The reconstructed data symbol is also recorded into the block so a subsequent
// sibling reconstruction in the same block reuses it.
func (f *fecReassembler) Reconstruct(seq uint32) ([]byte, bool) {
	blk := seq / uint32(f.k)
	idx := int(seq % uint32(f.k))
	f.mu.Lock()
	defer f.mu.Unlock()
	bs := f.blocks[blk]
	if bs == nil || bs.got < f.k || bs.present[idx] {
		return nil, false
	}
	// Build recv/present of exactly K+R slots for the coder.
	recv := make([][]byte, f.k+f.r)
	for i := 0; i < f.k+f.r; i++ {
		if bs.present[i] {
			recv[i] = bs.slots[i]
		} else {
			recv[i] = make([]byte, f.symLen) // ignored (present=false)
		}
	}
	data, err := f.coder.Decode(recv, bs.present)
	if err != nil {
		return nil, false
	}
	// Cache all reconstructed data slots so sibling gaps in this block are free.
	for j := 0; j < f.k; j++ {
		if !bs.present[j] {
			bs.present[j] = true
			bs.got++
			bs.dataGot++
			bs.slots[j] = data[j]
		}
	}
	return desymbolize(data[idx]), true
}

// --- routeMux integration (see route_mux.go fec* fields) ---

// fecInit constructs the striper + reassembler with the default block geometry.
// Called once at handshake when CapFEC is negotiated. On bad dims it disables
// FEC rather than leaving half-built state.
func (m *routeMux) fecInit() {
	m.fecStriper = newFECStriper(fecDefaultK, fecDefaultR, fecSymLen)
	m.fecReassembler = newFECReassembler(fecDefaultK, fecDefaultR, fecSymLen)
	if m.fecStriper == nil || m.fecReassembler == nil {
		m.fecStriper = nil
		m.fecReassembler = nil
		m.fecEnabled = false
	}
}

// fecOnSend feeds one on-wire (post-seal) data-frame payload to the striper and
// queues any repair frames the completed block produced. Called from wrapPayload.
func (m *routeMux) fecOnSend(seq uint32, wire []byte) {
	if !m.fecEnabled || m.fecStriper == nil {
		return
	}
	frames := m.fecStriper.Add(seq, wire)
	if len(frames) == 0 {
		return
	}
	m.fecRepairMu.Lock()
	m.fecRepairQ = append(m.fecRepairQ, frames...)
	m.fecRepairMu.Unlock()
}

// fecDrainRepairs returns and clears the queued repair frames for the send loop.
func (m *routeMux) fecDrainRepairs() []fecRepairFrame {
	if !m.fecEnabled {
		return nil
	}
	m.fecRepairMu.Lock()
	q := m.fecRepairQ
	m.fecRepairQ = nil
	m.fecRepairMu.Unlock()
	return q
}

// fecOnRecvData records an on-wire (pre-open) data-frame payload into the
// reassembler so a sibling in its block can be reconstructed later. Called from
// deliverData BEFORE open.
func (m *routeMux) fecOnRecvData(seq uint32, wire []byte) {
	if !m.fecEnabled || m.fecReassembler == nil {
		return
	}
	m.fecReassembler.RecordData(seq, wire)
}

// fecOnRecvRepair records a repair symbol and returns any plaintext frames the
// reorder frontier can now deliver in order (reconstructed + opened). Called from
// the RepairPacket handler.
func (m *routeMux) fecOnRecvRepair(blockID uint32, idx uint8, symbol []byte) [][]byte {
	if !m.fecEnabled || m.fecReassembler == nil {
		return nil
	}
	m.fecReassembler.RecordRepair(blockID, idx, symbol)
	return m.fecTryAdvance()
}

// fecTryAdvance reconstructs consecutive gap-blocked frontier frames from the
// reassembler and inserts them (after open) so the reorder frontier advances
// without waiting for the slow leg. Returns the plaintext frames newly delivered
// in order, to be handed to the app exactly like deliverData's return. A
// reconstructed frame that fails to open is not delivered (a real frame or a
// retransmit will fill the gap). Double-reconstruction across concurrent callers
// is harmless: Insert of an already-delivered seq is a discarded no-op.
func (m *routeMux) fecTryAdvance() [][]byte {
	if !m.fecEnabled || m.fecReassembler == nil || m.reorderBuf == nil {
		return nil
	}
	var out [][]byte
	for {
		next := m.reorderBuf.NextSeq()
		wire, ok := m.fecReassembler.Reconstruct(next)
		if !ok {
			break
		}
		data := wire
		if m.open != nil {
			pt, err := m.open(next, wire)
			if err != nil {
				if m.logger != nil {
					m.logger.WithError(err).Tracef("FEC-reconstructed seq %d failed open; not delivering", next)
				}
				break
			}
			data = pt
		}
		delivered := m.reorderBuf.Insert(next, data)
		if len(delivered) == 0 {
			break
		}
		out = append(out, delivered...)
		if m.sackEnabled && m.sackTracker != nil {
			m.sackTracker.AdvanceContiguous(m.reorderBuf.NextSeq())
		}
	}
	return out
}
