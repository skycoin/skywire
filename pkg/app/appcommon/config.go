package appcommon

// Config defines configuration parameters for `Proc`.
type Config struct {
	Name       string `json:"name"`
	ServerHost string `json:"server_host"`
	ServerPort uint   `json:"server_port"`
	VisorPK    string `json:"visor_pk"`
	BinaryDir  string `json:"binary_dir"`
	WorkDir    string `json:"work_dir"`
}
