// Copyright 2016 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fuse

const useSingleReader = false

func (r *fuseFD) write(req *request) Status {
	if req.outPayloadSize() == 0 {
		err := handleEINTR(func() error {
			_, err := r.writevFD([][]byte{req.outHeaderBuf, req.outDataBuf})
			return err
		})
		return ToStatus(err)
	}

	if req.readResult != nil {
		defer func() {
			req.readResult.Done()
			req.readResult = nil
		}()
		if ws, ok := req.readResult.(withSlice); ok {
			return r.writeWithSlice(req, ws)
		}

		if r.server.canSplice && !r.server.opts.DisableSplice {
			err := r.trySplice(req, req.readResult)
			if err == nil {
				return OK
			}
			if err != errRecoverSplice {
				r.server.opts.Logger.Println("trySplice:", err)
			}
		}

		req.outPayload, req.status = req.readResult.Bytes(req.outPayload)
		req.serializeHeader(len(req.outPayload))
	}

	_, err := r.writevFD([][]byte{req.outHeaderBuf, req.outDataBuf, req.outPayload})
	return ToStatus(err)
}
