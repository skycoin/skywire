package progressive

import "encoding/base64"

// Notifications, the favicon, sound and dropped files (PROTOCOL.md).

// Notify is the sequence for a notification (OSC 777 notify, which urxvt
// and foot show too; websh also takes iTerm2's OSC 9 and kitty's OSC 99).
// The host shows it as the system's when the person is elsewhere and has
// allowed it, or over the terminal.
func Notify(title, body string) string {
	return "\x1b]777;notify;" + title + ";" + body + "\x1b\\"
}

// Icon is the sequence setting the page's favicon while the program runs:
// an http(s) or data:image/ URL.
func Icon(url string) string { return osc("icon;" + b64json(map[string]string{"url": url})) }

// Sound is the sequence playing the sound at url as id (loop, at volume 0
// to 1; a negative volume leaves it as it is). The same id again replaces
// it; SoundStop stops it, and the host stops every sound when the program
// exits.
func Sound(id, url string, volume float64, loop bool) string {
	m := map[string]any{"id": id, "url": url, "loop": loop}
	if volume >= 0 {
		m["volume"] = volume
	}
	return osc("sound;" + b64json(m))
}

// SoundData is the sequences playing data (of MIME type mime: audio/wav,
// audio/mpeg, audio/ogg ...) as id, sent in chunks.
func SoundData(id, mime string, data []byte, volume float64, loop bool) []string {
	var seqs []string
	for len(data) > 0 || seqs == nil {
		n := min(len(data), shipChunk)
		m := map[string]any{"id": id, "type": mime, "loop": loop, "more": n < len(data)}
		if volume >= 0 {
			m["volume"] = volume
		}
		seqs = append(seqs, osc("sound;"+b64json(m)+";"+base64.StdEncoding.EncodeToString(data[:n])))
		data = data[n:]
	}
	return seqs
}

// SoundStop is the sequence stopping sound id.
func SoundStop(id string) string {
	return osc("sound;" + b64json(map[string]any{"id": id, "stop": true}))
}

// ListenDrop is the sequence asking for files dropped onto the terminal as
// events (ID "drop", Type "drop", Data {"path", "name", "size", "type"})
// rather than their paths typed. It lasts until the program exits.
func ListenDrop() string { return osc("listen;drop") }

// Image is the sequence showing a picture in the text at the cursor, as
// iTerm2's inline images do (OSC 1337 File= inline=1), which websh and
// other terminals that show pictures understand. width and height are each
// "N" cells, "Npx", "N%" of the terminal or "auto" ("" is auto); the
// picture keeps its shape. It scrolls with the text and stays in the
// scrollback.
func Image(name string, data []byte, width, height string) string {
	s := "\x1b]1337;File=name=" + base64.StdEncoding.EncodeToString([]byte(name)) +
		";size=" + itoa(len(data)) + ";inline=1"
	if width != "" {
		s += ";width=" + width
	}
	if height != "" {
		s += ";height=" + height
	}
	return s + ":" + base64.StdEncoding.EncodeToString(data) + "\x07"
}
