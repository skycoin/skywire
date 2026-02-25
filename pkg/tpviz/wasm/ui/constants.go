//go:build js && wasm

package ui

// Colors - matching vis-network JavaScript UI
const (
	ColorBackground = "#1a1a2e"
	// Node colors (same as JS getNodeColor)
	ColorOnlineBg      = "#00d9a5"
	ColorOnlineBorder  = "#00b386"
	ColorOfflineBg     = "#e94560"
	ColorOfflineBorder = "#ffffff"
	ColorUnknownBg     = "#ffd166"
	ColorUnknownBorder = "#ccaa52"
	// Local visor (cyan with magenta border)
	ColorLocalVisorBg     = "#00ffff"
	ColorLocalVisorBorder = "#ff00ff"
	// Service colors (matching JS)
	ColorVPNBg       = "#9f6efc"
	ColorVPNBorder   = "#7c3aed"
	ColorProxyBg     = "#ffa500"
	ColorProxyBorder = "#cc8400"
	// Selection/hover
	ColorSelected = "#e94560"
	ColorHovered  = "#ff6b6b"
	// Transport edge colors (from JS colors object)
	ColorSTCPR = "#00d9a5" // stcpr
	ColorSUDPH = "#00b4d8" // sudph
	ColorDMSG  = "#ffd166" // dmsg
	// Local edge color (cyan, from LOCAL_EDGE_COLOR)
	ColorLocalEdge = "#00ffff"
	// Dim colors
	ColorEdgeDim       = "rgba(100, 100, 100, 0.3)"
	ColorText          = "#ffffff"
	ColorTextDim       = "#aaaaaa"
	ColorTooltipBg     = "rgba(22, 33, 62, 0.95)"
	ColorTooltipBorder = "#0f3460"
	// DMSG server colors
	ColorDMSGServerBg     = "#9f6efc" // Purple for DMSG servers
	ColorDMSGServerBorder = "#7c3aed"
	ColorDMSGConnection   = "#e94560" // Red for DMSG connections
	ColorRoutePath        = "#ff00ff" // Magenta for route paths
)

// Default cluster colors for IP groups (matching TypeScript palette)
var DefaultClusterColors = []string{
	"#00d9a5", "#00b4d8", "#ffd166", "#e94560", "#9f6efc",
	"#ff6b6b", "#4ecdc4", "#ffe66d", "#95e1d3", "#f38181",
	"#aa96da", "#fcbad3", "#a8d8ea", "#dcedc1", "#ffd3b6",
}
