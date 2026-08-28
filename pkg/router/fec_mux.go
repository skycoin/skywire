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
	"sync/atomic"
)

// FEC block geometry. K data frames + R repair frames per block; recovers all K
// from any K of the K+R. K=8/R=2 tolerates a full slow/dead leg's share of a
// 4–8 leg group (~1 frame in 8 striped onto it) at a 25% repair overhead. These
// are the defaults; the negotiated values could later be carried in the handshake.
const (
	fecDefaultK  = 8
	fecDefaultR  = 2
	fecLenPrefix = 2 // uint16 BE frame length inside each symbol
	// fecMaxSymLen caps a block's ADAPTIVE symbol length. A routing DataPacket
	// payload is at most ~64 KiB, so this covers any on-wire mux frame; a frame
	// that somehow exceeded it is excluded from coding (stored empty) rather than
	// silently corrupting the block. Symbol length is NOT fixed: each block sizes
	// its symbols to max(frame len)+fecLenPrefix, so repair overhead stays at the
	// design R/K ratio for any frame size — a fixed pad would bloat small-frame
	// repair and, worse, corrupt frames larger than the pad (they would code as a
	// zero symbol and reconstruct to an empty frame).
	fecMaxSymLen = 65535
)

// fecRepairFrame is one repair symbol ready to be scheduled onto a leg. blockID
// and idx place it in the block slot (K+idx); symLen is the block's adaptive
// symbol length, which the receiver needs to size the block for decoding.
type fecRepairFrame struct {
	blockID uint32
	idx     uint8
	symLen  int
	symbol  []byte // exactly symLen bytes
}

// symbolize length-prefixes and zero-pads a frame to symLen.
// Returns (symbol, true); (nil, false) if the payload is too large to code.
func symbolize(payload []byte, symLen int) ([]byte, bool) {
	if len(payload) > symLen-fecLenPrefix {
		return nil, false
	}
	sym := make([]byte, symLen)
	binary.BigEndian.PutUint16(sym, uint16(len(payload))) //nolint:gosec
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
	mu     sync.Mutex
	k, r   int
	frames [][]byte // K slots of RAW frame bytes (copied); nil = empty
	maxLen int      // longest raw frame in the block currently filling
	block  uint32   // blockID currently filling
	filled int      // count of non-nil slots in the current block
	inited bool
}

func newFECStriper(k, r int) *fecStriper {
	if newFECBlockCoder(k, r, 1) == nil { // validate dims (symLen picked per block)
		return nil
	}
	return &fecStriper{k: k, r: r, frames: make([][]byte, k)}
}

// Add feeds one on-wire data frame (identified by its mux seq). When the frame
// completes a block it sizes the block's erasure symbols to the LONGEST frame in
// the block (+ length prefix), encodes, and returns the R repair frames to
// schedule; otherwise nil. Frames are stored raw and symbolized only at block
// completion so the symbol length is exactly what the block needs — never a fixed
// pad. A frame beyond fecMaxSymLen (never happens on the wire) is stored empty.
func (s *fecStriper) Add(seq uint32, frame []byte) []fecRepairFrame {
	s.mu.Lock()
	defer s.mu.Unlock()

	blk := seq / uint32(s.k)      //nolint:gosec
	idx := int(seq % uint32(s.k)) //nolint:gosec

	if !s.inited {
		s.block = blk
		s.inited = true
	}
	// A seq jump (should not happen with contiguous writeSeq) resets to the new
	// block to avoid mixing frames across blocks.
	if blk != s.block {
		s.reset(blk)
	}

	if len(frame) > fecMaxSymLen-fecLenPrefix {
		frame = nil // defensive: un-codable, stand in an empty frame
	}
	cp := make([]byte, len(frame))
	copy(cp, frame)
	if s.frames[idx] == nil {
		s.filled++
	}
	s.frames[idx] = cp
	if len(cp) > s.maxLen {
		s.maxLen = len(cp)
	}

	if s.filled < s.k {
		return nil
	}
	// Block complete → adaptive symbol length = longest frame + prefix.
	symLen := s.maxLen + fecLenPrefix
	syms := make([][]byte, s.k)
	for i, f := range s.frames {
		sym, _ := symbolize(f, symLen) // fits: len(f) <= maxLen == symLen-prefix
		syms[i] = sym
	}
	coder := newFECBlockCoder(s.k, s.r, symLen)
	repair, err := coder.Encode(syms)
	if err != nil {
		s.reset(blk + 1)
		return nil
	}
	frames := make([]fecRepairFrame, s.r)
	for i := 0; i < s.r; i++ {
		frames[i] = fecRepairFrame{blockID: blk, idx: uint8(i), symLen: symLen, symbol: repair[i]}
	}
	s.reset(blk + 1)
	return frames
}

// reset clears the fill state for the next block. Caller holds s.mu.
func (s *fecStriper) reset(nextBlock uint32) {
	for i := range s.frames {
		s.frames[i] = nil
	}
	s.filled = 0
	s.maxLen = 0
	s.block = nextBlock
}

// --- receiver: fecReassembler ---

// fecBlockState holds the RAW frames + repair symbols received for one block
// until it is fully delivered or evicted. Data frames are stored at their natural
// size and symbolized to the block's adaptive symLen only at decode time; symLen
// is learned from the first repair frame (all repairs for a block carry it).
type fecBlockState struct {
	rawData       [][]byte // K slots of raw received frame bytes; nil = absent
	repair        [][]byte // R slots of repair symbols (symLen bytes); nil = absent
	dataPresent   []bool   // K
	repairPresent []bool   // R
	got           int      // count of present data + present repair slots
	symLen        int      // block's adaptive symbol length (0 until a repair arrives)
}

// fecReassembler retains recently-received frames per block so that when the
// reorder frontier stalls on a missing seq it can reconstruct it from ≥K of the
// block's K+R symbols. It keeps a bounded window of blocks (fecWindowBlocks);
// blocks older than the window are evicted (their data was either delivered or
// is permanently lost, past FEC's help). Safe for concurrent use.
type fecReassembler struct {
	mu     sync.Mutex
	k, r   int
	blocks map[uint32]*fecBlockState
	// lowBlock is the oldest block still retained; blocks below it are evicted.
	lowBlock uint32
	haveLow  bool
}

// fecWindowBlocks bounds retained blocks. 64 blocks * (K+R) * up-to-64KiB is the
// worst case — ample for reorder depths in practice.
const fecWindowBlocks = 64

func newFECReassembler(k, r int) *fecReassembler {
	if newFECBlockCoder(k, r, 1) == nil { // validate dims (symLen picked per block)
		return nil
	}
	return &fecReassembler{
		k:      k,
		r:      r,
		blocks: make(map[uint32]*fecBlockState),
	}
}

func (f *fecReassembler) blockFor(blk uint32) *fecBlockState {
	bs := f.blocks[blk]
	if bs == nil {
		bs = &fecBlockState{
			rawData:       make([][]byte, f.k),
			repair:        make([][]byte, f.r),
			dataPresent:   make([]bool, f.k),
			repairPresent: make([]bool, f.r),
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

// RecordData stores a received data frame's raw bytes at its seq (copied). It is
// symbolized to the block's adaptive symLen only at decode time, so the receiver
// need not know symLen until a repair frame arrives.
func (f *fecReassembler) RecordData(seq uint32, frame []byte) {
	blk := seq / uint32(f.k)      //nolint:gosec
	idx := int(seq % uint32(f.k)) //nolint:gosec
	f.mu.Lock()
	defer f.mu.Unlock()
	bs := f.blockFor(blk)
	if !bs.dataPresent[idx] {
		bs.dataPresent[idx] = true
		bs.got++
		cp := make([]byte, len(frame))
		copy(cp, frame)
		bs.rawData[idx] = cp
	}
}

// RecordRepair notes a received repair symbol (and the block's adaptive symLen,
// which every repair for the block carries) for later reconstruction.
func (f *fecReassembler) RecordRepair(blk uint32, idx uint8, symLen int, symbol []byte) {
	if int(idx) >= f.r || symLen <= fecLenPrefix || len(symbol) != symLen {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	bs := f.blockFor(blk)
	bs.symLen = symLen
	if !bs.repairPresent[idx] {
		bs.repairPresent[idx] = true
		bs.got++
		cp := make([]byte, symLen)
		copy(cp, symbol)
		bs.repair[idx] = cp
	}
}

// Reconstruct attempts to recover the on-wire bytes for a missing seq. It
// succeeds only when the seq's block holds ≥K of its K+R symbols, a repair has
// set the block's symLen, and the seq's own data slot is absent. Returns
// (frame, true) on success. It is idempotent and side-effect-free on the block
// state (symbolizes/decodes from COPIES), so each missing frontier frame in a
// multi-erasure block reconstructs correctly and independently.
func (f *fecReassembler) Reconstruct(seq uint32) ([]byte, bool) {
	blk := seq / uint32(f.k)      //nolint:gosec
	idx := int(seq % uint32(f.k)) //nolint:gosec
	f.mu.Lock()
	defer f.mu.Unlock()
	bs := f.blocks[blk]
	if bs == nil || bs.symLen == 0 || bs.got < f.k || bs.dataPresent[idx] {
		return nil, false
	}
	symLen := bs.symLen
	// Build recv/present of exactly K+R symLen-sized symbols. Data frames are
	// symbolized (length-prefixed + padded) to symLen here; repair symbols are
	// copied. COPIES are essential: Decode/gfSolve solves in place and MUTATES the
	// rhs vectors, which would corrupt the block's retained symbols and make a
	// subsequent sibling reconstruction decode from garbage (the multi-erasure
	// corruption the end-to-end test exposed).
	recv := make([][]byte, f.k+f.r)
	present := make([]bool, f.k+f.r)
	for i := 0; i < f.k; i++ {
		if bs.dataPresent[i] {
			sym, ok := symbolize(bs.rawData[i], symLen)
			if !ok { // a stored frame longer than this block's symLen: inconsistent
				return nil, false
			}
			recv[i] = sym
			present[i] = true
		} else {
			recv[i] = make([]byte, symLen)
		}
	}
	for i := 0; i < f.r; i++ {
		if bs.repairPresent[i] {
			cp := make([]byte, symLen)
			copy(cp, bs.repair[i])
			recv[f.k+i] = cp
			present[f.k+i] = true
		} else {
			recv[f.k+i] = make([]byte, symLen)
		}
	}
	coder := newFECBlockCoder(f.k, f.r, symLen)
	if coder == nil {
		return nil, false
	}
	data, err := coder.Decode(recv, present)
	if err != nil {
		return nil, false
	}
	// Do NOT cache decoded siblings back as present — that would strand an
	// uncached sibling (a later Reconstruct short-circuits on present) and leave
	// the frontier stalled. Each missing frontier frame decodes independently.
	return desymbolize(data[idx]), true
}

// --- routeMux integration (see route_mux.go fec* fields) ---

// fecInit constructs the striper + reassembler with the default block geometry.
// Called once at handshake when CapFEC is negotiated. On bad dims it disables
// FEC rather than leaving half-built state.
func (m *routeMux) fecInit() {
	m.fecStriper = newFECStriper(fecDefaultK, fecDefaultR)
	m.fecReassembler = newFECReassembler(fecDefaultK, fecDefaultR)
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
	// FEC only helps a MULTI-leg group — repair rescues a straggler striped onto a
	// slow leg. On a single active leg there is no head-of-line wall to remove, so
	// coding would be pure ~R/K bandwidth overhead for zero benefit (the common
	// mux=1 proxy). Skip it; when a second leg is added mid-stream the striper
	// resumes on the next frame and self-realigns on the block boundary.
	if m.activeLegCount() < 2 {
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
func (m *routeMux) fecOnRecvRepair(blockID uint32, idx uint8, symLen int, symbol []byte) [][]byte {
	if !m.fecEnabled || m.fecReassembler == nil {
		return nil
	}
	m.fecReassembler.RecordRepair(blockID, idx, symLen, symbol)
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
		atomic.AddUint64(&m.fecReconstructs, 1)
		out = append(out, delivered...)
		if m.sackEnabled && m.sackTracker != nil {
			m.sackTracker.AdvanceContiguous(m.reorderBuf.NextSeq())
		}
	}
	return out
}
