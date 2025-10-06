package commands

var errorCode = map[string]int{
	"SUCCESS":               0,
	"FAILURE":               1,
	"FAILED_INIT":           2,
	"URL_MALFORMAT":         3,
	"DMSG_INIT":             4,
	"COULDNT_RESOLVE_PROXY": 5,
	"COULDNT_RESOLVE_HOST":  6,
	"WRITE_INIT":            22,
	"WRITE_ERROR":           23,
	"READ_ERROR":            26,
	"SEND_ERROR":            55,
	"RECV_ERROR":            56,
	"DOWNLOAD_ERROR":        57,
	"FILESIZE_EXCEEDED":     63,
	"CONTEXT_CANCELED":      64,
}
var errorDesc = map[string]string{
	"SUCCESS":               "No error",
	"FAILURE":               "An error occurred",
	"FAILED_INIT":           "Very early initialization code failed.",
	"URL_MALFORMAT":         "The URL was not properly formatted.",
	"DMSG_INIT":             "Couldn't resolve dmsg initialziation.",
	"COULDNT_RESOLVE_PROXY": "Couldn't resolve proxy. The given proxy host could not be resolved.",
	"COULDNT_RESOLVE_HOST":  "Couldn't resolve host. The given remote host was not resolved.",
	"WRITE_INIT":            "An error occurred when creating output file.",
	"WRITE_ERROR":           "An error occurred when writing received data to a local file, or an error was returned to dmsgcurl from a write callback.",
	"READ_ERROR":            "There was a problem reading a local file or an error returned by the read callback.",
	"SEND_ERROR":            "Failed sending network data.",
	"RECV_ERROR":            "Failure with receiving network data.",
	"DOWNLOAD_ERROR":        "Failure with downloading data.",
	"FILESIZE_EXCEEDED":     "Maximum file size exceeded.",
	"CONTEXT_CANCELED":      "Operation canceled by user",
}

// curlError is the struct of dmsgcurl functions output, to set appropriate exit code
type curlError struct {
	Error error
	Code  int
}
