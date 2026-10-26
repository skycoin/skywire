import SwiftUI

/// Material Icons from Assets.xcassets (ios/scripts/material-icons.py), by Android's variant and name.
enum MI {
    static let filledArrowDropDown = "mi-filled-arrow-drop-down"
    static let filledCall = "mi-filled-call"
    static let filledCallEnd = "mi-filled-call-end"
    static let filledCheck = "mi-filled-check"
    static let filledClose = "mi-filled-close"
    static let filledExpandLess = "mi-filled-expand-less"
    static let filledExpandMore = "mi-filled-expand-more"
    static let filledKeyboardArrowRight = "mi-filled-keyboard-arrow-right"
    static let filledMic = "mi-filled-mic"
    static let filledMicOff = "mi-filled-mic-off"
    static let filledPause = "mi-filled-pause"
    static let filledPerson = "mi-filled-person"
    static let filledPlayArrow = "mi-filled-play-arrow"
    static let filledRefresh = "mi-filled-refresh"
    static let filledShare = "mi-filled-share"
    static let filledStar = "mi-filled-star"
    static let filledVolumeDown = "mi-filled-volume-down"
    static let filledVolumeUp = "mi-filled-volume-up"
    static let outlinedAccountBalanceWallet = "mi-outlined-account-balance-wallet"
    static let outlinedAdd = "mi-outlined-add"
    static let outlinedArrowDownward = "mi-outlined-arrow-downward"
    static let outlinedArrowUpward = "mi-outlined-arrow-upward"
    static let outlinedChat = "mi-outlined-chat"
    static let outlinedCheckCircle = "mi-outlined-check-circle"
    static let outlinedClose = "mi-outlined-close"
    static let outlinedContentCopy = "mi-outlined-content-copy"
    static let outlinedDeleteOutline = "mi-outlined-delete-outline"
    static let outlinedDns = "mi-outlined-dns"
    static let outlinedEdit = "mi-outlined-edit"
    static let outlinedErrorOutline = "mi-outlined-error-outline"
    static let outlinedFileDownload = "mi-outlined-file-download"
    static let outlinedHelpOutline = "mi-outlined-help-outline"
    static let outlinedHome = "mi-outlined-home"
    static let outlinedKey = "mi-outlined-key"
    static let outlinedKeyboardArrowDown = "mi-outlined-keyboard-arrow-down"
    static let outlinedKeyboardArrowRight = "mi-outlined-keyboard-arrow-right"
    static let outlinedKeyboardArrowUp = "mi-outlined-keyboard-arrow-up"
    static let outlinedLock = "mi-outlined-lock"
    static let outlinedLockOpen = "mi-outlined-lock-open"
    static let outlinedMoreVert = "mi-outlined-more-vert"
    static let outlinedOpenInNew = "mi-outlined-open-in-new"
    static let outlinedQrCodeScanner = "mi-outlined-qr-code-scanner"
    static let outlinedSchedule = "mi-outlined-schedule"
    static let outlinedScreenshotMonitor = "mi-outlined-screenshot-monitor"
    static let outlinedSearch = "mi-outlined-search"
    static let outlinedSettings = "mi-outlined-settings"
    static let outlinedStarBorder = "mi-outlined-star-border"
    static let outlinedSync = "mi-outlined-sync"
    static let outlinedVisibility = "mi-outlined-visibility"
    static let outlinedWarningAmber = "mi-outlined-warning-amber"
    static let roundedAccountBalanceWallet = "mi-rounded-account-balance-wallet"
    static let roundedAltRoute = "mi-rounded-alt-route"
    static let roundedAlternateEmail = "mi-rounded-alternate-email"
    static let roundedArrowBack = "mi-rounded-arrow-back"
    static let roundedAttachFile = "mi-rounded-attach-file"
    static let roundedCandlestickChart = "mi-rounded-candlestick-chart"
    static let roundedChat = "mi-rounded-chat"
    static let roundedCheckCircle = "mi-rounded-check-circle"
    static let roundedChevronRight = "mi-rounded-chevron-right"
    static let roundedClose = "mi-rounded-close"
    static let roundedContacts = "mi-rounded-contacts"
    static let roundedContentCopy = "mi-rounded-content-copy"
    static let roundedDelete = "mi-rounded-delete"
    static let roundedDns = "mi-rounded-dns"
    static let roundedDownload = "mi-rounded-download"
    static let roundedEdit = "mi-rounded-edit"
    static let roundedEmail = "mi-rounded-email"
    static let roundedForum = "mi-rounded-forum"
    static let roundedHelpOutline = "mi-rounded-help-outline"
    static let roundedHome = "mi-rounded-home"
    static let roundedHub = "mi-rounded-hub"
    static let roundedPersonAdd = "mi-rounded-person-add"
    static let roundedReply = "mi-rounded-reply"
    static let roundedRoute = "mi-rounded-route"
    static let roundedSend = "mi-rounded-send"
    static let roundedSettings = "mi-rounded-settings"
    static let roundedShare = "mi-rounded-share"
    static let roundedVideocam = "mi-rounded-videocam"
    static let roundedVpnLock = "mi-rounded-vpn-lock"
    static let roundedWarning = "mi-rounded-warning"
}

/// A Material glyph tinted by the foreground style, `size` points square (Android `Icon`).
struct MaterialIcon: View {
    let name: String
    var size: CGFloat = 24

    init(_ name: String, size: CGFloat = 24) {
        self.name = name
        self.size = size
    }

    var body: some View {
        Image(name).renderingMode(.template).resizable().scaledToFit().frame(width: size, height: size)
            .accessibilityHidden(true)
    }
}
