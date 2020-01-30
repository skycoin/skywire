package appcommon

// ProcID identifies the current instance of an app (an app process).
// The visor is responsible for starting apps, and the started process
// should be provided with a ProcID.
type ProcID uint16
