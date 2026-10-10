package progressive

import "encoding/base64"

// Font is the sequences that bring the host a font for this program's
// terminal: data is the font file (WOFF2, WOFF, TTF or OTF), family its name,
// size the cell font size in CSS pixels (0 keeps the host's). The host loads
// it, draws the terminal in it and measures the cells again, which the
// program hears as an ordinary resize. It puts its own font back when the
// program exits. Hosts give a program on another machine no say over the
// person's font, so this is only for a program the host trusts with it
// (Discovery's "font").
func Font(family string, size float64, data []byte) []string {
	var seqs []string
	for len(data) > 0 || seqs == nil {
		n := min(len(data), shipChunk)
		meta := map[string]any{"family": family, "more": n < len(data)}
		if size > 0 {
			meta["size"] = size
		}
		seqs = append(seqs, osc("font;"+b64json(meta)+";"+base64.StdEncoding.EncodeToString(data[:n])))
		data = data[n:]
	}
	return seqs
}

// FontReset is the sequence asking the host for its own font back.
func FontReset() string { return osc("font;reset") }
