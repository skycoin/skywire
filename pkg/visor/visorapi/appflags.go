// Package visorapi pkg/visor/visorapi/appflags.go c3-vis-core
package visorapi

// Flag types reported in an AppFlag.
const (
	FlagTypeBool       = "bool"
	FlagTypeString     = "string"
	FlagTypeInt        = "int"
	FlagTypeDuration   = "duration"
	FlagTypeStringList = "stringList"
)

// Where an AppFlags schema came from.
const (
	FlagSourceRegistry = "registry"
	FlagSourceHelp     = "help"
)

// AppFlags is the settings schema of one configured app.
type AppFlags struct {
	App    string    `json:"app"`
	Source string    `json:"source"`
	Flags  []AppFlag `json:"flags"`
}

// AppFlag describes one flag an app accepts and the value its launcher
// args give it. Set is false when the args leave the flag at its default.
type AppFlag struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand,omitempty"`
	Type      string `json:"type"`
	Default   string `json:"default"`
	Usage     string `json:"usage"`
	Check     string `json:"check,omitempty"`
	Value     string `json:"value,omitempty"`
	Set       bool   `json:"set"`
}
