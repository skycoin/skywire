package launcher

// AppStatus defines running status of an App.
type AppStatus int

const (
	// AppStatusStopped represents status of a stopped App.
	AppStatusStopped AppStatus = iota

	// AppStatusRunning represents status of a running App.
	AppStatusRunning
)

// AppState defines state parameters for a registered App.
type AppState struct {
	AppConfig
	Status AppStatus `json:"status"`
}
