import Foundation

/// One destination of a send as typed, before its values are checked (Android: DestDraft).
struct DestDraft: Equatable {
    var address = ""
    var amountText = ""
    var hoursText = ""
}

/// Bulk send text, read; `line` is 1-based and names the line at fault.
enum BulkParse: Equatable {
    case ok([DestDraft])
    case empty
    case badLine(Int)
    case mixedHours(Int)
}

/// Reads one destination per line, as `address,amount` or `address,amount,hours`, the format
/// skycoin-web's bulk send takes. Either every line has hours or none does.
func parseBulk(_ text: String) -> BulkParse {
    // "\r\n" is one Character, so a Windows line end counts as one line, as Kotlin's lines() does.
    let lines = text.split(omittingEmptySubsequences: false, whereSeparator: \.isNewline)
        .enumerated()
        .map { ($0.offset, $0.element.trimmingCharacters(in: .whitespaces)) }
        .filter { !$0.1.isEmpty }
    if lines.isEmpty { return .empty }
    var width = 0
    var out: [DestDraft] = []
    for (index, line) in lines {
        let fields = line.split(separator: ",", omittingEmptySubsequences: false).map { $0.trimmingCharacters(in: .whitespaces) }
        if !(2...3).contains(fields.count) || fields.contains(where: \.isEmpty) { return .badLine(index + 1) }
        if width != 0 && fields.count != width { return .mixedHours(index + 1) }
        width = fields.count
        out.append(DestDraft(address: fields[0], amountText: fields[1], hoursText: fields.count == 3 ? fields[2] : ""))
    }
    return .ok(out)
}
