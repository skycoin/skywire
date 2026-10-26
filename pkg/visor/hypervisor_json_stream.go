package visor

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// writeStreamedJSON writes v piece by piece with stream. encoding/json pools its
// buffers, and in the wasm visor a megabyte buffer left in that pool stays alive.
func (hv *Hypervisor) writeStreamedJSON(w http.ResponseWriter, r *http.Request, v interface{}, stream func(io.Writer) error) {
	if pretty, _ := httputil.BoolFromQuery(r, "pretty", false); pretty { //nolint:errcheck
		httputil.WriteJSON(w, r, http.StatusOK, v)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	bw := bufio.NewWriterSize(w, 32<<10)
	err := stream(bw)
	if err == nil {
		err = bw.WriteByte('\n')
	}
	if err == nil {
		err = bw.Flush()
	}
	if err != nil {
		hv.log(r).WithError(err).Debug("Failed to write streamed JSON response")
	}
}

// writeJSONArray writes items as a JSON array, encoding one element at a time.
func writeJSONArray[T any](w io.Writer, items []T) error {
	if items == nil {
		_, err := io.WriteString(w, "null")
		return err
	}
	if _, err := io.WriteString(w, "["); err != nil {
		return err
	}
	for i := range items {
		if i > 0 {
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
		}
		b, err := json.Marshal(items[i])
		if err != nil {
			return err
		}
		if _, err := w.Write(b); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "]")
	return err
}

// writeVisorTree writes resp as json.Marshal would, one visor at a time.
func writeVisorTree(w io.Writer, resp VisorTreeResponse) error {
	if resp.Sections == nil {
		_, err := io.WriteString(w, `{"sections":null}`)
		return err
	}
	if _, err := io.WriteString(w, `{"sections":[`); err != nil {
		return err
	}
	for i, s := range resp.Sections {
		if i > 0 {
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
		}
		// The fields before visors, in VisorTreeSection's order.
		head, err := json.Marshal(struct {
			HypervisorPK cipher.PubKey   `json:"hypervisor_pk"`
			ViaChain     []cipher.PubKey `json:"via_chain,omitempty"`
		}{s.HypervisorPK, s.ViaChain})
		if err != nil {
			return err
		}
		if _, err := w.Write(head[:len(head)-1]); err != nil {
			return err
		}
		if _, err := io.WriteString(w, `,"visors":`); err != nil {
			return err
		}
		if err := writeJSONArray[visorapi.Summary](w, s.Visors); err != nil {
			return err
		}
		if s.SubError != "" {
			subErr, err := json.Marshal(s.SubError)
			if err != nil {
				return err
			}
			if _, err := io.WriteString(w, `,"sub_error":`); err != nil {
				return err
			}
			if _, err := w.Write(subErr); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "}"); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "]}")
	return err
}
