// Package visor pkg/visor/hypervisor_handlers_ui2_rewards.go c3-vis-core
package visor

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/rewardconfig"
	"github.com/skycoin/skywire/pkg/visor/ui2"
	"github.com/skycoin/skywire/rewards"
)

// The rewards tab of the server rendered UI.

func (hv *Hypervisor) ui2RewardsRoutes(r *httputil.Router) {
	r.Get("/visors/{pk}/rewards", hv.ui2Rewards(false))
	r.Get("/visors/{pk}/rewards/address", hv.ui2Rewards(true))
	r.Put("/visors/{pk}/rewards/address", hv.ui2RewardSave())
	r.Delete("/visors/{pk}/rewards/address", hv.ui2RewardDelete())
	r.Get("/visors/{pk}/rewards/rules", func(w http.ResponseWriter, r *http.Request) {
		hv.render(w, r, ui2.RewardRules(rewards.MainnetRules))
	})
	r.Get("/visors/{pk}/rewards/history", hv.ui2RewardHistory())
}

// ui2RewardView fills the address view for addr: the addresses an xpub
// derives, or whether a plain address has spent, as the JSON routes do.
func (hv *Hypervisor) ui2RewardView(pk, addr string) ui2.Rewards {
	rw := ui2.Rewards{PK: pk, Address: addr, Draft: addr}
	if addr == "" {
		return rw
	}
	_, xpub, err := rewardconfig.ValidateRewardAddress(addr)
	rw.Xpub = err == nil && xpub
	switch {
	case rw.Xpub:
		if rw.Addresses, err = rewardconfig.RewardAddresses(addr, 5); err != nil {
			rw.XpubErr = err.Error()
		}
	case err == nil && hv.visor != nil:
		// Chain data, so the hypervisor's own visor answers, as in getRewardAddressExposure.
		if e, err := hv.visor.RewardAddressExposure(addr); err == nil {
			rw.Spent, rw.Balance = e.Spent, e.Balance
		}
	}
	return rw
}

// ui2Rewards serves the tab, or with part set only the address view, in
// edit mode when ?edit is given.
func (hv *Hypervisor) ui2Rewards(part bool) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		pk := r.PathValue("pk")
		addr, err := ctx.API.GetRewardAddress()
		rw := hv.ui2RewardView(pk, addr)
		if err != nil {
			rw.Err = err.Error()
		}
		rw.Editing = r.URL.Query().Get("edit") != ""
		if part {
			hv.render(w, r, ui2.RewardAddress(rw))
			return
		}
		hv.render(w, r, ui2.RewardsPage(rw))
	})
}

// ui2RewardSave sets the reward address: an account xpub, or a single address
// with the accept_risk box ticked, as putRewardAddress takes. An empty field removes it.
func (hv *Hypervisor) ui2RewardSave() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		pk := r.PathValue("pk")
		if err := r.ParseForm(); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		draft := r.PostForm.Get("address")
		acceptRisk := r.PostForm.Get("accept_risk") != ""
		fail := func(msg string) {
			cur, _ := ctx.API.GetRewardAddress() //nolint:errcheck
			rw := hv.ui2RewardView(pk, cur)
			rw.Editing, rw.Draft, rw.AcceptRisk, rw.Err = true, draft, acceptRisk, msg
			hv.render(w, r, ui2.RewardAddress(rw))
		}
		if draft == "" {
			if err := ctx.API.DeleteRewardAddress(); err != nil {
				fail(err.Error())
				return
			}
			hv.render(w, r, ui2.RewardAddress(hv.ui2RewardView(pk, "")), ui2.Toast(hv.ui2T(r, "rewards-address-config.done"), false))
			return
		}
		canonical, xpub, err := rewardconfig.ValidateRewardSetting(draft, acceptRisk)
		switch {
		case errors.Is(err, rewardconfig.ErrSingleAddress):
			fail(hv.ui2T(r, "rewards-address-config.single-needs-opt-in"))
			return
		case err != nil:
			fail(hv.ui2T(r, "rewards-address-config.address-error"))
			return
		}
		setFn := ctx.API.SetRewardAddress
		if !xpub {
			setFn = ctx.API.SetSingleRewardAddress
		}
		set, err := setFn(canonical)
		if err != nil {
			fail(err.Error())
			return
		}
		hv.render(w, r, ui2.RewardAddress(hv.ui2RewardView(pk, set)), ui2.Toast(hv.ui2T(r, "rewards-address-config.done"), false))
	})
}

func (hv *Hypervisor) ui2RewardDelete() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		pk := r.PathValue("pk")
		if err := ctx.API.DeleteRewardAddress(); err != nil {
			hv.render(w, r, ui2.Toast(err.Error(), true))
			return
		}
		hv.render(w, r, ui2.RewardAddress(hv.ui2RewardView(pk, "")), ui2.Toast(hv.ui2T(r, "rewards-address-config.done"), false))
	})
}

// ui2RewardHistory asks the reward system what it paid the visor, the call
// the Angular tab makes through /api/rewards/.
func (hv *Hypervisor) ui2RewardHistory() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var pk cipher.PubKey
		if err := pk.Set(r.PathValue("pk")); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		h := ui2.RewardHistory{PK: pk.Hex(), Days: 7}
		switch d, _ := strconv.Atoi(r.URL.Query().Get("days")); d { //nolint:errcheck
		case 30, 90:
			h.Days = d
		}
		body, _, err := hv.rewardSystemGet("skycoin-rewards/visor/" + h.PK + "?days=" + strconv.Itoa(h.Days))
		if err == nil {
			h.List, h.Total, err = ui2.ParseRewardHistory(body)
		}
		if err != nil {
			h.Err = err.Error()
		}
		hv.render(w, r, ui2.RewardHistoryView(h))
	}
}
