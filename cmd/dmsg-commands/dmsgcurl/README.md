## DMSGCURL
#### Usage
```
$ skywire dmsg curl dmsg://{pk}:{port}/xxx
```

#### Errors
We trying to use same error's status code like what libcurl used as below:
| ERROR CODE | SHORT DESCRIPTION | LONG DESCRIPTION |
|---|---|---|
| 0 | OK | All fine. Proceed as usual. |
| 2 | FAILED_INIT | Very early initialization code failed. |
| 3 | URL_MALFORMAT | The URL was not properly formatted. |
| 4 | DMSG_INIT | Couldn't resolve dmsg initialziation. |
| 5 | COULDNT_RESOLVE_PROXY | Couldn't resolve proxy. The given proxy host could not be resolved. |
| 6 | COULDNT_RESOLVE_HOST | Couldn't resolve host. The given remote host was not resolved. |
| 22 | WRITE_INIT | An error occurred when creating output file. |
| 23 | WRITE_ERROR | An error occurred when writing received data to a local file, or an error was returned to dmsgcurl from a write callback. |
| 26 | READ_ERROR | There was a problem reading a local file or an error returned by the read callback. |
| 55 | SEND_ERROR | Failed sending network data. |
| 56 | RECV_ERROR | Failure with receiving network data. |
| 57 | DOWNLOAD_ERROR | Failure with downloading data. |
| 63 | FILESIZE_EXCEEDED | Maximum file size exceeded. |
| 64 | CONTEXT_CANCELED | Operation canceled by user. |

