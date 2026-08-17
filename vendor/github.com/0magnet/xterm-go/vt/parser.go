package vt

// Parser states of the escape sequence parser (VT500-compatible).
const (
	stateGround             = 0
	stateEscape             = 1
	stateEscapeIntermediate = 2
	stateCsiEntry           = 3
	stateCsiParam           = 4
	stateCsiIntermediate    = 5
	stateCsiIgnore          = 6
	stateSosPmApcString     = 7
	stateOscString          = 8
	stateDcsEntry           = 9
	stateDcsParam           = 10
	stateDcsIgnore          = 11
	stateDcsIntermediate    = 12
	stateDcsPassthrough     = 13
)

// Parser actions.
const (
	actionIgnore      = 0
	actionError       = 1
	actionPrint       = 2
	actionExecute     = 3
	actionOscStart    = 4
	actionOscPut      = 5
	actionOscEnd      = 6
	actionCsiDispatch = 7
	actionParam       = 8
	actionCollect     = 9
	actionEscDispatch = 10
	actionClear       = 11
	actionDcsHook     = 12
	actionDcsPut      = 13
	actionDcsUnhook   = 14
)

// payloadLimit caps OSC and DCS payloads.
const payloadLimit = 10000000

// Table access layout:
//
//	index: currentState << indexStateShift | charCode
//	value: action << transitionActionShift | nextState
const (
	transitionActionShift = 4
	transitionStateMask   = 15
	indexStateShift       = 8
)

// nonASCIIPrintable is the pseudo-character placeholder for printable
// non-ascii characters (unicode).
const nonASCIIPrintable = 0xA0

// transitionTable is the VT500 transition table
// (https://vt100.net/emu/dec_ansi_parser).
type transitionTable struct {
	table []uint8
}

func newTransitionTable(length int) transitionTable {
	return transitionTable{table: make([]uint8, length)}
}

func (t transitionTable) setDefault(action, next uint8) {
	for i := range t.table {
		t.table[i] = action<<transitionActionShift | next
	}
}

func (t transitionTable) add(code int, state, action, next uint8) {
	t.table[int(state)<<indexStateShift|code] = action<<transitionActionShift | next
}

func (t transitionTable) addMany(codes []int, state, action, next uint8) {
	for _, code := range codes {
		t.add(code, state, action, next)
	}
}

func codeRange(start, end int) []int {
	r := make([]int, 0, end-start)
	for i := start; i < end; i++ {
		r = append(r, i)
	}
	return r
}

// vt500TransitionTable builds the VT500-compatible transition table,
// mirroring VT500_TRANSITION_TABLE of the original.
func vt500TransitionTable() transitionTable {
	table := newTransitionTable(4095)

	printables := codeRange(0x20, 0x7f) // 0x20 (SP) included, 0x7F (DEL) excluded
	executables := codeRange(0x00, 0x18)
	executables = append(executables, 0x19)
	executables = append(executables, codeRange(0x1c, 0x20)...)

	// set default transition
	table.setDefault(actionError, stateGround)
	// printables
	table.addMany(printables, stateGround, actionPrint, stateGround)
	// global anywhere rules
	for state := uint8(stateGround); state <= stateDcsPassthrough; state++ {
		table.addMany([]int{0x18, 0x1a, 0x99, 0x9a}, state, actionExecute, stateGround)
		table.addMany(codeRange(0x80, 0x90), state, actionExecute, stateGround)
		table.addMany(codeRange(0x90, 0x98), state, actionExecute, stateGround)
		table.add(0x9c, state, actionIgnore, stateGround)      // ST as terminator
		table.add(0x1b, state, actionClear, stateEscape)       // ESC
		table.add(0x9d, state, actionOscStart, stateOscString) // OSC
		table.addMany([]int{0x98, 0x9e, 0x9f}, state, actionIgnore, stateSosPmApcString)
		table.add(0x9b, state, actionClear, stateCsiEntry) // CSI
		table.add(0x90, state, actionClear, stateDcsEntry) // DCS
	}
	// rules for executables and 7f
	table.addMany(executables, stateGround, actionExecute, stateGround)
	table.addMany(executables, stateEscape, actionExecute, stateEscape)
	table.add(0x7f, stateEscape, actionIgnore, stateEscape)
	table.addMany(executables, stateOscString, actionIgnore, stateOscString)
	table.addMany(executables, stateCsiEntry, actionExecute, stateCsiEntry)
	table.add(0x7f, stateCsiEntry, actionIgnore, stateCsiEntry)
	table.addMany(executables, stateCsiParam, actionExecute, stateCsiParam)
	table.add(0x7f, stateCsiParam, actionIgnore, stateCsiParam)
	table.addMany(executables, stateCsiIgnore, actionExecute, stateCsiIgnore)
	table.addMany(executables, stateCsiIntermediate, actionExecute, stateCsiIntermediate)
	table.add(0x7f, stateCsiIntermediate, actionIgnore, stateCsiIntermediate)
	table.addMany(executables, stateEscapeIntermediate, actionExecute, stateEscapeIntermediate)
	table.add(0x7f, stateEscapeIntermediate, actionIgnore, stateEscapeIntermediate)
	// osc
	table.add(0x5d, stateEscape, actionOscStart, stateOscString)
	table.addMany(printables, stateOscString, actionOscPut, stateOscString)
	table.add(0x7f, stateOscString, actionOscPut, stateOscString)
	table.addMany([]int{0x9c, 0x1b, 0x18, 0x1a, 0x07}, stateOscString, actionOscEnd, stateGround)
	table.addMany(codeRange(0x1c, 0x20), stateOscString, actionIgnore, stateOscString)
	// sos/pm/apc does nothing
	table.addMany([]int{0x58, 0x5e, 0x5f}, stateEscape, actionIgnore, stateSosPmApcString)
	table.addMany(printables, stateSosPmApcString, actionIgnore, stateSosPmApcString)
	table.addMany(executables, stateSosPmApcString, actionIgnore, stateSosPmApcString)
	table.add(0x9c, stateSosPmApcString, actionIgnore, stateGround)
	table.add(0x7f, stateSosPmApcString, actionIgnore, stateSosPmApcString)
	// csi entries
	table.add(0x5b, stateEscape, actionClear, stateCsiEntry)
	table.addMany(codeRange(0x40, 0x7f), stateCsiEntry, actionCsiDispatch, stateGround)
	table.addMany(codeRange(0x30, 0x3c), stateCsiEntry, actionParam, stateCsiParam)
	table.addMany([]int{0x3c, 0x3d, 0x3e, 0x3f}, stateCsiEntry, actionCollect, stateCsiParam)
	table.addMany(codeRange(0x30, 0x3c), stateCsiParam, actionParam, stateCsiParam)
	table.addMany(codeRange(0x40, 0x7f), stateCsiParam, actionCsiDispatch, stateGround)
	table.addMany([]int{0x3c, 0x3d, 0x3e, 0x3f}, stateCsiParam, actionIgnore, stateCsiIgnore)
	table.addMany(codeRange(0x20, 0x40), stateCsiIgnore, actionIgnore, stateCsiIgnore)
	table.add(0x7f, stateCsiIgnore, actionIgnore, stateCsiIgnore)
	table.addMany(codeRange(0x40, 0x7f), stateCsiIgnore, actionIgnore, stateGround)
	table.addMany(codeRange(0x20, 0x30), stateCsiEntry, actionCollect, stateCsiIntermediate)
	table.addMany(codeRange(0x20, 0x30), stateCsiIntermediate, actionCollect, stateCsiIntermediate)
	table.addMany(codeRange(0x30, 0x40), stateCsiIntermediate, actionIgnore, stateCsiIgnore)
	table.addMany(codeRange(0x40, 0x7f), stateCsiIntermediate, actionCsiDispatch, stateGround)
	table.addMany(codeRange(0x20, 0x30), stateCsiParam, actionCollect, stateCsiIntermediate)
	// esc_intermediate
	table.addMany(codeRange(0x20, 0x30), stateEscape, actionCollect, stateEscapeIntermediate)
	table.addMany(codeRange(0x20, 0x30), stateEscapeIntermediate, actionCollect, stateEscapeIntermediate)
	table.addMany(codeRange(0x30, 0x7f), stateEscapeIntermediate, actionEscDispatch, stateGround)
	table.addMany(codeRange(0x30, 0x50), stateEscape, actionEscDispatch, stateGround)
	table.addMany(codeRange(0x51, 0x58), stateEscape, actionEscDispatch, stateGround)
	table.addMany([]int{0x59, 0x5a, 0x5c}, stateEscape, actionEscDispatch, stateGround)
	table.addMany(codeRange(0x60, 0x7f), stateEscape, actionEscDispatch, stateGround)
	// dcs entry
	table.add(0x50, stateEscape, actionClear, stateDcsEntry)
	table.addMany(executables, stateDcsEntry, actionIgnore, stateDcsEntry)
	table.add(0x7f, stateDcsEntry, actionIgnore, stateDcsEntry)
	table.addMany(codeRange(0x1c, 0x20), stateDcsEntry, actionIgnore, stateDcsEntry)
	table.addMany(codeRange(0x20, 0x30), stateDcsEntry, actionCollect, stateDcsIntermediate)
	table.addMany(codeRange(0x30, 0x3c), stateDcsEntry, actionParam, stateDcsParam)
	table.addMany([]int{0x3c, 0x3d, 0x3e, 0x3f}, stateDcsEntry, actionCollect, stateDcsParam)
	table.addMany(executables, stateDcsIgnore, actionIgnore, stateDcsIgnore)
	table.addMany(codeRange(0x20, 0x80), stateDcsIgnore, actionIgnore, stateDcsIgnore)
	table.addMany(codeRange(0x1c, 0x20), stateDcsIgnore, actionIgnore, stateDcsIgnore)
	table.addMany(executables, stateDcsParam, actionIgnore, stateDcsParam)
	table.add(0x7f, stateDcsParam, actionIgnore, stateDcsParam)
	table.addMany(codeRange(0x1c, 0x20), stateDcsParam, actionIgnore, stateDcsParam)
	table.addMany(codeRange(0x30, 0x3c), stateDcsParam, actionParam, stateDcsParam)
	table.addMany([]int{0x3c, 0x3d, 0x3e, 0x3f}, stateDcsParam, actionIgnore, stateDcsIgnore)
	table.addMany(codeRange(0x20, 0x30), stateDcsParam, actionCollect, stateDcsIntermediate)
	table.addMany(executables, stateDcsIntermediate, actionIgnore, stateDcsIntermediate)
	table.add(0x7f, stateDcsIntermediate, actionIgnore, stateDcsIntermediate)
	table.addMany(codeRange(0x1c, 0x20), stateDcsIntermediate, actionIgnore, stateDcsIntermediate)
	table.addMany(codeRange(0x20, 0x30), stateDcsIntermediate, actionCollect, stateDcsIntermediate)
	table.addMany(codeRange(0x30, 0x40), stateDcsIntermediate, actionIgnore, stateDcsIgnore)
	table.addMany(codeRange(0x40, 0x7f), stateDcsIntermediate, actionDcsHook, stateDcsPassthrough)
	table.addMany(codeRange(0x40, 0x7f), stateDcsParam, actionDcsHook, stateDcsPassthrough)
	table.addMany(codeRange(0x40, 0x7f), stateDcsEntry, actionDcsHook, stateDcsPassthrough)
	table.addMany(executables, stateDcsPassthrough, actionDcsPut, stateDcsPassthrough)
	table.addMany(printables, stateDcsPassthrough, actionDcsPut, stateDcsPassthrough)
	table.add(0x7f, stateDcsPassthrough, actionIgnore, stateDcsPassthrough)
	table.addMany([]int{0x1b, 0x9c, 0x18, 0x1a}, stateDcsPassthrough, actionDcsUnhook, stateGround)
	// special handling of unicode chars
	table.add(nonASCIIPrintable, stateGround, actionPrint, stateGround)
	table.add(nonASCIIPrintable, stateOscString, actionOscPut, stateOscString)
	table.add(nonASCIIPrintable, stateCsiIgnore, actionIgnore, stateCsiIgnore)
	table.add(nonASCIIPrintable, stateDcsIgnore, actionIgnore, stateDcsIgnore)
	table.add(nonASCIIPrintable, stateDcsPassthrough, actionDcsPut, stateDcsPassthrough)
	return table
}

var vt500Table = vt500TransitionTable()

// FunctionID identifies a CSI/ESC/DCS function: an optional prefix byte
// (0x3c..0x3f), up to two intermediates (0x20..0x2f) and the final byte.
type FunctionID struct {
	Prefix        string
	Intermediates string
	Final         string
}

// handler types
type (
	// PrintHandler receives printable codepoints data[start:end].
	PrintHandler func(data []uint32, start, end int)
	// ExecuteHandler is invoked for C0/C1 control codes. Returns handled.
	ExecuteHandler func() bool
	// CsiHandler is invoked for CSI sequences. Returns handled (true stops
	// bubbling to older handlers).
	CsiHandler func(params *Params) bool
	// EscHandler is invoked for ESC sequences.
	EscHandler func() bool
)

// DcsHandler handles DCS sequences.
type DcsHandler interface {
	Hook(params *Params)
	Put(data []uint32, start, end int)
	Unhook(success bool) bool
}

// OscHandlerIface handles OSC commands.
type OscHandlerIface interface {
	Start()
	Put(data []uint32, start, end int)
	End(success bool) bool
}

// ParsingState is handed to the error handler.
type ParsingState struct {
	Position     int
	Code         uint32
	CurrentState int
	Collect      int
	Params       *Params
	Abort        bool
}

// Parser is the port of EscapeSequenceParser: an ANSI/DEC compatible
// parser as described by Paul Williams (https://vt100.net/emu/dec_ansi_parser),
// operating in Zero Default Mode with sub parameter support.
//
// Unlike the original, handlers are synchronous only.
type Parser struct {
	initialState int
	currentState int
	// PrecedingJoinState carries the unicode join state across print calls.
	PrecedingJoinState int

	params  *Params
	collect int

	printHandler    PrintHandler
	executeHandlers map[byte]ExecuteHandler
	csiHandlers     map[int][]CsiHandler
	escHandlers     map[int][]EscHandler
	oscParser       *OscParser
	dcsParser       *DcsParser
	errorHandler    func(state ParsingState) ParsingState

	printHandlerFb   PrintHandler
	executeHandlerFb func(code uint32)
	csiHandlerFb     func(ident int, params *Params)
	escHandlerFb     func(ident int)
}

// NewParser creates a parser with the VT500 transition table.
func NewParser() *Parser {
	p := &Parser{
		initialState:    stateGround,
		currentState:    stateGround,
		params:          NewParams(),
		executeHandlers: map[byte]ExecuteHandler{},
		csiHandlers:     map[int][]CsiHandler{},
		escHandlers:     map[int][]EscHandler{},
		oscParser:       NewOscParser(),
		dcsParser:       NewDcsParser(),
	}
	p.params.AddParam(0) // ZDM
	p.printHandlerFb = func(data []uint32, start, end int) {}
	p.executeHandlerFb = func(code uint32) {}
	p.csiHandlerFb = func(ident int, params *Params) {}
	p.escHandlerFb = func(ident int) {}
	p.printHandler = p.printHandlerFb
	p.errorHandler = func(state ParsingState) ParsingState { return state }

	// swallow 7bit ST (ESC+\)
	p.RegisterEscHandler(FunctionID{Final: "\\"}, func() bool { return true })
	return p
}

func (p *Parser) identifier(id FunctionID, finalRange [2]int) int {
	res := 0
	if id.Prefix != "" {
		if len(id.Prefix) > 1 {
			panic("only one byte as prefix supported")
		}
		res = int(id.Prefix[0])
		if res != 0 && (res < 0x3c || res > 0x3f) {
			panic("prefix must be in range 0x3c .. 0x3f")
		}
	}
	if id.Intermediates != "" {
		if len(id.Intermediates) > 2 {
			panic("only two bytes as intermediates are supported")
		}
		for i := 0; i < len(id.Intermediates); i++ {
			intermediate := int(id.Intermediates[i])
			if intermediate < 0x20 || intermediate > 0x2f {
				panic("intermediate must be in range 0x20 .. 0x2f")
			}
			res <<= 8
			res |= intermediate
		}
	}
	if len(id.Final) != 1 {
		panic("final must be a single byte")
	}
	finalCode := int(id.Final[0])
	if finalCode < finalRange[0] || finalCode > finalRange[1] {
		panic("final out of range")
	}
	res <<= 8
	res |= finalCode
	return res
}

// IdentToString renders a packed identifier back to its byte string.
func IdentToString(ident int) string {
	var res []byte
	for ident != 0 {
		res = append([]byte{byte(ident & 0xFF)}, res...)
		ident >>= 8
	}
	return string(res)
}

// SetPrintHandler installs the print handler.
func (p *Parser) SetPrintHandler(handler PrintHandler) { p.printHandler = handler }

// RegisterEscHandler adds an ESC sequence handler.
func (p *Parser) RegisterEscHandler(id FunctionID, handler EscHandler) {
	ident := p.identifier(id, [2]int{0x30, 0x7e})
	p.escHandlers[ident] = append(p.escHandlers[ident], handler)
}

// SetExecuteHandler installs a handler for a C0/C1 control code.
func (p *Parser) SetExecuteHandler(flag byte, handler ExecuteHandler) {
	p.executeHandlers[flag] = handler
}

// RegisterCsiHandler adds a CSI sequence handler.
func (p *Parser) RegisterCsiHandler(id FunctionID, handler CsiHandler) {
	ident := p.identifier(id, [2]int{0x40, 0x7e})
	p.csiHandlers[ident] = append(p.csiHandlers[ident], handler)
}

// RegisterDcsHandler adds a DCS sequence handler.
func (p *Parser) RegisterDcsHandler(id FunctionID, handler DcsHandler) {
	p.dcsParser.RegisterHandler(p.identifier(id, [2]int{0x40, 0x7e}), handler)
}

// RegisterOscHandler adds an OSC command handler.
func (p *Parser) RegisterOscHandler(ident int, handler OscHandlerIface) {
	p.oscParser.RegisterHandler(ident, handler)
}

// SetCsiHandlerFallback installs the CSI fallback.
func (p *Parser) SetCsiHandlerFallback(fn func(ident int, params *Params)) { p.csiHandlerFb = fn }

// SetEscHandlerFallback installs the ESC fallback.
func (p *Parser) SetEscHandlerFallback(fn func(ident int)) { p.escHandlerFb = fn }

// SetExecuteHandlerFallback installs the execute fallback.
func (p *Parser) SetExecuteHandlerFallback(fn func(code uint32)) { p.executeHandlerFb = fn }

// Reset returns the parser to its initial state.
func (p *Parser) Reset() {
	p.currentState = p.initialState
	p.oscParser.Reset()
	p.dcsParser.Reset()
	p.params.Reset()
	p.params.AddParam(0) // ZDM
	p.collect = 0
	p.PrecedingJoinState = 0
}

// Parse processes UTF32 codepoints in data[:length].
func (p *Parser) Parse(data []uint32, length int) {
	var code uint32
	var transition uint8

	for i := 0; i < length; i++ {
		code = data[i]

		// normal transition & action lookup
		lookup := code
		if lookup >= nonASCIIPrintable {
			lookup = nonASCIIPrintable
		}
		transition = vt500Table.table[p.currentState<<indexStateShift|int(lookup)]
		switch transition >> transitionActionShift {
		case actionPrint:
			// read ahead: 0x20 (SP) included, 0x7F (DEL) excluded
			for j := i + 1; ; j++ {
				if j >= length || func() bool { code = data[j]; return code < 0x20 || (code > 0x7e && code < nonASCIIPrintable) }() {
					p.printHandler(data, i, j)
					i = j - 1
					break
				}
			}
		case actionExecute:
			if handler, ok := p.executeHandlers[byte(code)]; ok { // #nosec G115 -- single-byte C0/C1 controls and ASCII digits
				handler()
			} else {
				p.executeHandlerFb(code)
			}
			p.PrecedingJoinState = 0
		case actionIgnore:
		case actionError:
			inject := p.errorHandler(ParsingState{
				Position:     i,
				Code:         code,
				CurrentState: p.currentState,
				Collect:      p.collect,
				Params:       p.params,
			})
			if inject.Abort {
				return
			}
		case actionCsiDispatch:
			handlers := p.csiHandlers[p.collect<<8|int(code)]
			j := len(handlers) - 1
			for ; j >= 0; j-- {
				if handlers[j](p.params) {
					break
				}
			}
			if j < 0 {
				p.csiHandlerFb(p.collect<<8|int(code), p.params)
			}
			p.PrecedingJoinState = 0
		case actionParam:
			// inner loop: digits (0x30 - 0x39) and ; (0x3b) and : (0x3a)
			for {
				switch code {
				case 0x3b:
					p.params.AddParam(0) // ZDM
				case 0x3a:
					p.params.AddSubParam(-1)
				default: // 0x30 - 0x39
					p.params.AddDigit(int32(code) - 48) // #nosec G115 -- single-byte C0/C1 controls and ASCII digits
				}
				i++
				if i >= length {
					break
				}
				code = data[i]
				if code <= 0x2f || code >= 0x3c {
					break
				}
			}
			i--
		case actionCollect:
			p.collect <<= 8
			p.collect |= int(code)
		case actionEscDispatch:
			handlers := p.escHandlers[p.collect<<8|int(code)]
			j := len(handlers) - 1
			for ; j >= 0; j-- {
				if handlers[j]() {
					break
				}
			}
			if j < 0 {
				p.escHandlerFb(p.collect<<8 | int(code))
			}
			p.PrecedingJoinState = 0
		case actionClear:
			p.params.Reset()
			p.params.AddParam(0) // ZDM
			p.collect = 0
		case actionDcsHook:
			p.dcsParser.Hook(p.collect<<8|int(code), p.params)
		case actionDcsPut:
			// inner loop - exit DCS_PUT: 0x18, 0x1a, 0x1b, 0x7f, 0x80 - 0x9f
			for j := i + 1; ; j++ {
				if j >= length || func() bool {
					code = data[j]
					return code == 0x18 || code == 0x1a || code == 0x1b || (code > 0x7f && code < nonASCIIPrintable)
				}() {
					p.dcsParser.Put(data, i, j)
					i = j - 1
					break
				}
			}
		case actionDcsUnhook:
			p.dcsParser.Unhook(code != 0x18 && code != 0x1a)
			if code == 0x1b {
				transition |= stateEscape
			}
			p.params.Reset()
			p.params.AddParam(0) // ZDM
			p.collect = 0
			p.PrecedingJoinState = 0
		case actionOscStart:
			p.oscParser.Start()
		case actionOscPut:
			// inner loop: 0x20 (SP) included, 0x7F (DEL) included
			for j := i + 1; ; j++ {
				if j >= length || func() bool {
					code = data[j]
					return code < 0x20 || (code > 0x7f && code < nonASCIIPrintable)
				}() {
					p.oscParser.Put(data, i, j)
					i = j - 1
					break
				}
			}
		case actionOscEnd:
			p.oscParser.End(code != 0x18 && code != 0x1a)
			if code == 0x1b {
				transition |= stateEscape
			}
			p.params.Reset()
			p.params.AddParam(0) // ZDM
			p.collect = 0
			p.PrecedingJoinState = 0
		}
		p.currentState = int(transition & transitionStateMask)
	}
}
