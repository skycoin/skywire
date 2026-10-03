import Foundation

/// Reads a `text/event-stream` body as events, the part of the format the
/// visor writes: `data:` lines, a blank line ending the event, and `:`
/// comments (its pings and its opening line), which are skipped. `event:`,
/// `id:` and `retry:` are not used by any visor route and are ignored.
/// Chunks can split a line or an event anywhere; what is left over waits for
/// the next chunk.
struct ServerSentEvents {
    private var pending = Data()
    private var lines: [String] = []

    /// Takes the next chunk and returns every event it completed: each
    /// event's `data` lines, joined with newlines.
    mutating func feed(_ chunk: Data) -> [String] {
        pending.append(chunk)
        var events: [String] = []
        while let newline = pending.firstIndex(of: 0x0A) {
            var line = pending[pending.startIndex..<newline]
            pending = Data(pending[pending.index(after: newline)...])
            if line.last == 0x0D {
                line = line.dropLast()
            }
            let text = String(decoding: line, as: UTF8.self)
            if text.isEmpty {
                if !lines.isEmpty {
                    events.append(lines.joined(separator: "\n"))
                    lines = []
                }
            } else if text.hasPrefix("data:") {
                var value = text.dropFirst("data:".count)
                if value.first == " " {
                    value = value.dropFirst()
                }
                lines.append(String(value))
            }
        }
        return events
    }
}
