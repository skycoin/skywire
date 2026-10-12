package progressive

import "encoding/base64"

// The page a program runs in, and the person's files and clipboard
// (PROTOCOL.md, The page; Files). Where a host does not do these, the
// sequences are ignored.

// Title is the sequence naming the program's window (OSC 2): in websh, the
// page's title while the program runs.
func Title(title string) string { return "\x1b]2;" + title + "\x1b\\" }

// Page is the sequence telling the host where the program is, path being
// the program's own (a product, a document): the host puts it in the page's
// address, so the place can be linked to, and a link to it hands path back
// in Discovery (Caps.Path) when it opens the program again.
func Page(path, title string) string {
	return osc("page;" + b64json(map[string]string{"path": path, "title": title}))
}

// Download is the sequence offering a file for the person to save (iTerm2's
// OSC 1337 File=, which other terminals that save files understand too).
// Keep it to a few megabytes: it travels as one sequence.
func Download(name string, data []byte) string {
	return "\x1b]1337;File=name=" + base64.StdEncoding.EncodeToString([]byte(name)) +
		";size=" + itoa(len(data)) + ";inline=0:" + base64.StdEncoding.EncodeToString(data) + "\x07"
}

// Copy is the sequence putting text on the clipboard (OSC 52).
func Copy(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x1b\\"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
