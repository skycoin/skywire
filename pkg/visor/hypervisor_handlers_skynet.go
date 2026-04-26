// Package visor pkg/visor/hypervisor.go
package visor

import (
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/usermanager"
)

func (hv *Hypervisor) getPorts() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		ports, err := ctx.API.Ports()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, ports)
	})
}

func (hv *Hypervisor) getSkynetPorts() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		ports, err := ctx.API.ListTCPPorts()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, ports)
	})
}

func (hv *Hypervisor) postRegisterSkynetPort() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var reqBody struct {
			Port int `json:"port"`
		}
		if err := httputil.ReadJSON(r, &reqBody); err != nil {
			if err != io.EOF {
				hv.log(r).Warnf("postRegisterSkynetPort request: %v", err)
			}
			httputil.WriteJSON(w, r, http.StatusBadRequest, usermanager.ErrMalformedRequest)
			return
		}
		if err := ctx.API.RegisterTCPPort(reqBody.Port); err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, struct{}{})
	})
}

func (hv *Hypervisor) postDeregisterSkynetPort() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var reqBody struct {
			Port int `json:"port"`
		}
		if err := httputil.ReadJSON(r, &reqBody); err != nil {
			if err != io.EOF {
				hv.log(r).Warnf("postDeregisterSkynetPort request: %v", err)
			}
			httputil.WriteJSON(w, r, http.StatusBadRequest, usermanager.ErrMalformedRequest)
			return
		}
		if err := ctx.API.DeregisterTCPPort(reqBody.Port); err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, struct{}{})
	})
}

func (hv *Hypervisor) getForwardedPorts() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		ports, err := ctx.API.ListForwardedPorts()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, ports)
	})
}

func (hv *Hypervisor) postRegisterForwardedPort() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var p ForwardedPort
		if err := httputil.ReadJSON(r, &p); err != nil {
			if err != io.EOF {
				hv.log(r).Warnf("postRegisterForwardedPort: %v", err)
			}
			httputil.WriteJSON(w, r, http.StatusBadRequest, usermanager.ErrMalformedRequest)
			return
		}
		if err := ctx.API.RegisterForwardedPort(p); err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, struct{}{})
	})
}

func (hv *Hypervisor) postUpdateForwardedPort() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var p ForwardedPort
		if err := httputil.ReadJSON(r, &p); err != nil {
			if err != io.EOF {
				hv.log(r).Warnf("postUpdateForwardedPort: %v", err)
			}
			httputil.WriteJSON(w, r, http.StatusBadRequest, usermanager.ErrMalformedRequest)
			return
		}
		if err := ctx.API.UpdateForwardedPort(p); err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, struct{}{})
	})
}

func (hv *Hypervisor) getSkynetForwards() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		fwds, err := ctx.API.List()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, fwds)
	})
}

func (hv *Hypervisor) postSkynetConnect() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var reqBody struct {
			RemotePK   cipher.PubKey `json:"remote_pk"`
			RemotePort int           `json:"remote_port"`
			LocalPort  int           `json:"local_port"`
		}
		if err := httputil.ReadJSON(r, &reqBody); err != nil {
			if err != io.EOF {
				hv.log(r).Warnf("postSkynetConnect request: %v", err)
			}
			httputil.WriteJSON(w, r, http.StatusBadRequest, usermanager.ErrMalformedRequest)
			return
		}
		id, err := ctx.API.Connect(reqBody.RemotePK, reqBody.RemotePort, reqBody.LocalPort)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, struct {
			ID string `json:"id"`
		}{ID: id.String()})
	})
}

func (hv *Hypervisor) postSkynetDisconnect() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var reqBody struct {
			ID string `json:"id"`
		}
		if err := httputil.ReadJSON(r, &reqBody); err != nil {
			if err != io.EOF {
				hv.log(r).Warnf("postSkynetDisconnect request: %v", err)
			}
			httputil.WriteJSON(w, r, http.StatusBadRequest, usermanager.ErrMalformedRequest)
			return
		}
		uid, err := uuid.Parse(reqBody.ID)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, fmt.Errorf("invalid UUID: %w", err))
			return
		}
		if err := ctx.API.Disconnect(uid); err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, struct{}{})
	})
}
