package visor

import (
	"io/ioutil"
	"net/http"
)

// FetchHvPk fetches the hypervisor public key from the ip:port passed to it.
func FetchHvPk(ipPort string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, "http://"+ipPort, nil)
	if err != nil {
		return "", err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close() //nolint:errcheck
	resBody, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	return string(resBody), nil
}
