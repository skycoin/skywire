package appserver

// Config defines configuration parameters for `Proc`.
type Config struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	SockFile  string `json:"sock_file"`
	BinaryDir string `json:"binary_dir"`
	WorkDir   string `json:"work_dir"`
}
