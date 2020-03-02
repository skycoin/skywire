package appcommon

// Config defines configuration parameters for `Proc`.
type Config struct {
	Name         string `json:"name"`
	SockFilePath string `json:"sock_file_path"`
	VisorPK      string `json:"visor_pk"`
	BinaryDir    string `json:"binary_dir"`
	WorkDir      string `json:"work_dir"`
}
