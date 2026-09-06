//go:build windows

package osnotify

import (
	"os"
	"os/exec"
)

// available reports whether PowerShell is on PATH (it ships with Windows). A
// missing interactive session just makes the toast a no-op at display time.
func available() bool {
	_, err := lookPath("powershell")
	return err == nil
}

// toastScript is a FIXED PowerShell script that builds a Windows toast. The
// title/body are read from environment variables ($env:OSN_*) and inserted via
// the XML DOM's CreateTextNode — which XML-escapes them — so untrusted text is
// never interpolated into the script or the toast XML. It borrows PowerShell's
// registered AppUserModelID so the toast displays without registering our own.
const toastScript = `
$ErrorActionPreference = 'Stop'
$title = $env:OSN_TITLE
$body  = $env:OSN_BODY
$null = [Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime]
$xml = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$nodes = $xml.GetElementsByTagName('text')
$null = $nodes.Item(0).AppendChild($xml.CreateTextNode($title))
$null = $nodes.Item(1).AppendChild($xml.CreateTextNode($body))
$toast = [Windows.UI.Notifications.ToastNotification]::new($xml)
$aumid = '{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe'
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($aumid).Show($toast)
`

func send(n Notification) error {
	c := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", toastScript) //nolint:gosec // fixed script; data via env
	c.Env = append(os.Environ(),
		"OSN_TITLE="+n.Title,
		"OSN_BODY="+n.Body,
	)
	return runCmd(c)
}
